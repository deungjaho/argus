package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestAdversarialForbiddenVariants stress-tests assertNotPort9224 and isPort9224Target
// against various attack vectors and encodings aiming to bypass the barrier.
func TestAdversarialForbiddenVariants(t *testing.T) {
	forbiddenTargets := []struct {
		name   string
		target string
		ports  []int
	}{
		// 1. Direct Integer Ports
		{"direct integer 9224", "arbitrary_target", []int{9224}},
		{"multi port with 9224", "arbitrary_target", []int{8080, 9224, 9000}},

		// 2. IPv4 Formats
		{"ipv4 127.0.0.1", "127.0.0.1:9224", nil},
		{"ipv4 http url", "http://127.0.0.1:9224", nil},
		{"ipv4 http url with trailing slash", "http://127.0.0.1:9224/", nil},
		{"ipv4 http url version endpoint", "http://127.0.0.1:9224/json/version", nil},
		{"ipv4 https url", "https://127.0.0.1:9224/json", nil},
		{"ipv4 ws url", "ws://127.0.0.1:9224/devtools/page/123", nil},
		{"ipv4 wss url", "wss://127.0.0.1:9224/devtools/browser/abc", nil},
		{"ipv4 0.0.0.0:9224", "0.0.0.0:9224", nil},
		{"ipv4 127.0.0.2:9224", "127.0.0.2:9224", nil},
		{"ipv4 127.1:9224", "127.1:9224", nil},
		{"ipv4 octal 0177.0.0.1:9224", "0177.0.0.1:9224", nil},
		{"ipv4 hex 0x7f.0.0.1:9224", "0x7f.0.0.1:9224", nil},
		{"ipv4 integer 2130706433:9224", "2130706433:9224", nil},

		// 3. Localhost & Hostnames
		{"localhost plain", "localhost:9224", nil},
		{"localhost http", "http://localhost:9224", nil},
		{"localhost http slash", "http://localhost:9224/", nil},
		{"localhost ws", "ws://localhost:9224/devtools/page", nil},
		{"subdomain localhost", "subdomain.localhost:9224", nil},
		{"nip.io subdomain", "app.127.0.0.1.nip.io:9224", nil},
		{"any domain with port 9224", "myhost.internal:9224", nil},

		// 4. IPv6 Formats
		{"ipv6 bracket loopback", "[::1]:9224", nil},
		{"ipv6 http url", "http://[::1]:9224/json/version", nil},
		{"ipv6 ws url", "ws://[::1]:9224/devtools/page", nil},
		{"ipv6 all zeroes", "[::]:9224", nil},
		{"ipv6 full expansion", "[0000:0000:0000:0000:0000:0000:0000:0001]:9224", nil},
		{"ipv4-mapped ipv6", "[::ffff:127.0.0.1]:9224", nil},
		{"ipv4-mapped ipv6 http", "http://[::ffff:127.0.0.1]:9224/json/version", nil},

		// 5. URL Query, Hash, UserInfo
		{"url with userinfo", "http://admin:secret@127.0.0.1:9224/json", nil},
		{"url with query params", "http://127.0.0.1:9224/json/version?t=123&v=2", nil},
		{"url with hash fragment", "http://127.0.0.1:9224/json/version#section", nil},
		{"url with query and hash", "http://localhost:9224/path?a=1#b", nil},

		// 6. Schemes Case-Insensitivity & Whitespace
		{"upper case HTTP scheme", "HTTP://127.0.0.1:9224/json", nil},
		{"upper case WS scheme", "WS://LOCALHOST:9224/devtools", nil},
		{"surrounding whitespace", "  http://127.0.0.1:9224/json  ", nil},
		{"trailing newline", "127.0.0.1:9224\n", nil},

		// 7. Non-standard or raw target strings
		{"raw colon port", ":9224", nil},
		{"tcp scheme", "tcp://127.0.0.1:9224", nil},

		// 8. Leading Zero Representations (Bypass Hardening)
		{"leading zero port 09224", "127.0.0.1:09224", nil},
		{"leading double zero port 009224", "127.0.0.1:009224", nil},
		{"leading triple zero port 0009224", "127.0.0.1:0009224", nil},
		{"leading zero http url", "http://127.0.0.1:09224/json/version", nil},
		{"leading double zero http url", "http://127.0.0.1:009224/json/version", nil},
		{"leading zero ws url", "ws://127.0.0.1:09224/devtools/page/abc", nil},
		{"leading zero wss url", "wss://127.0.0.1:09224/devtools/browser/xyz", nil},
		{"leading zero localhost", "localhost:09224", nil},
		{"leading double zero localhost", "localhost:009224", nil},
		{"leading zero ipv6", "[::1]:09224", nil},
		{"leading double zero ipv6", "[::1]:009224", nil},
		{"leading zero raw colon", ":09224", nil},
		{"leading double zero raw colon", ":009224", nil},
		{"bare leading zero string", "09224", nil},
		{"bare double leading zero string", "009224", nil},
	}

	for _, tt := range forbiddenTargets {
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
				assertNotPort9224(tt.target, tt.ports...)
			}()

			if !panicked {
				t.Errorf("SECURITY FAILURE: Target %q with ports %v bypassed assertNotPort9224 without panicking!", tt.target, tt.ports)
			} else {
				msg := fmt.Sprint(panicVal)
				if !strings.Contains(msg, "Anti-9224 barrier") {
					t.Errorf("Panicked but message did not mention Anti-9224 barrier: %s", msg)
				}
			}
		})
	}
}

