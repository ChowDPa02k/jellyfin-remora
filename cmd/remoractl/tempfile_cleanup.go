package main

import (
	"os"
	"os/signal"
	"sync"
)

type temporaryFileCleanup struct {
	path      string
	signals   chan os.Signal
	stop      chan struct{}
	stopOnce  sync.Once
	cleanOnce sync.Once
}

func createSensitiveTemp(pattern string) (*os.File, func(), error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, nil, err
	}
	cleanup := protectTemporaryFile(file.Name())
	return file, cleanup, nil
}

func protectTemporaryFile(path string) func() {
	guard := &temporaryFileCleanup{
		path:    path,
		signals: make(chan os.Signal, 1),
		stop:    make(chan struct{}),
	}
	signal.Notify(guard.signals, temporaryFileSignals()...)
	go guard.run(terminateAfterTemporaryFileCleanup)
	return guard.close
}

func (g *temporaryFileCleanup) run(terminate func(os.Signal)) {
	select {
	case received := <-g.signals:
		g.cleanup()
		signal.Stop(g.signals)
		terminate(received)
	case <-g.stop:
	}
}

func (g *temporaryFileCleanup) close() {
	g.stopOnce.Do(func() {
		signal.Stop(g.signals)
		close(g.stop)
	})
	g.cleanup()
}

func (g *temporaryFileCleanup) cleanup() {
	g.cleanOnce.Do(func() { _ = os.Remove(g.path) })
}
