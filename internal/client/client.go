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
	if strings.HasPrefix(c.baseURL, "http://") {
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

	c.mu.Lock()
	c.accessToken = tokenResp.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn)*time.Second - refreshSlack)
	c.mu.Unlock()

	return nil
}

func (c *Client) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	expired := time.Now().After(c.expiresAt)
	c.mu.Unlock()

	if expired {
		slog.Debug("Refreshing Keycloak access token")
		return c.authenticate(ctx)
	}
	return nil
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
		if err := c.ensureToken(ctx); err != nil {
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

		c.mu.Lock()
		token := c.accessToken
		c.mu.Unlock()

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

func readError(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("unexpected status %d (failed to read body: %v)", resp.StatusCode, err)
	}
	return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
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

	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("creating client: no Location header in response")
	}
	// Location is typically: .../clients/{uuid}
	idx := strings.LastIndex(location, "/")
	if idx < 0 || idx == len(location)-1 {
		return "", fmt.Errorf("creating client: unexpected Location header format: %s", location)
	}
	return location[idx+1:], nil
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
