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
	"sync"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

const (
	defaultDeviceID  = "jellyfin-remora"
	adminDeviceID    = "jellyfin-remora-admin"
	watchdogDeviceID = "jellyfin-remora-watchdog"
)

type Client struct {
	baseURL       string
	http          *http.Client
	watchdogMu    sync.Mutex
	watchdogToken string
}

type PublicInfo struct {
	Version                string `json:"Version"`
	ServerName             string `json:"ServerName"`
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
type localizationOption struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}
type cultureOption struct {
	DisplayName              string `json:"DisplayName"`
	TwoLetterISOLanguageName string `json:"TwoLetterISOLanguageName"`
}
type countryOption struct {
	DisplayName            string `json:"DisplayName"`
	TwoLetterISORegionName string `json:"TwoLetterISORegionName"`
}
type selectionOption struct {
	label string
	value string
}
type AuthenticationInfo struct {
	AccessToken string `json:"AccessToken"`
	AppName     string `json:"AppName"`
	IsActive    bool   `json:"IsActive"`
}
type authenticationInfoQuery struct {
	Items []AuthenticationInfo `json:"Items"`
}

type sessionInfo struct {
	ID             string `json:"Id"`
	UserName       string `json:"UserName"`
	Client         string `json:"Client"`
	DeviceName     string `json:"DeviceName"`
	IsActive       bool   `json:"IsActive"`
	NowPlayingItem *struct {
		Name       string `json:"Name"`
		SeriesName string `json:"SeriesName"`
	} `json:"NowPlayingItem"`
	PlayState *struct {
		IsPaused bool `json:"IsPaused"`
	} `json:"PlayState"`
	TranscodingInfo json.RawMessage `json:"TranscodingInfo"`
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

// ProbeDatabase asks Jellyfin to execute a small authenticated read against its
// primary database. Remora deliberately never opens jellyfin.db itself.
func (c *Client) ProbeDatabase(ctx context.Context, token string) error {
	for _, path := range []string{
		"/Users?StartIndex=0&Limit=1",
		"/Items?Recursive=true&Limit=1&EnableTotalRecordCount=false",
		"/System/ActivityLog/Entries?StartIndex=0&Limit=1",
	} {
		var result json.RawMessage
		if err := c.do(ctx, http.MethodGet, path, token, nil, &result, http.StatusOK); err != nil {
			return err
		}
	}
	return nil
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
	initial, err := c.startupConfiguration(ctx, cfg)
	if err != nil {
		return "", err
	}
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

func (c *Client) startupConfiguration(ctx context.Context, cfg config.InitConfig) (map[string]any, error) {
	initial := make(map[string]any)
	if err := c.do(ctx, http.MethodGet, "/Startup/Configuration", "", nil, &initial, http.StatusOK); err != nil {
		return nil, err
	}
	var displayLanguages []localizationOption
	if err := c.do(ctx, http.MethodGet, "/Localization/Options", "", nil, &displayLanguages, http.StatusOK); err != nil {
		return nil, err
	}
	var metadataLanguages []cultureOption
	if err := c.do(ctx, http.MethodGet, "/Localization/Cultures", "", nil, &metadataLanguages, http.StatusOK); err != nil {
		return nil, err
	}
	var regions []countryOption
	if err := c.do(ctx, http.MethodGet, "/Localization/Countries", "", nil, &regions, http.StatusOK); err != nil {
		return nil, err
	}

	if cfg.ServerName != "" {
		initial["ServerName"] = cfg.ServerName
	}
	if cfg.DisplayLanguage != "" {
		options := make([]selectionOption, len(displayLanguages))
		for i, option := range displayLanguages {
			options[i] = selectionOption{label: option.Name, value: option.Value}
		}
		value, err := selectedValue("init.display-language", cfg.DisplayLanguage, options)
		if err != nil {
			return nil, err
		}
		initial["UICulture"] = value
	}
	if cfg.PreferredMetadataLanguage != "" {
		options := make([]selectionOption, len(metadataLanguages))
		for i, option := range metadataLanguages {
			options[i] = selectionOption{label: option.DisplayName, value: option.TwoLetterISOLanguageName}
		}
		value, err := selectedValue("init.preferred-metadata-language", cfg.PreferredMetadataLanguage, options)
		if err != nil {
			return nil, err
		}
		initial["PreferredMetadataLanguage"] = value
	}
	if cfg.PreferredMetadataRegion != "" {
		options := make([]selectionOption, len(regions))
		for i, option := range regions {
			options[i] = selectionOption{label: option.DisplayName, value: option.TwoLetterISORegionName}
		}
		value, err := selectedValue("init.preferred-metadata-region", cfg.PreferredMetadataRegion, options)
		if err != nil {
			return nil, err
		}
		initial["MetadataCountryCode"] = value
	}
	return initial, nil
}

func selectedValue(field, configured string, options []selectionOption) (string, error) {
	want := strings.TrimSpace(configured)
	for _, option := range options {
		if option.label == want {
			return option.value, nil
		}
	}
	return "", fmt.Errorf("%s %q is not a label offered by the Jellyfin web selection", field, configured)
}

func (c *Client) UpdateUsername(ctx context.Context, token string, user map[string]any, name string) error {
	id, _ := user["Id"].(string)
	if id == "" {
		return fmt.Errorf("Jellyfin authentication response did not include the first user ID")
	}
	user["Name"] = name
	return c.doWithDeviceID(ctx, http.MethodPost, "/Users?userId="+url.QueryEscape(id), token, user, nil, adminDeviceID, http.StatusNoContent)
}

// UpdateServerName persists the configured name after the startup wizard.
// Jellyfin 10.10 accepts ServerName in /Startup/Configuration but does not
// persist it, while newer releases do. Updating the complete system document
// is idempotent and preserves release-specific fields on both API generations.
func (c *Client) UpdateServerName(ctx context.Context, token, name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	var system map[string]any
	if err := c.doWithDeviceID(ctx, http.MethodGet, "/System/Configuration", token, nil, &system, adminDeviceID, http.StatusOK); err != nil {
		return err
	}
	system["ServerName"] = name
	return c.doWithDeviceID(ctx, http.MethodPost, "/System/Configuration", token, system, nil, adminDeviceID, http.StatusNoContent)
}

func (c *Client) Authenticate(ctx context.Context, user, password string) (AuthenticationResult, error) {
	return c.authenticateWithDeviceID(ctx, user, password, adminDeviceID)
}

func (c *Client) authenticateWithDeviceID(ctx context.Context, user, password, deviceID string) (AuthenticationResult, error) {
	var out AuthenticationResult
	err := c.doWithDeviceID(ctx, http.MethodPost, "/Users/AuthenticateByName", "", map[string]string{"Username": user, "Pw": password}, &out, deviceID, http.StatusOK)
	if err == nil && out.AccessToken == "" {
		err = fmt.Errorf("Jellyfin authentication returned an empty token")
	}
	return out, err
}

func (c *Client) EnsureAPIKey(ctx context.Context, adminToken string) (string, error) {
	keys, err := c.apiKeysWithDeviceID(ctx, adminToken, adminDeviceID)
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if key.AppName == "Jellyfin Remora" && key.AccessToken != "" {
			return key.AccessToken, nil
		}
	}
	path := "/Auth/Keys?app=" + url.QueryEscape("Jellyfin Remora")
	if err := c.doWithDeviceID(ctx, http.MethodPost, path, adminToken, nil, nil, adminDeviceID, http.StatusNoContent); err != nil {
		return "", err
	}
	keys, err = c.apiKeysWithDeviceID(ctx, adminToken, adminDeviceID)
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
	return c.apiKeysWithDeviceID(ctx, token, defaultDeviceID)
}

func (c *Client) apiKeysWithDeviceID(ctx context.Context, token, deviceID string) ([]AuthenticationInfo, error) {
	var out authenticationInfoQuery
	err := c.doWithDeviceID(ctx, http.MethodGet, "/Auth/Keys", token, nil, &out, deviceID, http.StatusOK)
	return out.Items, err
}

func (c *Client) APIKeys(ctx context.Context, token string) ([]AuthenticationInfo, error) {
	return c.apiKeys(ctx, token)
}

func (c *Client) CreateAPIKey(ctx context.Context, token, name string) error {
	return c.do(ctx, http.MethodPost, "/Auth/Keys?app="+url.QueryEscape(name), token, nil, nil, http.StatusNoContent)
}

func (c *Client) RevokeAPIKey(ctx context.Context, token, key string) error {
	const errorPath = "/Auth/Keys/{key}"
	return c.doWithDeviceIDAndErrorPath(ctx, http.MethodDelete, "/Auth/Keys/"+url.PathEscape(key), errorPath, token, nil, nil, defaultDeviceID, http.StatusNoContent)
}

func (c *Client) ValidateAPIKey(ctx context.Context, token string) error {
	_, err := c.apiKeys(ctx, token)
	return err
}

func (c *Client) Sessions(ctx context.Context, token string) ([]model.Session, error) {
	var raw []sessionInfo
	if err := c.do(ctx, http.MethodGet, "/Sessions", token, nil, &raw, http.StatusOK); err != nil {
		return nil, err
	}
	sessions := make([]model.Session, 0, len(raw))
	for _, item := range raw {
		if item.ID == "" || item.UserName == "" || !item.IsActive {
			continue
		}
		status := "idle"
		media := ""
		if item.NowPlayingItem != nil {
			media = item.NowPlayingItem.Name
			if item.NowPlayingItem.SeriesName != "" && item.NowPlayingItem.SeriesName != media {
				media = item.NowPlayingItem.SeriesName + " — " + media
			}
			status = "playing"
			if item.PlayState != nil && item.PlayState.IsPaused {
				status = "paused"
			}
		}
		device := item.Client
		if item.DeviceName != "" && !strings.EqualFold(item.DeviceName, item.Client) {
			if device == "" {
				device = item.DeviceName
			} else {
				device += " (" + item.DeviceName + ")"
			}
		}
		transcoding := len(item.TranscodingInfo) > 0 && string(item.TranscodingInfo) != "null"
		sessions = append(sessions, model.Session{ID: item.ID, Status: status, User: item.UserName, Device: device, Media: media, Transcoding: transcoding})
	}
	return sessions, nil
}

func (c *Client) StopSession(ctx context.Context, token, sessionID string) error {
	return c.do(ctx, http.MethodPost, "/Sessions/"+url.PathEscape(sessionID)+"/Playing/Stop", token, nil, nil, http.StatusNoContent)
}

func (c *Client) Shutdown(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("Jellyfin API key is unavailable")
	}
	return c.do(ctx, http.MethodPost, "/System/Shutdown", token, nil, nil, http.StatusNoContent, http.StatusOK)
}

