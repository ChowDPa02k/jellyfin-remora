//go:build !windows

package control

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"strconv"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func listenLocalControl(cfg *config.Config, log *slog.Logger) (net.Listener, string, error) {
	if err := safeRemoveSocket(cfg.RESTAPI.UnixSocket); err != nil {
		return nil, "", err
	}
	listener, err := net.Listen("unix", cfg.RESTAPI.UnixSocket)
	if err != nil {
		return nil, "", fmt.Errorf("listen unix socket: %w", err)
	}
	if err := setSocketOwner(cfg.RESTAPI.UnixSocket, cfg.Jellyfin.RunAsUser, cfg.Jellyfin.RunAsGroup); err != nil {
		log.Warn("cannot set unix socket owner", "error", err)
	}
	return listener, cfg.RESTAPI.UnixSocket, nil
}

func cleanupLocalControl(cfg *config.Config) {
	_ = os.Remove(cfg.RESTAPI.UnixSocket)
}

func safeRemoveSocket(path string) error {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket path %s", path)
	}
	return os.Remove(path)
}

func setSocketOwner(path, username, groupname string) error {
	if err := os.Chmod(path, 0o660); err != nil {
		return err
	}
	if os.Geteuid() != 0 || username == "" {
		return nil
	}
	u, err := user.Lookup(username)
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gidText := u.Gid
	if groupname != "" {
		g, err := user.LookupGroup(groupname)
		if err != nil {
			return err
		}
		gidText = g.Gid
	}
	gid, _ := strconv.Atoi(gidText)
	return os.Chown(path, uid, gid)
}
