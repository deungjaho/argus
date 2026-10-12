package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestChallengerLeadingZeroVariants rigorously stress-tests the barrier against
// all variants of leading zeros across schemes, encodings, host representations,
// and CDP subsystems.
func TestChallengerLeadingZeroVariants(t *testing.T) {
	leadingZeroAttacks := []struct {
		name   string
		target string
	}{
		// 1. Bare numbers with varying leading zeroes
		{"bare 09224", "09224"},
		{"bare 009224", "009224"},
		{"bare 0009224", "0009224"},
		{"bare 00009224", "00009224"},
		{"bare 000009224", "000009224"},

		// 2. Colon-prefixed
		{":09224", ":09224"},
		{":009224", ":009224"},
		{":0009224", ":0009224"},

		// 3. IPv4 hostport without scheme
		{"127.0.0.1:09224", "127.0.0.1:09224"},
		{"127.0.0.1:009224", "127.0.0.1:009224"},
		{"127.0.0.1:0009224", "127.0.0.1:0009224"},
		{"0.0.0.0:09224", "0.0.0.0:09224"},
		{"0.0.0.0:009224", "0.0.0.0:009224"},
		{"127.0.0.2:09224", "127.0.0.2:09224"},

		// 4. Localhost hostport without scheme
		{"localhost:09224", "localhost:09224"},
		{"localhost:009224", "localhost:009224"},
		{"sub.localhost:09224", "sub.localhost:09224"},

		// 5. IPv6 hostport without scheme
		{"[::1]:09224", "[::1]:09224"},
		{"[::1]:009224", "[::1]:009224"},
		{"[::]:09224", "[::]:09224"},
		{"[0000:0000:0000:0000:0000:0000:0000:0001]:09224", "[0000:0000:0000:0000:0000:0000:0001]:09224"},

		// 6. HTTP scheme with paths/queries
		{"http://127.0.0.1:09224", "http://127.0.0.1:09224"},
		{"http://127.0.0.1:009224", "http://127.0.0.1:009224"},
		{"http://127.0.0.1:0009224", "http://127.0.0.1:0009224"},
		{"http://127.0.0.1:09224/json/version", "http://127.0.0.1:09224/json/version"},
		{"http://127.0.0.1:009224/json/list", "http://127.0.0.1:009224/json/list"},
		{"http://127.0.0.1:09224/test?arg=val#fragment", "http://127.0.0.1:09224/test?arg=val#fragment"},
		{"http://localhost:09224/json", "http://localhost:09224/json"},
		{"http://localhost:009224/json/version", "http://localhost:009224/json/version"},
		{"http://[::1]:09224/json/version", "http://[::1]:09224/json/version"},
		{"http://[::1]:009224/json/version", "http://[::1]:009224/json/version"},

		// 7. HTTPS scheme
		{"https://127.0.0.1:09224", "https://127.0.0.1:09224"},
		{"https://127.0.0.1:009224", "https://127.0.0.1:009224"},

		// 8. WS and WSS schemes
		{"ws://127.0.0.1:09224/devtools/page/abc", "ws://127.0.0.1:09224/devtools/page/abc"},
		{"ws://127.0.0.1:009224/devtools/browser/123", "ws://127.0.0.1:009224/devtools/browser/123"},
		{"ws://localhost:09224/devtools/page/1", "ws://localhost:09224/devtools/page/1"},
		{"ws://localhost:009224/devtools/page/1", "ws://localhost:009224/devtools/page/1"},
		{"ws://[::1]:09224/devtools/page/1", "ws://[::1]:09224/devtools/page/1"},
		{"wss://127.0.0.1:09224/devtools/page/xyz", "wss://127.0.0.1:09224/devtools/page/xyz"},

		// 9. Schemeless with path
		{"127.0.0.1:09224/json/version", "127.0.0.1:09224/json/version"},
		{"127.0.0.1:009224/devtools/browser", "127.0.0.1:009224/devtools/browser"},
		{"localhost:09224/json", "localhost:09224/json"},

		// 10. Whitespace and case variations
		{"whitespace leading 09224", "   http://127.0.0.1:09224/json   "},
		{"whitespace newline 009224", "\nhttp://127.0.0.1:009224/json\t"},
		{"uppercase HTTP 09224", "HTTP://127.0.0.1:09224/JSON"},
		{"uppercase WS 009224", "WS://LOCALHOST:009224/DEVTOOLS"},
		{"userinfo in url 09224", "http://user:pass@127.0.0.1:09224/json"},
	}

	for _, tt := range leadingZeroAttacks {
		t.Run(tt.name, func(t *testing.T) {
			panicked := false
			var panicVal any
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
						panicVal = r
					}
				}()
				assertNotPort9224(tt.target)
			}()

			if !panicked {
				t.Fatalf("CRITICAL SECURITY FAILURE: Leading zero target %q bypassed assertNotPort9224!", tt.target)
			}
			msg := fmt.Sprint(panicVal)
			if !strings.Contains(msg, "Anti-9224 barrier") {
				t.Fatalf("Unexpected panic message for %q: %s", tt.target, msg)
			}
		})
	}
}

