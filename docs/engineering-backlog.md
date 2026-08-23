# Engineering Backlog — Perception and Action Quality

Status: **plan, not completed implementation.** This backlog records the
high-value conclusions of a static code audit (commit `2269432`) and two
read-only studies. Runtime 8 has independently passed at product commit
`fa31923`; do not edit product Go code on this branch. No item below is
implemented here.

Items are dependency-ordered and grouped by urgency:

- **Mandatory blockers** (BL-1 through BL-4): correctness or resource
  issues to fix before any perception-fusion work.
- **Next-slice** (BL-5 through BL-8, BL-12): perception fusion and
  action evidence, after the mandatory blockers and after Runtime 8
  closure.
- **Later / gated** (BL-9 through BL-11): opportunistic optimization and
  gated enhancements.

The 12 items are **not equally urgent**. BL-1 (Critical) and BL-2/BL-3
(High/Medium correctness) are the highest priority. BL-10 (OCR) is
gated behind a measured benchmark result and may never be needed.

Each item cites: evidence, severity, smallest remedy, expected benefit,
acceptance test, and milestone. Full contracts are in
[perception-and-action-design.md](perception-and-action-design.md); the
benchmark gate is in
[perception-benchmark.md](perception-benchmark.md).

## Mandatory blockers (fix-now)

### BL-1: Chrome ref generation tracking

| Field | Value |
|-------|-------|
| Audit ID | F1, F10 |
| Severity | **Critical** |
| Milestone | Pre-perception (P2-B foundation) |
| Effort | ~200 lines |
| Migration risk | Low (backward compatible) |
| Dependencies | None |
| Blocks | BL-7 (a11y fusion), BL-8 (action postconditions) |

**Evidence** `[verified]`: `tools_snapshot.go` returns
`ref: selectorFor(el)` — a raw CSS selector with no generation.
`objects.go` defines `SnapshotID`/`ElementRef` types with a generation
format but they are unused in the Chrome path. `autowait.go` uses
`document.querySelector(selector)` which re-evaluates on each call with
no generation check. After a re-render, the same CSS selector may
silently match a different element. WeChat refs
(`automator_refs.go`) already implement generation + epoch + page
tracking with `ErrStaleReference` — the correct reference model.

**Failure scenario**: Agent snapshots page A, gets `css:#submit`. Page
re-renders. Agent clicks `css:#submit`. `querySelector` finds whatever
now matches — possibly a different button. No error. Silent
wrong-element action.

**Smallest remedy**: Add a per-target snapshot generation counter.
Increment on `Page.frameNavigated` in `recorder_sub.go`. Append
`@gen_N` to Chrome refs. `click`/`fill`/`type` extract generation and
return `ErrStaleReference` on mismatch. Old refs (no `@gen`) treated as
generation 0 — backward compatible, no staleness check, no breakage.

**Benefit**: Correctness — eliminates silent wrong-element clicks (the
single most dangerous failure mode for an agent tool).

**Acceptance**:
```bash
gofmt -l *.go
go vet ./...
go test -count=1 -timeout 60s -run TestChromeRef ./...
go test -race -count=1 -timeout 60s -run TestChromeRef ./...
go test -tags=integration -count=1 -timeout 180s -run TestIntegrationChromeRef ./...
```
Failing test first: snapshot page A → get ref → navigate to page B →
click ref → expect `ErrStaleReference` (currently silently clicks
whatever matches).

**Rollback**: Revert ref format change. Old refs work as raw selectors.

### BL-2: Occlusion check in auto-wait

| Field | Value |
|-------|-------|
| Audit ID | F3 |
| Severity | **High** |
| Milestone | Pre-perception (correctness) |
| Effort | ~30 lines |
| Migration risk | Low |
| Dependencies | None |
| Blocks | BL-8 (action postconditions) |

**Evidence** `[experiment-verified]`: `autowait.go` `visible()` checks
display/visibility/size but not whether another element covers the
target. `document.elementFromPoint(centerX, centerY)` returns the
topmost element; if it differs from the target, the target is occluded.
Probe confirmed `#covered` is reported visible but is actually covered
by `#coverer`.

**Smallest remedy**: Add `elementFromPoint` check in
`elementStateExpression`. Add `receives_events` field to returned state.
`autoWaitForElement` checks `receives_events`. `no_wait=true` skips the
check. Occluded element click fails with an actionable error.