// TestAdversarialSafePorts ensures that legitimate non-9224 ports NEVER panic.
func TestAdversarialSafePorts(t *testing.T) {
	safeTargets := []struct {
		name   string
		target string
		ports  []int
	}{
		{"safe dummy port 59999", "http://127.0.0.1:59999/json/version", []int{59999}},
		{"isolated harness port 9333", "http://127.0.0.1:9333/json/version", []int{9333}},
		{"isolated harness port 9334", "http://127.0.0.1:9334/json/version", []int{9334}},
		{"ephemeral port 49152", "http://127.0.0.1:49152/json/version", []int{49152}},
		{"ephemeral port 65535", "http://127.0.0.1:65535/json/version", []int{65535}},
		{"boundary port 9223 (one below)", "http://127.0.0.1:9223/json/version", []int{9223}},
		{"boundary port 9225 (one above)", "http://127.0.0.1:9225/json/version", []int{9225}},
		{"port 19224 (substring 9224)", "http://127.0.0.1:19224/json/version", []int{19224}},
		{"port 29224 (substring 9224)", "http://127.0.0.1:29224/json/version", []int{29224}},
		{"localhost safe port 59999", "http://localhost:59999/json/version", []int{59999}},
		{"ipv6 safe port 59999", "http://[::1]:59999/json/version", []int{59999}},
		{"ws safe port 59999", "ws://127.0.0.1:59999/devtools/page/1", nil},
		{"query param mentioning 9224 on safe port", "http://127.0.0.1:59999/test?item_id=9224", []int{59999}},
		{"path mentioning 9224 on safe port", "http://127.0.0.1:59999/issues/9224", []int{59999}},
	}

	for _, tt := range safeTargets {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("FALSE POSITIVE: Safe target %q with ports %v triggered panic: %v", tt.target, tt.ports, r)
				}
			}()
			assertNotPort9224(tt.target, tt.ports...)
		})
	}
}

// TestAdversarialHTTPClientInterceptor tests anti9224Transport directly and via cdpHTTPClient.
func TestAdversarialHTTPClientInterceptor(t *testing.T) {
	t.Run("cdpHTTPClient blocks forbidden URL", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			_, _ = cdpHTTPClient.Get("http://127.0.0.1:9224/json/version")
		}()
		if !panicked {
			t.Fatal("cdpHTTPClient failed to panic on http://127.0.0.1:9224/json/version")
		}
	})

	t.Run("cdpHTTPClient blocks localhost 9224", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			_, _ = cdpHTTPClient.Get("http://localhost:9224/json/list")
		}()
		if !panicked {
			t.Fatal("cdpHTTPClient failed to panic on http://localhost:9224/json/list")
		}
	})

	t.Run("cdpHTTPClient blocks IPv6 [::1]:9224", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			_, _ = cdpHTTPClient.Get("http://[::1]:9224/json/version")
		}()
		if !panicked {
			t.Fatal("cdpHTTPClient failed to panic on http://[::1]:9224/json/version")
		}
	})

	t.Run("cdpHTTPClient does not panic on safe port", func(t *testing.T) {
		// Target a non-existent port 59999 with short timeout; must not panic (connection refused is expected)
		client := &http.Client{
			Transport: cdpHTTPClient.Transport,
			Timeout:   50 * time.Millisecond,
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("cdpHTTPClient panicked on safe port 59999: %v", r)
			}
		}()
		_, _ = client.Get("http://127.0.0.1:59999/json/version")
	})
}

