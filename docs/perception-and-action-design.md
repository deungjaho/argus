# Perception and Action Design

Status: design handoff. This document persists the conclusions of two
read-only studies (usability + perception feasibility) and a static code
audit. It is **design input, not completed implementation**. The
engineering backlog in [engineering-backlog.md](engineering-backlog.md)
is the actionable plan; this document is the contract and rationale.

Evidence labels used throughout:

- `[verified]` — directly observed in committed source, official docs, or a
  run experiment.
- `[experiment-verified]` — confirmed by a probe against a real Chrome or
  Apple Vision instance (no Argus code modified).
- `[inference]` — reasoned from evidence but not directly measured.
- `[proposal]` — a design choice offered for owner decision.
- `[unresolved]` — open question requiring owner decision or live
  experimentation.

Reviewed commits:

- Static code audit: `2269432` (Argus HEAD, 2026-08-14).
- Runtime 8 PASS product commit: `fa31923` (independently passed; do not
  edit product code on this branch).
- Studies and experiments run on 2026-08-14 against Chrome
  151.0.7922.109 (headless) on macOS 15.6 arm64.

## 1. Scope and non-goals

### In scope

Argus is an MCP server that gives coding agents reliable browser control
via CDP (and a WeChat Automator backend). This document defines how Argus
should **perceive** page state and how **action** tools should report
their preconditions, execution, and postconditions — within the existing
compact tool surface.

The design fuses multiple bounded perception channels (DOM, a11y,
geometry, style, OCR) inside the existing `snapshot` tool and enriches
existing action tools (`click`, `fill`, `type`) with bounded evidence.
No new MCP tools are introduced.

### Non-goals

- Not a general E2E test framework (use Playwright).
- Not a performance profiler (use chrome-devtools MCP).
- Not anti-bot evasion, CAPTCHA solving, fingerprint spoofing, proxy
  rotation, paywall bypass, or access-control bypass.
- Not a workflow engine: no sequencing, branching, or automatic retry
  across tools. The agent decides order; Argus tools are independent
  atoms.
- Not a universal `act({action: ...})` meta-tool. Semantic atoms, not
  meta-actions.
- Not a package reorganization. The single `package main` is deliberate.
- Not pixel-perfect visual regression. The transferable lesson is bounded
  diff, not pixel match.
