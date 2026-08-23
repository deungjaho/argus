# Perception Benchmark

Status: `[proposal]`. Modeled on the existing Runtime 8 complex test
patterns (`runtime8_complex_test.go`, `eval_harness_test.go`). This is a
specification for a benchmark suite, not a completed implementation.

The benchmark is the gate for OCR (BL-10) and for any future perception
implementation: a measured workflow must demonstrate that
single-channel perception is insufficient before fusion work beyond the
correctness fixes proceeds. See
[perception-and-action-design.md](perception-and-action-design.md)
section 10 and [engineering-backlog.md](engineering-backlog.md) BL-10.

## 1. Design principles

- **Three-run rule**: every scenario runs 3 consecutive times. A scenario
  passes only if all 3 runs succeed. This catches flakes and state
  leakage.
- **No hidden retry**: the benchmark records every tool call. Retries
  (agent calling a tool again after failure) are counted as
  `retry_count`. A scenario with `success=true` but `retry_count > 0` is
  marked `success_with_retries`, not `success`.
- **Escape-hatch accounting**: use of `eval`, `call_wx_method`, `wxml`,
  `page_data`, or raw RPC for task execution is counted as
  `escape_hatch_used`. A scenario that succeeds only via escape hatches
  is marked `success_via_escape_hatch`, not `success`.
- **Wrong-element accounting**: if the agent clicks/fills an element that
  is not the intended target (determined by ground-truth assertion), it
  is counted as `wrong_element`. A scenario with `wrong_element=true` is
  a failure regardless of final state.
- **Bounded output/RPC/time/events**: every run records input bytes,
  output bytes, estimated tokens, RPC count, latency, and event count.
  Runs exceeding bounds are marked `budget_exceeded`.

## 2. Cross-backend static task suite

### 2.1. Static perception tasks

| Task | Description | Backend | Ground truth | Metrics |
|------|-------------|---------|-------------|---------|
| S1 | Identify all interactive elements on a sparse form (4 elements) | Chrome + WeChat | Element count = 4; each has correct tag, role, text | Element count accuracy, field completeness, token cost |
| S2 | Identify all interactive elements on a dense table (200 rows × 2 buttons) | Chrome + WeChat | Element count = 400; bounded at 80 in snapshot | Bound enforcement, truncation visibility, token cost |
| S3 | Determine visibility of 10 elements with varying display/visibility/opacity/occlusion | Chrome | Per-element: visible=true/false, covered_by, in_viewport | Visibility accuracy, occlusion detection rate |
| S4 | Get bounding boxes for 20 elements | Chrome + WeChat | Rect matches `getBoundingClientRect` (Chrome) or `size()+offset()` (WeChat) within 1px tolerance | Geometry accuracy, latency, RPC count |
| S5 | Get computed style for 5 properties on 10 elements | Chrome + WeChat | Style values match `getComputedStyle` (Chrome) or `element.style()` (WeChat) | Style accuracy, payload size |
| S6 | Read text from a canvas element (OCR fallback) | Chrome | OCR text matches "Submit Order" and "Cancel" | OCR accuracy, confidence, latency |
| S7 | Identify scroll containers and whether an element is within one | Chrome | `scroll_parent` correctly identified for nested-scroll and scroller elements | Scroll parent accuracy |

### 2.2. Static task ground truth

Ground truth is established by a separate, independent probe (not using
Argus tools):

- Chrome: direct CDP `DOM.getBoxModel` + `Runtime.evaluate` with
  `getBoundingClientRect`/`getComputedStyle`/`elementFromPoint`.
- WeChat: direct Automator `element.size()`/`element.offset()`/
  `element.style()`/`element.attribute()`.

Ground truth is recorded before the benchmark run and compared against
Argus tool output.

## 3. Cross-backend dynamic task suite

### 3.1. Dynamic action tasks

