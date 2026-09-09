// Package client is a Keycloak Admin REST API adapter built on net/http,
// with no Keycloak SDK. Every payload is an untyped map[string]any so the
// provisioner can send sparse bodies and let Keycloak keep the fields it is
// not asked to change.
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

// Protocol mapper containers. Mappers hang off a client or a client scope, and
// both expose the identical /protocol-mappers/models sub-resource; only the
// parent segment of the URL differs.
const (
	MapperContainerClients      = "clients"
	MapperContainerClientScopes = "client-scopes"
)

func protocolMapperPath(realm, container, containerID string) string {
	return "/admin/realms/" + url.PathEscape(realm) + "/" + url.PathEscape(container) +
		"/" + url.PathEscape(containerID) + "/protocol-mappers/models"
}

// GetProtocolMappers returns the protocol mappers of a client or client scope.
// container is MapperContainerClients or MapperContainerClientScopes.
func (c *Client) GetProtocolMappers(ctx context.Context, realm, container, containerID string) ([]map[string]any, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, protocolMapperPath(realm, container, containerID), nil)
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

// CreateProtocolMapper creates a protocol mapper on a client or client scope.
func (c *Client) CreateProtocolMapper(ctx context.Context, realm, container, containerID string, body map[string]any) error {
	resp, err := c.doRequest(ctx, http.MethodPost, protocolMapperPath(realm, container, containerID), body)
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
func (c *Client) UpdateProtocolMapper(ctx context.Context, realm, container, containerID, mapperID string, body map[string]any) error {
	path := protocolMapperPath(realm, container, containerID) + "/" + url.PathEscape(mapperID)

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

// GetRequiredActions returns the required actions registered in a realm.
//
// The set is server-wide in practice — realms are seeded from the same
// providers — so callers validating config can read it from any realm.
func (c *Client) GetRequiredActions(ctx context.Context, realm string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/authentication/required-actions"

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
		return nil, fmt.Errorf("decoding required actions: %w", err)
	}

	return result, nil
}

// GetUserCredentials returns the credentials a user holds.
//
// Secrets are not included — Keycloak returns the type, id, label and public
// metadata only, which is what the reconciler matches on to decide whether a
// credential is already present.
func (c *Client) GetUserCredentials(ctx context.Context, realm, userID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/users/" + url.PathEscape(userID) + "/credentials"

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
		return nil, fmt.Errorf("decoding user credentials: %w", err)
	}

	return result, nil
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

// Role mapping subjects. Keycloak's role-mapping endpoint is the same shape for
// both: /{users|groups}/{id}/role-mappings/...
const (
	RoleSubjectUsers  = "users"
	RoleSubjectGroups = "groups"
)

func roleMappingPath(realm, subject, subjectID string) string {
	return "/admin/realms/" + url.PathEscape(realm) + "/" + url.PathEscape(subject) +
		"/" + url.PathEscape(subjectID) + "/role-mappings"
}

func (c *Client) getRoleMappings(ctx context.Context, path, what string) ([]map[string]any, error) {
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

func (c *Client) addRoleMappings(ctx context.Context, path string, roles []map[string]any) error {
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

// GetRealmRoleMappings returns the realm roles mapped to a user or group.
// subject is RoleSubjectUsers or RoleSubjectGroups.
func (c *Client) GetRealmRoleMappings(ctx context.Context, realm, subject, subjectID string) ([]map[string]any, error) {
	return c.getRoleMappings(ctx, roleMappingPath(realm, subject, subjectID)+"/realm", "realm role mappings")
}

// AddRealmRoleMappings grants realm roles to a user or group.
func (c *Client) AddRealmRoleMappings(ctx context.Context, realm, subject, subjectID string, roles []map[string]any) error {
	return c.addRoleMappings(ctx, roleMappingPath(realm, subject, subjectID)+"/realm", roles)
}

// GetClientRoleMappings returns the roles of one client mapped to a user or group.
func (c *Client) GetClientRoleMappings(ctx context.Context, realm, subject, subjectID, clientUUID string) ([]map[string]any, error) {
	path := roleMappingPath(realm, subject, subjectID) + "/clients/" + url.PathEscape(clientUUID)

	return c.getRoleMappings(ctx, path, "client role mappings")
}

// AddClientRoleMappings grants roles of one client to a user or group.
func (c *Client) AddClientRoleMappings(ctx context.Context, realm, subject, subjectID, clientUUID string, roles []map[string]any) error {
	path := roleMappingPath(realm, subject, subjectID) + "/clients/" + url.PathEscape(clientUUID)

	return c.addRoleMappings(ctx, path, roles)
}

// Scope mapping owners. Keycloak's scope-mapping endpoint is the same shape for
// both: /{clients|client-scopes}/{id}/scope-mappings/...
const (
	ScopeOwnerClients      = "clients"
	ScopeOwnerClientScopes = "client-scopes"
)

func scopeMappingPath(realm, owner, ownerID string) string {
	return "/admin/realms/" + url.PathEscape(realm) + "/" + url.PathEscape(owner) +
		"/" + url.PathEscape(ownerID) + "/scope-mappings"
}

// GetRealmScopeMappings returns the realm roles in the scope of a client or a
// client scope. owner is ScopeOwnerClients or ScopeOwnerClientScopes.
//
// The listing is of directly assigned roles, which is what the reconciler
// compares against: the sibling /available and /composite sub-resources answer
// different questions and would make an already-applied mapping look missing.
func (c *Client) GetRealmScopeMappings(ctx context.Context, realm, owner, ownerID string) ([]map[string]any, error) {
	return c.getRoleMappings(ctx, scopeMappingPath(realm, owner, ownerID)+"/realm", "realm scope mappings")
}

// AddRealmScopeMappings puts realm roles into the scope of a client or a client scope.
func (c *Client) AddRealmScopeMappings(ctx context.Context, realm, owner, ownerID string, roles []map[string]any) error {
	return c.addRoleMappings(ctx, scopeMappingPath(realm, owner, ownerID)+"/realm", roles)
}

// GetClientScopeMappings returns the roles of one client that are in the scope
// of a client or a client scope.
func (c *Client) GetClientScopeMappings(ctx context.Context, realm, owner, ownerID, clientUUID string) ([]map[string]any, error) {
	path := scopeMappingPath(realm, owner, ownerID) + "/clients/" + url.PathEscape(clientUUID)

	return c.getRoleMappings(ctx, path, "client scope mappings")
}

// AddClientScopeMappings puts roles of one client into the scope of a client or
// a client scope.
func (c *Client) AddClientScopeMappings(ctx context.Context, realm, owner, ownerID, clientUUID string, roles []map[string]any) error {
	path := scopeMappingPath(realm, owner, ownerID) + "/clients/" + url.PathEscape(clientUUID)

	return c.addRoleMappings(ctx, path, roles)
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

// deleteClientScopeAssignment is the shared writer for detaching a client
// scope from one of those four lists. Like the PUTs it is idempotent, and
// Keycloak answers 204 whether or not the scope was attached.
func (c *Client) deleteClientScopeAssignment(ctx context.Context, path string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
}

// Client scope assignment kinds. Keycloak spells the realm-level and
// client-level endpoints differently, but both distinguish the same two kinds,
// and they are the values config.ClientScope.Type already carries.
const (
	ClientScopeDefault  = "default"
	ClientScopeOptional = "optional"
)

// realmClientScopePath is the realm-level default/optional scope endpoint.
// Keycloak names these "default-default-" and "default-optional-".
func realmClientScopePath(realm, kind string) string {
	return "/admin/realms/" + url.PathEscape(realm) + "/default-" + url.PathEscape(kind) + "-client-scopes"
}

func clientScopePath(realm, clientUUID, kind string) string {
	return "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) +
		"/" + url.PathEscape(kind) + "-client-scopes"
}

// GetRealmClientScopes returns the realm's default or optional client scopes.
// Default scopes are the ones Keycloak assigns to every newly created client.
func (c *Client) GetRealmClientScopes(ctx context.Context, realm, kind string) ([]map[string]any, error) {
	return c.listClientScopeAssignments(ctx, realmClientScopePath(realm, kind), "realm "+kind+" client scopes")
}

// AddRealmClientScope adds a client scope to the realm's defaults or optionals.
func (c *Client) AddRealmClientScope(ctx context.Context, realm, scopeID, kind string) error {
	return c.putClientScopeAssignment(ctx, realmClientScopePath(realm, kind)+"/"+url.PathEscape(scopeID))
}

// RemoveRealmClientScope detaches a client scope from the realm's defaults or
// optionals. It is needed to change a scope's type: an assignment conflicting
// with the existing one is rejected with 409, so the scope has to leave the
// other list first.
func (c *Client) RemoveRealmClientScope(ctx context.Context, realm, scopeID, kind string) error {
	return c.deleteClientScopeAssignment(ctx, realmClientScopePath(realm, kind)+"/"+url.PathEscape(scopeID))
}

// GetClientScopeAssignments returns the default or optional scopes assigned to
// a client.
func (c *Client) GetClientScopeAssignments(ctx context.Context, realm, clientUUID, kind string) ([]map[string]any, error) {
	return c.listClientScopeAssignments(ctx, clientScopePath(realm, clientUUID, kind), "client "+kind+" scopes")
}

// AddClientScopeAssignment assigns a client scope to a client as a default or
// optional scope.
func (c *Client) AddClientScopeAssignment(ctx context.Context, realm, clientUUID, scopeID, kind string) error {
	return c.putClientScopeAssignment(ctx, clientScopePath(realm, clientUUID, kind)+"/"+url.PathEscape(scopeID))
}

// RemoveClientScopeAssignment detaches a client scope from a client's defaults
// or optionals, for the same reason RemoveRealmClientScope exists.
func (c *Client) RemoveClientScopeAssignment(ctx context.Context, realm, clientUUID, scopeID, kind string) error {
	return c.deleteClientScopeAssignment(ctx, clientScopePath(realm, clientUUID, kind)+"/"+url.PathEscape(scopeID))
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

// GetOrganization returns one organization's full representation.
//
// The search listing omits "attributes", so an update that has to merge them
// needs this instead. Everything else the update must preserve — alias,
// domains, redirectUrl — is in both.
func (c *Client) GetOrganization(ctx context.Context, realm, orgID string) (map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID)

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
		return nil, fmt.Errorf("decoding organization: %w", err)
	}

	return result, nil
}

// GetOrganizationMembers returns the members of an organization.
func (c *Client) GetOrganizationMembers(ctx context.Context, realm, orgID string) ([]map[string]any, error) {
	// max=-1 asks for every member. Keycloak defaults this endpoint to 10,
	// which silently truncates the caller's view of who is already a member —
	// it then re-adds the rest and Keycloak answers 409. Among the listings
	// this client uses, only this one is capped: organization groups, their
	// members, organization identity providers, a user's groups, and the
	// realm-level listings all return in full.
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) + "/members?max=-1"
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

// GetOrganizationGroups returns the groups of an organization at one level.
// parentID is "" for top-level groups; otherwise the children of that group are
// returned.
//
// Organization groups live in a namespace of their own: they do not appear
// under the realm's groups, and Keycloak refuses to manage them through the
// normal group API. The listing never populates "subGroups", which is why
// descending needs a call per level.
func (c *Client) GetOrganizationGroups(ctx context.Context, realm, orgID, parentID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) + "/groups"
	if parentID != "" {
		path += "/" + url.PathEscape(parentID) + "/children"
	}

	// max=-1 asks for every group at this level. The children endpoint defaults
	// to 10, and callers match by name over the whole listing rather than
	// asking Keycloak to filter — the realm group API takes an exact search,
	// this one does not — so a truncated page reads as "not there" and the
	// caller tries to create a sibling that already exists.
	path += "?max=-1"

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

// CreateOrganizationGroup creates a group in an organization and returns its
// UUID from the Location header. parentID is "" for a top-level group.
//
// Keycloak rejects a duplicate name with 409, so callers must check the listing
// first.
func (c *Client) CreateOrganizationGroup(ctx context.Context, realm, orgID, parentID string, body map[string]any) (string, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) + "/groups"
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
	return parseLocationID(resp, "creating organization group")
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

// PartialImportUsers creates users through Keycloak's partial import, which is
// the only way to give a user a chosen id.
//
// The create-user endpoint accepts an "id" in the representation and silently
// discards it, generating its own — verified against 26.6. Partial import
// honours it.
//
// ifResourceExists is SKIP, so a username that already exists is left exactly
// as it is and the call stays idempotent. Import is therefore only useful for
// creating; everything afterwards goes through the normal endpoints.
func (c *Client) PartialImportUsers(ctx context.Context, realm string, users []map[string]any) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/partialImport"

	body := map[string]any{
		"ifResourceExists": "SKIP",
		"users":            users,
	}

	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}
	return nil
}

// identityProviderPath is the instances endpoint for one realm.
func identityProviderPath(realm string) string {
	return "/admin/realms/" + url.PathEscape(realm) + "/identity-provider/instances"
}

// GetIdentityProviders returns every identity provider in the realm.
//
// The listing carries each provider's full representation, including its config
// and the organization it belongs to, so one call per realm is enough and
// nothing has to be re-read per alias.
//
// realmOnly is deliberately not set: it hides providers that belong to an
// organization, and a caller that could not see them would try to create one
// that already exists and get a 409 on every run.
func (c *Client) GetIdentityProviders(ctx context.Context, realm string) ([]map[string]any, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, identityProviderPath(realm), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding identity providers: %w", err)
	}
	return result, nil
}

