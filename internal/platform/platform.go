package platform

import (
	"context"
	"os/exec"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

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
	PID         int
	PGID        int
	State       string
	Command     string
	Arguments   []string
	CPUPercent  float64
	MemoryBytes uint64
	Ports       []int
}

func New() Backend { return newBackend() }
