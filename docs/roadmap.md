# Argus Roadmap and Agent Handoff

Date: 2026-07-28

## Current Baseline

Argus 0.1.0-alpha is a reliable browser-control and diagnostic layer for coding
agents. P0 reliability work and the P1 flight-recorder closure are complete.

Verified baseline:

- 55 registered MCP tools: 47 canonical tools, 5 deprecated aliases, and 3
  trace tools.
- 174 Go test functions.
- 35 integration tests exercise a real Chrome; 9 of them cross the registered
  MCP handler boundary through an in-memory transport.
- `gofmt`, `go vet`, `go test -race`, integration vet, and integration tests
  pass.
- The worktree was clean at the P1 acceptance point (`89a4842`).

The current product is more than a browser remote control: it can correlate
agent actions with bounded runtime evidence and return a target-scoped,
time-ordered diagnostic record. It is not yet a complete "browser GDB":
network/navigation evidence, runtime-to-source mapping, state diffs, and
multi-session isolation are still missing.

## Completed P1 Contract

The following behavior is now baseline and must not regress:

- All 29 state-changing handlers record bounded, redacted parameter, result,
  and error summaries through the shared action API.
- `action_id` filtering happens after time-overlap attribution.
- `timeline` returns one ordered stream with `kind`, stable `seq`,
  RFC3339Nano timestamps, target scope, and explicit attribution confidence.
- `diagnose` is target-scoped and reports real failed-action evidence.
- Trace output applies whole-object redaction.
- `export_trace` writes only under the controlled trace directory, refuses
  overwrite, and creates files with mode `0600`.
- A single event is limited to 16 KiB.
- Dropped events, truncated data, CDP domain-enable errors, reconnect gaps, and
  recorder completeness are visible in trace metadata.
- Recorder evidence remains best-effort. Heuristic attribution is never
  presented as certain causality.

## Next Iteration: P2 Unified Evidence Graph

P2 should deepen the existing evidence model rather than add unrelated tools.
The goal is:

> Given an agent action or a customer-reported failure, reconstruct what the
> browser did, which target/frame/navigation it belonged to, which requests
> failed, and—where evidence permits—which source location is relevant.

### P2-A — Network Evidence in the Existing Timeline (COMPLETED)

P2-A is accepted. Network events are captured in the persistent recorder
and appear in the unified timeline. The `network_requests` tool supports
a `from_recorder` compatibility view with target isolation.

Closure fixes applied:
- Recorder subscriber starts only on `/devtools/page/` endpoints, not
  the Browser endpoint (no spurious `domain_enable_error`).
- `sanitizeURL` parses first, removes query/fragment, then applies a
  UTF-8-safe byte limit. Parse failure strips query to avoid leaking
  secrets.
- URL-type headers (Referer, Location, Origin) are sanitized as URLs
  before storage.
- Redirect hops are tracked as an ordered sequence per `loaderID`
  (0, 1, 2, ...), not a 0/1 flag.
- `from_recorder` defaults to the currently selected target for
  isolation and supports an explicit `target_id` parameter.

Capture, at minimum:

- `Network.requestWillBeSent`
- `Network.responseReceived`
- `Network.loadingFailed`

Required model:

- Add typed request/loader/frame identifiers or equivalent validated internal
  types.
- Represent redirects as ordered hops. Do not assume one CDP `requestId`
  always means one URL.
- Preserve target ID, connection epoch, request ID, loader ID, frame ID,
  resource type, method, sanitized URL, status/failure, timestamps, and hop
  number when available.
- Join these records into the existing `timeline`; do not create a second
  competing `network_timeline` unless a measured response-size or usability
  problem proves it necessary.
- Keep `network_requests` as a compatibility/query view over recorder data
  where practical.

Default data policy:

- Do not record request or response bodies.
- Do not retain cookies, authorization headers, complete query secrets, or
  arbitrary POST data.
- Header capture must use an explicit allowlist or aggressive redaction.
- Bound URL, header, error, and aggregate event bytes before insertion.

Acceptance:

- Successful request, HTTP error, DNS/connection failure, redirect chain,
  cross-frame traffic, target filtering, reconnect gap, truncation, and
  redaction are covered.
- At least one integration test invokes `timeline` through the real MCP handler
  and observes a real Chrome network event.
- Race tests and the full P1 handler-contract suite still pass.