**Benefit**: Correctness — prevents clicking through overlays, modals,
and loading spinners.

**Acceptance**:
```bash
gofmt -l *.go
go vet ./...
go test -count=1 -timeout 60s -run TestOcclus ./...
go test -tags=integration -count=1 -timeout 180s -run TestIntegrationOcclus ./...
```

**Rollback**: Revert `elementStateExpression` change.

### BL-3: Screenshot temp file cleanup

| Field | Value |
|-------|-------|
| Audit ID | F6 |
| Severity | Medium |
| Milestone | Pre-perception (resource hygiene) |
| Effort | ~5 lines |
| Migration risk | None |
| Dependencies | None |
| Blocks | None |

**Evidence** `[verified]`: `screenshot.go` `defaultScreenshotPath()`
creates a temp file. `tools_screenshot.go` removes it only on error,
never on success. Path is returned to the agent. Files accumulate in
`$TMPDIR`.

**Smallest remedy**: `defer os.Remove(path)` after successful
`os.ReadFile`. Path still returned in metadata. Error paths still clean
up (existing behavior).

**Benefit**: Resource hygiene — eliminates temp file leak.

**Acceptance**:
```bash
gofmt -l *.go
go vet ./...
go test -count=1 -timeout 60s -run TestScreenshotCleanup ./...
```

**Rollback**: Remove `defer os.Remove`.

### BL-4: try_until description fix

| Field | Value |
|-------|-------|
| Audit ID | F8 |
| Severity | Medium |
| Milestone | Pre-perception (schema accuracy) |
| Effort | ~10 lines |
| Migration risk | None |
| Dependencies | None |
| Blocks | None |

**Evidence** `[verified]`: `tools_wait.go` `try_until` description
mentions `tool`/`params` fields but the implementation only evaluates
`condition` (a JS expression). `tool`/`params` are accepted but ignored.

**Smallest remedy**: Description accurately describes `condition` as the
only functional field. `tool`/`params` marked deprecated or removed from
schema. No behavior change.

**Benefit**: Schema accuracy — tool description matches implementation
(AGENTS.md principle 4).

**Acceptance**:
```bash
gofmt -l *.go
go vet ./...
go test -count=1 -timeout 60s -run TestTryUntil ./...
```

**Rollback**: Revert description.

## Next-slice items (after mandatory blockers)

### BL-5: Ambiguity check in click/fill

| Field | Value |
|-------|-------|
| Audit ID | F2 |
| Severity | **High** |
| Milestone | Perception (correctness) |
| Effort | ~50 lines |
| Dependencies | None (independent of BL-1) |
| Blocks | None |

**Evidence** `[verified]`: `autowait.go` uses
`document.querySelector(selector)` (first match, no count check). `find`
reports `ambiguous: true, match_count` but `click`/`fill` don't check.

**Smallest remedy**: `querySelectorAll(selector).length` check in
`elementStateExpression`. If > 1 match: error with `match_count` and up
to 5 descriptions. Ref-based click skips the check (ref is already
specific). Single-match selectors still work.

**Benefit**: Correctness — prevents silent first-match actions on
non-specific selectors.

**Acceptance**:
```bash
gofmt -l *.go
go vet ./...
go test -count=1 -timeout 60s -run TestAmbig ./...
```

**Rollback**: Revert ambiguity check.

### BL-6: WeChat snapshot geometry

| Field | Value |
|-------|-------|
| Audit ID | F4 |
| Severity | **High** |
| Milestone | Perception (parity) |
| Effort | ~100 lines |
| Dependencies | None |
| Blocks | BL-8 (WeChat action postconditions) |

**Evidence** `[verified]`: `automator_snapshot.go` `wxSnapshotElement`
has no rect/box field. `element.size()` and `element.offset()` exist in
the Automator API but are never called. Adding them costs **2 RPCs per
element** (bounded at 80 elements = 160 RPCs max, ~1.6s).

**Smallest remedy**: Call `element.size()` and `element.offset()` per
element. Add `rect: {x, y, width, height}` to element output. RPC count
bounded at 2 per element. Graceful degradation when size/offset fails.
Snapshot metadata includes RPC count. See owner decision D2
(default vs opt-in).

**Benefit**: Parity — WeChat agents gain position/size info, enabling
visibility checks and coordinate-based reasoning.