| Task | Description | Backend | Ground truth | Metrics |
|------|-------------|---------|-------------|---------|
| D1 | Click a button that triggers navigation; verify route changed | Chrome + WeChat | `after_path != before_path`; new page title matches expected | Route change detected, postcondition evidence, latency |
| D2 | Fill a form field; verify value persisted after blur | Chrome + WeChat | Re-read value matches input | Value persistence, postcondition evidence |
| D3 | Click an element occluded by an overlay; verify precondition failure | Chrome | `ErrTimeout` or precondition failure with `receives_events: false` | Occlusion detected, no click dispatched |
| D4 | Click an element in a virtual list that requires scrolling | Chrome | Element scrolled into view, click succeeded, correct element clicked | Scroll-into-view, correct element, latency |
| D5 | Fill a form with 5 fields; verify all values persisted | Chrome + WeChat | All 5 values match on re-read | Batch fill accuracy, partial success reporting |
| D6 | Click a stale ref after navigation; verify `ErrStaleReference` | Chrome + WeChat | `ErrStaleReference` returned, not silent re-match | Stale detection, error code correctness |
| D7 | Click a button that triggers a re-render; verify element identity after re-render | Chrome | New ref obtained, old ref returns stale, correct element clicked | Ref staleness, re-snapshot success |
| D8 | Navigate, re-render, and reconnect during a multi-step task | Chrome + WeChat | All steps complete, epoch transition detected, no cross-epoch ID join | Epoch tracking, reconnect recovery |

### 3.2. Dynamic task ground truth

Each dynamic task has:

1. **Pre-state assertion**: page state before action (URL, element count,
   specific element values).
2. **Action specification**: exact tool call(s) to perform.
3. **Post-state assertion**: page state after action (URL changed, value
   persisted, element appeared/disappeared).
4. **Evidence requirement**: action result must contain enough evidence
   to verify success without a separate `snapshot` call.

## 4. Three-run rule

### 4.1. Execution

```bash
# Chrome static suite (3 runs)
go test -tags=benchmark -count=3 -timeout 300s -run TestBenchmarkStatic ./...

# Chrome dynamic suite (3 runs)
go test -tags=benchmark -count=3 -timeout 600s -run TestBenchmarkDynamic ./...

# WeChat suite (3 runs, requires DevTools)
go test -tags=benchmark -count=3 -timeout 600s -run TestBenchmarkWeChat ./...
```

### 4.2. Pass criteria

A task passes the three-run rule if:

1. All 3 runs succeed (ground truth assertions pass).
2. No run uses escape hatches for task execution.
3. No run exceeds budget bounds (output ≤ 20 KiB, RPC ≤ 100, latency
   ≤ 30s, events ≤ 500).
4. No run has `wrong_element = true`.
5. `retry_count ≤ 2` across all 3 runs (retries are allowed but
   counted).

### 4.3. Failure classification

| Classification | Condition |
|---------------|-----------|
| `pass` | All 3 runs succeed, no escape hatches, within budget, no wrong elements |
| `pass_with_retries` | Passes but retry_count > 0 |
| `fail_wrong_element` | Any run clicked/filled the wrong element |
| `fail_escape_hatch` | Any run used eval/raw RPC for task execution |
| `fail_budget` | Any run exceeded output/RPC/latency/event bounds |
| `fail_flake` | Some runs pass, some fail (non-deterministic) |
| `fail_all` | All 3 runs fail |

## 5. Quality metrics

| Metric | Definition | Target |
|--------|-----------|--------|
| Task success rate | `pass` + `pass_with_retries` / total tasks | ≥ 90% |
| First-action success rate | Tasks where the first tool call achieved the goal | ≥ 70% |
| Wrong-element rate | `wrong_element = true` / total actions | 0% |
| Escape-hatch rate | `escape_hatch_used = true` / total tasks | 0% |
| Stale-ref detection rate | `ErrStaleReference` returned / stale-ref scenarios | 100% |
| Occlusion detection rate | Precondition failure on occluded elements / occluded scenarios | 100% (Chrome) |
| Postcondition evidence rate | Action results with postcondition data / total actions | ≥ 80% |

## 6. Productivity metrics

