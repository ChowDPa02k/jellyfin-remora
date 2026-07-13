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
	"path/filepath"
	"strings"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/buildinfo"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "remoractl:", err)
		os.Exit(1)
	}
}
func run() error {
	global := flag.NewFlagSet("remoractl", flag.ContinueOnError)
	host := global.String("host", "", "loopback Remora URL")
	socket := global.String("socket", filepath.Join(os.TempDir(), "jellyfin-remora.sock"), "Remora unix socket")
	jsonOutput := global.Bool("json", false, "print machine-readable JSON")
	showVersion := global.Bool("version", false, "show version")
	if err := global.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(buildinfo.Current("remoractl"))
		return nil
	}
	args := global.Args()
	if len(args) == 0 {
		return errors.New("usage: remoractl [--host URL] [--json] <init|start|stop|restart|status|healthcheck>")
	}
	if args[0] == "init" {
		return runInit(args[1:])
	}
	client, base, err := newClient(*host, *socket)
	if err != nil {
		return err
	}
	cmd := args[0]
	useJSON := *jsonOutput || contains(args[1:], "--json")
	method := http.MethodGet
	path := "/v1/status"
	switch cmd {
	case "status":
	case "start", "stop", "restart", "healthcheck":
		method = http.MethodPost
		path = "/v1/" + cmd
	default:
		return fmt.Errorf("unsupported command %q", cmd)
	}
	if (cmd == "stop" || cmd == "restart") && contains(args[1:], "--force") {
		path += "?force=true"
	}
	st, err := request(client, method, base+path)
	if err != nil {
		return err
	}
	if cmd == "start" || cmd == "stop" || cmd == "restart" {
		return wait(client, base, cmd, st.PID, os.Stdout, useJSON)
	}
	return writeStatus(os.Stdout, st, useJSON)
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
			return &http.Client{Transport: transport, Timeout: 10 * time.Second}, strings.TrimRight(host, "/"), nil
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		return &http.Client{Transport: transport, Timeout: 10 * time.Second}, strings.TrimRight(host, "/"), nil
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}, "http://unix", nil
}
func request(c *http.Client, method, url string) (model.Status, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(nil))
	if err != nil {
		return model.Status{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return model.Status{}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return model.Status{}, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var st model.Status
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	return st, nil
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
		if st.State == model.StateStorageFenced || st.State == model.StateProcessFailed {
			return fmt.Errorf("operation failed in state %s: %s", st.State, st.LastError)
		}
	}
	return errors.New("operation timed out")
}
func contains(items []string, want string) bool {
	for _, v := range items {
		if v == want {
			return true
		}
	}
	return false
}