// CreateIdentityProvider creates an identity provider.
//
// A duplicate alias is a 409, so callers must check the listing first. The
// Location header carries the alias the caller already knows, so nothing is
// parsed out of it.
func (c *Client) CreateIdentityProvider(ctx context.Context, realm string, body map[string]any) error {
	resp, err := c.doRequest(ctx, http.MethodPost, identityProviderPath(realm), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}
	return nil
}

// UpdateIdentityProvider replaces an identity provider.
//
// Keycloak replaces the whole representation: a field or config key left out of
// body is removed, not left alone. Callers must send the merged result of the
// current representation and their changes. The body's alias must equal the one
// in the path — a different alias renames the provider.
func (c *Client) UpdateIdentityProvider(ctx context.Context, realm, alias string, body map[string]any) error {
	path := identityProviderPath(realm) + "/" + url.PathEscape(alias)

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

// GetIdentityProviderMappers returns the mappers of an identity provider.
func (c *Client) GetIdentityProviderMappers(ctx context.Context, realm, alias string) ([]map[string]any, error) {
	path := identityProviderPath(realm) + "/" + url.PathEscape(alias) + "/mappers"

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
		return nil, fmt.Errorf("decoding identity provider mappers: %w", err)
	}
	return result, nil
}

// CreateIdentityProviderMapper creates a mapper on an identity provider.
// A duplicate name is a 400 rather than a 409, so callers must look up by name
// first.
func (c *Client) CreateIdentityProviderMapper(ctx context.Context, realm, alias string, body map[string]any) error {
	path := identityProviderPath(realm) + "/" + url.PathEscape(alias) + "/mappers"

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

// UpdateIdentityProviderMapper replaces a mapper.
//
// body must carry id, name, identityProviderAlias and identityProviderMapper:
// without the id Keycloak answers 500, and without the mapper type it answers
// 409.
func (c *Client) UpdateIdentityProviderMapper(ctx context.Context, realm, alias, mapperID string, body map[string]any) error {
	path := identityProviderPath(realm) + "/" + url.PathEscape(alias) + "/mappers/" + url.PathEscape(mapperID)

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

// GetOrganizationIdentityProviders returns the providers associated with an
// organization.
func (c *Client) GetOrganizationIdentityProviders(ctx context.Context, realm, orgID string) ([]map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) + "/identity-providers"

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
		return nil, fmt.Errorf("decoding organization identity providers: %w", err)
	}
	return result, nil
}

