package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	maxRetries   = 15
	initialDelay = 1 * time.Second
	maxDelay     = 30 * time.Second
	totalTimeout = 5 * time.Minute
	refreshSlack = 30 * time.Second
)

// Client is an authenticated Keycloak Admin REST API client with retry logic.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

// New creates a new Keycloak API client.
func New(baseURL, username, password string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		username:   username,
		password:   password,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Connect authenticates to Keycloak with retry logic.
// Warns if the base URL does not use HTTPS.
func (c *Client) Connect(ctx context.Context) error {
	parsed, err := url.Parse(c.baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid Keycloak URL %q: must include scheme and host", c.baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid Keycloak URL %q: scheme must be http or https", c.baseURL)
	}
	if parsed.Scheme == "http" {
		slog.Warn("Keycloak URL uses plain HTTP — credentials will be sent unencrypted", "url", c.baseURL)
	}
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := c.authenticate(ctx)
		if err == nil {
			slog.Info("Connected to Keycloak", "url", c.baseURL)
			return nil
		}

		if ctx.Err() != nil {
			return fmt.Errorf("connection timeout after %s: %w", totalTimeout, ctx.Err())
		}

		delay := time.Duration(float64(initialDelay) * math.Pow(2, float64(attempt)))
		if delay > maxDelay {
			delay = maxDelay
		}

		slog.Warn("Keycloak not ready, retrying",
			"attempt", attempt+1,
			"max_retries", maxRetries,
			"delay", delay,
			"error", err,
		)

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("connection timeout: %w", ctx.Err())
		}
	}

	return fmt.Errorf("failed to connect after %d retries", maxRetries+1)
}

func (c *Client) authenticate(ctx context.Context) error {
	tokenURL := c.baseURL + "/realms/master/protocol/openid-connect/token"

	form := url.Values{
		"grant_type": {"password"},
		"client_id":  {"admin-cli"},
		"username":   {c.username},
		"password":   {c.password},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("token request failed with status %d (failed to read body: %v)", resp.StatusCode, readErr)
		}
		return fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("decoding token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return fmt.Errorf("token response missing access_token")
	}

	c.mu.Lock()
	c.accessToken = tokenResp.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn)*time.Second - refreshSlack)
	c.mu.Unlock()

	return nil
}

// currentToken returns the current access token, refreshing if expired.
// All read/write to accessToken happens under c.mu.
func (c *Client) currentToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	expired := time.Now().After(c.expiresAt)
	c.mu.Unlock()

	if expired {
		slog.Debug("Refreshing Keycloak access token")
		if err := c.authenticate(ctx); err != nil {
			return "", err
		}
	}

	c.mu.Lock()
	token := c.accessToken
	c.mu.Unlock()
	return token, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	// Marshal body once so it can be reused on retry.
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request body: %w", err)
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.currentToken(ctx)
		if err != nil {
			return nil, fmt.Errorf("ensuring token: %w", err)
		}

		fullURL := c.baseURL + path

		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("executing request: %w", err)
		}

		// Retry once on 401 (token may have expired mid-run)
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			slog.Debug("Got 401, refreshing token and retrying")
			if err := c.authenticate(ctx); err != nil {
				return nil, fmt.Errorf("re-authenticating after 401: %w", err)
			}
			continue
		}

		return resp, nil
	}

	// Unreachable, but satisfies the compiler.
	return nil, fmt.Errorf("unexpected: exhausted 401 retries for %s %s", method, path)
}

// StatusError is returned when Keycloak answers with an unexpected status.
// Callers that need to react to a particular status — telling "you may not do
// this" apart from "the server is broken" — can reach it with errors.As rather
// than matching on the message.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("unexpected status %d", e.Code)
	}

	return fmt.Sprintf("unexpected status %d: %s", e.Code, e.Body)
}

// StatusCode reports the HTTP status. It is a method rather than a bare field
// so callers can match on a minimal interface instead of importing this
// package.
func (e *StatusError) StatusCode() int {
	return e.Code
}

func readError(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("unexpected status %d (failed to read body: %v)", resp.StatusCode, err)
	}

	return &StatusError{Code: resp.StatusCode, Body: string(body)}
}