**Acceptance**:
```bash
gofmt -l *.go
go vet ./...
go test -count=1 -timeout 60s -run TestWeChatGeometry ./...
go test -tags=integration -count=1 -timeout 180s -run TestIntegrationWeChatGeometry ./...
```

**Rollback**: Revert geometry RPC calls.

### BL-7: a11y fusion in snapshot

| Field | Value |
|-------|-------|
| Audit ID | F7 |
| Severity | Medium |
| Milestone | Perception (fusion) |
| Effort | ~100 lines |
| Dependencies | BL-1 (refs with generation) |
| Blocks | BL-10 (OCR supplement) |

**Evidence** `[verified]`: `a11y_snapshot` returns raw CDP AX nodes with
no refs, no bounding boxes, no filtering (`filterA11yByDepth` is a
no-op). Not composable with `click`/`fill`. Full AX tree on a 200-row
table is **1.7 MiB / 4,628 nodes**; must filter to interactive nodes
(400 on dense page, bounded at 80).

**Smallest remedy**: One `Accessibility.getFullAXTree` call per Chrome
snapshot. Filter to interactive nodes only. Join to DOM via
`backendDOMNodeId`. Enrich elements with a11y role + computed name.
Provenance indicates a11y source. WeChat unaffected (no a11y API). See
owner decision D3 (replace vs supplement attribute role).

**Benefit**: Semantic precision — agents get computed a11y role/name
(including implicit roles), improving element understanding.

**Acceptance**:
```bash
gofmt -l *.go
go vet ./...
go test -count=1 -timeout 60s -run TestA11yFusion ./...
go test -tags=integration -count=1 -timeout 180s -run TestIntegrationA11yFusion ./...
```

**Rollback**: Revert a11y fusion. Snapshot returns to DOM-only role.

### BL-8: Action postconditions and state delta

| Field | Value |
|-------|-------|
| Audit ID | F5, F9 |
| Severity | **High** |
| Milestone | Perception (evidence) |
| Effort | ~150 lines |
| Dependencies | BL-1 (refs), BL-2 (occlusion) |
| Blocks | None |

**Evidence** `[verified]`: Chrome `click` returns
`{action_id, clicked:{x,y}}` only — no before/after state, no navigation
detection, no semantic diff. Neither backend verifies fill value
persisted after blur. `[experiment-verified]` bounded before/after
semantic diff is **148 bytes vs 567-byte full snapshot** (26%) — small
enough for action results (≤ 2 KiB bound).

**Smallest remedy**: Click result includes `state_delta` with changed
elements. Fill result includes `value_persisted` postcondition. WeChat
click includes `route_settled` (already partial). Evidence payload ≤ 2
KiB. No performance regression on sparse pages (< 5ms overhead). Action
record includes postconditions in recorder. See owner decision D5
(synchronous vs async).

**Benefit**: Productivity — agents no longer need a separate `snapshot`
call to verify action effects. Estimated 200–500 tokens saved per
action.

**Acceptance**:
```bash
gofmt -l *.go
go vet ./...
go test -count=1 -timeout 60s -run TestPostcondition ./...
go test -tags=integration -count=1 -timeout 180s -run TestIntegrationPostcondition ./...
```

**Rollback**: Revert before/after snapshot calls.

### BL-12: fill_form explicit partial success

| Field | Value |
|-------|-------|
| Audit ID | F16 |
| Severity | Medium |
| Milestone | Perception (contract accuracy) |
| Effort | ~10 lines |
| Dependencies | None |
| Blocks | None |

**Evidence** `[verified]`: `tools_interaction.go` `fill_form` returns
`{action_id, results, count}` with no `partial_success`,
`failed_count`, or `success_count`. AGENTS.md states: "Batch operations
report partial success explicitly."

**Smallest remedy**: After the loop, compute `failedCount`. Add
`failed_count`, `success_count`, `partial_success: failedCount > 0` to
result. All-succeed case: `partial_success: false, failed_count: 0`.
Can fold into BL-8 or stand alone.

**Benefit**: Contract accuracy — agent detects failures without
iterating per-field results.

**Acceptance**:
```bash
gofmt -l *.go
go vet ./...
go test -count=1 -timeout 60s -run TestFillFormPartial ./...
```

**Rollback**: Remove added fields.

## Later / gated items