// TestChallengerLeadingZeroSubsystems verifies that higher-level subsystems
// (cdpHTTPClient, dialConn, collectNetworkRequests, makeHTTPRequest) also block
// leading-zero port 9224 variants.
func TestChallengerLeadingZeroSubsystems(t *testing.T) {
	// Subsystem 1: cdpHTTPClient
	t.Run("cdpHTTPClient leading zero", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			_, _ = cdpHTTPClient.Get("http://127.0.0.1:009224/json/version")
		}()
		if !panicked {
			t.Fatal("CRITICAL: cdpHTTPClient failed to block http://127.0.0.1:009224/json/version")
		}
	})

	// Subsystem 2: dialConn
	t.Run("dialConn leading zero", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			_, _ = dialConn("ws://127.0.0.1:009224/devtools/page/test")
		}()
		if !panicked {
			t.Fatal("CRITICAL: dialConn failed to block ws://127.0.0.1:009224/devtools/page/test")
		}
	})

	// Subsystem 3: collectNetworkRequests
	t.Run("collectNetworkRequests leading zero", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			_, _ = collectNetworkRequests(ctx, "ws://127.0.0.1:09224/devtools/page/test", 50*time.Millisecond)
		}()
		if !panicked {
			t.Fatal("CRITICAL: collectNetworkRequests failed to block ws://127.0.0.1:09224/devtools/page/test")
		}
	})
}