// GetRealm returns a realm representation or nil if not found.
func (c *Client) GetRealm(ctx context.Context, name string) (map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(name)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding realm: %w", err)
	}
	return result, nil
}

// CreateRealm creates a new realm.
func (c *Client) CreateRealm(ctx context.Context, body map[string]any) error {
	resp, err := c.doRequest(ctx, http.MethodPost, "/admin/realms", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}
	return nil
}

// UpdateRealm updates an existing realm.
func (c *Client) UpdateRealm(ctx context.Context, name string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(name)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetClients returns clients matching the given clientId filter.
func (c *Client) GetClients(ctx context.Context, realm, clientID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients?clientId=" + url.QueryEscape(clientID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding clients: %w", err)
	}
	return result, nil
}

// CreateClient creates a new client in the given realm.
// Returns the UUID of the newly created client, extracted from the Location header.
func (c *Client) CreateClient(ctx context.Context, realm string, body map[string]any) (string, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", readError(resp)
	}
	return parseLocationID(resp, "creating client")
}

// UpdateClient updates an existing client by UUID.
func (c *Client) UpdateClient(ctx context.Context, realm, uuid string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(uuid)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetRealmRole returns a realm role or nil if not found.
func (c *Client) GetRealmRole(ctx context.Context, realm, name string) (map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/roles/" + url.PathEscape(name)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding realm role: %w", err)
	}
	return result, nil
}

// CreateRealmRole creates a new realm role.
func (c *Client) CreateRealmRole(ctx context.Context, realm string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/roles"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}
	return nil
}

// UpdateRealmRole updates an existing realm role.
func (c *Client) UpdateRealmRole(ctx context.Context, realm, name string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/roles/" + url.PathEscape(name)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetClientRole returns a client role or nil if not found.
func (c *Client) GetClientRole(ctx context.Context, realm, clientUUID, name string) (map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/roles/" + url.PathEscape(name)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding client role: %w", err)
	}
	return result, nil
}

// CreateClientRole creates a new client role.
func (c *Client) CreateClientRole(ctx context.Context, realm, clientUUID string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/roles"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}
	return nil
}

// UpdateClientRole updates an existing client role.
func (c *Client) UpdateClientRole(ctx context.Context, realm, clientUUID, name string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/roles/" + url.PathEscape(name)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetProtocolMappers returns all protocol mappers for a client.
func (c *Client) GetProtocolMappers(ctx context.Context, realm, clientUUID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/protocol-mappers/models"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding protocol mappers: %w", err)
	}
	return result, nil
}

// CreateProtocolMapper creates a new protocol mapper for a client.
func (c *Client) CreateProtocolMapper(ctx context.Context, realm, clientUUID string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/protocol-mappers/models"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}
	return nil
}

// UpdateProtocolMapper updates a protocol mapper by ID.
func (c *Client) UpdateProtocolMapper(ctx context.Context, realm, clientUUID, mapperID string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/protocol-mappers/models/" + url.PathEscape(mapperID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetUsers returns users matching the given username with exact match.
func (c *Client) GetUsers(ctx context.Context, realm, username string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users?username=" + url.QueryEscape(username) + "&exact=true"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding users: %w", err)
	}
	return result, nil
}

// CreateUser creates a new user in the given realm.
// Returns the UUID of the newly created user, extracted from the Location header.
func (c *Client) CreateUser(ctx context.Context, realm string, body map[string]any) (string, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", readError(resp)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("creating user: no Location header in response")
	}
	idx := strings.LastIndex(location, "/")
	if idx < 0 || idx == len(location)-1 {
		return "", fmt.Errorf("creating user: unexpected Location header format: %s", location)
	}
	return location[idx+1:], nil
}

// UpdateUser updates an existing user by UUID.
func (c *Client) UpdateUser(ctx context.Context, realm, userID string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users/" + url.PathEscape(userID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// ResetUserPassword sets a user's password. When temporary is true, the user
// must change the password on first login.
func (c *Client) ResetUserPassword(ctx context.Context, realm, userID, password string, temporary bool) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users/" + url.PathEscape(userID) + "/reset-password"
	body := map[string]any{
		"type":      "password",
		"value":     password,
		"temporary": temporary,
	}
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetUserRealmRoleMappings returns the realm role mappings for a user.
func (c *Client) GetUserRealmRoleMappings(ctx context.Context, realm, userID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users/" + url.PathEscape(userID) + "/role-mappings/realm"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding realm role mappings: %w", err)
	}
	return result, nil
}