// AddOrganizationIdentityProvider associates an existing provider with an
// organization.
//
// Like AddOrganizationMember, the body is a bare JSON string rather than an
// object; an object is rejected with 400. A provider already associated
// elsewhere is also a 400, and one already associated here is a 409.
func (c *Client) AddOrganizationIdentityProvider(ctx context.Context, realm, orgID, alias string) error {
	path := "/admin/realms/" + url.PathEscape(realm) + "/organizations/" + url.PathEscape(orgID) + "/identity-providers"

	resp, err := c.doRequest(ctx, http.MethodPost, path, alias)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}
	return nil
}

// Fine-grained admin permissions (the v1 model).
//
// Two resources are involved and they live on different clients. The
// enable/disable switch and the scope permission ids hang off the *target*
// client, at /management/permissions. The permissions themselves, and the
// policies that satisfy them, are authorization objects on the
// *realm-management* client's resource server. Every method below takes the
// UUID of whichever client owns the object it touches.

// authzPath builds a path under a resource server owned by resourceServerUUID,
// which is always the realm-management client for fine-grained admin
// permissions.
func authzPath(realm, resourceServerUUID, suffix string) string {
	return "/admin/realms/" + url.PathEscape(realm) +
		"/clients/" + url.PathEscape(resourceServerUUID) +
		"/authz/resource-server" + suffix
}