### P2-B — Navigation, Frame, and Connection-Epoch Evidence (COMPLETED)

P2-B is accepted. Navigation, frame lifecycle, and connection-epoch
evidence are captured in the persistent recorder and appear in the
unified timeline. Connection epochs prevent pre-reconnect CDP IDs from
being silently joined to post-reconnect IDs. Exact CDP-ID links
(frameId, loaderId, requestId) are labelled with
`attribution="exact_cdp_id"`, distinct from time-overlap heuristic
attribution. The `network_requests` from_recorder view enriches requests
with navigation context via a `navigation_url` field.

Closure details are in [docs/stage-p2b-handoff.md](stage-p2b-handoff.md).

Capture the smallest useful navigation lifecycle:

- frame navigation and detach;
- loader/frame/target identity;
- lifecycle or load/failure milestones needed to explain a navigation;
- connection epoch so IDs from before and after a reconnect cannot be confused.

The timeline must support the chain:

```text
action -> attributed navigation -> frame/loader -> request hops -> outcome
```

This is an evidence relationship, not guaranteed causality. Exact links derived
from CDP IDs should be labelled differently from time-overlap attribution.

Acceptance:

- normal navigation, same-document navigation, redirect, iframe navigation,
  failed navigation, tab closure, and reconnect are tested;
- no event from another target appears in a target-scoped query;
- gaps and missing domains remain explicit.

### P2-UX — Agent UX: Tool Discovery, Bounded Output, Element Ambiguity, Browser Errors

P2-UX is the next milestone after P2-B, per explicit PM decision. It
precedes P2-C. This is the first slice only — it addresses the most
urgent agent-experience gaps that make Argus hard to use without reading
the source. No scraping/anti-bot expansion. No P2-C.

#### 1. Compact tool discovery/summary

Agents need a concise list of available tools and their capabilities
without reading 55 tool definitions. The current MCP tool surface
presents every tool's full JSON schema, which is high-token and
difficult to scan.

Scope:

- Add a `tool_summary` query (or equivalent) that returns a compact,
  categorized list of available tools with a one-line description and
  key parameter hints per tool.
- Group tools by capability set (core, diagnose, debug, full) and
  domain (target, observe, act, wait, network, trace, console).
- Do not duplicate the full JSON schema — the summary is a discovery
  aid, not a replacement for individual tool schemas.

Acceptance:

- A single `tool_summary` call returns all registered tools in under
  2 KiB of output.
- Each tool entry has: name, category, one-line description, and at
  most 3 key parameters.
- A unit test verifies the summary covers all registered tools and
  fits within the byte bound.
- An integration test invokes `tool_summary` through the real MCP
  handler.

#### 2. Bounded high-frequency output

Tools that produce large output (screenshots, DOM dumps, network logs)
must have bounded summaries with truncation metadata. Currently, a
large page snapshot or a long network log can produce unbounded output
that consumes an agent's context window.

Scope:

- Add a `summary` or `bounded` mode to high-output tools (`snapshot`,
  `a11y_snapshot`, `network_requests`, `timeline`) that returns a
  truncated representation with explicit truncation metadata (total
  count, returned count, truncated bytes).
- The default behavior may remain full output for backward
  compatibility, but the bounded mode must be available via a
  parameter.
- Truncation metadata must include: total items available, items
  returned, and a flag indicating whether more data exists.

Acceptance:

- `snapshot` with `summary=true` returns a bounded DOM summary under
  4 KiB with truncation metadata.
- `network_requests` with `from_recorder=true` and a `limit` parameter
  returns truncation metadata (total requests, returned count).
- `timeline` with a `limit` parameter returns truncation metadata.
- Unit tests verify the byte bound and truncation metadata for each
  bounded mode.
- An integration test verifies bounded output through the real MCP
  handler.

#### 3. Explicit element ambiguity

When an element selector matches multiple elements, return explicit
ambiguity info (count, distinguishing attributes) not silent
first-match. Currently, `click`, `fill`, and other action tools may
silently operate on the first match when a selector matches multiple
elements, which is a correctness risk.

Scope:

- When a selector matches multiple elements, action tools return an
  `ambiguous_match` error with: match count, up to 5 distinguishing
  attributes (text, role, id, class, position) per match, and a
  suggestion to refine the selector.