// AddUserRealmRoleMappings adds realm role mappings to a user.
func (c *Client) AddUserRealmRoleMappings(ctx context.Context, realm, userID string, roles []map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users/" + url.PathEscape(userID) + "/role-mappings/realm"
	resp, err := c.doRequest(ctx, http.MethodPost, path, roles)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetUserClientRoleMappings returns the client role mappings for a user.
func (c *Client) GetUserClientRoleMappings(ctx context.Context, realm, userID, clientUUID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users/" + url.PathEscape(userID) + "/role-mappings/clients/" + url.PathEscape(clientUUID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding client role mappings: %w", err)
	}
	return result, nil
}

// AddUserClientRoleMappings adds client role mappings to a user.
func (c *Client) AddUserClientRoleMappings(ctx context.Context, realm, userID, clientUUID string, roles []map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users/" + url.PathEscape(userID) + "/role-mappings/clients/" + url.PathEscape(clientUUID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, roles)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetUserGroups returns the groups the user is a direct member of.
func (c *Client) GetUserGroups(ctx context.Context, realm, userID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users/" + url.PathEscape(userID) + "/groups"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding user groups: %w", err)
	}
	return result, nil
}

// AddUserToGroup adds the user to the given group. The operation is
// idempotent on the Keycloak side — adding an existing member succeeds.
func (c *Client) AddUserToGroup(ctx context.Context, realm, userID, groupID string) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users/" + url.PathEscape(userID) + "/groups/" + url.PathEscape(groupID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetServiceAccountUser returns the service account user for a client.
func (c *Client) GetServiceAccountUser(ctx context.Context, realm, clientUUID string) (map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/service-account-user"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding service account user: %w", err)
	}
	return result, nil
}

// parseLocationID extracts the trailing ID segment from a response's Location
// header (e.g. ".../groups/{uuid}" -> "{uuid}"). op labels errors.
func parseLocationID(resp *http.Response, op string) (string, error) {
	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("%s: no Location header in response", op)
	}
	idx := strings.LastIndex(location, "/")
	if idx < 0 || idx == len(location)-1 {
		return "", fmt.Errorf("%s: unexpected Location header format: %s", op, location)
	}
	return location[idx+1:], nil
}

// GetGroups returns groups matching the given name (exact match).
//
// parentID is "" for top-level groups; otherwise the children of that group are
// searched. Keycloak exposes the two as different paths, but they answer the
// same question and callers already track which level they are at.
func (c *Client) GetGroups(ctx context.Context, realm, parentID, search string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/groups"
	if parentID != "" {
		path += "/" + url.PathEscape(parentID) + "/children"
	}

	path += "?search=" + url.QueryEscape(search) + "&exact=true"

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding groups: %w", err)
	}
	return result, nil
}

// GetGroup returns the full representation of a group by UUID, or nil if not found.
// Unlike GetGroups, this includes attributes and other detail fields.
func (c *Client) GetGroup(ctx context.Context, realm, id string) (map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/groups/" + url.PathEscape(id)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding group: %w", err)
	}
	return result, nil
}

// CreateGroup creates a group and returns its UUID, taken from the Location
// header. parentID is "" for a top-level group; otherwise the group is created
// as a child of it.
func (c *Client) CreateGroup(ctx context.Context, realm, parentID string, body map[string]any) (string, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/groups"
	if parentID != "" {
		path += "/" + url.PathEscape(parentID) + "/children"
	}

	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", readError(resp)
	}
	return parseLocationID(resp, "creating group")
}

// UpdateGroup updates an existing group by UUID.
func (c *Client) UpdateGroup(ctx context.Context, realm, id string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/groups/" + url.PathEscape(id)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetGroupRealmRoleMappings returns the realm roles currently mapped to a group.
func (c *Client) GetGroupRealmRoleMappings(ctx context.Context, realm, groupID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/groups/" + url.PathEscape(groupID) + "/role-mappings/realm"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding group realm role mappings: %w", err)
	}
	return result, nil
}

