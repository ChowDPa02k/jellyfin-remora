package control

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/buildinfo"
	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/supervisor"
)

type Server struct {
	cfg        *config.Config
	supervisor Controller
	log        *slog.Logger
	tcp        *http.Server
	local      *http.Server
	operations atomic.Uint64
	configPath string
	logPath    string
}

type Controller interface {
	Status() model.Status
	Events(int) []model.Event
	Submit(context.Context, supervisor.Action, bool) error
	APIKeys(context.Context) ([]model.APIKey, error)
	CreateAPIKey(context.Context, string) (model.APIKey, error)
	DeleteAPIKey(context.Context, string) error
	Sessions(context.Context) ([]model.Session, error)
	StopSession(context.Context, string) error
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	OperationID string `json:"operation_id"`
}

type EventResponse struct {
	Events []model.Event `json:"events"`
}

type Options struct {
	ConfigPath string
	LogPath    string
}

type LogResponse struct {
	Source    string   `json:"source"`
	Path      string   `json:"path"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

type ConfigResponse struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type APIKeysResponse struct {
	Keys []model.APIKey `json:"keys"`
}

type SessionsResponse struct {
	Sessions []model.Session `json:"sessions"`
}

type DiagnosticBundle struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Build       buildinfo.Info   `json:"build"`
	Status      model.Status     `json:"status"`
	Events      []model.Event    `json:"events"`
	Config      DiagnosticConfig `json:"config"`
	Logs        LogResponse      `json:"logs"`
}

type DiagnosticConfig struct {
	Version      int      `json:"version"`
	Control      string   `json:"control"`
	UnixSocket   string   `json:"unix_socket"`
	NamedPipe    string   `json:"named_pipe,omitempty"`
	DataDir      string   `json:"data_dir"`
	JellyfinPath string   `json:"jellyfin_path"`
	Storage      []string `json:"storage"`
}

var diagnosticSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(access token\s+)[A-Za-z0-9._~-]+`),
	regexp.MustCompile(`(?i)("?(?:password|access_token|api_key|token)"?\s*[:=]\s*"?)[^\s,"]+`),
}

func New(cfg *config.Config, s Controller, log *slog.Logger) *Server {
	return NewWithOptions(cfg, s, log, Options{})
}

func NewWithOptions(cfg *config.Config, s Controller, log *slog.Logger, options Options) *Server {
	if options.ConfigPath == "" {
		options.ConfigPath = filepath.Join(cfg.Jellyfin.ConfigDir, "config.yaml")
	}
	if options.LogPath == "" {
		options.LogPath = filepath.Join(cfg.Remora.Logs.Path, "jellyfin-remora.log")
	}
	return &Server{cfg: cfg, supervisor: s, log: log, configPath: options.ConfigPath, logPath: options.LogPath}
}