- Add an optional `strict` parameter (default true) to action tools.
  When `strict=true`, multiple matches are an error. When
  `strict=false`, the first match is used with a warning in the result.
- `inspect_point` and `snapshot` should report match counts when a
  selector is provided.

Acceptance:

- `click` with a selector matching 3 elements and `strict=true`
  returns `ambiguous_match` with count=3 and distinguishing attributes.
- `click` with `strict=false` operates on the first match and includes
  a `warning` field in the result.
- Unit tests verify ambiguity detection, attribute extraction, and
  strict/non-strict behavior.
- An integration test verifies ambiguity handling with a real Chrome
  page containing duplicate elements.

#### 4. Actionable browser errors

Chrome launch/navigation errors should be actionable (suggest fixes,
not just report failure). Currently, a Chrome launch failure or
navigation error returns a generic message without guidance.

Scope:

- When Chrome launch fails, return an error with: the discovered
  Chrome path (if any), the platform-specific search locations
  checked, and a suggested fix (install Chrome, set
  `ARGUS_CHROME_PATH`, etc.).
- When navigation fails (DNS error, connection refused, SSL error),
  return an error with: the error category, the URL, and a suggested
  fix (check URL, verify network, check certificate).
- Errors must remain bounded and must not leak sensitive information
  (no full environment dump, no cookie/credential exposure).

Acceptance:

- Chrome launch failure with no Chrome installed returns an error
  with searched paths and a suggested fix.
- Navigation to a non-resolvable URL returns an error with the DNS
  failure category and a suggested fix.
- Unit tests verify error categorization and fix suggestion content.
- An integration test verifies navigation error reporting through the
  real MCP handler.

#### Explicit non-goals (first slice)

- No scraping/anti-bot expansion.
- No P2-C (runtime-to-source mapping).
- No multi-session isolation (P2-E).
- No new CDP domains beyond what P2-A and P2-B already capture.
- This is the first slice only — deeper UX improvements are deferred.

### P2-C — Runtime-to-Source Location

Build source location as a small capability, not a full debugger:

- Normalize console and exception stack frames into `SourceLocation`.
- Record `Debugger.scriptParsed` metadata only when needed and within bounds.
- Resolve source maps locally where possible.
- Introduce one focused `locate_source` query only after the internal contract
  is stable.
- Return confidence, original generated location, mapped location, and a clear
  failure reason.

Security:

- Treat source maps, source URLs, and paths as untrusted input.
- No unrestricted remote source-map fetching.
- Apply SSRF protection to any permitted fetch.
- Prevent path traversal and arbitrary local-file reads.
- Bound map size, parse time, cache size, and recursion.

Acceptance:

- generated-only stack, valid local source map, malformed map, missing source,
  cross-origin script, oversized map, and traversal attempts are tested.

### P2-D — Selective Snapshot Diff

Add bounded state comparison only where it improves diagnosis:

- Use stable semantic/a11y or selected DOM summaries, not a raw full-DOM dump.
- Capture before/after snapshots on demand or for selected action classes.
- Report added, removed, and materially changed nodes with truncation metadata.
- Link both snapshots and the diff to the action and target.

Do not capture a full snapshot around every action by default. It adds latency,
memory pressure, sensitive content, and noisy diffs.

Acceptance:

- small deterministic changes are useful to an agent;
- dynamic attributes and reorder noise are controlled;
- stale references, navigation boundaries, sensitive form values, oversized
  pages, and partial captures are represented honestly.

### P2-E — Release and Long-Running Hardening

Before calling the project beta:

- Add repeatable CI for format, vet, unit, race, build, and an explicitly
  managed Chrome integration lane.
- Run reconnect, event-flood, slow-consumer, tab-churn, and shutdown soak tests.
- Define configuration and trace schema versioning.
- Document the five deprecated aliases and their removal window.
- Make installation, Chrome discovery errors, permissions, logs, and first-run
  behavior understandable without reading the source.
- Decide and document whether one process supports one MCP client only. Do not
  claim multi-session support until target selection, recorder state,
  capabilities, artifacts, and shutdown ownership are isolated per session.

## Planned P3: Composable Document Acquisition

Argus may later provide browser primitives that help coding agents retrieve
normal development documentation: JavaScript rendering, access-state
assessment, bounded semantic extraction, and explicit user handoff/resume.
Search, HTTP/browser fallback orchestration, caching, indexing, and citation
management remain a separate composable component.