// AddGroupRealmRoleMappings grants the given realm roles to a group.
// Each entry must contain at least the role "id" and "name".
func (c *Client) AddGroupRealmRoleMappings(ctx context.Context, realm, groupID string, roles []map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/groups/" + url.PathEscape(groupID) + "/role-mappings/realm"
	resp, err := c.doRequest(ctx, http.MethodPost, path, roles)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetGroupClientRoleMappings returns the client roles of the given client currently mapped to a group.
func (c *Client) GetGroupClientRoleMappings(ctx context.Context, realm, groupID, clientUUID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/groups/" + url.PathEscape(groupID) + "/role-mappings/clients/" + url.PathEscape(clientUUID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding group client role mappings: %w", err)
	}
	return result, nil
}

// AddGroupClientRoleMappings grants the given client roles to a group.
// Each entry must contain at least the role "id" and "name".
func (c *Client) AddGroupClientRoleMappings(ctx context.Context, realm, groupID, clientUUID string, roles []map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/groups/" + url.PathEscape(groupID) + "/role-mappings/clients/" + url.PathEscape(clientUUID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, roles)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetClientScopes returns all client scopes defined in the realm.
// The admin API has no lookup by name, so callers match on the "name" field.
func (c *Client) GetClientScopes(ctx context.Context, realm string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/client-scopes"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding client scopes: %w", err)
	}
	return result, nil
}

// CreateClientScope creates a new client scope in the realm.
// Returns the UUID of the newly created scope, extracted from the Location header.
func (c *Client) CreateClientScope(ctx context.Context, realm string, body map[string]any) (string, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/client-scopes"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", readError(resp)
	}
	return parseLocationID(resp, "creating client scope")
}

// UpdateClientScope updates a client scope by ID.
func (c *Client) UpdateClientScope(ctx context.Context, realm, scopeID string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/client-scopes/" + url.PathEscape(scopeID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetClientScopeProtocolMappers returns the protocol mappers of a client scope.
func (c *Client) GetClientScopeProtocolMappers(ctx context.Context, realm, scopeID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/client-scopes/" + url.PathEscape(scopeID) + "/protocol-mappers/models"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding client scope protocol mappers: %w", err)
	}
	return result, nil
}

// CreateClientScopeProtocolMapper creates a protocol mapper on a client scope.
func (c *Client) CreateClientScopeProtocolMapper(ctx context.Context, realm, scopeID string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/client-scopes/" + url.PathEscape(scopeID) + "/protocol-mappers/models"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}
	return nil
}

// UpdateClientScopeProtocolMapper updates a protocol mapper on a client scope by ID.
func (c *Client) UpdateClientScopeProtocolMapper(ctx context.Context, realm, scopeID, mapperID string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/client-scopes/" + url.PathEscape(scopeID) + "/protocol-mappers/models/" + url.PathEscape(mapperID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// listClientScopeAssignments is the shared reader for the four endpoints that
// return a list of client scopes assigned somewhere (realm-wide defaults and
// optionals, and a single client's defaults and optionals).
func (c *Client) listClientScopeAssignments(ctx context.Context, path, what string) ([]map[string]any, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", what, err)
	}
	return result, nil
}

// putClientScopeAssignment is the shared writer for the four endpoints that
// assign a client scope. All of them are idempotent PUTs with no body.
func (c *Client) putClientScopeAssignment(ctx context.Context, path string) error {
	resp, err := c.doRequest(ctx, http.MethodPut, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetRealmDefaultClientScopes returns the realm's default client scopes, which
// Keycloak assigns to every newly created client.
func (c *Client) GetRealmDefaultClientScopes(ctx context.Context, realm string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/default-default-client-scopes"
	return c.listClientScopeAssignments(ctx, path, "realm default client scopes")
}

// AddRealmDefaultClientScope adds a client scope to the realm's defaults.
func (c *Client) AddRealmDefaultClientScope(ctx context.Context, realm, scopeID string) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/default-default-client-scopes/" + url.PathEscape(scopeID)
	return c.putClientScopeAssignment(ctx, path)
}

// GetRealmOptionalClientScopes returns the realm's optional client scopes.
func (c *Client) GetRealmOptionalClientScopes(ctx context.Context, realm string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/default-optional-client-scopes"
	return c.listClientScopeAssignments(ctx, path, "realm optional client scopes")
}

// AddRealmOptionalClientScope adds a client scope to the realm's optionals.
func (c *Client) AddRealmOptionalClientScope(ctx context.Context, realm, scopeID string) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/default-optional-client-scopes/" + url.PathEscape(scopeID)
	return c.putClientScopeAssignment(ctx, path)
}