func (s *Server) Run(ctx context.Context) error {
	h := s.handler()
	s.tcp = managedHTTPServer(net.JoinHostPort(s.cfg.RESTAPI.Listen, strconv.Itoa(s.cfg.RESTAPI.Port)), h)
	s.local = managedHTTPServer("", h)
	localListener, localDescription, err := listenLocalControl(s.cfg, s.log)
	if err != nil {
		return err
	}
	tl, err := net.Listen("tcp", s.tcp.Addr)
	if err != nil {
		localListener.Close()
		return fmt.Errorf("listen REST API: %w", err)
	}
	errCh := make(chan error, 2)
	go func() { errCh <- s.local.Serve(localListener) }()
	go func() { errCh <- s.tcp.Serve(tl) }()
	s.log.Info("control API listening", "tcp", s.tcp.Addr, "local", localDescription)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.tcp.Shutdown(shutdownCtx)
		_ = s.local.Shutdown(shutdownCtx)
		cleanupLocalControl(s.cfg)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, s.supervisor.Status()) })
	mux.HandleFunc("GET /v1/events", s.listEvents)
	mux.HandleFunc("GET /v1/logs", s.listLogs)
	mux.HandleFunc("GET /v1/config", s.configInfo)
	mux.HandleFunc("GET /v1/diagnostics", s.diagnostics)
	mux.HandleFunc("GET /v1/apikeys", s.listAPIKeys)
	mux.HandleFunc("POST /v1/apikeys", s.createAPIKey)
	mux.HandleFunc("DELETE /v1/apikeys/{id}", s.deleteAPIKey)
	mux.HandleFunc("GET /v1/sessions", s.listSessions)
	mux.HandleFunc("POST /v1/sessions/{id}/stop", s.stopSession)
	mux.HandleFunc("POST /v1/start", s.action(supervisor.ActionStart))
	mux.HandleFunc("POST /v1/stop", s.action(supervisor.ActionStop))
	mux.HandleFunc("POST /v1/restart", s.action(supervisor.ActionRestart))
	mux.HandleFunc("POST /v1/healthcheck", s.action(supervisor.ActionHealthcheck))
	for _, path := range []string{"/v1/status", "/v1/events", "/v1/logs", "/v1/config", "/v1/diagnostics", "/v1/apikeys", "/v1/sessions", "/v1/start", "/v1/stop", "/v1/restart", "/v1/healthcheck"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "HTTP method is not supported for this operation", operationID(r))
		})
	}
	for _, path := range []string{"/v1/apikeys/{id}", "/v1/sessions/{id}/stop"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "HTTP method is not supported for this operation", operationID(r))
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, http.StatusNotFound, "not_found", "API operation not found", operationID(r))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Remora-API-Version", "1")
		operation := fmt.Sprintf("op-%016x", s.operations.Add(1))
		w.Header().Set("X-Remora-Operation-ID", operation)
		r = r.WithContext(context.WithValue(r.Context(), operationIDKey{}, operation))
		mux.ServeHTTP(w, r)
	})
}

func managedHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    32 * 1024,
	}
}

func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	lines, err := boundedInt(r, "lines", 200, 1, 2000)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", err.Error(), operationID(r))
		return
	}
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "remora"
	}
	path := s.logPath
	if source == "jellyfin" {
		path, err = newestLog(s.cfg.Jellyfin.LogDir)
	} else if source != "remora" {
		err = errors.New("source must be remora or jellyfin")
	}
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "log_unavailable", err.Error(), operationID(r))
		return
	}
	logLines, truncated, err := tailLines(path, lines)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "log_unavailable", err.Error(), operationID(r))
		return
	}
	writeJSON(w, http.StatusOK, LogResponse{Source: source, Path: path, Lines: logLines, Truncated: truncated})
}

func (s *Server) configInfo(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(s.configPath)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "config_unavailable", err.Error(), operationID(r))
		return
	}
	sum := sha256.Sum256(b)
	writeJSON(w, http.StatusOK, ConfigResponse{Path: s.configPath, SHA256: fmt.Sprintf("%x", sum[:])})
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.supervisor.APIKeys(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "jellyfin_error", err.Error(), operationID(r))
		return
	}
	writeJSON(w, http.StatusOK, APIKeysResponse{Keys: keys})
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", err.Error(), operationID(r))
		return
	}
	key, err := s.supervisor.CreateAPIKey(r.Context(), request.Name)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "operation_rejected", err.Error(), operationID(r))
		return
	}
	writeJSON(w, http.StatusCreated, key)
}

func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if err := s.supervisor.DeleteAPIKey(r.Context(), r.PathValue("id")); err != nil {
		writeAPIError(w, http.StatusBadRequest, "operation_rejected", err.Error(), operationID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.supervisor.Sessions(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "jellyfin_error", err.Error(), operationID(r))
		return
	}
	writeJSON(w, http.StatusOK, SessionsResponse{Sessions: sessions})
}

