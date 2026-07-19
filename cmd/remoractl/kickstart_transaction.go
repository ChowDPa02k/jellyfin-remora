package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type kickstartRollback struct {
	name string
	undo func() error
}

type kickstartTransaction struct {
	rollbacks []kickstartRollback
}

func (t *kickstartTransaction) record(name string, undo func() error) {
	t.rollbacks = append(t.rollbacks, kickstartRollback{name: name, undo: undo})
}

func (t *kickstartTransaction) capturePath(path string) error {
	return t.capturePathThen(path, nil)
}

func (t *kickstartTransaction) capturePathThen(path string, after func() error) error {
	snapshot, err := snapshotKickstartPath(path)
	if err != nil {
		return err
	}
	t.record(path, func() error {
		if err := snapshot.restore(); err != nil {
			return err
		}
		if after != nil {
			return after()
		}
		return nil
	})
	return nil
}

func (t *kickstartTransaction) fail(cause error) error {
	var cleanup []string
	for index := len(t.rollbacks) - 1; index >= 0; index-- {
		if err := t.rollbacks[index].undo(); err != nil {
			cleanup = append(cleanup, fmt.Sprintf("%s: %v", t.rollbacks[index].name, err))
		}
	}
	if len(cleanup) == 0 {
		return cause
	}
	return fmt.Errorf("%w\nkickstart rollback incomplete; cleanup required:\n  - %s", cause, strings.Join(cleanup, "\n  - "))
}

type kickstartPathSnapshot struct {
	path   string
	exists bool
	dir    bool
	mode   os.FileMode
	data   []byte
	owner  any
}

func snapshotKickstartPath(path string) (kickstartPathSnapshot, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return kickstartPathSnapshot{path: path}, nil
	}
	if err != nil {
		return kickstartPathSnapshot{}, fmt.Errorf("snapshot %s: %w", path, err)
	}
	snapshot := kickstartPathSnapshot{path: path, exists: true, dir: info.IsDir(), mode: info.Mode(), owner: captureKickstartPathOwner(info)}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return kickstartPathSnapshot{}, fmt.Errorf("snapshot %s: %w", path, err)
		}
		if len(entries) != 0 {
			return kickstartPathSnapshot{}, fmt.Errorf("refusing to transactionally replace non-empty directory: %s", path)
		}
		return snapshot, nil
	}
	if !info.Mode().IsRegular() {
		return kickstartPathSnapshot{}, fmt.Errorf("refusing to transactionally replace non-regular path: %s", path)
	}
	snapshot.data, err = os.ReadFile(path)
	if err != nil {
		return kickstartPathSnapshot{}, fmt.Errorf("snapshot %s: %w", path, err)
	}
	return snapshot, nil
}

func (s kickstartPathSnapshot) restore() error {
	if !s.exists {
		if err := os.RemoveAll(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.RemoveAll(s.path); err != nil {
		return err
	}
	if s.dir {
		if err := os.Mkdir(s.path, s.mode.Perm()); err != nil {
			return err
		}
		return restoreKickstartPathOwner(s.path, s.owner)
	}
	if err := atomicWriteFile(s.path, s.data, s.mode.Perm()); err != nil {
		return err
	}
	return restoreKickstartPathOwner(s.path, s.owner)
}

func captureMissingKickstartDirectories(transaction *kickstartTransaction, paths []string) error {
	seen := make(map[string]bool)
	var missing []string
	for _, path := range paths {
		for _, candidate := range kickstartMissingParents(path) {
			if !seen[candidate] {
				seen[candidate] = true
				missing = append(missing, candidate)
			}
		}
	}
	// Parents are recorded first so rollback removes children first.
	sort.Slice(missing, func(i, j int) bool { return pathDepth(missing[i]) < pathDepth(missing[j]) })
	for _, path := range missing {
		if err := transaction.capturePath(path); err != nil {
			return err
		}
	}
	return nil
}

func kickstartMissingParents(path string) []string {
	var missing []string
	for candidate := filepath.Clean(path); ; candidate = filepath.Dir(candidate) {
		if _, err := os.Lstat(candidate); err == nil || !errors.Is(err, os.ErrNotExist) {
			return missing
		}
		missing = append(missing, candidate)
		if filepath.Dir(candidate) == candidate {
			return missing
		}
	}
}

func pathDepth(path string) int { return strings.Count(filepath.Clean(path), string(os.PathSeparator)) }