// GetClientDefaultScopes returns the default client scopes assigned to a client.
func (c *Client) GetClientDefaultScopes(ctx context.Context, realm, clientUUID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/default-client-scopes"
	return c.listClientScopeAssignments(ctx, path, "client default scopes")
}

// AddClientDefaultScope assigns a client scope to a client as a default scope.
func (c *Client) AddClientDefaultScope(ctx context.Context, realm, clientUUID, scopeID string) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/default-client-scopes/" + url.PathEscape(scopeID)
	return c.putClientScopeAssignment(ctx, path)
}

// GetClientOptionalScopes returns the optional client scopes assigned to a client.
func (c *Client) GetClientOptionalScopes(ctx context.Context, realm, clientUUID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/optional-client-scopes"
	return c.listClientScopeAssignments(ctx, path, "client optional scopes")
}

// AddClientOptionalScope assigns a client scope to a client as an optional scope.
func (c *Client) AddClientOptionalScope(ctx context.Context, realm, clientUUID, scopeID string) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/optional-client-scopes/" + url.PathEscape(scopeID)
	return c.putClientScopeAssignment(ctx, path)
}

// GetOrganizations returns organizations in the realm matching the given name
// (exact match). Requires Keycloak 26+ with organizations enabled on the realm.
func (c *Client) GetOrganizations(ctx context.Context, realm, search string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations?search=" + url.QueryEscape(search) + "&exact=true"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding organizations: %w", err)
	}
	return result, nil
}

// CreateOrganization creates a new organization in the realm.
// Returns the UUID of the newly created organization, extracted from the
// Location header.
func (c *Client) CreateOrganization(ctx context.Context, realm string, body map[string]any) (string, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", readError(resp)
	}
	return parseLocationID(resp, "creating organization")
}

// UpdateOrganization updates an organization by ID.
func (c *Client) UpdateOrganization(ctx context.Context, realm, orgID string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetOrganizationMembers returns the members of an organization.
func (c *Client) GetOrganizationMembers(ctx context.Context, realm, orgID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) + "/members"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding organization members: %w", err)
	}
	return result, nil
}

// AddOrganizationMember adds an existing realm user to an organization.
//
// Unlike every other endpoint here, this one takes the user ID as a bare JSON
// string rather than an object, which is what Keycloak's addMember expects.
func (c *Client) AddOrganizationMember(ctx context.Context, realm, orgID, userID string) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) + "/members"
	resp, err := c.doRequest(ctx, http.MethodPost, path, userID)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetOrganizationGroups returns the top-level groups of an organization.
//
// Organization groups live in a namespace of their own: they do not appear
// under the realm's groups, and Keycloak refuses to manage them through the
// normal group API. The returned entries never populate "subGroups" — use
// GetOrganizationSubGroups to descend.
func (c *Client) GetOrganizationGroups(ctx context.Context, realm, orgID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) + "/groups"
	return c.listOrganizationGroups(ctx, path)
}

// GetOrganizationSubGroups returns the direct children of an organization group.
func (c *Client) GetOrganizationSubGroups(ctx context.Context, realm, orgID, groupID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) +
		"/groups/" + url.PathEscape(groupID) + "/children"
	return c.listOrganizationGroups(ctx, path)
}

func (c *Client) listOrganizationGroups(ctx context.Context, path string) ([]map[string]any, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding organization groups: %w", err)
	}
	return result, nil
}

// CreateOrganizationGroup creates a top-level group in an organization.
// Returns the UUID from the Location header. Keycloak rejects a duplicate name
// with 409, so callers must check for an existing group first.
func (c *Client) CreateOrganizationGroup(ctx context.Context, realm, orgID string, body map[string]any) (string, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) + "/groups"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", readError(resp)
	}
	return parseLocationID(resp, "creating organization group")
}