| Metric | Definition | Target |
|--------|-----------|--------|
| Task success per token | Successful tasks / total estimated tokens | ≥ 1 success per 2000 tokens |
| Avg latency per task | Wall-clock time from first tool call to final assertion | ≤ 15s (Chrome), ≤ 30s (WeChat) |
| Avg RPC count per task | Total CDP/Automator RPCs / total tasks | ≤ 20 (Chrome), ≤ 50 (WeChat) |
| Avg output bytes per task | Total tool output bytes / total tasks | ≤ 10 KiB |
| Snapshot-to-action ratio | `snapshot` calls / action calls | ≤ 2:1 (agent shouldn't snapshot excessively) |
| Hidden retry count | Tool calls that are retries of failed calls | ≤ 2 per task |

## 7. Budget bounds

| Resource | Per-action | Per-task | Per-run |
|----------|-----------|----------|---------|
| Output bytes | 8 KiB | 20 KiB | 100 KiB |
| RPC count | 10 | 50 | 200 |
| Latency | 10s | 30s | 120s |
| Events recorded | 50 | 200 | 1000 |
| Screenshot bytes | 500 KiB | 2 MiB | 10 MiB |
| Temp files | 1 | 5 | 20 |

Bound violations are recorded as `budget_exceeded` with the specific
resource and actual value. They do not abort the run but mark it as
failed for pass criteria.

## 8. Fixtures

### Chrome fixtures

- `sparse.html`: 4 interactive elements, simple form.
- `dense.html`: 200-row table with 400 buttons.
- `geometry.html`: transformed, clipped, covered, offscreen,
  opacity:0, display:none, scroll containers, virtual list.
- `canvas.html`: canvas with text.
- `dynamic.html`: button that triggers navigation, form that triggers
  re-render, overlay that appears on hover.

### WeChat fixtures

- `mp-weixin-runtime8`: existing fixture in the Argus repo (complex
  form, dynamic list, navigation, stale refs).
- Additional fixture: page with buttons, inputs, scroll-view, and canvas
  components.

## 9. Benchmark runner

The runner uses registered MCP handlers (not direct helper functions) to
call tools, matching the Runtime 8 acceptance requirement.

```go
type BenchmarkRun struct {
    TaskID     string
    Backend    string
    RunNumber  int // 1, 2, 3
    StartTime  time.Time
    EndTime    time.Time
    ToolCalls  []ToolCallRecord
    Result     BenchmarkResult
}

type ToolCallRecord struct {
    Tool          string
    Params        map[string]any // sanitized, bounded
    Result        map[string]any // sanitized, bounded
    Error         *ToolError
    LatencyMs     int64
    RPCCount      int
    OutputBytes   int
    IsRetry       bool
    IsEscapeHatch bool
}

type BenchmarkResult struct {
    Success           bool
    FirstActionSuccess bool
    WrongElement      bool
    UsedEscapeHatch   bool
    RetryCount        int
    EstTokens         int
    LatencyMs         int64
    RPCCount          int
    OutputBytes       int
    BudgetExceeded    string // "" if within bounds
}
```

## 10. Gate for OCR and future perception implementation

OCR (BL-10) and any perception fusion beyond the correctness fixes
(BL-1 through BL-4) may proceed only when **all** of the following are
true:

1. The semantic + geometry baseline (BL-1 through BL-8, BL-12) is
   implemented and the static + dynamic suites above pass the
   three-run rule.
2. At least one benchmark scenario exists where canvas/image text is the
   **only** available perception channel (i.e., the semantic-only
   baseline fails that scenario). This is the "measured workflow shows
   single-channel perception insufficient" trigger from the roadmap.
3. OCR must not inflate the MCP surface: `ocr` is an option on
   `snapshot`, not a new tool.
4. OCR observations carry confidence and are marked `unstable` below the
   0.7 threshold. Semantic evidence always wins over OCR.

If no scenario demonstrates single-channel insufficiency, OCR is not
implemented — the semantic baseline is sufficient and the token/latency
cost of OCR is not justified.
