package model

import "time"

type State string

const (
	StateInit            State = "INIT"
	StatePreflight       State = "PREFLIGHT"
	StateStopped         State = "STOPPED"
	StateStarting        State = "STARTING"
	StateFirstStart      State = "FIRST_START"
	StateRunning         State = "RUNNING"
	StateDegraded        State = "DEGRADED"
	StateStopping        State = "STOPPING"
	StateRestartBackoff  State = "RESTART_BACKOFF"
	StateStorageFenced   State = "STORAGE_FENCED"
	StateProcessFailed   State = "PROCESS_FAILED"
	StateDatabaseDamaged State = "DATABASE_DAMAGED"
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
	LatencyMS int64     `json:"latency_ms"`
}

type HealthResult struct {
	Healthy    bool      `json:"healthy"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

type DatabaseResult struct {
	Damaged    bool      `json:"damaged"`
	Suspected  bool      `json:"suspected"`
	Message    string    `json:"message,omitempty"`
	DetectedAt time.Time `json:"detected_at,omitempty,omitzero"`
}

type Session struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	User        string `json:"user,omitempty"`
	Device      string `json:"device,omitempty"`
	Media       string `json:"media,omitempty"`
	Transcoding bool   `json:"transcoding"`
}

type APIKey struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Active   bool   `json:"active"`
	IsRemora bool   `json:"is_remora"`
}

type Event struct {
	Sequence  uint64    `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	State     State     `json:"state,omitempty"`
	Message   string    `json:"message,omitempty"`
}

type Status struct {
	State            State           `json:"state"`
	DesiredState     DesiredState    `json:"desired_state"`
	ManualStop       bool            `json:"manual_stop"`
	UID              int             `json:"uid"`
	Username         string          `json:"username,omitempty"`
	PID              int             `json:"pid,omitempty"`
	ProcessState     string          `json:"process_state,omitempty"`
	UptimeSeconds    int64           `json:"uptime_seconds,omitempty"`
	ProcessStarted   time.Time       `json:"process_started_at,omitempty,omitzero"`
	Executable       string          `json:"executable,omitempty"`
	Version          string          `json:"version,omitempty"`
	ServerName       string          `json:"server_name,omitempty"`
	Arguments        []string        `json:"arguments,omitempty"`
	Ports            []int           `json:"ports,omitempty"`
	CPUPercent       float64         `json:"cpu_percent,omitempty"`
	MemoryBytes      uint64          `json:"memory_bytes,omitempty"`
	FFmpegProcesses  int             `json:"ffmpeg_processes"`
	ActiveTranscodes int             `json:"active_transcodes"`
	Storage          []StorageResult `json:"storage"`
	Sessions         []Session       `json:"sessions,omitempty"`
	PlayingUsers     []string        `json:"playing_users,omitempty"`
	Jellyfin         HealthResult    `json:"jellyfin"`
	Database         DatabaseResult  `json:"database"`
	LastError        string          `json:"last_error,omitempty"`
	LastTransition   time.Time       `json:"last_transition"`
}