// CreateOrganizationSubGroup creates a group nested under an organization group.
func (c *Client) CreateOrganizationSubGroup(ctx context.Context, realm, orgID, parentID string, body map[string]any) (string, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) +
		"/groups/" + url.PathEscape(parentID) + "/children"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", readError(resp)
	}
	return parseLocationID(resp, "creating organization subgroup")
}

// UpdateOrganizationGroup updates an organization group by ID.
func (c *Client) UpdateOrganizationGroup(ctx context.Context, realm, orgID, groupID string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) +
		"/groups/" + url.PathEscape(groupID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetOrganizationGroupMembers returns the members of an organization group.
func (c *Client) GetOrganizationGroupMembers(ctx context.Context, realm, orgID, groupID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) +
		"/groups/" + url.PathEscape(groupID) + "/members"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding organization group members: %w", err)
	}
	return result, nil
}

// AddOrganizationGroupMember adds a user to an organization group. The user must
// already be a member of the organization; Keycloak answers 400 otherwise.
func (c *Client) AddOrganizationGroupMember(ctx context.Context, realm, orgID, groupID, userID string) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) +
		"/groups/" + url.PathEscape(groupID) + "/members/" + url.PathEscape(userID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}
	return nil
}

// GetAuthenticationFlows returns all authentication flows in the realm.
func (c *Client) GetAuthenticationFlows(ctx context.Context, realm string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/authentication/flows"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding authentication flows: %w", err)
	}
	return result, nil
}

// CreateAuthenticationFlow creates a new top-level authentication flow.
func (c *Client) CreateAuthenticationFlow(ctx context.Context, realm string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/authentication/flows"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}
	return nil
}

// CopyAuthenticationFlow copies an existing flow, including its executions,
// under a new alias. This is how a built-in flow is customised without
// modifying it in place.
func (c *Client) CopyAuthenticationFlow(ctx context.Context, realm, sourceAlias, newName string) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/authentication/flows/" + url.PathEscape(sourceAlias) + "/copy"
	resp, err := c.doRequest(ctx, http.MethodPost, path, map[string]any{"newName": newName})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetAuthenticationFlowExecutions returns the flattened execution tree of a
// flow. Each entry carries "level" and "index" describing its position.
func (c *Client) GetAuthenticationFlowExecutions(ctx context.Context, realm, flowAlias string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/authentication/flows/" + url.PathEscape(flowAlias) + "/executions"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding authentication executions: %w", err)
	}
	return result, nil
}

// UpdateAuthenticationFlowExecution updates an execution in place, which is how
// its requirement is set after it has been added.
func (c *Client) UpdateAuthenticationFlowExecution(ctx context.Context, realm, flowAlias string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/authentication/flows/" + url.PathEscape(flowAlias) + "/executions"
	resp, err := c.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// CreateAuthenticationExecution appends an authenticator to a flow.
func (c *Client) CreateAuthenticationExecution(ctx context.Context, realm, flowAlias string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/authentication/flows/" + url.PathEscape(flowAlias) + "/executions/execution"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// CreateAuthenticationSubflow appends a nested flow to a flow.
func (c *Client) CreateAuthenticationSubflow(ctx context.Context, realm, flowAlias string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/authentication/flows/" + url.PathEscape(flowAlias) + "/executions/flow"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// CreateAuthenticationExecutionConfig attaches a configuration to an execution.
func (c *Client) CreateAuthenticationExecutionConfig(ctx context.Context, realm, executionID string, body map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/authentication/executions/" + url.PathEscape(executionID) + "/config"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// GetServerInfo returns the Keycloak server info document, which carries the
// server version under "systemInfo" and the server feature list under
// "features". It is the only source for both, and is read once at startup.
func (c *Client) GetServerInfo(ctx context.Context) (map[string]any, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/admin/serverinfo", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding server info: %w", err)
	}
	return result, nil
}

// GetAuthenticationProviders returns the providers of one authentication kind
// the server offers, such as "authenticator-providers" or
// "form-action-providers". Each entry carries an "id".
//
// The lists are server-wide rather than realm-specific, so any existing realm
// answers for all of them; callers use master, which always exists.
func (c *Client) GetAuthenticationProviders(ctx context.Context, realm, kind string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/authentication/" + url.PathEscape(kind)

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", kind, err)
	}
	return result, nil
}