### BL-9: WeChat wait_for RPC optimization

| Field | Value |
|-------|-------|
| Audit ID | F11 |
| Severity | Medium |
| Milestone | Post-perception (optimization) |
| Effort | ~50 lines |
| Dependencies | None |
| Verdict | `later` (opportunistic) |

**Evidence** `[verified]`: `automator_snapshot.go` text condition does
`Page.getData` (1 RPC) + `automatorFindBudget` (snapshot + enrichment,
up to 20 RPCs) per poll iteration. At 200ms poll over 10s, up to 1000
RPCs.

**Smallest remedy**: Cache page data between polls; only re-fetch if
page stack changed. Reduces per-poll RPCs from ~21 to ~2.

**Benefit**: Resource hygiene — 10× fewer RPCs for text condition
polling.

### BL-10: OCR optional supplement

| Field | Value |
|-------|-------|
| Slice | S9 |
| Severity | Enhancement |
| Milestone | Post-perception (gated) |
| Effort | ~200 lines + Swift helper |
| Dependencies | BL-7 (a11y fusion), roadmap gate |
| Verdict | `later` (gated) |

**Evidence** `[experiment-verified]`: Apple Vision
`VNRecognizeTextRequest` reads canvas text invisible to DOM/a11y. Cold
start **213ms** (borderline for 200ms target). Confidence 0.5 for test
canvas texts — below 0.7 stable threshold. macOS-only.

**Gate**: Must have a benchmark scenario where canvas/image text is the
**only** available perception channel (semantic-only baseline fails).
See [perception-benchmark.md](perception-benchmark.md) section 10. Must
not inflate MCP surface (`ocr` is an option on `snapshot`, not a new
tool). If no scenario demonstrates single-channel insufficiency, OCR is
not implemented.

**Smallest remedy**: One-shot Swift helper process, stdin/stdout JSON.
OCR observations carry confidence; `unstable` below 0.7. Semantic
evidence always wins over OCR. Returns `ErrUnsupported` on non-macOS.
See owner decisions D4, D6.

**Benefit**: Perception fallback for canvas/image text when semantic
channels fail — only if the benchmark proves it is needed.

### BL-11: CDP context cancellation propagation

| Field | Value |
|-------|-------|
| Audit ID | F13 |
| Severity | Medium |
| Milestone | Post-perception (resource hygiene) |
| Effort | ~100 lines |
| Dependencies | None (but after Phase 0 to avoid file churn) |
| Verdict | `next-slice` |

**Evidence** `[verified]`: `click` handler receives `ctx` from the MCP
framework and checks it in `autoWaitForElement`, but once it calls
`cdpDispatchMouseClick`, the CDP call runs for up to 10s regardless of
context cancellation. MCP client disconnect is not honored. On
shutdown, in-flight CDP calls delay shutdown by up to 10s each.

**Smallest remedy**: Add `callContext(ctx, method, params, result)` to
`cdpConn`. Add `select { case <-ctx.Done(): return ErrCanceled }`.
Thread `ctx` through `cdpRequest`/`cdpEvaluate`/`cdpDispatch*`. Keep
`cdpRequest` without ctx as a wrapper with 10s timeout (backward
compatible).

**Benefit**: Resource hygiene — cancellation honored immediately;
shutdown faster.

**Acceptance**:
```bash
gofmt -l *.go
go vet ./...
go test -count=1 -timeout 60s -run TestCDPCancel ./...
go test -race -count=1 -timeout 60s -run TestCDPCancel ./...
```
Failing test first: call `click` with a cancelled context → expect
`ErrCanceled` immediately, not 10s wait.

**Rollback**: Revert `callContext` additions; `cdpRequest` falls back to
10s timeout.

## Dependency graph

```
BL-1 (Chrome ref gen) ─┬─→ BL-7 (a11y fusion) ──→ BL-10 (OCR, gated)
                       └─→ BL-8 (postconditions)
BL-2 (Occlusion) ──────→ BL-8 (postconditions)
BL-3 (Screenshot cleanup) — independent
BL-4 (try_until fix) — independent
BL-5 (Ambiguity check) — independent
BL-6 (WeChat geometry) — independent (blocks WeChat postconditions)
BL-9 (WeChat wait_for opt) — independent, later
BL-11 (CDP context cancel) — independent, after Phase 0
BL-12 (fill_form partial) — independent, trivial
```

