package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func main() {
	port := 8096
	healthFile := ""
	envFile := ""
	childPIDFile := ""
	childMode := false
	startupMarker := ""
	wizardFalseCount := int64(0)
	for _, arg := range os.Args[1:] {
		switch {
		case strings.HasPrefix(arg, "--fakeport="):
			port, _ = strconv.Atoi(strings.TrimPrefix(arg, "--fakeport="))
		case strings.HasPrefix(arg, "--healthfile="):
			healthFile = strings.TrimPrefix(arg, "--healthfile=")
		case strings.HasPrefix(arg, "--envfile="):
			envFile = strings.TrimPrefix(arg, "--envfile=")
		case strings.HasPrefix(arg, "--childpidfile="):
			childPIDFile = strings.TrimPrefix(arg, "--childpidfile=")
		case arg == "--child=true":
			childMode = true
		case strings.HasPrefix(arg, "--startupmarker="):
			startupMarker = strings.TrimPrefix(arg, "--startupmarker=")
		case strings.HasPrefix(arg, "--wizardfalsecount="):
			wizardFalseCount, _ = strconv.ParseInt(strings.TrimPrefix(arg, "--wizardfalsecount="), 10, 64)
		}
	}
	if envFile != "" {
		environment := make(map[string]string, len(os.Environ()))
		for _, entry := range os.Environ() {
			name, value, ok := strings.Cut(entry, "=")
			if ok {
				environment[name] = value
			}
		}
		data, err := json.Marshal(environment)
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(envFile, data, 0600); err != nil {
			panic(err)
		}
	}
	if childMode {
		time.Sleep(5 * time.Minute)
		return
	}
	if childPIDFile != "" {
		child := childCommand()
		if err := child.Start(); err != nil {
			panic(err)
		}
		_ = os.WriteFile(childPIDFile, []byte(strconv.Itoa(child.Process.Pid)+"\n"), 0600)
	}
	var publicCalls atomic.Int64
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if healthFile != "" {
			if b, err := os.ReadFile(healthFile); err == nil {
				switch strings.TrimSpace(string(b)) {
				case "hang":
					<-r.Context().Done()
					return
				case "healthy":
				default:
					http.Error(w, "Unhealthy", http.StatusServiceUnavailable)
					return
				}
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Healthy"))
	})
	http.HandleFunc("/System/Info/Public", func(w http.ResponseWriter, r *http.Request) {
		complete := publicCalls.Add(1) > wizardFalseCount
		_ = json.NewEncoder(w).Encode(map[string]any{"Version": "12.0.0-test", "ServerName": "Fake Jellyfin", "StartupWizardCompleted": complete})
	})
	for _, path := range []string{"/Startup/User", "/Startup/Configuration", "/Startup/RemoteAccess", "/Startup/Complete"} {
		http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if startupMarker != "" && r.Method == http.MethodPost {
				_ = os.WriteFile(startupMarker, []byte(r.URL.Path+"\n"), 0600)
			}
			if r.Method == http.MethodGet && r.URL.Path == "/Startup/User" {
				_ = json.NewEncoder(w).Encode(map[string]string{"Name": "fake-user"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
	if err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
