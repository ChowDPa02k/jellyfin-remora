package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/control"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

func runManagementCommand(client *http.Client, base, command string, args []string, globalJSON bool) error {
	switch command {
	case "logs":
		return runLogs(client, base, args, globalJSON)
	case "edit-config":
		return runEditConfig(client, base, args)
	case "apikey":
		return runAPIKey(client, base, args, globalJSON)
	case "session":
		return runSession(client, base, args, globalJSON)
	case "diagnose":
		return runDiagnose(client, base, args)
	default:
		return &usageError{message: "unsupported management command"}
	}
}

func runLogs(client *http.Client, base string, args []string, globalJSON bool) error {
	args = normalizeLogArgs(args)
	fs := flag.NewFlagSet("remoractl logs", flag.ContinueOnError)
	lines := fs.Int("lines", 200, "number of trailing lines (1-2000)")
	sourceFlag := fs.String("source", "", "remora or jellyfin")
	followShort := fs.Bool("f", false, "follow appended log output")
	followLong := fs.Bool("follow", false, "follow appended log output")
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 1 || *lines < 1 || *lines > 2000 {
		return &usageError{message: "usage: remoractl logs [remora|jellyfin] [-f|--follow] [--lines 1..2000] [--json]"}
	}
	source := "remora"
	if *sourceFlag != "" {
		source = *sourceFlag
	}
	if fs.NArg() == 1 {
		if *sourceFlag != "" && *sourceFlag != fs.Arg(0) {
			return &usageError{message: "log source was specified twice with different values"}
		}
		source = fs.Arg(0)
	}
	if source != "remora" && source != "jellyfin" {
		return &usageError{message: "usage: remoractl logs [remora|jellyfin] [-f|--follow] [--lines 1..2000] [--json]"}
	}
	follow := *followShort || *followLong
	if follow && (globalJSON || *asJSON) {
		return &usageError{message: "--json cannot be combined with --follow"}
	}
	var response control.LogResponse
	requestURL := fmt.Sprintf("%s/v1/logs?source=%s&lines=%d", base, source, *lines)
	if follow {
		return followLogs(client, requestURL+"&follow=true")
	}
	if err := requestJSON(client, http.MethodGet, requestURL, &response); err != nil {
		return err
	}
	if globalJSON || *asJSON {
		return writeIndentedJSON(os.Stdout, response)
	}
	for _, line := range response.Lines {
		fmt.Fprintln(os.Stdout, line)
	}
	return nil
}

func normalizeLogArgs(args []string) []string {
	if len(args) > 1 && (args[0] == "remora" || args[0] == "jellyfin") {
		normalized := append([]string(nil), args[1:]...)
		return append(normalized, args[0])
	}
	return args
}

func followLogs(client *http.Client, requestURL string) error {
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	streamingClient := *client
	streamingClient.Timeout = 0
	resp, err := streamingClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusMultipleChoices {
		return decodeHTTPError(resp)
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func runEditConfig(client *http.Client, base string, args []string) error {
	fs := flag.NewFlagSet("remoractl edit-config", flag.ContinueOnError)
	editor := fs.String("editor", "", "editor executable; defaults to $VISUAL, $EDITOR, vi, then nano")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return &usageError{message: "usage: remoractl edit-config [--editor vi|nano]"}
	}
	var location control.ConfigResponse
	if err := requestJSON(client, http.MethodGet, base+"/v1/config", &location); err != nil {
		return err
	}
	return editExistingConfig(location, *editor)
}

func editExistingConfig(location control.ConfigResponse, configuredEditor string) error {
	if !filepath.IsAbs(location.Path) {
		return errors.New("daemon returned a non-absolute configuration path")
	}
	info, err := os.Lstat(location.Path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("configuration path must be a regular non-symlink file")
	}
	original, err := os.ReadFile(location.Path)
	if err != nil {
		return err
	}
	if digest(original) != location.SHA256 {
		return errors.New("configuration changed after daemon inspection; retry edit-config")
	}
	temporary, cleanupTemporary, err := createSensitiveTemp("jellyfin-remora-edit-*.yaml")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer cleanupTemporary()
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(original); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	editor, err := chooseEditor(configuredEditor)
	if err != nil {
		return err
	}
	if err := editConfigFile(editor, temporaryPath); err != nil {
		return err
	}
	if _, err := config.Load(temporaryPath); err != nil {
		return fmt.Errorf("edited configuration is invalid; original was preserved: %w", err)
	}
	current, err := os.ReadFile(location.Path)
	if err != nil {
		return err
	}
	if digest(current) != location.SHA256 {
		return errors.New("configuration changed while the editor was open; original was preserved")
	}
	edited, err := os.ReadFile(temporaryPath)
	if err != nil {
		return err
	}
	if err := replaceConfigurationFile(location.Path, edited, 0o600, info); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "configuration updated: %s\nrestart jellyfin-remora to load it\n", location.Path)
	return nil
}

