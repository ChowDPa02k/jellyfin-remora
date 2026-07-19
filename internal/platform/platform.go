package platform

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

type MountIdentityError struct{ Err error }

func (e MountIdentityError) Error() string { return e.Err.Error() }
func (e MountIdentityError) Unwrap() error { return e.Err }

func IsMountIdentityError(err error) bool {
	var identityError MountIdentityError
	return errors.As(err, &identityError)
}

type MountInfo struct {
	Source  string
	Target  string
	FSType  string
	Options string
}

type Backend interface {
	Mounts(context.Context) ([]MountInfo, error)
	Mount(context.Context, config.DiskConfig) error
	ResolvePhysical(context.Context, config.DiskConfig) (string, error)
	ExecutableProvenance(string) (bool, error)
	ConfigureProcess(*exec.Cmd, string, string) error
	SignalGroup(pid int, force bool) error
	ProcessInfo(context.Context, int) (ProcessInfo, error)
	FindProcesses(context.Context, string, []string) ([]ProcessInfo, error)
}

type ProcessInfo struct {
	PID             int
	PGID            int
	State           string
	Command         string
	Arguments       []string
	CPUPercent      float64
	MemoryBytes     uint64
	FFmpegProcesses int
	Ports           []int
	StartedAt       time.Time
}

func New() Backend { return newBackend() }
