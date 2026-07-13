package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/supervisor"
)

type Server struct {
	cfg        *config.Config
	supervisor Controller
	log        *slog.Logger
	tcp        *http.Server
	unix       *http.Server
	operations atomic.Uint64
}

type Controller interface {
	Status() model.Status
	Events(int) []model.Event
	Submit(context.Context, supervisor.Action, bool) error
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

func New(cfg *config.Config, s Controller, log *slog.Logger) *Server {
	return &Server{cfg: cfg, supervisor: s, log: log}
}

func (s *Server) Run(ctx context.Context) error {
	h := s.handler()
	s.tcp = &http.Server{Addr: net.JoinHostPort(s.cfg.RESTAPI.Listen, strconv.Itoa(s.cfg.RESTAPI.Port)), Handler: h, ReadHeaderTimeout: 5 * time.Second}
	s.unix = &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	if err := safeRemoveSocket(s.cfg.RESTAPI.UnixSocket); err != nil {
		return err
	}
	ul, err := net.Listen("unix", s.cfg.RESTAPI.UnixSocket)
	if err != nil {
		return fmt.Errorf("listen unix socket: %w", err)
	}
	if err := setSocketOwner(s.cfg.RESTAPI.UnixSocket, s.cfg.Jellyfin.RunAsUser, s.cfg.Jellyfin.RunAsGroup); err != nil {
		s.log.Warn("cannot set unix socket owner", "error", err)
	}
	tl, err := net.Listen("tcp", s.tcp.Addr)
	if err != nil {
		ul.Close()
		return fmt.Errorf("listen REST API: %w", err)
	}
	errCh := make(chan error, 2)
	go func() { errCh <- s.unix.Serve(ul) }()
	go func() { errCh <- s.tcp.Serve(tl) }()
	s.log.Info("control API listening", "tcp", s.tcp.Addr, "unix", s.cfg.RESTAPI.UnixSocket)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.tcp.Shutdown(shutdownCtx)
		_ = s.unix.Shutdown(shutdownCtx)
		_ = os.Remove(s.cfg.RESTAPI.UnixSocket)
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
	mux.HandleFunc("POST /v1/start", s.action(supervisor.ActionStart))
	mux.HandleFunc("POST /v1/stop", s.action(supervisor.ActionStop))
	mux.HandleFunc("POST /v1/restart", s.action(supervisor.ActionRestart))
	mux.HandleFunc("POST /v1/healthcheck", s.action(supervisor.ActionHealthcheck))
	for _, path := range []string{"/v1/status", "/v1/events", "/v1/start", "/v1/stop", "/v1/restart", "/v1/healthcheck"} {
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
func safeRemoveSocket(path string) error {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket path %s", path)
	}
	return os.Remove(path)
}
func setSocketOwner(path, username, groupname string) error {
	if err := os.Chmod(path, 0660); err != nil {
		return err
	}
	if os.Geteuid() != 0 || username == "" {
		return nil
	}
	u, err := user.Lookup(username)
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gidText := u.Gid
	if groupname != "" {
		g, err := user.LookupGroup(groupname)
		if err != nil {
			return err
		}
		gidText = g.Gid
	}
	gid, _ := strconv.Atoi(gidText)
	return os.Chown(path, uid, gid)
}