func (s *Server) stopSession(w http.ResponseWriter, r *http.Request) {
	if err := s.supervisor.StopSession(r.Context(), r.PathValue("id")); err != nil {
		writeAPIError(w, http.StatusBadRequest, "operation_rejected", err.Error(), operationID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}

func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	logs, truncated, _ := tailLines(s.logPath, 200)
	logs = redactDiagnosticLines(logs)
	storage := make([]string, 0, len(s.cfg.Disks))
	for _, disk := range s.cfg.Disks {
		storage = append(storage, disk.Type+":"+disk.Target)
	}
	bundle := DiagnosticBundle{
		GeneratedAt: time.Now(),
		Build:       buildinfo.Current("jellyfin-remora"),
		Status:      s.supervisor.Status(),
		Events:      s.supervisor.Events(256),
		Config:      DiagnosticConfig{Version: s.cfg.ConfigVersion, Control: net.JoinHostPort(s.cfg.RESTAPI.Listen, strconv.Itoa(s.cfg.RESTAPI.Port)), UnixSocket: s.cfg.RESTAPI.UnixSocket, NamedPipe: s.cfg.RESTAPI.NamedPipe, DataDir: s.cfg.Remora.DataDir, JellyfinPath: s.cfg.Jellyfin.Path, Storage: storage},
		Logs:        LogResponse{Source: "remora", Path: s.logPath, Lines: logs, Truncated: truncated},
	}
	writeJSON(w, http.StatusOK, bundle)
}

func redactDiagnosticLines(lines []string) []string {
	redacted := make([]string, len(lines))
	for i, line := range lines {
		for _, pattern := range diagnosticSecretPatterns {
			line = pattern.ReplaceAllString(line, `${1}[REDACTED]`)
		}
		redacted[i] = line
	}
	return redacted
}

func boundedInt(r *http.Request, name string, fallback, minimum, maximum int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return value, nil
}

func newestLog(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	var newest string
	var newestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(entry.Name()), ".log") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil && (newest == "" || info.ModTime().After(newestTime)) {
			newest = filepath.Join(directory, entry.Name())
			newestTime = info.ModTime()
		}
	}
	if newest == "" {
		return "", errors.New("no Jellyfin log file found")
	}
	return newest, nil
}

func tailLines(path string, limit int) ([]string, bool, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, false, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, false, errors.New("log path must be a regular non-symlink file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	const maxBytes = 4 << 20
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !os.SameFile(pathInfo, info) {
		return nil, false, errors.New("log path changed while it was opened")
	}
	truncated := info.Size() > maxBytes
	if truncated {
		if _, err := f.Seek(-maxBytes, 2); err != nil {
			return nil, false, err
		}
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if truncated {
		_ = scanner.Scan() // Drop the possibly partial first line after the seek.
	}
	lines := make([]string, 0, limit)
	next := 0
	overwritten := false
	for scanner.Scan() {
		if len(lines) < limit {
			lines = append(lines, scanner.Text())
			continue
		}
		lines[next] = scanner.Text()
		next = (next + 1) % limit
		overwritten = true
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	if overwritten {
		ordered := append([]string(nil), lines[next:]...)
		ordered = append(ordered, lines[:next]...)
		lines = ordered
		truncated = true
	}
	return lines, truncated, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

type operationIDKey struct{}

func operationID(r *http.Request) string {
	value, _ := r.Context().Value(operationIDKey{}).(string)
	return value
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 256 {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "limit must be between 1 and 256", operationID(r))
			return
		}
		limit = parsed
	}
	writeJSON(w, http.StatusOK, EventResponse{Events: s.supervisor.Events(limit)})
}

func (s *Server) action(action supervisor.Action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if action == supervisor.ActionStart && s.supervisor.Status().State == model.StateStorageFenced {
			writeAPIError(w, http.StatusConflict, "storage_fenced", "required storage is unhealthy", operationID(r))
			return
		}
		rawForce := r.URL.Query().Get("force")
		if rawForce != "" && rawForce != "true" && rawForce != "false" {
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "force must be true or false", operationID(r))
			return
		}
		force := rawForce == "true"
		if err := s.supervisor.Submit(r.Context(), action, force); err != nil {
			writeAPIError(w, http.StatusBadRequest, "operation_rejected", err.Error(), operationID(r))
			return
		}
		code := http.StatusAccepted
		if action == supervisor.ActionHealthcheck {
			code = http.StatusOK
		}
		writeJSON(w, code, s.supervisor.Status())
	}
}

func writeAPIError(w http.ResponseWriter, status int, code, message, operation string) {
	writeJSON(w, status, ErrorResponse{Error: APIError{Code: code, Message: message, OperationID: operation}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
