# Argus E2E Testing Infrastructure & Runner Architecture

- **Document Version**: 1.0.0
- **Document Path**: `/Users/tangtszho/Work/olympus/argus/TEST_INFRA.md`
- **Owner**: Milestone 5 E2E Test Infrastructure & Runner Architect
- **Status**: Canonical Specification & Implementation Guide
- **Referenced Directives**: `PROJECT.md`, `ORIGINAL_REQUEST.md`, `survey_infra.md`, `test_isolated_harness.go`

---

## 1. Executive Overview & Architecture Principles

Argus is a high-performance Model Context Protocol (MCP) server written in Go providing browser automation and UI diagnostics over the Chrome DevTools Protocol (CDP).

Milestone 5 establishes the **Final End-to-End (E2E) Acceptance Testing Infrastructure** to validate the complete refactored system across:
1. All 9 core GitHub Issues (Issues #1 to #9).
2. The consolidated 27-tool surface (down from 48 tools, featuring canonical tools `tab`, `storage`, `get_element`, `wait_for`, `screenshot`, and `mock_route`).
3. Strict multi-tenant process and port isolation.

### 1.1 Core Architecture Principles

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                                   Go Test Runner (E2E)                                 │
│                                                                                        │
│   ┌────────────────────────────────┐         ┌─────────────────────────────────────┐   │
│   │     In-Memory MCP Server       │         │      E2E Test Orchestrator          │   │
│   │   (registerAllTools(server))   │         │ (Category-Partition, BVA, Pairwise) │   │
│   └───────────────▲────────────────┘         └──────────────────┬──────────────────┘   │
│                   │                                             │                      │
│     mcp.NewInMemoryTransports()                                 │                      │
│                   │                                             │                      │
│   ┌───────────────▼────────────────┐                            │                      │
│   │     In-Memory MCP Client       │◄───────────────────────────┘                      │
│   │  (callMCPTool/extractResult)   │                                                   │
│   └───────────────┬────────────────┘                                                   │
│                   │                                                                    │
│   ┌───────────────▼────────────────────────────────────────────────────────────────┐   │
│   │              Argus CDP Client Layer (ARGUS_ATTACH=true)                        │   │
│   │              • Hard Anti-9224 Panic Barrier (assertSafePort)                   │   │
│   │              • Configured Dynamic Port = EphemeralPort                         │   │
│   └───────────────────────────────────────────────┬────────────────────────────────┘   │
└───────────────────────────────────────────────────┼────────────────────────────────────┘
                                                    │ CDP over WS / HTTP (127.0.0.1:EphemeralPort)
                                                    ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                     Isolated Headless Chrome Instance (OS Child Process)                │
│                                                                                        │
│   • Binary:     /Applications/Chrome Argus.app/Contents/MacOS/chrome-argus.bin         │
│                 (Raw binary directly spawned via exec.Command; NO /usr/bin/open)       │
│   • Port:       Allocated dynamically via net.Listen("tcp", "127.0.0.1:0")             │
│   • Flags:      --headless=new --disable-gpu --remote-debugging-port=<Port>            │
│   • Profile:    os.MkdirTemp("", "argus-isolated-<Port>-*")                            │
│   • Target:     about:blank (pre-selected initial page target)                         │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

1. **Opaque-Box Requirement-Driven Testing**: Tests interact primarily through the standardized MCP protocol surface (`cs.CallTool`), simulating authentic agent tool execution rather than calling internal helper functions directly.
2. **Strict Browser & Host Isolation**: The permanent user Chrome Argus instance (`127.0.0.1:9224`) is sacrosanct. Any dial, connection, or probe touching 9224 will trip a hard panic barrier immediately.
3. **Deterministic Process & Directory Lifecycle**: Every isolated Chrome instance is tracked, cleanly terminated via CDP `Browser.close` with bounded fallback to OS kill, and its temporary profile directory is completely wiped from disk.
4. **Zero-Impact on Regular Unit Testing**: Regular unit tests (`make test`) must execute with mandatory `-short` mode in <15s without ever launching or requiring Chrome.

---

## 2. Headless Chrome Isolation & Safety Guarantees

### 2.1 The 7 Isolation Commandments

To guarantee absolute non-interference with host processes and active user sessions, every E2E test must adhere strictly to the Seven Isolation Commandments:

| # | Commandment | Implementation Mechanism | Safety Objective |
|---|---|---|---|
| **1** | **Direct Raw Binary Execution Only** | Resolve `chrome-argus.bin` or raw Chrome binary directly via `exec.CommandContext`. Never use `/usr/bin/open` or `.app` wrapper scripts. | Completely bypasses macOS LaunchServices, preventing blank windows and popups on the user's desktop screen. |
| **2** | **Forced Modern Headless Mode** | CLI flags `--headless=new` and `--disable-gpu` supplied on every launch. | Guarantees zero GUI window creation across macOS, Linux, and Windows. |
| **3** | **Dedicated Ephemeral User Profile** | `tmpDir, err := os.MkdirTemp("", fmt.Sprintf("argus-isolated-%d-*", port))`. | Absolute partition of cookies, localStorage, cache, and history; never touches `~/.config/argus/chrome-profile`. |
| **4** | **Dynamic Ephemeral Port Allocation** | Kernel port binding via `net.Listen("tcp", "127.0.0.1:0")`, extracting dynamic port. | Eliminates port collisions and allows fully concurrent test executions. |
| **5** | **Enforced Argus Attach Mode** | `os.Setenv("ARGUS_ATTACH", "true")`, `setConfig("about:blank", port, &attachTrue)`. | Prevents Argus from falling back to `launchChrome()` if connection drops. |
| **6** | **Hard Anti-9224 Panic Barrier** | `assertSafePort(port)` panics immediately if `port == 9224`. | Hard failsafe terminating test execution before any byte touches host port 9224. |
| **7** | **Deterministic Idempotent Cleanup** | `closeAllConns()`, CDP `Browser.close`, process kill after 3s deadline, 25-attempt profile deletion retry, reset config to `SafeDummyPort = 59999`. | Guarantees zero leaked Chrome zombies, zero leaked disk files, and a safe dummy state. |

### 2.2 Reusable Harness API (`test_isolated_harness.go`)

The project provides two standardized entry points in `test_isolated_harness.go`:

```go
// StartIsolatedChrome launches an isolated headless Chrome instance and configures Argus to attach to it.
// It automatically registers the idempotent cleanup function with t.Cleanup().
// Intended for use in Go tests taking *testing.T.
func StartIsolatedChrome(t *testing.T) (*IsolatedChromeInstance, func())

// LaunchIsolatedChrome launches an isolated headless Chrome instance and configures Argus to attach to it.
// Can be used in TestMain or custom standalone runners outside *testing.T.
func LaunchIsolatedChrome() (*IsolatedChromeInstance, func(), error)
```

Both functions return `*IsolatedChromeInstance`:
- `inst.Port`: Assigned dynamic TCP port (guaranteed `!= 9224`).
- `inst.ProfileDir`: Absolute path to the isolated temporary directory.
- `inst.Cmd`: Running `*exec.Cmd` child process.
- `inst.WebSocketDebuggerURL`: Browser-level CDP WebSocket URL.
- `inst.TargetID`: Auto-discovered and selected initial page target ID.
- `inst.BaseURL()`: HTTP root endpoint (e.g. `http://127.0.0.1:54321`).
- `inst.Version()`: Fetches `/json/version` metadata.
- `inst.Targets()`: Fetches `/json/list` targets.

---

## 3. Directory Layout & E2E Package Architecture

### 3.1 Package Architecture Decision: Root `package main` vs `test/e2e/`

A critical architectural constraint in Go is that **`package main` cannot be imported by external packages**.
Because the Argus server, tool handlers, global state (`rec`, `cdpPort`, `configuredAttach`), and `test_isolated_harness.go` reside in `package main`, placing Go test code in a subpackage (such as `package e2e` under `test/e2e/`) causes the compiler to reject imports of `github.com/deungjaho/argus`:
```
import "github.com/deungjaho/argus" is a program, not an importable package
```

#### Architectural Comparison

| Dimension | Option A: Subpackage `test/e2e/` | Option B: Root `package main` with `//go:build integration` (Selected) |
|---|---|---|
| **Compilation** | Cannot import root `package main`. Must spawn `bin/argus` as external CLI process over stdin/stdout. | Compiles natively with the full Argus codebase as `package main`. |
| **Harness Reuse** | Cannot import `StartIsolatedChrome()`. Must duplicate Chrome launch & anti-9224 barrier logic. | Seamless 1:1 reuse of `StartIsolatedChrome(t)` and `LaunchIsolatedChrome()`. |
| **MCP Execution** | Requires compiling binary and managing stdio JSON-RPC pipes. High turn latency (>150ms). | Direct in-memory transports (`mcp.NewInMemoryTransports()`). Ultra-fast turn latency (<20ms). |
| **White-Box Verification (Tier 5)** | Impossible. Cannot observe internal event hubs, goroutines, or CDP connection pools. | Full observability of `rec.Events()`, DOM mutation summaries, and internal memory cleanup. |
| **Safety Barrier** | Relies on external scripts to maintain isolation flags. | Enforced by Go compiler build tags (`//go:build integration`) and `if testing.Short() { t.Skip() }`. |

### 3.2 Canonical Directory Structure

```
argus/
├── TEST_INFRA.md                                  # This document: Architecture & Runner Specification
├── Makefile                                       # make test, make test-race, make test-e2e
├── test_isolated_harness.go                       # StartIsolatedChrome, LaunchIsolatedChrome (package main)
├── test_isolated_harness_test.go                  # Harness unit validation (//go:build integration)
│
├── e2e_runner_test.go                             # Shared E2E test setup: setupE2EMCPServer, setupE2EFixtureServer, assert helpers (//go:build integration)
├── e2e_tier1_features_test.go                     # Tier 1: Feature Coverage (Issues #1-#9 + Consolidated Tools) (//go:build integration)
├── e2e_tier2_boundaries_test.go                   # Tier 2: Boundary & Corner Cases (//go:build integration)
├── e2e_tier3_pairwise_test.go                     # Tier 3: Cross-Feature Combinations (Pairwise) (//go:build integration)
├── e2e_tier4_workloads_test.go                    # Tier 4: Real-World Multi-Step Application Workloads (//go:build integration)
├── e2e_tier5_adversarial_test.go                  # Tier 5: White-Box Stress & Adversarial Hardening (//go:build integration)
│
└── test/
    ├── scenarios/                                 # Static scenario HTML/JS files (G1-G5 golden scenarios)
    └── e2e/
        ├── fixtures/                              # HTML/JS test pages for E2E scenarios
        │   ├── controlled_form.html               # React-like form (Issue #2)
        │   ├── a11y_tree.html                     # Semantic ARIA tree (Issue #3)
        │   ├── dialog_triggers.html               # Alert/confirm/prompt triggers (Issue #6)
        │   ├── dev_overlays.html                  # Vite/Next/Webpack error overlays (Issue #7)
        │   ├── screenshot_target.html             # Styled elements for clipping (Issue #8)
        │   ├── mock_route_app.html                # Client app calling intercepted APIs (Issue #9)
        │   ├── smart_locators.html                # Cards, input wrappers, labels (Issue #5)
        │   └── complex_app.html                   # Multi-page SPA workflow (Tier 4)
        └── testdata/                              # Golden files, test images, mock JSON responses
```

---

## 4. Test Methodology Framework (Tiers 1–5)

The E2E testing framework employs a structured, requirement-driven 5-tier methodology:

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    ARGUS 5-TIER E2E TEST METHODOLOGY PYRAMID                    │
├─────────────────────────────────────────────────────────────────────────────────┤
│  Tier 5: Adversarial & White-Box Hardening (Stress, leaks, crash resilience)    │
├─────────────────────────────────────────────────────────────────────────────────┤
│  Tier 4: Real-World Application Workloads (Multi-turn workflows, SPA, checkout) │
├─────────────────────────────────────────────────────────────────────────────────┤
│  Tier 3: Cross-Feature Combinations (Pairwise combinatorial interactions)       │
├─────────────────────────────────────────────────────────────────────────────────┤
│  Tier 2: Boundary & Corner Cases (BVA: empty, extreme timeouts, unicode, zero)  │
├─────────────────────────────────────────────────────────────────────────────────┤
│  Tier 1: Feature Coverage (Category-Partition: >=5 cases per feature)           │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 4.1 Tier 1 — Feature Coverage (Category-Partition Testing)
- **Methodology**: Category-Partition Method (TSL). Decompose each functional specification into input domains, environmental characteristics, and output properties.
- **Requirement**: **>=5 test cases per feature** across all 9 GitHub Issues and 5 consolidated tools.
- **Execution Target**: Verify normal-path functionality, expected output structure, and causal feedback payloads.

### 4.2 Tier 2 — Boundary Value Analysis (BVA) & Corner Cases
- **Methodology**: Boundary Value Analysis on extreme inputs and state boundaries.
- **Requirement**: **>=5 test cases per feature**.
- **Scenarios**:
  - Empty strings, whitespace-only selectors, non-existent node IDs (`ax:99999`).
  - Rapid consecutive interactions without idle intervals.
  - Zero timeout (`timeout_ms: 0`) and extreme timeout values (`timeout_ms: 60000`).
  - Complex Unicode, emojis, multibyte UTF-8 input, and newline escapes in input fields.
  - Off-screen, zero-opacity, and deeply nested DOM elements.

### 4.3 Tier 3 — Cross-Feature Combinations (Pairwise / Combinatorial Testing)
- **Methodology**: Orthogonal Array / Pairwise testing of feature interactions.
- **Scenarios**:
  - `mock_route` + Form submit (`fill`) + `act-and-verify` network request tracking.
  - Form submit + Native JavaScript dialog trigger (`alert`) + dialog auto-resolve + DOM mutation verify.
  - `a11y_snapshot` reference + Smart framework locator container delegation + Element screenshot clipping.
  - `tab` switching + `storage` cookie isolation + `wait_for` transition polling.

### 4.4 Tier 4 — Real-World Application Workloads
- **Methodology**: Realistic multi-step end-to-end task workflows simulating complex web application interactions.
- **Scenarios**:
  - **Full Authentication Flow**: Navigate to login page -> controlled form fill -> submit -> mock API 200 OK -> verify session storage token -> route change.
  - **Dev Server Diagnostic & Recovery**: Navigate to Vite dev page -> detect error overlay via `diagnose` -> trigger code fix via mock -> verify overlay auto-cleared.
  - **E-Commerce Checkout Sequence**: Add item -> cart drawer opens -> fill shipping info -> select shipping radio -> submit -> confirm modal auto-accepts.

### 4.5 Tier 5 — Adversarial & White-Box Hardening
- **Methodology**: Stress testing and boundary validation of Argus concurrency and CDP resilience.
- **Scenarios**:
  - Rapid parallel CDP tool calls across concurrent tabs.
  - Sudden tab destruction mid-action (page closed while `fill` or `click` is executing).
  - Malformed CDP response simulation and WebSocket reconnection resilience.
  - Verification of zero goroutine leaks and zero memory leaks post-test execution.

---

## 5. Feature Matrix & Traceability

The following matrix maps every requirement, issue, and consolidated tool to its test tier, verification criteria, and test implementation targets:

| Feature ID | Subject | Scope | Tier 1 (Coverage) | Tier 2 (Boundary) | Tier 3 (Pairwise) | Tier 4 (Workload) | Verification Target |
|:---|:---|:---|:---:|:---:|:---:|:---:|:---|
| **ISS-01** | Issue #1 | `batch_actions` alias (`key`, `press`, step chains) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Issue1_BatchActions` |
| **ISS-02** | Issue #2 | Controlled component setter & transition-aware wait | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Issue2_ControlledComponent` |
| **ISS-03** | Issue #3 | Unified a11y `nodeId` reference (`ax:N` ↔ click/fill) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Issue3_A11yNodeBridge` |
| **ISS-04** | Issue #4 | Act-and-verify feedback loop (`verification` payload) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Issue4_ActAndVerify` |
| **ISS-05** | Issue #5 | Smart framework locator (container & wrapper sniffing) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Issue5_SmartLocator` |
| **ISS-06** | Issue #6 | CDP dialog anti-deadlock (auto-accept/dismiss) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Issue6_DialogAntiDeadlock` |
| **ISS-07** | Issue #7 | Dev overlay auto-diagnose & `wait_for` abort | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Issue7_DevOverlayDiagnose` |
| **ISS-08** | Issue #8 | Element screenshot & lightweight disk mode | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Issue8_ElementScreenshot` |
| **ISS-09** | Issue #9 | `mock_route` tool (glob/regex pattern, status, body) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Issue9_MockRoute` |
| **TOOL-TAB** | Tool Cons. | `tab` tool (`list`, `new`, `select`, `close`) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Tool_Tab` |
| **TOOL-STO** | Tool Cons. | `storage` tool (`get`, `set`, `clear`, cookie/local) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Tool_Storage` |
| **TOOL-ELM** | Tool Cons. | `get_element` tool (attributes, box model, text) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Tool_GetElement` |
| **TOOL-WAT** | Tool Cons. | `wait_for` tool (selector, text, predicate, timeout) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Tool_WaitFor` |
| **TOOL-SHT** | Tool Cons. | `screenshot` tool (`full_page: true`, element) | 5 cases | 5 cases | Yes | Yes | `TestE2E_Tier1_Tool_Screenshot` |

---

## 6. Runner Invocation Commands & Execution Matrix

### 6.1 Regular Unit Testing (Zero Chrome Spawns)

Regular unit testing must remain completely isolated from real browser processes. It runs in <15s with `-short` mode:

```bash
# Standard unit test run (100% passing, 0 Chrome spawns, attaches to dummy port 59999)
make test
# Equivalent direct command:
ARGUS_ATTACH=1 ARGUS_CDP_PORT=59999 go test -short -count=1 -timeout 60s ./...

# Race-detector unit test run (0 data races, <30s)
make test-race
# Equivalent direct command:
ARGUS_ATTACH=1 ARGUS_CDP_PORT=59999 go test -short -count=1 -race -timeout 120s ./...
```

### 6.2 Full End-to-End Suite Invocation

```bash
# Execute complete E2E acceptance suite against isolated real headless Chrome
make test-e2e
# Equivalent direct command:
go test -v -tags=integration -count=1 -timeout 15m -run "TestE2E_" ./...
```

### 6.3 Selective Execution by Test Tier

The test runner supports targeted execution for rapid iterative feedback:

```bash
# Execute Tier 1: Feature Coverage suite
go test -v -tags=integration -count=1 -timeout 5m -run "TestE2E_Tier1_" ./...

# Execute Tier 2: Boundary & Corner Cases suite
go test -v -tags=integration -count=1 -timeout 5m -run "TestE2E_Tier2_" ./...

# Execute Tier 3: Cross-Feature Combinations (Pairwise) suite
go test -v -tags=integration -count=1 -timeout 5m -run "TestE2E_Tier3_" ./...

# Execute Tier 4: Real-World Workload Workflows suite
go test -v -tags=integration -count=1 -timeout 5m -run "TestE2E_Tier4_" ./...

# Execute Tier 5: Adversarial & Stress Hardening suite
go test -v -tags=integration -count=1 -timeout 5m -run "TestE2E_Tier5_" ./...
```

### 6.4 Selective Execution by GitHub Issue / Feature

```bash
# Test specific Issue #1 (batch_actions alias)
go test -v -tags=integration -count=1 -timeout 2m -run "TestE2E_Tier1_Issue1" ./...

# Test specific Issue #2 (controlled components)
go test -v -tags=integration -count=1 -timeout 2m -run "TestE2E_Tier1_Issue2" ./...

# Test specific Issue #6 (dialog anti-deadlock)
go test -v -tags=integration -count=1 -timeout 2m -run "TestE2E_Tier1_Issue6" ./...

# Test specific Issue #9 (mock_route)
go test -v -tags=integration -count=1 -timeout 2m -run "TestE2E_Tier1_Issue9" ./...
```

---

## 7. Pass/Fail Semantics & Invariants

An E2E test execution run is declared **SUCCESSFUL** if and only if all of the following conditions are satisfied:

### 7.1 Zero-Tolerance Test Execution Invariants
1. **100% Pass Rate**: Zero test failures, zero test errors, and zero panic occurrences across all executed test functions.
2. **Zero Data Races**: When run with `-race`, Go race detector reports 0 data races.
3. **Bounded Execution Timeout**: All tests finish within their configured timeout budgets (per-test deadline: <=30s; full suite deadline: <=15m).

### 7.2 Strict Environmental Isolation Invariants
1. **Zero Host Connections to 9224**: No test may connect, ping, or communicate with `127.0.0.1:9224`. Any violation triggers an immediate panic in `assertSafePort(port)` and terminates the run.
2. **Zero Leaked Chrome Processes**: After `t.Cleanup()` executes, no orphan `chrome-argus.bin` or Chrome child process remains alive. Verified via child process wait and PID verification.
3. **Zero Leaked Temporary Directories**: The temporary profile directory `argus-isolated-<port>-*` must be completely removed from `/tmp`.
4. **Deterministic Port Reset**: Global Argus configuration is safely restored to `SafeDummyPort = 59999` with `attach=true`.

---

## 8. Unit Test Safety Barrier & Verification Proof

### 8.1 Dual Safety Guard Architecture

Regular unit tests (`make test`) are protected from accidental Chrome launches by a two-layer barrier:

```
Layer 1: Go Build Tag Barrier (//go:build integration)
  └── Files: e2e_*_test.go, test_isolated_harness_test.go, integration_test.go
  └── Rule: When running "go test ./..." without -tags=integration, the Go compiler
      completely ignores and excludes these test files. They are NOT compiled.

Layer 2: Testing Short Mode Guard (if testing.Short())
  └── Rule: In the event a developer runs with -tags=integration AND -short:
      if testing.Short() {
          t.Skip("skipping E2E test in -short mode")
      }
  └── Result: The test returns immediately before calling StartIsolatedChrome().
```

### 8.2 Baseline Verification Evidence

Running `make test` on the repository produces the following benchmark output:

```
ARGUS_ATTACH=1 ARGUS_CDP_PORT=59999 go test -short -count=1 -timeout 60s ./...
ok  	github.com/deungjaho/argus	11.684s
```

- **Execution Duration**: 11.684s
- **Pass Rate**: 100% (57 test files, all tests passing)
- **Chrome Launches**: Exactly 0
- **Connections to Port 9224**: Exactly 0
