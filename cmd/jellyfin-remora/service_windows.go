//go:build windows

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/contract"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

const windowsServiceName = contract.WindowsServiceName

type serviceHandler struct {
	run func(context.Context) error
}

func runPlatformService(run func(context.Context) error) error {
	return svc.Run(windowsServiceName, &serviceHandler{run: run})
}

func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown
	statuses <- svc.Status{State: svc.StartPending, WaitHint: 15000}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	statuses <- svc.Status{State: svc.Running, Accepts: accepts}
	logServiceEvent(false, "service started")

	stopping := false
	checkpoint := uint32(1)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			cancel()
			if err != nil {
				logServiceEvent(true, fmt.Sprintf("service stopped with an error: %v", err))
				return true, 1
			}
			logServiceEvent(false, "service stopped")
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				if stopping {
					statuses <- svc.Status{State: svc.StopPending, CheckPoint: checkpoint, WaitHint: 300000}
				} else {
					statuses <- svc.Status{State: svc.Running, Accepts: accepts}
				}
			case svc.Stop, svc.Shutdown, svc.PreShutdown:
				if !stopping {
					stopping = true
					statuses <- svc.Status{State: svc.StopPending, CheckPoint: checkpoint, WaitHint: 300000}
					cancel()
				}
			}
		case <-ticker.C:
			if stopping {
				checkpoint++
				statuses <- svc.Status{State: svc.StopPending, CheckPoint: checkpoint, WaitHint: 300000}
			}
		}
	}
}

func logServiceEvent(isError bool, message string) {
	log, openErr := eventlog.Open(windowsServiceName)
	if openErr != nil {
		return
	}
	defer log.Close()
	if isError {
		_ = log.Error(1, message)
		return
	}
	_ = log.Info(1, message)
}