func (c *Client) EnsureWatchdogUser(ctx context.Context, adminToken string, cfg config.UserLoginWatchdogConfig) error {
	if !cfg.Enabled {
		return nil
	}
	c.watchdogMu.Lock()
	defer c.watchdogMu.Unlock()

	if c.watchdogToken != "" {
		err := c.checkWatchdogSession(ctx, c.watchdogToken)
		if err == nil {
			return nil
		}
		if !isAuthenticationFailure(err) {
			return err
		}
		c.watchdogToken = ""
	}

	auth, err := c.authenticateWithDeviceID(ctx, cfg.User, cfg.Password, watchdogDeviceID)
	if err != nil {
		if !isAuthenticationFailure(err) {
			return err
		}
		if adminToken == "" {
			return fmt.Errorf("watchdog user %q is missing and no administrator token is available", cfg.User)
		}
		if err := c.doWithDeviceID(ctx, http.MethodPost, "/Users/New", adminToken, map[string]string{"Name": cfg.User, "Password": cfg.Password}, nil, adminDeviceID, http.StatusOK); err != nil {
			return err
		}
		auth, err = c.authenticateWithDeviceID(ctx, cfg.User, cfg.Password, watchdogDeviceID)
		if err != nil {
			return err
		}
	}
	c.watchdogToken = auth.AccessToken
	err = c.checkWatchdogSession(ctx, auth.AccessToken)
	if isAuthenticationFailure(err) {
		c.watchdogToken = ""
	}
	return err
}

func (c *Client) checkWatchdogSession(ctx context.Context, token string) error {
	var me map[string]any
	return c.doWithDeviceID(ctx, http.MethodGet, "/Users/Me", token, nil, &me, watchdogDeviceID, http.StatusOK)
}

func isAuthenticationFailure(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

func (c *Client) do(ctx context.Context, method, path, token string, body, out any, expected ...int) error {
	return c.doWithDeviceID(ctx, method, path, token, body, out, defaultDeviceID, expected...)
}

func (c *Client) doWithDeviceID(ctx context.Context, method, path, token string, body, out any, deviceID string, expected ...int) error {
	return c.doWithDeviceIDAndErrorPath(ctx, method, path, path, token, body, out, deviceID, expected...)
}

func (c *Client) doWithDeviceIDAndErrorPath(ctx context.Context, method, path, errorPath, token string, body, out any, deviceID string, expected ...int) error {
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
	auth := `MediaBrowser Client="Jellyfin%20Remora", DeviceId="` + deviceID + `", Device="Jellyfin%20Remora", Version="dev"`
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
		return &APIError{StatusCode: resp.StatusCode, Method: method, Path: errorPath, Message: strings.TrimSpace(string(data))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
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