// GetClientManagementPermissions returns the client's fine-grained permission
// state. When they are disabled the representation is just {"enabled": false};
// when enabled it also carries "scopePermissions", mapping each scope name to
// the id of the scope permission that implements it.
func (c *Client) GetClientManagementPermissions(ctx context.Context, realm, clientUUID string) (map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/management/permissions"
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
		return nil, fmt.Errorf("decoding client management permissions: %w", err)
	}
	return result, nil
}

// SetClientManagementPermissions enables fine-grained permissions on the client
// and returns the resulting representation, including the scope permission ids.
//
// It is idempotent: enabling permissions that are already enabled returns the
// same ids rather than creating new ones.
func (c *Client) SetClientManagementPermissions(ctx context.Context, realm, clientUUID string, enabled bool) (map[string]any, error) {
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + url.PathEscape(clientUUID) + "/management/permissions"
	resp, err := c.doRequest(ctx, http.MethodPut, path, map[string]any{"enabled": enabled})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding client management permissions: %w", err)
	}
	return result, nil
}

// GetAuthzClientPolicies searches the resource server's client policies by
// name.
//
// Keycloak matches the name as a substring, so a search for "a.b" also returns
// "x.a.b.y". Callers that need one policy must compare names themselves.
func (c *Client) GetAuthzClientPolicies(ctx context.Context, realm, resourceServerUUID, name string) ([]map[string]any, error) {
	path := authzPath(realm, resourceServerUUID, "/policy/client?name="+url.QueryEscape(name))
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
		return nil, fmt.Errorf("decoding client policies: %w", err)
	}
	return result, nil
}