// TestAdversarialCDPPoolDialConn tests dialConn panic on 9224.
func TestAdversarialCDPPoolDialConn(t *testing.T) {
	t.Run("dialConn panics on ws 9224", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			_, _ = dialConn("ws://127.0.0.1:9224/devtools/page/test")
		}()
		if !panicked {
			t.Fatal("dialConn failed to panic on ws://127.0.0.1:9224/devtools/page/test")
		}
	})

	t.Run("dialConn panics on localhost ws 9224", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			_, _ = dialConn("ws://localhost:9224/devtools/browser/abc")
		}()
		if !panicked {
			t.Fatal("dialConn failed to panic on ws://localhost:9224/devtools/browser/abc")
		}
	})

	t.Run("dialConn panics on ipv6 ws 9224", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			_, _ = dialConn("ws://[::1]:9224/devtools/page/123")
		}()
		if !panicked {
			t.Fatal("dialConn failed to panic on ws://[::1]:9224/devtools/page/123")
		}
	})
}

// TestAdversarialHarnessAssertSafePort tests assertSafePort from test_isolated_harness.go.
func TestAdversarialHarnessAssertSafePort(t *testing.T) {
	t.Run("assertSafePort panics on 9224", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			assertSafePort(9224)
		}()
		if !panicked {
			t.Fatal("assertSafePort failed to panic on 9224")
		}
	})

	t.Run("assertSafePort panics on port 0", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			assertSafePort(0)
		}()
		if !panicked {
			t.Fatal("assertSafePort failed to panic on 0")
		}
	})

	t.Run("assertSafePort panics on port > 65535", func(t *testing.T) {
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			assertSafePort(70000)
		}()
		if !panicked {
			t.Fatal("assertSafePort failed to panic on 70000")
		}
	})

	t.Run("assertSafePort accepts safe ports", func(t *testing.T) {
		for _, port := range []int{9333, 59999, 9223, 9225, 49152} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("assertSafePort panicked on safe port %d: %v", port, r)
					}
				}()
				assertSafePort(port)
			}()
		}
	})
}

// TestAdversarialEdgeCases probes corner cases: leading zeros, encoded URLs, etc.
func TestAdversarialEdgeCases(t *testing.T) {
	// Leading zeros in port: "http://127.0.0.1:09224"
	t.Run("leading zero port 09224", func(t *testing.T) {
		target := "http://127.0.0.1:09224/json/version"
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			assertNotPort9224(target)
		}()
		t.Logf("target=%q panicked=%v", target, panicked)
	})

	// Percent-encoded port: "http://127.0.0.1:%39%32%32%34"
	t.Run("percent encoded port %39%32%32%34", func(t *testing.T) {
		target := "http://127.0.0.1:%39%32%32%34/json/version"
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			assertNotPort9224(target)
		}()
		t.Logf("target=%q panicked=%v", target, panicked)
	})

	// Percent-encoded colon: "http://127.0.0.1%3A9224"
	t.Run("percent encoded colon %3A9224", func(t *testing.T) {
		target := "http://127.0.0.1%3A9224/json/version"
		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			assertNotPort9224(target)
		}()
		t.Logf("target=%q panicked=%v", target, panicked)
	})
}

// TestEmpiricalLeadingZeroNetworkBypass empirically proves that OS/Go network dialers
// accept leading zeros in port numbers (connecting to the underlying port),
// demonstrating that failing to catch leading zero strings in isPort9224Target is a real bypass.
func TestEmpiricalLeadingZeroNetworkBypass(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open test listener: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	// Accept in background
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	leadingZeroAddr := fmt.Sprintf("127.0.0.1:0%d", port)
	conn, err := net.DialTimeout("tcp", leadingZeroAddr, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("net.Dial to %s failed: %v", leadingZeroAddr, err)
	}
	conn.Close()

	// Prove that isPort9224Target flags 09224
	if isPort9224Target("http://127.0.0.1:09224/json/version") {
		t.Log("isPort9224Target flagged 09224")
	} else {
		t.Log("EMPIRICAL CONFIRMATION: isPort9224Target does NOT detect leading zero '09224'!")
	}
}

func TestAdversarialLeadingZeroEndToEnd(t *testing.T) {
	t.Run("cdpHTTPClient blocks leading zero 09224", func(t *testing.T) {
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
			_, _ = cdpHTTPClient.Get("http://127.0.0.1:09224/json/version")
		}()
		if !panicked {
			t.Fatal("SECURITY FAILURE: cdpHTTPClient failed to panic on http://127.0.0.1:09224/json/version")
		}
	})

	t.Run("dialConn blocks leading zero ws 09224", func(t *testing.T) {
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
			_, _ = dialConn("ws://127.0.0.1:09224/devtools/browser/abc")
		}()
		if !panicked {
			t.Fatal("SECURITY FAILURE: dialConn failed to panic on ws://127.0.0.1:09224/devtools/browser/abc")
		}
	})
}