// TestChallengerSetConfigForbiddenURL rigorously stress-tests setConfig against
// forbidden URLs with port=0, non-zero ports, schemes, and verifies state immunity.
func TestChallengerSetConfigForbiddenURL(t *testing.T) {
	configMu.Lock()
	initialURL := configuredURL
	initialPort := configuredPort
	configMu.Unlock()

	defer func() {
		configMu.Lock()
		configuredURL = initialURL
		configuredPort = initialPort
		configMu.Unlock()
	}()

	forbiddenCases := []struct {
		name string
		url  string
		port int
	}{
		{"standard 9224 url with port 0", "http://127.0.0.1:9224", 0},
		{"standard 9224 url with slash and port 0", "http://127.0.0.1:9224/", 0},
		{"leading zero 09224 url with port 0", "http://127.0.0.1:09224", 0},
		{"leading double zero 009224 url with port 0", "http://127.0.0.1:009224", 0},
		{"localhost 9224 url with port 0", "http://localhost:9224/json", 0},
		{"localhost 09224 url with port 0", "http://localhost:09224/json", 0},
		{"ipv6 [::1]:9224 url with port 0", "http://[::1]:9224", 0},
		{"ipv6 [::1]:09224 url with port 0", "http://[::1]:09224", 0},
		{"ws 9224 url with port 0", "ws://127.0.0.1:9224/devtools", 0},
		{"ws 09224 url with port 0", "ws://127.0.0.1:09224/devtools", 0},
		{"schemeless 127.0.0.1:9224 with port 0", "127.0.0.1:9224", 0},
		{"schemeless 127.0.0.1:09224 with port 0", "127.0.0.1:09224", 0},
		{"empty url with forbidden port 9224", "", 9224},
		{"safe url with forbidden port 9224", "http://127.0.0.1:59999", 9224},
		{"forbidden url and forbidden port 9224", "http://127.0.0.1:9224", 9224},
		{"forbidden leading zero url and forbidden port 9224", "http://127.0.0.1:09224", 9224},
	}

	for _, tt := range forbiddenCases {
		t.Run(tt.name, func(t *testing.T) {
			configMu.Lock()
			configuredURL = "http://safe-sentinel.local:59999"
			configuredPort = 59999
			configMu.Unlock()

			panicked := false
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
					}
				}()
				setConfig(tt.url, tt.port, nil)
			}()

			if !panicked {
				t.Fatalf("CRITICAL SECURITY FAILURE: setConfig(%q, %d, nil) did NOT panic!", tt.url, tt.port)
			}

			// Invariant check: verify configuration state was NOT mutated to forbidden value
			configMu.RLock()
			currentURL := configuredURL
			currentPort := configuredPort
			configMu.RUnlock()

			if currentURL == tt.url && tt.url != "" {
				t.Fatalf("STATE CORRUPTION: setConfig panicked but configuredURL was updated to %q!", currentURL)
			}
			if currentPort == tt.port && tt.port != 0 {
				t.Fatalf("STATE CORRUPTION: setConfig panicked but configuredPort was updated to %d!", currentPort)
			}
		})
	}

	// Safe setConfig calls must succeed without panic
	safeCases := []struct {
		name string
		url  string
		port int
	}{
		{"empty url and port 0", "", 0},
		{"about:blank with port 59999", "about:blank", 59999},
		{"port 19224 safe url", "http://127.0.0.1:19224", 19224},
		{"port 29224 safe url", "http://127.0.0.1:29224", 29224},
		{"port 59999 safe url", "http://127.0.0.1:59999", 59999},
	}

	for _, tt := range safeCases {
		t.Run("safe_"+tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("FALSE POSITIVE: setConfig(%q, %d, nil) panicked: %v", tt.url, tt.port, r)
				}
			}()
			attachTrue := true
			setConfig(tt.url, tt.port, &attachTrue)
		})
	}
}

