// Package contract contains the public compatibility surface that is frozen
// for the v0.9 beta series. Values in this package must not be changed without
// following the compatibility policy in docs/compatibility.md.
package contract

const (
	Baseline = "v0.9"

	ConfigVersion = 2
	APIVersion    = 1

	APIHeaderVersion     = "X-Remora-API-Version"
	APIHeaderOperationID = "X-Remora-Operation-ID"

	ExitSuccess     = 0
	ExitInternal    = 1
	ExitUsage       = 2
	ExitUnavailable = 3
	ExitConflict    = 4
	ExitTimeout     = 5

	StateFileName  = "jellyfin.state"
	PIDFileName    = "jellyfin.pid"
	APIKeyFileName = ".remora_api_key"

	DarwinServiceLabel = "io.github.chowdpa02k.jellyfin-remora"
	DarwinStdoutPath   = "/var/log/jellyfin-remora.launchd.log"
	DarwinStderrPath   = "/var/log/jellyfin-remora.launchd.err"

	LinuxServiceName = "jellyfin-remora.service"
	LinuxConfigPath  = "/etc/jellyfin-remora/remora-config.yaml"
	LinuxDaemonPath  = "/usr/bin/jellyfin-remora"
	LinuxCLIPath     = "/usr/bin/remoractl"
	LinuxRuntimeDir  = "/run/jellyfin-remora"
	LinuxStateDir    = "/var/lib/jellyfin-remora"
	LinuxLogDir      = "/var/log/jellyfin-remora"
	LinuxSocketPath  = "/run/jellyfin-remora/remora.sock"

	WindowsServiceName = "JellyfinRemora"
	WindowsTaskName    = "JellyfinRemora-User"
	WindowsNamedPipe   = `\\.\pipe\jellyfin-remora`
)

type Operation struct {
	Method  string
	Path    string
	Success int
}

var APIOperations = []Operation{
	{Method: "GET", Path: "/v1/status", Success: 200},
	{Method: "GET", Path: "/v1/events", Success: 200},
	{Method: "GET", Path: "/v1/logs", Success: 200},
	{Method: "GET", Path: "/v1/config", Success: 200},
	{Method: "GET", Path: "/v1/diagnostics", Success: 200},
	{Method: "GET", Path: "/v1/apikeys", Success: 200},
	{Method: "POST", Path: "/v1/apikeys", Success: 201},
	{Method: "DELETE", Path: "/v1/apikeys/{id}", Success: 200},
	{Method: "GET", Path: "/v1/sessions", Success: 200},
	{Method: "POST", Path: "/v1/sessions/{id}/stop", Success: 200},
	{Method: "POST", Path: "/v1/start", Success: 202},
	{Method: "POST", Path: "/v1/stop", Success: 202},
	{Method: "POST", Path: "/v1/restart", Success: 202},
	{Method: "POST", Path: "/v1/healthcheck", Success: 200},
}

var APIErrorStatus = map[string]int{
	"invalid_argument":     400,
	"operation_rejected":   400,
	"not_found":            404,
	"log_unavailable":      404,
	"config_unavailable":   404,
	"method_not_allowed":   405,
	"storage_fenced":       409,
	"follow_limit_reached": 429,
	"jellyfin_error":       502,
}
