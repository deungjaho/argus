package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAnti9224PanicBarrier(t *testing.T) {
	// 1. Verify assertNotPort9224 panics on port 9224 integer
	t.Run("port integer 9224", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic on port 9224, but got none")
			}
			if !strings.Contains(fmt.Sprint(r), "Anti-9224 barrier") {
				t.Fatalf("unexpected panic message: %v", r)
			}
		}()
		assertNotPort9224("test", 9224)
	})

	// 2. Verify assertNotPort9224 panics on URL with port 9224
	t.Run("url string 9224", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic on URL with port 9224, but got none")
			}
			if !strings.Contains(fmt.Sprint(r), "Anti-9224 barrier") {
				t.Fatalf("unexpected panic message: %v", r)
			}
		}()
		assertNotPort9224("http://127.0.0.1:9224/json/version")
	})

	// 3. Verify cdpBaseURL panics if configured to 9224
	t.Run("cdpBaseURL port 9224", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic from cdpBaseURL on port 9224, but got none")
			}
			if !strings.Contains(fmt.Sprint(r), "Anti-9224 barrier") {
				t.Fatalf("unexpected panic message: %v", r)
			}
		}()
		configMu.Lock()
		origPort := configuredPort
		configuredPort = 9224
		configMu.Unlock()
		defer func() {
			configMu.Lock()
			configuredPort = origPort
			configMu.Unlock()
		}()
		_ = cdpBaseURL()
	})

	// 4. Verify cdpHTTPClient panics on request to 9224
	t.Run("cdpHTTPClient to 9224", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic from cdpHTTPClient on port 9224, but got none")
			}
			if !strings.Contains(fmt.Sprint(r), "Anti-9224 barrier") {
				t.Fatalf("unexpected panic message: %v", r)
			}
		}()
		_, _ = cdpHTTPClient.Get("http://127.0.0.1:9224/json/version")
	})

	// 5. Verify safe ports do NOT panic
	t.Run("safe port 59999", func(t *testing.T) {
		assertNotPort9224("http://127.0.0.1:59999/json/version", 59999)
	})
}

func TestSetConfigAnti9224Barrier(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"standard url 9224", "http://127.0.0.1:9224"},
		{"leading zero url 09224", "http://127.0.0.1:09224"},
		{"localhost 9224", "http://localhost:9224/json"},
		{"ipv6 9224", "http://[::1]:9224"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panicked := false
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
						if !strings.Contains(fmt.Sprint(r), "Anti-9224 barrier") {
							t.Errorf("unexpected panic message: %v", r)
						}
					}
				}()
				setConfig(tt.url, 0, nil)
			}()
			if !panicked {
				t.Fatalf("SECURITY FAILURE: setConfig(%q, 0, nil) failed to panic!", tt.url)
			}
		})
	}
}

func TestCollectNetworkRequestsAnti9224Barrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				if !strings.Contains(fmt.Sprint(r), "Anti-9224 barrier") {
					t.Errorf("unexpected panic message: %v", r)
				}
			}
		}()
		_, _ = collectNetworkRequests(ctx, "ws://127.0.0.1:9224/devtools/page/test", 100*time.Millisecond)
	}()
	if !panicked {
		t.Fatal("SECURITY FAILURE: collectNetworkRequests failed to panic on ws://127.0.0.1:9224/...")
	}
}