// CreateAuthzClientPolicy creates a client policy and returns its id.
//
// Unlike most create endpoints here the id comes from the response body, not a
// Location header. A name already in use is rejected with 409.
func (c *Client) CreateAuthzClientPolicy(ctx context.Context, realm, resourceServerUUID string, body map[string]any) (string, error) {
	path := authzPath(realm, resourceServerUUID, "/policy/client")
	resp, err := c.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", readError(resp)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding created client policy: %w", err)
	}

	id, ok := result["id"].(string)
	if !ok || id == "" {
		return "", fmt.Errorf("creating client policy: response carried no id")
	}
	return id, nil
}

// UpdateAuthzClientPolicy replaces a client policy. The "clients" list it
// carries is authoritative: the policy afterwards names exactly those clients.
func (c *Client) UpdateAuthzClientPolicy(ctx context.Context, realm, resourceServerUUID, policyID string, body map[string]any) error {
	path := authzPath(realm, resourceServerUUID, "/policy/client/"+url.PathEscape(policyID))
	return c.doAuthzWrite(ctx, http.MethodPut, path, body, "updating client policy")
}

// GetAuthzScopePermission returns one scope permission.
//
// The representation does NOT include the policies attached to it — "policies"
// is accepted on write and absent on read. Use GetAuthzAssociatedPolicies to
// find out what is currently attached.
func (c *Client) GetAuthzScopePermission(ctx context.Context, realm, resourceServerUUID, permissionID string) (map[string]any, error) {
	path := authzPath(realm, resourceServerUUID, "/permission/scope/"+url.PathEscape(permissionID))
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
		return nil, fmt.Errorf("decoding scope permission: %w", err)
	}
	return result, nil
}

// GetAuthzAssociatedPolicies returns the policies currently attached to a
// permission. It is the only way to read them: the permission's own
// representation omits them.
func (c *Client) GetAuthzAssociatedPolicies(ctx context.Context, realm, resourceServerUUID, permissionID string) ([]map[string]any, error) {
	path := authzPath(realm, resourceServerUUID, "/policy/"+url.PathEscape(permissionID)+"/associatedPolicies")
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
		return nil, fmt.Errorf("decoding associated policies: %w", err)
	}
	return result, nil
}

// UpdateAuthzScopePermission replaces a scope permission. The "policies" list
// it carries is authoritative — a policy left out is detached — so callers must
// send the full intended set, not just the one they are adding.
func (c *Client) UpdateAuthzScopePermission(ctx context.Context, realm, resourceServerUUID, permissionID string, body map[string]any) error {
	path := authzPath(realm, resourceServerUUID, "/permission/scope/"+url.PathEscape(permissionID))
	return c.doAuthzWrite(ctx, http.MethodPut, path, body, "updating scope permission")
}

// doAuthzWrite performs a write against the authorization endpoints, which do
// not agree with the rest of the admin API on what a successful write returns:
// these answer 201 where 204 is conventional elsewhere. Any 2xx is accepted
// rather than pinning one, since the choice varies by endpoint and release.
func (c *Client) doAuthzWrite(ctx context.Context, method, path string, body any, what string) error {
	resp, err := c.doRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: %w", what, readError(resp))
	}
	return nil
}
