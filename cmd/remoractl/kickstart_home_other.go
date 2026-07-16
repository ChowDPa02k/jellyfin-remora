//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"

	"github.com/ChowDPa02K/jellyfin-remora/internal/kickstart"
)

func prepareKickstartHome(answers kickstart.Answers) error {
	if info, err := os.Stat(answers.Home); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("Jellyfin home is not a directory: %s", answers.Home)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	missing, err := missingDirectories(answers.Home)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(answers.Home, 0o750); err != nil {
		return fmt.Errorf("create Jellyfin home: %w", err)
	}
	if os.Geteuid() != 0 || answers.RunAsUser == "" {
		return nil
	}
	account, err := user.Lookup(answers.RunAsUser)
	if err != nil {
		return fmt.Errorf("lookup Jellyfin run user: %w", err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if answers.RunAsGroup != "" {
		if group, lookupErr := user.LookupGroup(answers.RunAsGroup); lookupErr == nil {
			gid, err = strconv.Atoi(group.Gid)
		}
	}
	if err != nil {
		return err
	}
	for _, path := range missing {
		if err := os.Chown(path, uid, gid); err != nil {
			return fmt.Errorf("set Jellyfin home ownership on %s: %w", path, err)
		}
	}
	return nil
}