- Not a WebDriver BiDi migration. Argus is CDP-focused; BiDi lacks a full
  AX tree API (w3c/webdriver-bidi#443, open since 2023).
- Not `replay_action` until action records contain typed replayable
  specs (per `AGENTS.md`).
- Not a persistent OCR daemon. One-shot process is sufficient unless
  cold start is confirmed problematic (measured 213ms — borderline,
  monitor).

## 2. PerceptionNode contract

Status: `[proposal]`. Based on `[verified]` CDP/Playwright/WeChat
Automator capabilities and `[experiment-verified]` data.

`PerceptionNode` is the smallest cross-backend element perception that
preserves identity, semantics, geometry, and provenance. It is an
internal type fused inside `snapshot`; it is not a new MCP tool.

### 2.1. Minimal typed schema

```go
type PerceptionNode struct {
    // Identity (required)
    Ref         string  // opaque, backend-specific, pass back verbatim
    Backend     string  // "cdp" | "wechat"
    SnapshotID  string  // "<targetID>:<gen>" or "<pageID>:<gen>"
    Epoch       int     // connection epoch for staleness detection

    // Semantics (required)
    Role        string  // a11y role or inferred tag-based role
    Name        string  // accessible name (label, aria-label, text)
    Tag         string  // local name (button, input, view, ...)
    Text        string  // bounded visible text (<= 200 chars)

    // State (required)
    Visible     bool
    Enabled     bool
    Disabled    bool    `json:"disabled,omitempty"`

    // Geometry (optional — present when backend provides)
    Rect        *Rect   `json:"rect,omitempty"`         // viewport-relative
    InViewport  bool    `json:"in_viewport,omitempty"`
    CoveredBy   string  `json:"covered_by,omitempty"`   // ref of occluder
    ScrollParent string `json:"scroll_parent,omitempty"` // ref of scroll container

    // Bounded style projection (optional, on-demand)
    Style       map[string]string `json:"style,omitempty"` // whitelist only

    // Provenance (required)
    Sources     []Provenance

    // Confidence (required for OCR/inferred; 1.0 for direct, omit)
    Confidence  float64 `json:"confidence,omitempty"` // 0.0-1.0
    Unstable    bool    `json:"unstable,omitempty"`    // confidence < threshold
}

type Rect struct {
    X, Y, Width, Height float64
}

type Provenance struct {
    Field  string // which field this provenance applies to
    Source string // "dom" | "a11y" | "ocr" | "computed_style" | "inferred"
    Method string // "getBoundingClientRect" | "Accessibility.getFullAXTree" | ...
    Direct bool   // true = directly observed; false = inferred
}
```

### 2.2. Identity and lifetime

**Chrome (CDP)** `[proposal]`:

- Ref: `css:<selector>@<snapshot_gen>` — CSS selector + snapshot
  generation. Generation increments on navigation
  (`Page.frameNavigated`). See owner decision D1 for SPA re-render
  handling.
- Lifetime: valid within the same snapshot generation and connection
  epoch. After navigation/re-render, generation increments; old refs
  return `ErrStaleReference`.
- Join key: `backendDOMNodeId` for a11y↔DOM fusion. Stable across
  snapshot calls within the same epoch.
- This requires adding generation tracking to Chrome refs, which
  currently lack it (audit finding F1/F10, Critical).

**WeChat (Automator)** `[verified]`:

- Ref: `wx:<base64(pageID,elementID,snapshot,epoch)>` — already
  implemented in `automator_refs.go`.
- Lifetime: valid within same page + epoch. After route change,
  `invalidatePage(pageID)`; after reconnect, `invalidateAll()`.
- Join key: `elementId` + `pageId` for Automator RPC.

**OCR (Vision)** `[experiment-verified]`:

- Ref: `ocr:<text_hash>@<image_box>` — derived from text recognition,
  not DOM identity.
- Lifetime: valid only within the screenshot it was derived from. No
  persistence across snapshots.
- Confidence must be < 1.0; marked `unstable` if confidence < 0.7
  `[proposal threshold]`.
- OCR-only refs cannot be used with `click`/`fill` directly; they must
  be converted to viewport coordinates via the screenshot's
  image-to-viewport scale.

### 2.3. Provenance rules

Every field declares how it was derived. Conflicts are recorded in
provenance, not silently dropped.

| Field | Chrome source | WeChat source | OCR source |
|-------|--------------|--------------|------------|
| `role` | CDP Accessibility (direct) or inferred from tag (indirect) | Inferred from tag (indirect) | N/A |
| `name` | CDP Accessibility (direct) or `aria-label`/`innerText` (indirect) | WXML text + `aria-label` (indirect) | OCR text (direct, low confidence) |
| `text` | `innerText` (direct) | WXML inner text (direct) | OCR text (direct, confidence-tagged) |
| `rect` | `getBoundingClientRect()` (direct) | `element.size()` + `element.offset()` (direct) `[proposal]` | Vision `boundingBox` → viewport (direct, image-coordinate) |
| `visible` | `getComputedStyle` + `getBoundingClientRect` (inferred) | `hidden` attribute + `element.size()` (inferred) | N/A |
| `enabled` | `el.disabled` + `aria-disabled` (direct) | `disabled` attribute (direct) | N/A |
| `covered_by` | `elementFromPoint` at center (direct) | N/A (no equivalent) | N/A |
| `style` | `DOMSnapshot` whitelist or `getComputedStyle` (direct) | `element.style(name)` (direct) `[proposal]` | N/A |

### 2.4. Confidence

| Source | Default confidence | When to mark unstable |
|--------|-------------------|----------------------|
| Direct DOM observation | 1.0 (omit) | Never |
| Direct a11y observation | 1.0 (omit) | Never |
| Direct computed style | 1.0 (omit) | Never |
| Inferred tag → role | 0.9 | Element has no explicit role and tag is ambiguous |
| OCR text recognition | 0.5–0.95 (from Vision) | confidence < 0.7 |
| OCR → coordinate mapping | 0.8 | Screenshot resized or DPR ≠ 1 |
| Inferred visibility | 0.9 | Partially in viewport or `opacity:0` |

### 2.5. Honest degradation

When a channel is unavailable, the node omits the field and provenance
records the gap — it never fabricates evidence.

- WeChat has no a11y API: `role` is inferred from tag (`Direct: false`).
- WeChat geometry is currently absent: `rect` omitted, `sources` indicate
  missing. `[proposal]` adds `element.size()`/`element.offset()`.
- OCR on non-macOS: returns `ErrUnsupported`.
- `opacity:0` elements are reported `visible: true` (matches Playwright
  by design) `[experiment-verified]`; this is documented, not "fixed".

### 2.6. Bounds

| Bound | Default | Hard max | Rationale |
|-------|---------|----------|-----------|
| Nodes per snapshot | 80 | 200 | Token budget; matches current WeChat bound |
| Text per node | 200 chars | 500 chars | Bounded output |
| Style properties | 10 | 20 | Whitelist: display, visibility, opacity, overflow, position, z-index, transform, color, background-color, pointer-events |
| Sources per node | 8 | 15 | Provenance overhead |
| OCR observations per screenshot | 50 | 100 | Vision `maxObservations` equivalent |
| Total snapshot payload | 8 KiB | 20 KiB | Token budget for agent context |

### 2.7. Error semantics

| Condition | Error code | Behavior |
|-----------|-----------|----------|
| Ref not in registry | `ErrStaleReference` | Return error, do NOT re-match |
| Ref epoch ≠ current epoch | `ErrStaleReference` | Return error with epoch details |
| Ref pageID ≠ current pageID | `ErrStaleReference` | Return error with page details |
| Element not found at ref | `ErrElementNotFound` | Return error, suggest re-snapshot |
| Element found but not visible | No error | Return node with `visible: false`; agent decides |
| Element found but covered | No error | Return node with `covered_by`; agent decides |
| OCR confidence < threshold | No error | Return node with `unstable: true`; agent decides |
| Backend not configured | `ErrUnsupported` | Return error with configuration hint |
| Geometry unavailable (WeChat) | No error | Omit `rect`; `sources` indicate missing |

## 3. Evidence channels

### 3.1. Semantic (DOM + a11y)

`[verified]` CDP `Accessibility.getFullAXTree` returns role, name, and
`backendDOMNodeId` for joining to DOM. ~70% of AX nodes have
`backendDOMNodeId`; interactive nodes consistently have it.

`[verified]` Full AX tree on a 200-row table is **1.7 MiB / 4,628 nodes**
— far too large for agent context. Must filter to interactive nodes
only (role in button/link/textbox/checkbox/radio/combobox/etc.). On the
dense test page this reduces to 400 nodes, further bounded at 80 by the
existing snapshot limit. On sparse pages the full tree is 11.8 KiB / 27
nodes / 4 interactive.

WeChat has no a11y API; `role` is inferred from tag.

### 3.2. Geometry

`[experiment-verified]` JS batch `getBoundingClientRect()` +
`getComputedStyle()` is 5–20× smaller per element than
`DOM.getBoxModel` (225 bytes vs 308 bytes on sparse; 11 bytes/element on
dense) and avoids layout thrashing. Argus's `snapshot` already uses this
approach — correct.

`[verified]` WeChat `element.size()` returns `{width, height}`;
`element.offset()` returns `{left, top}` (absolute, page top-left
origin). They exist in the Automator API but are **not currently called**
(audit finding F4). Adding them costs **2 RPCs per element** (bounded at
80 elements = 160 RPCs max, ~1.6s at ~10ms/RPC).

### 3.3. Style

`[verified]` `DOMSnapshot.captureSnapshot` accepts a `computedStyles`
whitelist — only requested properties returned, as a parallel array.
Use this for on-demand style projection, never a full computed-style
dump. WeChat: `element.style(name)` exists but is not called
`[proposal]`.

### 3.4. OCR

`[experiment-verified]` Apple Vision `VNRecognizeTextRequest` (fast
mode, zh-Hans + en-US) reads canvas text invisible to DOM/a11y:

- Cold start: **213ms** (borderline for a 200ms target).
- Confidence: 0.5 for both canvas texts ("Submit Order", "Cancel") —
  below the proposed 0.7 stable threshold, so marked `unstable`.
- Bounding boxes are normalized lower-left origin; conversion to
  top-left image pixels then to viewport via `imageToViewportScale`.

OCR is optional (`ocr=true`, default false), on-demand, macOS-only.
Returns `ErrUnsupported` on other platforms. One-shot process, not a
daemon. Semantic (DOM/a11y) evidence always wins over OCR; OCR is
supplemental, never authoritative.

`[unresolved]` Warm latency, memory footprint, and Retina (DPR=2)
coordinate conversion not measured in headless mode.

## 4. Action transition contract

Status: `[proposal]`. Designed to fit within Argus's existing
action/recorder model without becoming a workflow engine.

### 4.1. Design constraints

MUST NOT: become a workflow engine; become a universal action DSL; add
hidden retries; inflate the MCP surface.

SHOULD: make `click`/`fill`/`type` return enough evidence for agents to
verify success without a separate `snapshot` call; detect and report
wrong-element, no-op, and partial-success cases; integrate with the
existing `ActionID` / `recorder.go` / `ConnectionEpoch` model.

### 4.2. Preconditions

Current state (committed HEAD `2269432`) `[verified]`:

| Action | Preconditions checked | Missing |
|--------|----------------------|---------|
| `click` (Chrome) | `visible`, `enabled` | `stable`, `receives-events` (occlusion) |
| `click` (WeChat) | None (no element-level auto-wait) | All |
| `fill` (Chrome) | `visible`, `enabled` | `editable`, `stable` |
| `fill` (WeChat) | None | All |
| `type` (Chrome) | `visible`, `enabled` (when selector provided) | `editable`, `stable` |

Proposed precondition sets (modeled on Playwright's actionability
conditions `[verified]`):

| Action | Preconditions |
|--------|---------------|
| `click` | visible, enabled, receives_events, stable |
| `fill` | visible, enabled, editable |
| `type` | visible, enabled, editable |
| `select_option` | visible, enabled |
| `hover` | visible, stable |

`receives_events` (occlusion) `[experiment-verified]`:
`document.elementFromPoint(centerX, centerY)` returns the topmost
element; if it differs from the target, the target is occluded. This is
the single most impactful missing precondition (F3).

### 4.3. Execution

Unchanged from current implementation. Chrome: `cdpDispatchMouseClick` /
`cdpInsertText`. WeChat: `Element.tap` / `Element.callFunction("input",
...)`. Recorded via `BeginAction`/`rec.SetParams`/`rec.SetResult`/
`rec.SetError` as today.

### 4.4. Postconditions

Current state `[verified]`:

| Action | Postconditions checked | Evidence returned |
|--------|----------------------|-------------------|
| `click` (Chrome) | None | `{action_id, clicked:{x,y,button,click_count}}` |
| `click` (WeChat) | Route settlement (2s poll) | `{action_id, before_path, after_path, route_changed, duration_ms}` |
| `fill` (both) | None | `{action_id, filled, value_length}` |

Proposed postcondition sets:

| Action | Postconditions | How |
|--------|---------------|-----|
| `click` | `route_settled` (if nav expected), `element_gone` (if should disappear), `no_console_error` | Compare before/after path; check element exists; check recorder for new console errors |
| `fill` / `type` | `value_persisted` | Re-read element value after blur; compare to input |
| `navigate` | `page_loaded` | Wait for `Page.frameStoppedLoading` (already in `recorder_sub`) |

### 4.5. Bounded state delta

`[experiment-verified]` A bounded semantic diff (interactive elements
only: tag, id, role, text, value, rect) is **148 bytes vs a 567-byte
full snapshot** (26.1%) for a small form change. Small enough to include
in action results.

Bounds: max 20 elements in diff, max 200 chars per text field, max 10
changed fields per element. Total evidence payload ≤ 2 KiB.

Limitation: the diff only covers interactive elements. Text changes in
non-interactive elements (divs, spans) are not captured. For full state
verification, the agent calls `snapshot` separately. The action-result
diff is a fast signal, not a complete state check.

### 4.6. Success/failure taxonomy

| Outcome | Condition | Agent action |
|---------|-----------|-------------|
| Success | Preconditions met + action dispatched + ≥1 postcondition met | Continue |
| Partial success | Action dispatched + some postconditions met, some not | Inspect `state_delta` |
| No-op | Action dispatched + no state change detected | Re-snapshot; element may be wrong or page static |
| Wrong element | Preconditions met but postconditions show unexpected state | Re-snapshot with more specific selector |
| Precondition failure | Preconditions not met before action | Fix precondition (wait, scroll, dismiss overlay) |
| Action error | CDP/Automator RPC failed | Retry or reconfigure |
| Stale reference | Ref epoch/page mismatch | Re-snapshot for new ref |
| Timeout | Auto-wait or postcondition check exceeded deadline | Re-snapshot or increase timeout |

Recovery hints are advisory, not automatic. The agent decides whether to
follow them. No hidden retries.

### 4.7. Recorder integration

Action records gain `preconditions`, `postconditions`, and `state_delta`
fields (bounded, sanitized, redacted as today). No new recorder data
structures — reuse `ActionRecord` with additional bounded fields. No
event replay, no cross-action correlation. Timeline remains one ordered
stream per target. Event attribution remains time-overlap heuristic,
labelled as such.

## 5. Recommended architecture: Internal Fusion

Status: `[proposal]`. Three architectures were evaluated; Option A is
recommended.

### 5.1. Option A — Internal Fusion (recommended)

Fuse a11y, geometry, style, and OCR evidence inside the existing
`snapshot` tool. Add generation-tracked refs to Chrome. Add geometry to
WeChat snapshot. Enrich action results with bounded before/after state
diff. **Zero new MCP tools.**

- Preserves "composition over monolith" — `snapshot` remains the single
  perception entry point.
- Preserves "semantic atoms" — `click`/`fill` remain atomic, just
  better-instrumented.
- Preserves "compact surface" — zero new MCP tools.
- Preserves "bounded everything" — all additions bounded.
- Honest about provenance — each field declares its source.
- Low migration risk: additive only; no tool removed/renamed; old Chrome
  refs still work as raw selectors (generation 0).
- Single package: no package split; fusion logic lives in existing
  files.

### 5.2. Option B — Separate Perception Layer (rejected)

An `internal/perception/` package that both `snapshot` and action tools
call. Same end result as A with more structural change. Violates the
single-package simplicity Argus deliberately maintains. The addendum
explicitly warns against "package-reorganization bias." The fusion logic
is small enough to live in existing files.

### 5.3. Option C — Minimal Patch (rejected as standalone, extracted as first slices)

Fix only F1 (Chrome ref staleness), F3 (occlusion), F6 (screenshot
cleanup). Leaves parity gaps (WeChat geometry) and evidence gaps (no
postconditions). Does not deliver the perception fusion the roadmap
calls for. However: C's individual fixes are the correct first
implementation slices of A.

### 5.4. Rejected alternatives (all options)

- New `perceive` MCP tool — duplicates `snapshot`, inflates surface.
- New `inspect_visual` MCP tool — separates visual from semantic.
- Workflow engine — rejected by design.
- Universal `act` tool — rejected by design.
- Package reorganization — rejected by addendum.
- Full AX tree dump — 1.7 MiB on dense pages; must filter to interactive.
- Full computed-style dump — use whitelist.
- Pixel-perfect visual regression — not the use case.
- WebDriver BiDi migration — Argus is CDP-focused; BiDi lacks full AX
  tree API.
- Persistent OCR daemon — one-shot process sufficient unless cold start
  confirmed problematic.
- Automatic retry in CDP calls — hides connection issues; violates "no
  hidden retries."
- Bulk `map[string]any` → typed DTO migration — continue organically,
  not a project.
- Nearest-neighbor → Lanczos downscale — marginal visual improvement,
  no agent utility benefit.
- Connection pooling for WeChat — single connection by protocol design.

## 6. Chrome vs WeChat adapter differences

| Aspect | Chrome (CDP) | WeChat (Automator) |
|--------|-------------|-------------------|
| Ref identity | `css:<selector>@<gen>` `[proposal]` | `wx:<base64(pageID,elementID,snapshot,epoch)>` `[verified]` |
| Ref staleness | `ErrStaleReference` on gen/epoch mismatch `[proposal]` | Already implemented `[verified]` |
| a11y | `Accessibility.getFullAXTree`, filtered to interactive | Not available; role inferred from tag |
| Geometry | `getBoundingClientRect()` batch `[verified]` | `element.size()` + `element.offset()`, 2 RPCs/element `[proposal]` |
| Style | `DOMSnapshot` whitelist or `getComputedStyle` | `element.style(name)` `[proposal]` |
| Occlusion | `elementFromPoint` `[experiment-verified]` | N/A (no equivalent) |
| Click postcondition | `route_settled`, `element_gone`, `no_console_error` `[proposal]` | `route_settled` already partial `[verified]` |
| Fill postcondition | `value_persisted` via `Runtime.evaluate` `[proposal]` | `value_persisted` via `Element.property("value")` `[proposal]` |
| State delta | JS batch snapshot diff `[proposal]` | `Page.getData` + element list diff `[proposal]` |
| Console errors | `recorder_sub.go` captures console/exception `[verified]` | `automatorEventRecord` ring buffer captures App.logAdded/exception `[verified]` |

Both backends return the same `ActionEvidence` schema. The agent does
not need to know which backend produced it.

## 7. Bounded output / RPC / latency constraints

| Resource | Per-action | Per-task | Per-run |
|----------|-----------|----------|---------|
| Output bytes | 8 KiB | 20 KiB | 100 KiB |
| RPC count | 10 | 50 | 200 |
| Latency | 10s | 30s | 120s |
| Events recorded | 50 | 200 | 1000 |
| Screenshot bytes | 500 KiB | 2 MiB | 10 MiB |
| Temp files | 1 | 5 | 20 |

Measured per-channel costs `[experiment-verified]`:

- Chrome snapshot: +1 a11y RPC (filtered to interactive, ~5–50ms).
- WeChat snapshot: +2 RPCs per element (60 RPCs for 30 elements,
  ~600ms).
- Action results: +1 JS batch before/after (~2ms sparse, ~5ms dense).
- OCR: 213ms cold start, on-demand only.
- State delta: 148 bytes on a small form change (26% of full snapshot).

## 8. Primary-source and community lessons

Access date: 2026-08-14. All facts verified against official docs or
source unless labelled otherwise.

| Lesson | Source | Transferable conclusion |
|--------|--------|------------------------|
| Lazy resolution | Playwright locators re-resolve at action time | Refs are resolution instructions, not cached nodes. WeChat already does this; Chrome needs generation tracking. |
| 5 actionability conditions | Playwright: visible, stable, receives-events, enabled, editable | Argus checks visible + enabled only. Occlusion (`receives-events`) is the key gap. |
| Strict mode by default | Playwright throws on multiple matches | Argus `find` reports ambiguity but `click`/`fill` use first-match. Add ambiguity check. |
| a11y tree as primary perception | Chrome DevTools MCP, Playwright MCP, browser-use | Token-efficient, semantic, pierces shadow DOM. Argus `a11y_snapshot` exists but is not composable (no refs, no boxes). |
| Ref stability within snapshot | Playwright MCP refs (`e5`) stable until page change | Argus WeChat refs have generation + epoch; Chrome refs need the same. |
| `backendDOMNodeId` join | CDP AX nodes include it for DOM join | Stable join key (not `nodeId` which resets). Use for a11y↔DOM fusion. |
| Computed-style whitelist | `DOMSnapshot.captureSnapshot` `computedStyles` param | Bounds output while providing batch style. Use, never full dump. |
| `elementFromPoint` occlusion | CDP/JS; experiment-verified | Topmost element at coordinate; differs from target → occluded. |
| Semantic query priority | Testing Library: getByRole > getByLabelText > ... | Role-based search via a11y improves semantic precision. |
| Confidence scores | Apple Vision `topCandidates(n)` with confidence | OCR observations carry confidence; `unstable` below threshold. |
| WeChat geometry access | Automator `element.size()`/`element.offset()` | Exist but not called; adding them provides geometry parity. |
| Before/after action snapshots | Playwright trace viewer (3 snapshots/action) | Bounded before/after semantic diff helps agents verify effects. |

Rejected patterns (with rationale): full AX tree dump as default
(600K+ tokens on Wikipedia); per-element `DOM.getBoxModel` in a loop
(layout thrashing); `CSS.getComputedStyleForNode` (unbounded);
WebDriver BiDi as primary protocol (no full AX tree API);
`VNDetectRectanglesRequest` for buttons (no semantic value);
coordinate-based clicking as primary (loses semantic context); OCR
daemon (lifecycle complexity); separate `inspect_visual` tool (inflates
surface); pixel-perfect visual regression (product-specific);
`page.accessibility.snapshot()` (deprecated, use `ariaSnapshot()`).

Citations (all accessed 2026-08-14):

- CDP DOMSnapshot: https://chromedevtools.github.io/devtools-protocol/tot/DOMSnapshot/
- CDP Accessibility: https://chromedevtools.github.io/devtools-protocol/tot/Accessibility/
- CDP DOM: https://chromedevtools.github.io/devtools-protocol/tot/DOM/
- Playwright locators: https://playwright.dev/docs/locators
- Playwright actionability: https://playwright.dev/docs/actionability
- Playwright ARIA snapshots: https://playwright.dev/docs/aria-snapshots
- Playwright trace viewer: https://playwright.dev/docs/trace-viewer
- WebDriver BiDi: https://w3c.github.io/webdriver-bidi/
- WebDriver BiDi accessibility issue: https://github.com/w3c/webdriver-bidi/issues/443
- Chrome DevTools MCP: https://github.com/ChromeDevTools/chrome-devtools-mcp
- Playwright MCP snapshots: https://playwright.dev/mcp/snapshots
- browser-use: https://github.com/browser-use/browser-use
- Stagehand: https://docs.stagehand.dev/v3/basics/observe
- Testing Library: https://testing-library.com/docs/
- Apple Vision VNRecognizeTextRequest: https://developer.apple.com/documentation/vision/vnrecognizetextrequest
- WeChat Automator Page API: https://developers.weixin.qq.com/miniprogram/en/dev/devtools/auto/page.html
- WeChat Automator Element API: https://developers.weixin.qq.com/miniprogram/en/dev/devtools/auto/element.html

## 9. Unresolved owner decisions

### D1: Chrome ref generation — navigation only or also DOM mutation?

`[unresolved]` The proposed implementation increments generation on
`Page.frameNavigated` (navigation). SPAs may re-render without
navigation (React state change, Vue reactivity). Should generation also
increment on significant DOM mutation?

Options: navigation only (simpler, fewer false stale refs, misses SPA
re-renders); navigation + DOM mutation observer (more correct, may
produce false stale refs on minor mutations); navigation + periodic
generation check (compromise).

Recommendation: start with navigation only. Add DOM mutation tracking
only if benchmarks show SPA re-renders causing wrong-element actions.

### D2: WeChat geometry — default or opt-in?

`[unresolved]` Adding geometry costs 2 RPCs per element (160 RPCs for 80
elements, ~1.6s). Default or opt-in (`geometry=true`)?

Recommendation: default true (parity with Chrome is important). If
latency is unacceptable in practice, make it opt-in.

### D3: a11y role — replace or supplement attribute role?

`[unresolved]` Currently `snapshot` gets role from
`el.getAttribute('role')`. a11y fusion gets role from the AX tree
(includes implicit roles). Replace or supplement?

Recommendation: a11y role replaces attribute role in the `role` field;
attribute role preserved in provenance. a11y role is more correct
(includes implicit roles like `<button>` → "button" without explicit
attribute).

### D4: OCR — macOS-only or cross-platform?

`[unresolved]` Apple Vision is macOS-only. Linux/Windows would need
Tesseract or a cloud OCR API.

Recommendation: macOS-only for now. Return `ErrUnsupported` on other
platforms. Cross-platform OCR is a separate future milestone if measured
demand exists.

### D5: Action postconditions — synchronous or async?

`[unresolved]` Capturing before/after state adds ~2–5ms per action.
Synchronous (blocking the action result) or async (published to
recorder, agent queries later)?

Recommendation: synchronous. The evidence is small (≤ 2 KiB) and the
latency is negligible (< 5ms on sparse pages). Async would require the
agent to make a separate query, defeating the purpose.

### D6: OCR cold start — one-shot vs daemon?

`[unresolved]` Measured cold start is 213ms (borderline for 200ms
target). A persistent daemon would avoid cold start but adds lifecycle
complexity.

Recommendation: one-shot process. Monitor warm latency in practice;
revisit a daemon only if cold start is confirmed problematic in a
measured workflow.

## 10. Roadmap gate compliance

The Argus roadmap gates perception fusion on:

1. P2-B navigation/frame/epoch evidence accepted — BL-1 (Chrome ref
   generation) is part of P2-B and must merge first. P2-B is accepted.
2. P2-C bounded runtime-to-source prototype exists — not addressed by
   this design; separate milestone.
3. Recorder/timeline/action attribution stable — the existing recorder
   is stable; BL-8 enriches action records additively.
4. Measured real workflow shows single-channel perception insufficient —
   `[unresolved]` not yet measured. The benchmark in
   [perception-benchmark.md](perception-benchmark.md) defines how to
   measure this. OCR (BL-10) is gated on it.
5. Reviewed perception-fusion design accepted — this document is the
   reviewed design. Owner decision required.

Recommendation: start Phase 0 (BL-1 through BL-4) immediately — these
are correctness fixes, not perception fusion. Start Phase 1 (BL-5
through BL-8, BL-12) after P2-B is accepted and Runtime 8 is closed.
Start Phase 2 (BL-9, BL-10, BL-11) after a measured workflow
demonstrates the need.