func runAPIKey(client *http.Client, base string, args []string, globalJSON bool) error {
	if len(args) == 0 {
		return &usageError{message: "usage: remoractl apikey <list|create|delete>"}
	}
	action := args[0]
	fs := flag.NewFlagSet("remoractl apikey "+action, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return &usageError{message: err.Error()}
	}
	switch action {
	case "list":
		if fs.NArg() != 0 {
			return &usageError{message: "usage: remoractl apikey list [--json]"}
		}
		var response control.APIKeysResponse
		if err := requestJSON(client, http.MethodGet, base+"/v1/apikeys", &response); err != nil {
			return err
		}
		return writeAPIKeys(os.Stdout, response.Keys, globalJSON || *asJSON)
	case "create":
		if fs.NArg() != 1 {
			return &usageError{message: "usage: remoractl apikey create NAME [--json]"}
		}
		var key model.APIKey
		if err := requestJSONBody(client, http.MethodPost, base+"/v1/apikeys", map[string]string{"name": fs.Arg(0)}, &key); err != nil {
			return err
		}
		return writeAPIKeys(os.Stdout, []model.APIKey{key}, globalJSON || *asJSON)
	case "delete":
		if fs.NArg() != 1 {
			return &usageError{message: "usage: remoractl apikey delete ID"}
		}
		if err := requestJSON(client, http.MethodDelete, base+"/v1/apikeys/"+url.PathEscape(fs.Arg(0)), &map[string]bool{}); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "API key deleted")
		return nil
	default:
		return &usageError{message: "usage: remoractl apikey <list|create|delete>"}
	}
}

func runSession(client *http.Client, base string, args []string, globalJSON bool) error {
	if len(args) == 0 {
		return &usageError{message: "usage: remoractl session <list|stop>"}
	}
	action := args[0]
	fs := flag.NewFlagSet("remoractl session "+action, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return &usageError{message: err.Error()}
	}
	switch action {
	case "list":
		if fs.NArg() != 0 {
			return &usageError{message: "usage: remoractl session list [--json]"}
		}
		var response control.SessionsResponse
		if err := requestJSON(client, http.MethodGet, base+"/v1/sessions", &response); err != nil {
			return err
		}
		return writeSessions(os.Stdout, response.Sessions, globalJSON || *asJSON)
	case "stop":
		if fs.NArg() != 1 {
			return &usageError{message: "usage: remoractl session stop ID"}
		}
		if err := requestJSON(client, http.MethodPost, base+"/v1/sessions/"+url.PathEscape(fs.Arg(0))+"/stop", &map[string]bool{}); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "session playback stopped")
		return nil
	default:
		return &usageError{message: "usage: remoractl session <list|stop>"}
	}
}

func runDiagnose(client *http.Client, base string, args []string) error {
	fs := flag.NewFlagSet("remoractl diagnose", flag.ContinueOnError)
	output := fs.String("output", "", "write diagnostic JSON to an owner-only file")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return &usageError{message: "usage: remoractl diagnose [--output FILE]"}
	}
	var bundle control.DiagnosticBundle
	if err := requestJSON(client, http.MethodGet, base+"/v1/diagnostics", &bundle); err != nil {
		return err
	}
	var encoded bytes.Buffer
	if err := writeIndentedJSON(&encoded, bundle); err != nil {
		return err
	}
	if *output == "" {
		_, err := io.Copy(os.Stdout, &encoded)
		return err
	}
	path, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(path, encoded.Bytes(), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "diagnostic bundle written: %s\n", path)
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func writeIndentedJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
