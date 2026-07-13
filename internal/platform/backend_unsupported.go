//go:build !darwin

package platform

import (
	"context"
	"errors"
	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"os/exec"
)

var errUnsupported = errors.New("platform backend is not implemented")

type unsupportedBackend struct{}

func newBackend() Backend                                                  { return &unsupportedBackend{} }
func (*unsupportedBackend) Mounts(context.Context) ([]MountInfo, error)    { return nil, errUnsupported }
func (*unsupportedBackend) Mount(context.Context, config.DiskConfig) error { return errUnsupported }
func (*unsupportedBackend) ResolvePhysical(context.Context, config.DiskConfig) (string, error) {
	return "", errUnsupported
}
func (*unsupportedBackend) ConfigureProcess(*exec.Cmd, string, string) error { return errUnsupported }
func (*unsupportedBackend) SignalGroup(int, bool) error                      { return errUnsupported }
func (*unsupportedBackend) ProcessInfo(context.Context, int) (ProcessInfo, error) {
	return ProcessInfo{}, errUnsupported
}
func (*unsupportedBackend) FindProcesses(context.Context, string, []string) ([]ProcessInfo, error) {
	return nil, errUnsupported
}