This work **must not start now**. Formal implementation begins only after P2-A,
P2-B, and P2-C are accepted and the session/profile/artifact ownership and
handoff test prerequisites are met. P2-A/P2-B completion permits non-public
fixture experiments only.

The complete scope, contracts, state machine, implementation template,
acceptance criteria, anti-bot boundary, and future-agent prompt are in
[docs/document-acquisition.md](document-acquisition.md).

Argus will not implement CAPTCHA solving, browser-fingerprint deception, proxy
rotation, paywall/login bypass, or claims of anti-bot evasion.

## Known Gaps and Priority

| Priority | Gap | Consequence | Planned closure |
|----------|-----|-------------|-----------------|
| P2-A | No persistent network evidence | Request failures cannot be tied to an action trace | Network events in unified timeline (COMPLETED) |
| P2-B | No navigation/frame/epoch model | Redirects, iframes, and reconnects are ambiguous | Typed navigation evidence and exact ID links (COMPLETED) |
| P2-UX | Agent UX: tool discovery, bounded output, element ambiguity, browser errors | Agents must read source to use Argus effectively; large output consumes context; silent first-match is a correctness risk | Compact tool summary, bounded output modes, explicit ambiguity, actionable errors |
| P2-C | No source-map resolution | Runtime failures do not point back to repository source | Bounded `SourceLocation` pipeline |
| P2-D | No action state diff | The result of a successful action can be hard to explain | Selective semantic snapshot diff |
| P2-E | Single-session/global state | Concurrent clients may contaminate evidence and target state | Explicit limitation, then scoped session design |
| P2-E | Limited soak/release automation | Short tests may miss leaks and reconnect degradation | CI and long-running fault tests |
| Later | No breakpoints/step/pause | Argus is not yet a literal debugger | Reassess after source location proves useful |

## Engineering Constraints for P2

### Required

- Extend the stable object and evidence model before adding a public tool.
- Reuse the recorder, ordered timeline, redaction, target scoping, and
  completeness metadata.
- Keep every queue, event, body, URL, header set, cache, and operation bounded.
- Assign every subscription and goroutine an owner, context, and exit path.
- Keep critical CDP consumers lossless where protocol correctness requires it;
  diagnostic consumers may drop only with visible counters.
- Preserve backward-compatible MCP schemas or introduce an explicit versioned
  transition.
- Add a failing test for a reproduced defect before fixing it.
- Use real handler-boundary integration tests for public contracts.

### Avoid

- A parallel network recorder, event bus, or timeline that duplicates current
  ownership.
- Package splitting whose only result is more directories and interfaces.
- Generic workflow engines, universal action DSLs, plugin systems, or storage
  backends before a concrete requirement exists.
- Treating temporal overlap as proof that an action caused an event.
- Capturing large raw payloads and relying on output redaction later.
- Adding tools solely to expose raw CDP primitives.

### Forbidden for This Iteration

- `replay_action`.
- Request/response body capture by default.
- Silent drops, silent truncation, or silent domain-enable failure.
- Arbitrary export paths, overwrite, remote source fetch, or local file access.
- Logging secrets or including secret-bearing raw objects in errors.
- Claiming multi-session isolation, audit-log guarantees, or deterministic
  replay.

`replay_action` may be reconsidered only after actions have typed replay
specifications, preconditions, sensitivity classification, confirmation or
dry-run semantics, idempotency guidance, and deterministic failure reporting.

## Future Direction: Evidence-Fusion Page Perception

This section is design input, not committed work. It records a perception
gap observed in browser-agent workflows that Argus's current tool surface
does not address. Implementation is gated by the trigger conditions below.

The reviewed design, benchmark gate, and dependency-ordered backlog that
concretize this section are persisted in:

- [docs/perception-and-action-design.md](perception-and-action-design.md) —
  `PerceptionNode` contract, action transition contract, recommended
  unresolved owner decisions.
- [docs/perception-benchmark.md](perception-benchmark.md) — cross-backend
  static and dynamic benchmark tasks, three-run rule, and the exact gate
  for OCR and future perception implementation.
- [docs/engineering-backlog.md](engineering-backlog.md) — compact BL-1
  through BL-12 with evidence, severity, remedy, acceptance, and milestone.

### The Perception Problem

Browser-control agents face a fundamental perception gap that no single
channel solves:

