package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/buildinfo"
	"github.com/ChowDPa02K/jellyfin-remora/internal/contract"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "remoractl:", err)
		os.Exit(exitCode(err))
	}
}

type usageError struct{ message string }

func (e *usageError) Error() string { return e.message }

type HTTPError struct {
	StatusCode  int
	Code        string
	Message     string
	OperationID string
}

func (e *HTTPError) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.StatusCode)
	}
	if e.OperationID != "" {
		return fmt.Sprintf("%s (code=%s, operation_id=%s)", message, e.Code, e.OperationID)
	}
	return message
}

var errOperationTimedOut = errors.New("operation timed out")

func run() error {
	global := flag.NewFlagSet("remoractl", flag.ContinueOnError)
	host := global.String("host", "", "loopback Remora URL")
	var socket string
	global.StringVar(&socket, "socket", defaultLocalControlEndpoint(), "Remora local control endpoint")
	global.StringVar(&socket, "s", defaultLocalControlEndpoint(), "Remora local control endpoint (shorthand)")
	jsonOutput := global.Bool("json", false, "print machine-readable JSON")
	showVersion := global.Bool("version", false, "show version")
	if err := global.Parse(os.Args[1:]); err != nil {
		return &usageError{message: err.Error()}
	}
	if *showVersion {
		fmt.Println(buildinfo.Current("remoractl"))
		return nil
	}
	args := global.Args()
	if len(args) == 0 {
		return &usageError{message: "usage: remoractl [--host URL | --socket PATH] [--json] <init|kickstart|start|stop|restart|status|events|logs|edit-config|apikey|session|diagnose|healthcheck>"}
	}
	if args[0] == "init" {
		return runInit(args[1:])
	}
	if args[0] == "kickstart" {
		return runKickstart(args[1:])
	}
	client, base, err := newClient(*host, socket)
	if err != nil {
		return err
	}
	cmd := args[0]
	if cmd == "logs" || cmd == "edit-config" || cmd == "apikey" || cmd == "session" || cmd == "diagnose" {
		return runManagementCommand(client, base, cmd, args[1:], *jsonOutput)
	}
	if cmd == "events" {
		fs := flag.NewFlagSet("remoractl events", flag.ContinueOnError)
		limit := fs.Int("limit", 50, "maximum events to return (1-256)")
		commandJSON := fs.Bool("json", false, "print machine-readable JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return &usageError{message: err.Error()}
		}
		if fs.NArg() != 0 || *limit < 1 || *limit > 256 {
			return &usageError{message: "usage: remoractl events [--limit 1..256] [--json]"}
		}
		var response eventResponse
		if err := requestJSON(client, http.MethodGet, fmt.Sprintf("%s/v1/events?limit=%d", base, *limit), &response); err != nil {
			return err
		}
		return writeEvents(os.Stdout, response.Events, *jsonOutput || *commandJSON)
	}
	method := http.MethodGet
	path := "/v1/status"
	switch cmd {
	case "status":
	case "start", "stop", "restart", "healthcheck":
		method = http.MethodPost
		path = "/v1/" + cmd
	default:
		return &usageError{message: fmt.Sprintf("unsupported command %q", cmd)}
	}
	commandFlags := flag.NewFlagSet("remoractl "+cmd, flag.ContinueOnError)
	commandJSON := commandFlags.Bool("json", false, "print machine-readable JSON")
	var force *bool
	if cmd == "stop" || cmd == "restart" {
		force = commandFlags.Bool("force", false, "force process termination")
	}
	if err := commandFlags.Parse(args[1:]); err != nil || commandFlags.NArg() != 0 {
		if err != nil {
			return &usageError{message: err.Error()}
		}
		return &usageError{message: fmt.Sprintf("unexpected arguments for %s", cmd)}
	}
	if force != nil && *force {
		path += "?force=true"
	}
	st, err := request(client, method, base+path)
	if err != nil {
		return err
	}
	if cmd == "start" || cmd == "stop" || cmd == "restart" {
		return wait(client, base, cmd, st.PID, os.Stdout, *jsonOutput || *commandJSON)
	}
	return writeStatus(os.Stdout, st, *jsonOutput || *commandJSON)
}
func newClient(host, socket string) (*http.Client, string, error) {
	if host != "" {
		u, err := url.Parse(host)
		if err != nil {
			return nil, "", err
		}
		h := u.Hostname()
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, "", errors.New("--host must use http or https")
		}
		ip := net.ParseIP(h)
		if h != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, "", errors.New("--host must resolve syntactically to localhost or a loopback IP")
		}
		if h == "localhost" {
			ips, err := net.LookupIP(h)
			if err != nil {
				return nil, "", err
			}
			for _, resolved := range ips {
				if !resolved.IsLoopback() {
					return nil, "", errors.New("localhost resolved to a non-loopback address")
				}
			}
			pinned := ips[0]
			for _, resolved := range ips {
				if resolved.To4() != nil {
					pinned = resolved
					break
				}
			}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				dialHost, port, splitErr := net.SplitHostPort(address)
				if splitErr != nil {
					return nil, splitErr
				}
				if dialHost != "localhost" {
					return nil, fmt.Errorf("refusing redirected host %q", dialHost)
				}
				return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(pinned.String(), port))
			}
			return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: rejectControlRedirect}, strings.TrimRight(host, "/"), nil
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: rejectControlRedirect}, strings.TrimRight(host, "/"), nil
	}
	return newLocalClient(socket)
}

func rejectControlRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}
func request(c *http.Client, method, url string) (model.Status, error) {
	var status model.Status
	err := requestJSON(c, method, url, &status)
	return status, err
}

type eventResponse struct {
	Events []model.Event `json:"events"`
}

func requestJSON(c *http.Client, method, url string, out any) error {
	return requestJSONBody(c, method, url, nil, out)
}

func requestJSONBody(c *http.Client, method, url string, body, out any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return decodeHTTPError(resp)
	}
	b, _ := io.ReadAll(resp.Body)
	if out == nil || len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return err
	}
	return nil
}

func decodeHTTPError(resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	var envelope struct {
		Error struct {
			Code        string `json:"code"`
			Message     string `json:"message"`
			OperationID string `json:"operation_id"`
		} `json:"error"`
	}
	_ = json.Unmarshal(b, &envelope)
	message := envelope.Error.Message
	if message == "" {
		message = strings.TrimSpace(string(b))
	}
	return &HTTPError{StatusCode: resp.StatusCode, Code: envelope.Error.Code, Message: message, OperationID: envelope.Error.OperationID}
}
func wait(c *http.Client, base, command string, initialPID int, output io.Writer, jsonOutput bool) error {
	started := time.Now()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		st, err := request(c, http.MethodGet, base+"/v1/status")
		if err != nil {
			return err
		}
		if command == "stop" && st.State == model.StateStopped {
			return writeStatus(output, st, jsonOutput)
		}
		if command == "start" && st.State == model.StateRunning {
			return writeStatus(output, st, jsonOutput)
		}
		if command == "restart" && st.State == model.StateRunning && st.PID != 0 && st.PID != initialPID {
			return writeStatus(output, st, jsonOutput)
		}
		if st.State == model.StateProcessFailed && (command == "start" || command == "restart") && time.Since(started) < 2*time.Second {
			continue
		}
		if st.State == model.StateStorageFenced && ((command == "start" || command == "restart") && st.DesiredState == model.DesiredRunning || command == "stop" && st.DesiredState == model.DesiredStopped) {
			continue
		}
		if st.State == model.StateDatabaseDamaged && (command == "start" && !st.Database.Damaged || command == "stop" && st.DesiredState == model.DesiredStopped) {
			continue
		}
		if st.State == model.StateStorageFenced || st.State == model.StateProcessFailed || st.State == model.StateDatabaseDamaged {
			return fmt.Errorf("operation failed in state %s: %s", st.State, st.LastError)
		}
	}
	return errOperationTimedOut
}
func exitCode(err error) int {
	var usage *usageError
	if errors.As(err, &usage) || errors.Is(err, flag.ErrHelp) {
		return contract.ExitUsage
	}
	if errors.Is(err, errOperationTimedOut) {
		return contract.ExitTimeout
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed:
			return contract.ExitUsage
		case http.StatusConflict:
			return contract.ExitConflict
		default:
			return contract.ExitUnavailable
		}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return contract.ExitUnavailable
	}
	return contract.ExitInternal
}