// TestChallengerSafePortsZeroFalsePositives confirms that safe ports (19224, 29224, 59999, etc.)
// never trigger false alarms, even when the digits '9224' appear in non-port segments.
func TestChallengerSafePortsZeroFalsePositives(t *testing.T) {
	safeTargets := []struct {
		name   string
		target string
		ports  []int
	}{
		// 1. Direct integers
		{"integer 19224", "arbitrary_target", []int{19224}},
		{"integer 29224", "arbitrary_target", []int{29224}},
		{"integer 59999", "arbitrary_target", []int{59999}},
		{"integer 9223", "arbitrary_target", []int{9223}},
		{"integer 9225", "arbitrary_target", []int{9225}},
		{"integer 49152", "arbitrary_target", []int{49152}},
		{"integer 65535", "arbitrary_target", []int{65535}},

		// 2. URLs with safe ports
		{"http 19224", "http://127.0.0.1:19224/json/version", []int{19224}},
		{"http 29224", "http://127.0.0.1:29224/json/version", []int{29224}},
		{"http 59999", "http://127.0.0.1:59999/json/version", []int{59999}},
		{"http localhost 19224", "http://localhost:19224/json/version", []int{19224}},
		{"http localhost 29224", "http://localhost:29224/json/version", []int{29224}},
		{"http localhost 59999", "http://localhost:59999/json/version", []int{59999}},
		{"ipv6 19224", "http://[::1]:19224/json/version", []int{19224}},
		{"ipv6 29224", "http://[::1]:29224/json/version", []int{29224}},
		{"ipv6 59999", "http://[::1]:59999/json/version", []int{59999}},

		// 3. Hostports without scheme
		{"raw hostport 19224", "127.0.0.1:19224", nil},
		{"raw hostport 29224", "127.0.0.1:29224", nil},
		{"raw hostport 59999", "127.0.0.1:59999", nil},
		{"raw localhost 19224", "localhost:19224", nil},
		{"raw localhost 29224", "localhost:29224", nil},
		{"raw localhost 59999", "localhost:59999", nil},
		{"raw ipv6 19224", "[::1]:19224", nil},
		{"raw ipv6 29224", "[::1]:29224", nil},
		{"raw ipv6 59999", "[::1]:59999", nil},

		// 4. Safe URLs with '9224' or '09224' embedded in path or query
		{"path has 9224 on port 59999", "http://127.0.0.1:59999/issues/9224", []int{59999}},
		{"path has 09224 on port 59999", "http://127.0.0.1:59999/issues/09224", []int{59999}},
		{"query has 9224 on port 59999", "http://127.0.0.1:59999/search?q=9224", []int{59999}},
		{"query has 09224 on port 59999", "http://127.0.0.1:59999/search?port=09224&id=9224", []int{59999}},
		{"path has 9224 on port 19224", "http://127.0.0.1:19224/item/9224", []int{19224}},
		{"path has 9224 on port 29224", "http://127.0.0.1:29224/item/9224", []int{29224}},
		{"default port http path has 9224", "http://example.com/port/9224", nil},
		{"default port https path has 9224", "https://example.com/port/9224", nil},
		{"default port http query has 09224", "http://example.com/api?port=09224", nil},

		// 5. Port numbers starting or ending with 9224
		{"port 92240 (suffix zero)", "http://127.0.0.1:92240", nil},
		{"port 922 (prefix)", "http://127.0.0.1:922", nil},
	}

	for _, tt := range safeTargets {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("FALSE POSITIVE: Safe target %q with ports %v panicked: %v", tt.target, tt.ports, r)
				}
			}()
			assertNotPort9224(tt.target, tt.ports...)
		})
	}
}

// TestChallengerLsofUntouchedPort9224 executes lsof to verify that host port 9224
// is not touched or disturbed by any test suite (only the pre-existing host Chrome may be present).
func TestChallengerLsofUntouchedPort9224(t *testing.T) {
	cmd := exec.Command("lsof", "-nP", "-iTCP:9224", "-sTCP:LISTEN")
	output, err := cmd.CombinedOutput()
	// When lsof finds no matching listeners, it exits with non-zero exit code (1 on macOS).
	if err == nil {
		outStr := string(output)
		if strings.Contains(outStr, "chrome") {
			t.Logf("CONFIRMED: Pre-existing host Chrome Argus instance remains untouched on port 9224:\n%s", outStr)
			return
		}
		t.Fatalf("CRITICAL SECURITY VIOLATION: Unknown process on host port 9224 in LISTEN state!\nOutput:\n%s", outStr)
	}
	t.Logf("CONFIRMED: Port 9224 is clean and untouched (lsof exit code non-zero, zero listeners).")
}

// TestChallengerConcurrentStressRuns asserts that concurrent execution of barrier checks
// across 50 goroutines remains thread-safe with 0 data races.
func TestChallengerConcurrentStressRuns(t *testing.T) {
	var wg sync.WaitGroup
	workers := 50
	iterations := 100

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// 1. Safe target should never panic
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("worker %d: safe port panicked: %v", workerID, r)
						}
					}()
					assertNotPort9224(fmt.Sprintf("http://127.0.0.1:%d/test", 19224+(j%100)), 19224+(j%100))
				}()

				// 2. Forbidden target with leading zero must always panic
				func() {
					panicked := false
					defer func() {
						if r := recover(); r != nil {
							panicked = true
						}
					}()
					assertNotPort9224("http://127.0.0.1:009224/json")
					if !panicked {
						t.Errorf("worker %d: forbidden target failed to panic!", workerID)
					}
				}()
			}
		}(i)
	}

	wg.Wait()
}