- **DOM pipelines** struggle to build a universally useful structured tree
  and infer interactable nodes on complex apps. Shadow DOM, dynamic
  frameworks, virtual lists, canvas/WebGL, and framework-specific rendering
  make a raw DOM tree noisy, unstable, and often misleading about what is
  actually clickable or semantically meaningful.

- **Visual pipelines** (screenshots + vision models) degrade on dense pages
  and are costly in tokens and latency. They work for sparse layouts but
  lose precision on data-heavy UIs, overlapping elements, scroll containers,
  and dynamic content. They cannot express semantic state (disabled,
  loading, error) without additional inference.

- **CDP/tool wrappers** (Argus's current layer) reduce action execution
  burden — click, type, wait, snapshot — but do not solve page perception or
  meaning extraction. An agent can act reliably but still cannot reliably
  determine what to act on or why an action failed semantically.

### Likely Argus Direction

Argus's long-term value is not in another DOM parser or vision pipeline, but
in **evidence-fusion perception**: combining multiple bounded channels into a
stable, actionable page model that no single channel provides alone.

Candidate channels:

- DOM structure (bounded, semantic subset)
- Accessibility tree (a11y snapshot — interactable nodes, roles, states)
- Visual (screenshots + grid overlay — spatial layout, occlusion)
- Network (request/response status — loading state, failure, data flow)
- Runtime (console/exception — JS errors, state transitions)
- Action context (recorder evidence — what was attempted, what happened)

The fusion target is a per-target, per-snapshot perception record that an
agent can query for "what is interactable here, what is its meaning, what is
its state, and what evidence supports that claim" — with confidence and
source attribution, not a single-channel assertion.

### Questions (Not Yet Answered)

1. What is the minimal fusion schema that improves agent action selection
   without duplicating a full accessibility tree or vision model?
2. Can a11y + DOM + action evidence replace vision for interactable-node
   inference on most pages, reserving vision for spatial/occlusion cases?
3. How is perception confidence represented when channels disagree (e.g.,
   DOM says "button" but a11y says "static text" and visual says
   "non-interactive")?
4. What is the token/latency budget for a perception query, and how does it
   scale with page complexity?
5. Does fusion perception belong in Argus (the evidence/observation layer)
   or in a separate composable component that consumes Argus evidence?

### Boundaries

- **Not anti-bot bypass.** No CAPTCHA solving, fingerprint deception, proxy
  rotation, or human-imitation. This is perception for agent reliability,
  not evasion.
- **Not a replacement for existing tools.** `snapshot`, `a11y_snapshot`,
  `screenshot`, and `inspect_point` remain. Fusion perception is a
  higher-level query over their evidence, not a competing surface.
- **Not immediate implementation.** This is design input. No code, no tool
  surface, no schema until the trigger conditions are met.
- **Bounded by design.** Any future fusion pipeline must respect the same
  bounds as existing evidence: bounded caches, bounded response size,
  bounded latency, explicit confidence, and no unbounded raw retention.
- **No new dependencies on external vision/DOM libraries** without a
  measured requirement and a security/supply-chain review.

### Metrics (To Be Defined Before Implementation)

- Perception accuracy: does the fused model correctly identify
  interactable nodes vs. ground truth (manual or a11y)?
- Channel agreement rate: how often do DOM/a11y/visual agree on
  interactability? Disagreement is signal, not noise.
- Token cost per perception query vs. raw snapshot + agent inference.
- Latency per perception query at various page complexities.
- Action success rate improvement when agents use fused perception vs.
  single-channel snapshot.

### Trigger for Starting This Work

Implementation may begin only after ALL of the following are true:

1. P2-B (navigation/frame/epoch evidence) is accepted and closed.
2. P2-C (runtime-to-source mapping) has at least a bounded working
   prototype, so perception claims can be linked to source when relevant.
3. The existing evidence model (recorder + timeline + action attribution)
   is stable enough to serve as a fusion substrate without churn.
4. At least one measured agent workflow demonstrates that single-channel
   perception (snapshot OR a11y OR visual alone) is insufficient for a
   real diagnostic or action-selection task — not a hypothetical gap, but
   a documented failure with evidence.
5. A design doc (`docs/perception-fusion.md`) is written, reviewed, and
   accepted with the schema, channel list, bounds, and metrics above
   made concrete.

Until then, this section is design input only. Do not start implementation
or add public tools based on it.

## Cross-Cutting Backlog: Static Perception and Dynamic Workflow Quality

This backlog starts only after Runtime 8 receives an independent PASS. It
improves the existing `snapshot` / `inspect_point` / action loop rather
than adding many public MCP tools. It is design input for post-Runtime-8
work; do not begin implementation during Runtime 8 closure.

Runtime 8 has independently passed at product commit `fa31923`. The
concrete, dependency-ordered itemization of this backlog (BL-1 through
BL-12, with evidence, severity, smallest remedy, acceptance, and
milestone) is in [docs/engineering-backlog.md](engineering-backlog.md).
The contracts it depends on are in
[docs/perception-and-action-design.md](perception-and-action-design.md)
and the benchmark gate is in
[docs/perception-benchmark.md](perception-benchmark.md). The phases
below remain the authoritative delivery order; the linked documents
expand each phase's items without changing the order or gates.

### P0 — Establish an evidence-based baseline

  dense table/list, duplicate labels, off-screen target, custom
  component, canvas or image text, layout/style defect, async update,
  navigation and reconnect.
- Record component identification accuracy, bounding-box accuracy,
  wrong-action rate, task completion rate, calls, output bytes/tokens
  and latency.
- Separate "task completed" from "correct element and final state
  verified".

Acceptance:

- Ground truth and expected final state are explicit for every case.
- Three consecutive runs are required; retries and escape hatches are
  reported.

### P1 — Static semantic and geometry parity

- Define one internal `PerceptionNode`: stable ref, snapshot
  generation, role/tag, text/value/state, viewport box,
  visibility/occlusion, provenance and confidence.
- Keep Chrome DOM/a11y as the primary semantic source.
  nodes.
- Custom components and canvas must report partial/unknown evidence
  honestly.
- Bind all refs, including future OCR refs, to snapshot generation.

Acceptance:

  and position through the default semantic path.
- Coordinate conversion is verified on Retina/resized screenshots.
- Duplicate elements never silently select the first match.
- Default snapshot remains bounded and does not regress the 13-tool
  surface.

### P1 — Dynamic state-transition reliability

- Record bounded before/after semantic state for selected actions, not
  every action by default.
- Let action results express expected and observed postconditions: value
  persisted after blur, navigation path changed, dialog closed, row
  appeared, loading/error state changed.
- Reconnect and navigation invalidate stale refs deterministically.
- `wait_for` observes semantic state transitions rather than fixed
  sleeps.

Acceptance:

  wrong element actions and zero unreported retries.
- Every reported success includes a verifiable final-state assertion.
- Failure returns the failed postcondition, last observation and an
  actionable recovery hint.

### P2 — On-demand style and layout inspection

- Expose only a bounded, on-demand style projection for a selected
  ref/region: box model, display/visibility, typography,
  foreground/background color, overflow, z-index and relevant
  accessibility state.
- Do not return the complete computed-style object or dump the whole
  page.
- Add region screenshot and selective screenshot/semantic diff for
  visual regressions.

Acceptance:

- Detect fixture defects in alignment, spacing, visibility, overflow,
  disabled styling and occlusion without arbitrary `eval`.
- Output has explicit field and byte limits and remains useful to
  text-only models.

### P2 — Text-only visual fallback

- Implement the `argus-vision` OCR helper only after the
  semantic/geometry baseline proves that OCR materially improves canvas,
  image-text or sparse Automator cases.
- OCR supplements semantic evidence; it never silently overrides it.
- Low-confidence or icon-only targets require another evidence source or
  an explicit handoff.

Acceptance is the B1–B10 perception benchmark suite plus a measured
improvement over the semantic-only baseline in task success per token
and latency.

### P2/P3 — Runtime element to source mapping

- Continue the existing bounded runtime-to-source milestone; do not
  claim that a DOM/WXML node maps to a React/Vue/uni-app source
  component until evidence supports it.
- Link source evidence to `PerceptionNode` only after the
  source-location contract is accepted.

### Delivery order for the cross-cutting backlog

1. Runtime 8 independent PASS and merge checkpoint.
2. Baseline benchmark and unified report schema.
4. Dynamic before/after postcondition verification.
5. Bounded on-demand style/layout inspection.
6. OCR fallback only when benchmark evidence justifies it.
7. Runtime-to-source linkage according to the main roadmap dependency.

Do not implement all items in one branch. Each step requires a failing
fixture, one bounded contract, implementation, cross-backend regression
evidence and an independent verifier report.

## Recommended Delivery Order

1. Freeze and document the accepted P1 contracts.
2. Implement P2-A in small commits: model, ingestion, timeline query,
   compatibility view, tests. (COMPLETED)
3. Add P2-B identifiers and navigation events before attempting source maps. (COMPLETED)
4. Implement P2-UX first slice: tool discovery, bounded output, element
   ambiguity, actionable browser errors. (Explicit PM decision: P2-UX
   precedes P2-C.)
5. Implement P2-C as a bounded lookup pipeline.
6. Add P2-D only after measuring diagnostic value and runtime cost.
7. Complete P2-E before a beta release.
8. Start P3 document-acquisition primitives only when the entry gates in
   `docs/document-acquisition.md` are all satisfied.
9. After Runtime 8 independent PASS, begin the cross-cutting
   static-perception and dynamic-workflow backlog (see "Cross-Cutting
   Backlog" section above) following its own delivery order.

Each commit should solve one problem and include its tests. Avoid mixing schema
changes, ingestion, unrelated cleanup, and package reorganization.

## Copyable Handoff Prompt

```text
你正在继续开发 Argus。它是一个通过 CDP 为 coding agent 提供浏览器控制和
运行证据的 MCP server，长期定位是 “browser GDB for agents”。

开始前完整阅读：
1. AGENTS.md
2. docs/roadmap.md
3. docs/tool-audit.md
4. README.md

当前基线：
- P0 可靠性和 P1 飞行记录器已经完成并验收。
- P1 代码验收点为 commit 89a4842。开始前检查工作区并保留已有文档或用户
  改动；不要擅自 reset、checkout 或覆盖。
- 55 个注册工具（47 canonical + 5 deprecated + 3 trace）。
- 174 个测试函数，35 个真实 Chrome integration tests。
- P1 契约包括：29 个 state-changing handler 的 action 记录、统一有序
  timeline、target-scoped diagnose、完整对象脱敏、受控 0600 trace 导出、
  16 KiB 单事件限制，以及 dropped/truncated/domain-enable/reconnect-gap 元数据。

本轮只执行 docs/roadmap.md 的 P2-A：把网络证据接入现有 recorder 和统一
timeline。不要同时做 P2-B/P2-C/P2-D，不要做 replay_action，不要创建第二套
network recorder 或 network_timeline。

先审计当前 recorder、event hub、CDP domain ownership、network_requests、
timeline DTO 和测试边界，然后提交一个简短实施计划。按以下顺序实现：
1. 先定义内部 typed network evidence 和 bounds。
2. 持续捕获 requestWillBeSent、responseReceived、loadingFailed。
3. 正确表示 redirect hops、target/frame/loader/request identity。
4. 将网络记录合并进现有 timeline，并尽量让 network_requests 成为兼容查询视图。
5. 贯穿 target scope、redaction、truncation、drop/domain/reconnect completeness。
6. 补单元测试、race 测试和至少一个通过真实 MCP handler + 真实 Chrome 的
   integration test。

安全和正确性约束：
- 默认绝不记录 request/response body。
- 不保留 Cookie、Authorization、完整 query secret、任意 POST data。
- URL/header/error 在进入 recorder 前即有字节上限；输出端继续做全对象脱敏。
- 时间重叠只能标为 heuristic，CDP ID 关联才可标为 exact。
- 所有 channel/cache/subscription/goroutine 必须有界、有 owner、有退出路径。
- 任何丢失、截断、domain enable 失败或 reconnect gap 必须显式暴露。
- 不做无关拆包、通用事件框架、插件系统或格式化重构。

验收命令：
gofmt -l .
go vet ./...
go test -race -count=1 -timeout 60s ./...
go vet -tags=integration ./...
go test -tags=integration -count=1 -timeout 180s ./...

提交要求：
- 一个 commit 只解决一个问题，测试与实现同 commit。
- commit message 使用 “component: what changed”。
- 最终汇报每个 commit、契约变化、测试证据、已知限制、P2-A 是否达到
  docs/roadmap.md 的 acceptance。
- 如果发现当前 P1 契约与文档不一致，先停止扩展并修复或报告，不要绕过。
```
