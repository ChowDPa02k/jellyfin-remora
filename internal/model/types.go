package model

import "time"

type State string

const (
	StateInit           State = "INIT"
	StatePreflight      State = "PREFLIGHT"
	StateStopped        State = "STOPPED"
	StateStarting       State = "STARTING"
	StateFirstStart     State = "FIRST_START"
	StateRunning        State = "RUNNING"
	StateDegraded       State = "DEGRADED"
	StateStopping       State = "STOPPING"
	StateRestartBackoff State = "RESTART_BACKOFF"
	StateStorageFenced  State = "STORAGE_FENCED"
	StateProcessFailed  State = "PROCESS_FAILED"
)

type DesiredState string

const (
	DesiredRunning DesiredState = "running"
	DesiredStopped DesiredState = "stopped"
)

type StorageResult struct {
	Index     int       `json:"index"`
	Type      string    `json:"type"`
	Device    string    `json:"device,omitempty"`
	Target    string    `json:"target"`
	Healthy   bool      `json:"healthy"`
	Fatal     bool      `json:"fatal"`
	Mounted   bool      `json:"mounted"`
	Writable  bool      `json:"writable"`
	Reachable bool      `json:"reachable"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

type HealthResult struct {
	Healthy    bool      `json:"healthy"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

type Status struct {
	State          State           `json:"state"`
	DesiredState   DesiredState    `json:"desired_state"`
	ManualStop     bool            `json:"manual_stop"`
	PID            int             `json:"pid,omitempty"`
	ProcessState   string          `json:"process_state,omitempty"`
	UptimeSeconds  int64           `json:"uptime_seconds,omitempty"`
	Executable     string          `json:"executable,omitempty"`
	Arguments      []string        `json:"arguments,omitempty"`
	Ports          []int           `json:"ports,omitempty"`
	CPUPercent     float64         `json:"cpu_percent,omitempty"`
	MemoryBytes    uint64          `json:"memory_bytes,omitempty"`
	Storage        []StorageResult `json:"storage"`
	Jellyfin       HealthResult    `json:"jellyfin"`
	LastError      string          `json:"last_error,omitempty"`
	LastTransition time.Time       `json:"last_transition"`
}
