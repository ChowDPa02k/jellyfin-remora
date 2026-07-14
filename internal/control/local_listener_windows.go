//go:build windows

package control

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listenLocalControl(cfg *config.Config, _ *slog.Logger) (net.Listener, string, error) {
	sddl, err := localPipeSecurityDescriptor()
	if err != nil {
		return nil, "", fmt.Errorf("build Windows named-pipe ACL: %w", err)
	}
	listener, err := winio.ListenPipe(cfg.RESTAPI.NamedPipe, &winio.PipeConfig{
		SecurityDescriptor: sddl,
	})
	if err != nil {
		return nil, "", fmt.Errorf("listen Windows named pipe %s: %w", cfg.RESTAPI.NamedPipe, err)
	}
	return listener, cfg.RESTAPI.NamedPipe, nil
}

func localPipeSecurityDescriptor() (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;%s)(A;;GRGW;;;IU)", user.User.Sid.String()), nil
}

func cleanupLocalControl(*config.Config) {}
