# Project: Argus MCP Server Refactoring and Upgrade

## 1. Architecture Overview
Argus is a high-performance Model Context Protocol (MCP) server written in Go (`go 1.26.5`) providing browser automation and diagnostic capabilities via the Chrome DevTools Protocol (CDP).

This project refactors Argus to:
1. Implement and resolve all 9 core GitHub issues (Issues #1 to #9).
2. Radically consolidate the tool surface from 48 down to 26 canonical tools (45.8% reduction), eliminating cold tools, dead code, and schema bloat.
3. Enforce strict headless Chrome test isolation (custom dynamic ports, temporary profiles, zero access to host CDP port 9224) and ensure 100% passing tests under `-race`.
4. Validate all 9 scenarios end-to-end in isolated real browser instances.

### Core Architectural Layers
- **MCP Server Layer** (`server.go`, `toolmeta.go`, `tools_*.go`): Exposes standard JSON-RPC MCP tools, schemas, and argument validation.
- **CDP Engine Layer** (`cdp.go`, `cdppool.go`, `chrome.go`, `chrome_darwin.go`, `eventhub.go`): Manages WebSocket connections, target routing, event multiplexing, and browser lifecycle.
- **Interaction & Perception Layer** (`action.go`, `autowait.go`, `screenshot.go`, `recorder.go`): Handles DOM queries, synthetic event dispatch, AX tree resolution, causal evidence recording, and visual capture.
- **Isolation & Test Harness Layer** (`test_isolated_harness.go`): Launches isolated headless Chrome child processes on ephemeral ports with dedicated temp directories.

---

## 2. Feature Inventory
Every requirement from the Survey phase and `ORIGINAL_REQUEST.md` is inventoried below with its assigned milestone:

| # | Feature / Requirement | Description | Milestone | Source |
|---|-----------------------|-------------|-----------|--------|
| F01 | Anti-9224 Panic Barrier & Isolation Config | Fix `config.go:init()` for `ARGUS_ATTACH`, panic barrier on 9224, fix `chrome_darwin.go` wrapper prioritization | M1 | Survey Infra |
| F02 | Non-browser Unit Test Guarding | Add `//go:build integration` to `eval_harness_test.go`, `-short` guard to `screenshot_test.go` | M1 | Survey Infra |
| F03 | Isolated Headless Chrome Test Harness | Reusable test harness (`StartIsolatedChrome`) with ephemeral port, temp profile, raw binary exec, `Browser.close` | M1 | Survey Infra |
| F04 | Issue #1: `batch_actions` Alias | Support `key` and `press` action aliases in `batch_actions` step dispatcher | M2 | Issue #1 |
| F05 | Issue #2: Controlled Component Setter & Events | Full synthetic event sequence (`focus`, `beforeinput`, prototype setter, `InputEvent`, `change`) in `fill` | M2 | Issue #2 |
| F06 | Issue #2: Transition-Aware Settle | Settle probe (`requestAnimationFrame`, animation/mutation settle) post-fill before snapshot | M2 | Issue #2 |
| F07 | Issue #3: Unified a11y `nodeId` Reference | Resolve `a11y_snapshot` node IDs in `click`, `fill`, `get_element` via `DOM.getBoxModel` and `DOM.resolveNode` | M2 | Issue #3 |
| F08 | Issue #4: Act-and-Verify Feedback Loop | Single-turn causal verification payload (`navigated`, `network_requests`, `dom_changes`) in action responses | M2 | Issue #4 |
| F09 | Issue #5: Smart Framework Locator | Container/label sniffing for `.el-input`, `<label>`, outer cards; invisible checkbox wrapper delegation | M2 | Issue #5 |
| F10 | Issue #6: CDP Dialog Anti-Deadlock | `Page.javascriptDialogOpening` background listener with auto-accept/dismiss and causal evidence recording | M3 | Issue #6 |
| F11 | Issue #7: Dev Overlay Auto-Diagnose | Detection of Vite, Next.js, Webpack overlays in `diagnose` and early abort in `wait_for` loops | M3 | Issue #7 |
| F12 | Issue #8: Element Screenshot & Disk Mode | Element bounding box clipping (`selector`/`ref`/`nodeId`) and lightweight disk mode (`disk: true`) without base64 blob | M3 | Issue #8 |
| F13 | Issue #9: `mock_route` Tool | High-level route interceptor supporting glob/regex patterns, status, headers, JSON body, rule listing/clearing | M3 | Issue #9 |
| F14 | Tool Consolidation: Tab CRUD | Merge `new_tab`, `list_tabs`, `select_tab`, `close_tab` into unified `tab` tool; delete `fork` | M4 | Tool Survey |
| F15 | Tool Consolidation: Storage CRUD | Merge `get_storage`, `set_storage`, `clear_storage` into unified `storage` tool | M4 | Tool Survey |
| F16 | Tool Consolidation: Element & Find | Merge `find` and `inspect_point` into unified `get_element` tool | M4 | Tool Survey |
| F17 | Tool Consolidation: Screenshot Merge | Merge `screenshot_full_page` into `screenshot(full_page=true)` | M4 | Tool Survey |
| F18 | Tool Consolidation: Wait & Conditions | Merge `exists` and `try_until` into unified `wait_for` tool | M4 | Tool Survey |
| F19 | Tool Elimination: Cold / Risky Tools | Delete `network_request` (SSRF risk), `pdf`, `list_downloads`, `diff_snapshot`, `export_trace`, `tool_summary`, `start_recording`, `stop_recording` | M4 | Tool Survey |
| F20 | Dead Code & Schema Pruning | Clean up unused schemas in `toolmeta.go`, obsolete handlers, types, and tests; produce Tool Review Report | M4 | Tool Survey |
| F21 | E2E Isolated Real Browser Suite (Tiers 1-4) | Comprehensive opaque-box E2E tests across all 9 scenarios in isolated headless Chrome instances | M5 | E2E Testing Track |
| F22 | Adversarial Hardening (Tier 5) | White-box stress testing and boundary validation | M5 | E2E Testing Track |

---

## 3. Milestones

| # | Name | Scope | Dependencies | Status |
|---|------|-------|--------------|--------|
| M1 | Isolation Infra & Test Safety Barrier | F01, F02, F03 | None | DONE |
| M2 | Core Interaction & Action Enhancements | F04, F05, F06, F07, F08, F09 | M1 | DONE |
| M3 | Diagnostics, Dialogs, Screenshot & Mocking | F10, F11, F12, F13 | M1 | DONE |
| M4 | Tool Consolidation & Dead Code Cleanup | F14, F15, F16, F17, F18, F19, F20 | M2, M3 | DONE |
| M5 | Final E2E Isolated Real Browser Acceptance | F21, F22 | M4, E2E Test Suite | IN_PROGRESS |


---

## 4. Interface Contracts

### 4.1 a11y Node Reference Bridge (`a11y_snapshot` ↔ Interaction Tools)
- Input: `ref` parameter accepts integer/string a11y node IDs (e.g. `"12"`, `"ax:12"`), CSS selectors, or ref IDs.
- Resolution Contract:
  - If `ref` matches `ax:\d+` or integer ID, lookup in tab's AX cache or call `Accessibility.getFullAXTree` / `DOM.resolveNode(backendNodeId)`.
  - For coordinates (`click`): call `DOM.getBoxModel` to compute center `(x, y)`.
  - For DOM manipulation (`fill`): call `Runtime.callFunctionOn` using the resolved `RemoteObject.ObjectId`.

### 4.2 Act-and-Verify Verification Payload (`click`, `fill`, `type`, `scroll`, `batch_actions`)
- Response schema extension:
  ```json
  "verification": {
    "navigated": boolean,
    "current_url": string,
    "network_requests": [{"method": string, "url": string, "status": int}],
    "dom_changes": {"added_count": int, "removed_count": int, "status_text": string},
    "dialog_handled": {"type": string, "message": string, "status": string},
    "verified": boolean
  }
  ```

### 4.3 Unified `mock_route` Contract
- Replaces `intercept`, `list_intercepts`, `clear_intercepts`.
- Input Schema:
  - `action`: `"mock"` (default), `"block"`, `"list"`, `"clear"`, `"remove"`
  - `pattern`: string (supports glob `*/api/*` or regex `^https://...`)
  - `method`: string (optional, e.g. `"POST"`)
  - `status`: int (default 200)
  - `headers`: map[string]string
  - `body`: any (string or JSON object)

### 4.4 Consolidated `tab` Contract
- Input Schema:
  - `action`: `"list"`, `"new"`, `"select"`, `"close"`
  - `url`: string (for `"new"`)
  - `tab_id`: string (for `"select"`, `"close"`)
  - `background`: bool

### 4.5 Consolidated `storage` Contract
- Input Schema:
  - `action`: `"get"`, `"set"`, `"clear"`
  - `type`: `"cookie"`, `"local"`, `"session"`, `"all"`
  - `key`: string
  - `value`: string (for `"set"`)

---

## 5. Code Layout & Exclusive Ownership

To prevent merge conflicts during concurrent execution:
- **Milestone 1**: Owns `config.go`, `chrome_darwin.go`, `eval_harness_test.go`, `screenshot_test.go`, and new `test_isolated_harness.go`.
- **Milestone 2**: Owns `tools_interaction.go`, `autowait.go`, `action.go`, `tools_snapshot.go` (a11y caching/bridge).
- **Milestone 3**: Owns `tools_dialog.go`, `tools_trace.go`, `tools_screenshot.go`, `tools_intercept.go` (new `mock_route`).
- **Milestone 4**: Owns `toolmeta.go`, `server.go`, `tools_tabs.go`, `tools_storage.go`, deletion of obsolete files (`tools_misc.go`, `tools_record.go`, `tools_diff.go`, `tools_discovery.go`), and `docs/tool-review-report.md`.
- **E2E Testing Track**: Owns `test/e2e/` (standalone isolated E2E runners, fixtures, and scenarios).

---

## 6. Strict Browser Isolation Policy & Red Lines

1. **Host Port 9224 is SACROSANCT**: Absolutely FORBIDDEN to dial, ping, or connect to `127.0.0.1:9224`. An anti-9224 panic barrier in test code will panic immediately if 9224 is attempted.
2. **Mandatory `-short` for Unit Tests**: Regular test runs MUST include `-short` (`go test -short ./...`) to ensure no unmocked tests ever trigger Chrome launches.
3. **No macOS LaunchServices open Commands**: Raw binary `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome` MUST be executed directly via `exec.Command` with `--headless=new` and `mktemp -d` user data directories.
4. **ARGUS_ATTACH Enforcement**: All tests interacting with Chrome must configure `ARGUS_ATTACH=true` to ensure Argus NEVER falls back to `launchChrome()`.
5. **Zero Data Races**: All code must pass `go test -short -race ./...` with 100% success.
