package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

type Client struct {
	baseURL string
	http    *http.Client
}

type PublicInfo struct {
	Version                string `json:"Version"`
	StartupWizardCompleted *bool  `json:"StartupWizardCompleted"`
}
type AuthenticationResult struct {
	AccessToken string         `json:"AccessToken"`
	ServerID    string         `json:"ServerId"`
	User        map[string]any `json:"User"`
}
type StartupUser struct {
	Name string `json:"Name"`
}
type AuthenticationInfo struct {
	AccessToken string `json:"AccessToken"`
	AppName     string `json:"AppName"`
	IsActive    bool   `json:"IsActive"`
}
type authenticationInfoQuery struct {
	Items []AuthenticationInfo `json:"Items"`
}
type APIError struct {
	StatusCode            int
	Method, Path, Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Jellyfin %s %s returned %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: timeout}}
}

func (c *Client) PublicInfo(ctx context.Context) (PublicInfo, error) {
	var out PublicInfo
	err := c.do(ctx, http.MethodGet, "/System/Info/Public", "", nil, &out, http.StatusOK)
	return out, err
}

func (c *Client) CompleteStartup(ctx context.Context, cfg config.InitConfig) (string, error) {
	if cfg.User == "" || cfg.Password == "" {
		return "", fmt.Errorf("init.user and init.password are required for first startup")
	}
	var first StartupUser
	if err := c.do(ctx, http.MethodGet, "/Startup/User", "", nil, &first, http.StatusOK); err != nil {
		return "", err
	}
	bootstrapUser := first.Name
	if bootstrapUser == "" {
		bootstrapUser = cfg.User
	}
	initial := map[string]any{"ServerName": cfg.ServerName, "UICulture": languageCode(cfg.DisplayLanguage), "MetadataCountryCode": regionCode(cfg.PreferredMetadataRegion), "PreferredMetadataLanguage": languageCode(cfg.PreferredMetadataLanguage)}
	if err := c.do(ctx, http.MethodPost, "/Startup/Configuration", "", initial, nil, http.StatusNoContent); err != nil {
		return "", err
	}
	// Jellyfin 12 seeds the first user from the OS account. Its setup endpoint
	// can set that user's password but cannot rename it in the same request.
	if err := c.do(ctx, http.MethodPost, "/Startup/User", "", map[string]any{"Name": bootstrapUser, "Password": cfg.Password}, nil, http.StatusNoContent); err != nil {
		return "", err
	}
	if err := c.do(ctx, http.MethodPost, "/Startup/RemoteAccess", "", map[string]any{"EnableRemoteAccess": cfg.AllowRemoteConnections}, nil, http.StatusNoContent); err != nil {
		return "", err
	}
	if err := c.do(ctx, http.MethodPost, "/Startup/Complete", "", nil, nil, http.StatusNoContent); err != nil {
		return "", err
	}
	return bootstrapUser, nil
}

func (c *Client) UpdateUsername(ctx context.Context, token string, user map[string]any, name string) error {
	id, _ := user["Id"].(string)
	if id == "" {
		return fmt.Errorf("Jellyfin authentication response did not include the first user ID")
	}
	user["Name"] = name
	return c.do(ctx, http.MethodPost, "/Users?userId="+url.QueryEscape(id), token, user, nil, http.StatusNoContent)
}

func (c *Client) Authenticate(ctx context.Context, user, password string) (AuthenticationResult, error) {
	var out AuthenticationResult
	err := c.do(ctx, http.MethodPost, "/Users/AuthenticateByName", "", map[string]string{"Username": user, "Pw": password}, &out, http.StatusOK)
	if err == nil && out.AccessToken == "" {
		err = fmt.Errorf("Jellyfin authentication returned an empty token")
	}
	return out, err
}

func (c *Client) EnsureAPIKey(ctx context.Context, adminToken string) (string, error) {
	keys, err := c.apiKeys(ctx, adminToken)
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if key.AppName == "Jellyfin Remora" && key.AccessToken != "" {
			return key.AccessToken, nil
		}
	}
	path := "/Auth/Keys?app=" + url.QueryEscape("Jellyfin Remora")
	if err := c.do(ctx, http.MethodPost, path, adminToken, nil, nil, http.StatusNoContent); err != nil {
		return "", err
	}
	keys, err = c.apiKeys(ctx, adminToken)
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if key.AppName == "Jellyfin Remora" && key.AccessToken != "" {
			return key.AccessToken, nil
		}
	}
	return "", fmt.Errorf("Jellyfin created the Remora API key but did not return it in the key list")
}
func (c *Client) apiKeys(ctx context.Context, token string) ([]AuthenticationInfo, error) {
	var out authenticationInfoQuery
	err := c.do(ctx, http.MethodGet, "/Auth/Keys", token, nil, &out, http.StatusOK)
	return out.Items, err
}

func (c *Client) ValidateAPIKey(ctx context.Context, token string) error {
	_, err := c.apiKeys(ctx, token)
	return err
}

func (c *Client) EnsureWatchdogUser(ctx context.Context, adminToken string, cfg config.UserLoginWatchdogConfig) error {
	if !cfg.Enabled {
		return nil
	}
	auth, err := c.Authenticate(ctx, cfg.User, cfg.Password)
	if err != nil {
		var apiErr *APIError
		if !errors.As(err, &apiErr) || (apiErr.StatusCode != http.StatusUnauthorized && apiErr.StatusCode != http.StatusForbidden) {
			return err
		}
		if adminToken == "" {
			return fmt.Errorf("watchdog user %q is missing and no administrator token is available", cfg.User)
		}
		if err := c.do(ctx, http.MethodPost, "/Users/New", adminToken, map[string]string{"Name": cfg.User, "Password": cfg.Password}, nil, http.StatusOK); err != nil {
			return err
		}
		auth, err = c.Authenticate(ctx, cfg.User, cfg.Password)
		if err != nil {
			return err
		}
	}
	var me map[string]any
	if err := c.do(ctx, http.MethodGet, "/Users/Me", auth.AccessToken, nil, &me, http.StatusOK); err != nil {
		return err
	}
	_ = c.do(ctx, http.MethodPost, "/Sessions/Logout", auth.AccessToken, nil, nil, http.StatusNoContent)
	return nil
}

func (c *Client) do(ctx context.Context, method, path, token string, body, out any, expected ...int) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	auth := `MediaBrowser Client="Jellyfin%20Remora", DeviceId="jellyfin-remora", Device="Jellyfin%20Remora", Version="dev"`
	if token != "" {
		auth += `, Token="` + token + `"`
	}
	req.Header.Set("Authorization", auth)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	accepted := false
	for _, code := range expected {
		if resp.StatusCode == code {
			accepted = true
			break
		}
	}
	if !accepted {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &APIError{StatusCode: resp.StatusCode, Method: method, Path: path, Message: strings.TrimSpace(string(data))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
}

func languageCode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "english", "en", "en-us":
		return "en-US"
	case "chinese", "简体中文", "zh", "zh-cn":
		return "zh-CN"
	default:
		return value
	}
}
func regionCode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "united states", "us", "usa":
		return "US"
	case "china", "cn":
		return "CN"
	default:
		return value
	}
}

func (c *Client) Health(ctx context.Context) model.HealthResult {
	r := model.HealthResult{CheckedAt: time.Now()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	resp, err := c.http.Do(req)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	r.StatusCode = resp.StatusCode
	r.Healthy = resp.StatusCode >= 200 && resp.StatusCode < 300
	if !r.Healthy {
		r.Error = fmt.Sprintf("health endpoint returned %s", resp.Status)
	}
	return r
}
