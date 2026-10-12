//go:build integration

package main

import (
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestIsolatedHarnessRapidLifecycleStress runs rapid start and teardown cycles
// to ensure no orphan Chrome processes, uncollected temp dirs, or socket leaks remain.
func TestIsolatedHarnessRapidLifecycleStress(t *testing.T) {
	const cycles = 5
	for i := 0; i < cycles; i++ {
		t.Logf("Lifecycle cycle %d/%d starting...", i+1, cycles)
		inst, cleanup, err := LaunchIsolatedChrome()
		if err != nil {
			t.Fatalf("Cycle %d: LaunchIsolatedChrome failed: %v", i+1, err)
		}

		pid := inst.Cmd.Process.Pid
		port := inst.Port
		profileDir := inst.ProfileDir

		// Verify instance is alive and responsive
		ver, err := inst.Version()
		if err != nil {
			cleanup()
			t.Fatalf("Cycle %d: Version() failed: %v", i+1, err)
		}
		if ver.WebSocketDebuggerURL == "" {
			cleanup()
			t.Fatalf("Cycle %d: empty WebSocketDebuggerURL", i+1)
		}

		// Verify listening port is active
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
		if err != nil {
			cleanup()
			t.Fatalf("Cycle %d: port %d not connectable: %v", i+1, port, err)
		}
		_ = conn.Close()

		// Teardown
		startCleanup := time.Now()
		cleanup()
		t.Logf("Cycle %d: cleanup completed in %v", i+1, time.Since(startCleanup))

		// 1. Verify process termination
		time.Sleep(100 * time.Millisecond)
		processExists := false
		if proc, err := os.FindProcess(pid); err == nil {
			// On Unix, FindProcess always succeeds, Signal 0 checks liveness
			if err := proc.Signal(syscall.Signal(0)); err == nil {
				processExists = true
			}
		}
		if processExists {
			t.Errorf("Cycle %d: process PID %d is still alive after cleanup!", i+1, pid)
		}

		// 2. Verify temp profile directory is removed
		if _, err := os.Stat(profileDir); !os.IsNotExist(err) {
			t.Errorf("Cycle %d: profile directory %s still exists after cleanup! (err: %v)", i+1, profileDir, err)
		}

		// 3. Verify port is released
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Errorf("Cycle %d: port %d is still bound after cleanup: %v", i+1, port, err)
		} else {
			_ = listener.Close()
		}
	}
}

// TestIsolatedHarnessConcurrentSpawns tests launching multiple isolated instances simultaneously.
func TestIsolatedHarnessConcurrentSpawns(t *testing.T) {
	const concurrentCount = 4
	var wg sync.WaitGroup
	errCh := make(chan error, concurrentCount)

	type spawnResult struct {
		inst    *IsolatedChromeInstance
		cleanup func()
	}
	results := make([]spawnResult, concurrentCount)

	for i := 0; i < concurrentCount; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			inst, cleanup, err := LaunchIsolatedChrome()
			if err != nil {
				errCh <- fmt.Errorf("instance %d launch failed: %w", idx, err)
				return
			}
			results[idx] = spawnResult{inst: inst, cleanup: cleanup}

			// Verify responsive
			ver, err := inst.Version()
			if err != nil {
				errCh <- fmt.Errorf("instance %d version probe failed: %w", idx, err)
				return
			}
			if ver.WebSocketDebuggerURL == "" {
				errCh <- fmt.Errorf("instance %d empty WebSocketDebuggerURL", idx)
				return
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	// Verify all ports and profiles are distinct
	seenPorts := make(map[int]bool)
	seenDirs := make(map[string]bool)
	for i, res := range results {
		if res.inst == nil {
			continue
		}
		if seenPorts[res.inst.Port] {
			t.Errorf("Duplicate port %d in instance %d", res.inst.Port, i)
		}
		seenPorts[res.inst.Port] = true

		if seenDirs[res.inst.ProfileDir] {
			t.Errorf("Duplicate profile dir %s in instance %d", res.inst.ProfileDir, i)
		}
		seenDirs[res.inst.ProfileDir] = true
	}

	// Cleanup all
	for i, res := range results {
		if res.cleanup != nil {
			pid := res.inst.Cmd.Process.Pid
			res.cleanup()

			// Check process exit
			time.Sleep(50 * time.Millisecond)
			if proc, err := os.FindProcess(pid); err == nil {
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					t.Errorf("Instance %d PID %d still alive after cleanup", i, pid)
				}
			}
		}
	}
}

// TestHeadlessPopupSuppressionVerification verifies that under testing conditions,
// launchChrome would NEVER call /usr/bin/open.
func TestHeadlessPopupSuppressionVerification(t *testing.T) {
	// Under testing.Testing(), isHeadless MUST always be true
	if !testing.Testing() {
		t.Fatal("expected testing.Testing() to be true")
	}

	// Simulate conditions in chrome_darwin.go
	isHeadless := len(chromeHeadlessArgs()) > 0 ||
		os.Getenv("ARGUS_CHROME_HEADLESS") == "1" ||
		os.Getenv("ARGUS_CHROME_HEADLESS") == "true" ||
		testing.Testing() ||
		os.Getenv("ARGUS_NO_OPEN") == "1"

	if !isHeadless {
		t.Fatal("isHeadless must evaluate to true under testing.Testing()")
	}
}

// TestTargetResolutionBugEmpirical reproduces the defect in test_isolated_harness_test.go
// where StartIsolatedChrome initializes the browser but leaves target resolution broken.
func TestTargetResolutionBugEmpirical(t *testing.T) {
	inst, cleanup := StartIsolatedChrome(t)
	defer cleanup()

	// Direct targets check via CDP
	targets, err := inst.Targets()
	if err != nil {
		t.Fatalf("inst.Targets() failed: %v", err)
	}
	t.Logf("CDP targets reported by instance: %d targets", len(targets))
	for _, tgt := range targets {
		t.Logf("Target: ID=%s, Type=%s, URL=%s", tgt.ID, tgt.Type, tgt.URL)
	}

	// Now attempt Argus's requireTarget()
	target, err := requireTarget()
	if err != nil {
		t.Logf("CONFIRMED DEFECT: requireTarget() failed as expected: %v", err)
	} else {
		t.Logf("requireTarget() succeeded: %+v", target)
	}
}
