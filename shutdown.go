package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// --- Graceful shutdown coordinator ---
//
// Inspired by ngrok's util.Shutdown but with context-aware cancellation.
// Each long-lived goroutine registers a Shutdown; when any one fails,
// it triggers a cascade that stops all others cleanly.

type Shutdown struct {
	mu         sync.Mutex
	inProgress bool
	begin      chan struct{} // closed when shutdown begins
	complete   chan struct{} // closed when shutdown completes
}

func NewShutdown() *Shutdown {
	return &Shutdown{
		begin:    make(chan struct{}),
		complete: make(chan struct{}),
	}
}

// Begin starts the shutdown. Safe to call multiple times; only the first call closes begin.
func (s *Shutdown) Begin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inProgress {
		return
	}
	s.inProgress = true
	close(s.begin)
}

// WaitBegin blocks until shutdown begins.
func (s *Shutdown) WaitBegin() {
	<-s.begin
}

// IsBegin returns true if shutdown has begun (non-blocking).
func (s *Shutdown) IsBegin() bool {
	select {
	case <-s.begin:
		return true
	default:
		return false
	}
}

// Complete signals that this goroutine has finished cleaning up.
func (s *Shutdown) Complete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.complete:
		// already closed
	default:
		close(s.complete)
	}
}

// WaitComplete blocks until shutdown completes.
func (s *Shutdown) WaitComplete() {
	<-s.complete
}

// --- Global shutdown registry ---
//
// Tracks all long-lived goroutines. On shutdown, Begin() all of them,
// then wait for all to Complete().

var (
	globalShutdownMu    sync.Mutex
	globalShutdowns     []*Shutdown
	globalShutdownBegun bool
)

// RegisterShutdown adds a Shutdown to the global registry.
func RegisterShutdown(s *Shutdown) {
	globalShutdownMu.Lock()
	defer globalShutdownMu.Unlock()
	globalShutdowns = append(globalShutdowns, s)
}

// BeginGlobalShutdown triggers all registered Shutdowns and waits for completion.
// Called from main on SIGINT/SIGTERM or normal exit.
func BeginGlobalShutdown() {
	globalShutdownMu.Lock()
	if globalShutdownBegun {
		globalShutdownMu.Unlock()
		return
	}
	globalShutdownBegun = true
	shutdowns := make([]*Shutdown, len(globalShutdowns))
	copy(shutdowns, globalShutdowns)
	globalShutdownMu.Unlock()

	for _, s := range shutdowns {
		s.Begin()
	}
	for _, s := range shutdowns {
		s.WaitComplete()
	}
}

// startPpidWatcher monitors the parent process (MCP client). If the client
// dies — e.g. killed with SIGKILL — the stdio pipe may stay open (kernel
// hasn't reclaimed the FD yet), so server.Run won't get EOF. By checking
// ppid periodically, we detect reparenting to PID 1 and shut down cleanly.
func startPpidWatcher(doShutdown func(string)) {
	origPpid := os.Getppid()
	sd := NewShutdown()
	RegisterShutdown(sd)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logError("ppid watcher panic: %v", r)
			}
			sd.Complete()
		}()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if cur := os.Getppid(); cur != origPpid {
					doShutdown(fmt.Sprintf("parent process gone (ppid %d → %d)", origPpid, cur))
					return
				}
			case <-sd.begin:
				return
			}
		}
	}()
}