## Milestone mapping

| Backlog item | Milestone | Roadmap reference |
|-------------|-----------|-------------------|
| BL-1 (Chrome ref gen) | Pre-perception | P2-B (navigation/frame/epoch evidence) |
| BL-2 (Occlusion) | Pre-perception | P2-B (actionability) |
| BL-3 (Screenshot cleanup) | Pre-perception | Release hardening |
| BL-4 (try_until fix) | Pre-perception | P2-B (schema accuracy) |
| BL-5 (Ambiguity check) | Perception | Perception fusion |
| BL-6 (WeChat geometry) | Perception | Perception fusion (parity) |
| BL-7 (a11y fusion) | Perception | Perception fusion |
| BL-8 (Action postconditions) | Perception | Action transition contract |
| BL-9 (WeChat wait_for opt) | Post-perception | Release hardening |
| BL-10 (OCR supplement) | Post-perception | Perception fusion (gated) |
| BL-11 (CDP context cancel) | Post-perception | Release hardening (resource hygiene) |
| BL-12 (fill_form partial) | Perception | Action transition contract |

## Strengths to preserve

These are deliberate design choices confirmed by the audit that should
**not** be changed:

1. **Single Go package** (~11.5K lines). Flat structure avoids import
   cycles and interface abstraction overhead. Intentional.
2. **WeChat ref registry** (`automator_refs.go`): bounded (512 entries,
   FIFO), epoch-aware, page-aware, returns `ErrStaleReference`. The
   correct model — reference for Chrome ref tracking (BL-1).
3. **Connection epoch tracking**: both backends track epochs; cross-epoch
   ID joins prevented. Correct.
4. **Bounded event ring buffer** (256 events, 16 KiB/event, drop count
   visible). Matches "bounded everything."
5. **Typed error codes** (`objects.go`): `ErrStaleReference`,
   `ErrElementNotFound`, `ErrTimeout`, etc. Agents can branch on codes.
6. **Action lifecycle** (`action.go`): no global current action prevents
   interleaved causality bugs.
7. **JS string escaping** (`helpers.go:jsString`): uses `json.Marshal` —
   correct, no injection risk.
8. **Snapshot selector uniqueness** (`selectorFor`): walks up DOM
   building `tag > tag:nth-of-type`, checks uniqueness at each level.
9. **WeChat click route settlement**: captures before/after path, waits
   for settlement, invalidates refs on route change. Correct
   postcondition behavior — Chrome should learn from it (BL-8).
10. **Screenshot compression**: cross-platform Go image processing, JPEG
    quality 60, max 1400px, env-configurable. Portable.
11. **CDP readLoop panic recovery**: `defer recover()` in read loops.
12. **Redaction at ingestion**: event params redacted before storage.
    Defense in depth.

## Rejected low-value refactors

These were evaluated and explicitly rejected (no evidence of material
benefit):

- **R1: Split into multiple packages** — single package is deliberate;
  no correctness/performance/maintainability problem evidenced.
- **R2: Replace `map[string]any` with typed DTOs everywhere** — typed
  handles exist where identity matters; bulk refactor adds boilerplate
  with no correctness benefit. Continue organically.
- **R3: Merge `a11y_snapshot` into `snapshot` and remove it** — they
  serve different use cases (full AX tree for accessibility auditing vs
  fused perception). Keep both.
- **R4: Replace JS expression construction with a template engine** —
  `jsString()` uses `json.Marshal` (safe); `fmt.Sprintf` is readable and
  sufficient.
- **R5: Add connection pooling for WeChat** — single connection by
  protocol design; pooling adds complexity with no benefit.
- **R6: Replace nearest-neighbor downscale with Lanczos** — marginal
  visual improvement, no agent utility benefit.
- **R7: Add retry logic to CDP calls** — hides connection issues;
  violates "no hidden retries." Agent decides.

Observations recorded but **not** promoted to TODOs (per the audit
gate): F12 (`opacity:0` visible — matches Playwright, document only);
F14 (WXML parser `>` fragility — low occurrence); F15 (no SDK version
check — logging sufficient); O1 (code duplication in embedded JS —
consolidate during BL-7, not standalone); O2 (integration test shared
Chrome — standard pattern); O3 (eval harness no hard assertions — by
design, measurement tool not gate).
