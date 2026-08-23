# Tool Audit: 43 Registered MCP Tools

Date: 2026-07-28 (updated 2026-08-23)
Status: Post-merge audit. Original 55-tool audit completed; deprecated tools
removed (5), interaction tools merged (fill_form/select_option/key → fill/type),
navigation tools merged (go/refresh/wait_for_load → navigate/wait_for),
emulation tools merged (resize/set_user_agent → emulate).
Current surface: 43 canonical tools, 0 deprecated.

## Methodology

Each tool was extracted from source code (tools_*.go) and classified by:
- **Capability set**: core / diagnose / debug
- **Side effects**: what does it change?
- **Output handles**: what stable IDs does it produce?
- **Input handles**: what stable IDs does it consume?
- **Sensitivity**: low / medium / high
- **Idempotent**: can it be safely retried?
- **Verdict**: keep / merge / internalize / rename / deprecate

## Current Summary

| Surface | Count | Notes |
|---------|-------|-------|
| Canonical | 47 | Current supported names |
| Deprecated aliases | 5 | Compatibility window; warn and remove only in a versioned release |
| Trace | 3 | `timeline`, `diagnose`, `export_trace` |
| Registered total | 55 | Capability-set filtering is implemented |

## Audit Table

### Target (7 tools)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `configure` | core | launches Chrome, sets global state | — | — | medium | yes | **keep** |
| `open` | core | navigates, may launch Chrome | — | — | low | yes | **merge→configure** (redundant: configure+navigate covers this) |
| `new_tab` | core | creates tab, sets current target | tab_id | — | low | no | **rename→target_create** |
| `list_tabs` | core | none | tabs[].id | — | low | yes | **rename→target_list** |
| `select_tab` | core | activates tab, sets current target | — | tab_id | low | yes | **rename→target_select** |
| `close_tab` | core | closes tab | — | tab_id | low | yes | **keep** (rename→target_close) |
| `fork` | core | creates tab from current URL | tab_id | — | low | no | **keep** (low priority, but useful for parallel testing) |

### Observe (8 tools)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `snapshot` | core | none | element refs (inline) | — | low | yes | **keep** (primary observation tool) |
| `a11y_snapshot` | core | none | nodes[].ref | — | low | yes | **keep** (more reliable than DOM snapshot for a11y) |
| `screenshot` | core | writes temp file | artifact path | — | low | yes | **keep** |
| `screenshot_grid` | core | writes temp file | artifact path | — | low | yes | **merge→screenshot** (add grid=true param) |
| `screenshot_full_page` | core | writes temp file | artifact path | — | low | yes | **keep** (different CDP call, worth separate tool) |
| `inspect_point` | core | none | element ref | x/y or image coords | low | yes | **keep** |
| `find` | core | none | element refs | — | low | yes | **keep** (cheaper than snapshot for element search) |
| `get_element` | core | none | — | ref/selector | low | yes | **keep** |

### Act (10 tools)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `click` | core | clicks element, may trigger navigation | — | ref/selector/text/x/y | low | no | **keep** (core interaction) |
| `type` | core | types text into element | — | ref/selector | low | no | **keep** |
| `fill` | core | fills form field | — | ref/selector | low | no | **keep** |
| `fill_form` | core | fills multiple fields | — | fields[].ref/selector | low | no | **keep** (L2 composite, reduces round trips) |
| `select_option` | core | selects dropdown option | — | ref/selector | low | no | **keep** |
| `key` | core | sends keyboard event | — | — | low | no | **keep** |
| `scroll` | core | scrolls viewport | — | x/y | low | yes | **keep** |
| `hover` | core | hovers element | — | ref/selector/text/x/y | low | yes | **keep** |
| `drag` | core | drags element | — | from/to refs | low | no | **keep** |
| `upload_file` | core | uploads files to input element | — | ref/selector, paths | medium | no | **keep** (path validation needed) |

### Wait (4 tools)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `wait_for` | core | none | — | text/selector/url | low | yes | **keep** |
| `wait_for_load` | core | none | — | wait_until | low | yes | **keep** |
| `exists` | core | none | — | ref/selector/text | low | yes | **keep** (non-throwing check) |
| `try_until` | core | none | — | condition | low | yes | **deprecate** (honest now, but misleading name; agent can use wait_for + click) |

### Navigate (4 tools)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `navigate` | core | changes URL, may trigger full page load | — | url | low | yes | **keep** |
| `navigate_back` | core | navigates history | — | — | low | yes | **merge→go** (combine with forward as `go {direction: back|forward}`) |
| `navigate_forward` | core | navigates history | — | — | low | yes | **merge→go** |
| `refresh` | core | reloads page | — | ignore_cache | low | yes | **keep** |

### Network (6 tools)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `network_requests` | diagnose | temporary CDP subscription | — | — | medium | yes | **keep** (will become query of recorder in P1) |
| `network_request` | diagnose | makes HTTP request from server | — | url | **high** (SSRF risk) | yes | **keep** + restrict schemes (no file://, no localhost bypass) |
| `console_messages` | diagnose | temporary CDP subscription | — | — | medium | yes | **keep** (will become query of recorder in P1) |
| `intercept` | diagnose | modifies request flow | — | url_pattern | medium | no | **keep** |
| `list_intercepts` | diagnose | none | — | — | low | yes | **keep** |
| `clear_intercepts` | diagnose | removes intercept rules | — | — | low | yes | **keep** |

### Emulate (3 tools)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `resize` | core | changes viewport | — | width/height | low | yes | **keep** |
| `emulate` | core | changes viewport + UA | — | device | low | yes | **keep** |
| `set_user_agent` | core | changes UA | — | user_agent | low | yes | **keep** (could merge into emulate, but independent UA override is useful) |

### Storage (3 tools)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `get_storage` | core | none | — | type/key | **high** (may expose cookies) | yes | **keep** + redact sensitive values |
| `set_storage` | core | writes cookie/storage | — | type/key/value | **high** | no | **keep** |
| `clear_storage` | core | deletes storage | — | type | medium | yes | **keep** |

### Misc (3 tools)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `pdf` | diagnose | writes PDF file | artifact path | — | low | yes | **keep** |
| `list_downloads` | diagnose | none | — | — | low | yes | **keep** |
| `handle_dialog` | core | accepts/dismisses dialog | — | — | low | no | **keep** |

### Config (2 tools)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `status` | core | none | — | — | low | yes | **keep** |
| `current_url` | core | none | — | — | low | yes | **merge→status** (add `lightweight=true` param, or just let agent use status) |

### Eval (1 tool)

| Tool | Capability | Side Effects | Output Handles | Input Handles | Sensitivity | Idempotent | Verdict |
|------|-----------|-------------|----------------|---------------|-------------|------------|---------|
| `eval` | core | arbitrary JS execution | — | — | **high** (arbitrary code) | no | **keep** (necessary escape hatch, but document risk) |

## Key Findings

### 1. Object model exists but migration is incomplete
Typed handles, error codes, ToolMeta, capability sets, and ActionID now exist.
Most public payloads still use `map[string]any`, so stable-handle semantics are
not yet consistently enforced.
**Action**: Migrate outputs incrementally at compatibility boundaries; do not
perform a cosmetic all-at-once rewrite.

### 2. Recorder evidence semantics are closed for P1
All 29 state-changing tools create ActionIDs and record bounded, redacted
parameter/result/error summaries. The global `currentAction` model has been
removed. Persistent action/event buffers are count- and per-entry-byte-bounded.
Console, exception, and log events are captured.

`action_id` filtering happens after heuristic attribution. Timeline output is a
single target-scoped ordered stream with stable sequence numbers, precise
timestamps, and explicit attribution confidence. Dropped events, truncation,
domain-enable failures, and reconnect gaps are represented in completeness
metadata.

**Action**: Preserve this contract while adding navigation evidence
according to `docs/roadmap.md`. Network evidence is now captured (P2-A).
Do not add replay yet.

### 3. Tool granularity is mostly right
51 tools is not excessive for the domain. The main issues are:
- `screenshot_grid` is a param of `screenshot`, not a separate tool.
- `navigate_back`/`navigate_forward` should be one `go` tool.
- `open` is redundant with `configure` + `navigate`.
- `current_url` is a subset of `status`.
- `try_until` is misleading even with honest description.

### 4. Sensitivity status
SSRF scheme/address restrictions, storage redaction, and upload path
canonicalization are implemented. `eval`, storage mutation, interception, and
trace export remain inherently sensitive capabilities.

Trace output now applies whole-object best-effort redaction. `export_trace`
writes only below the controlled trace directory, refuses accidental
overwrite, and uses mode `0600`. Raw in-memory diagnostic evidence remains
sensitive and the recorder is not an audit log.

**Action**: Apply ingestion-time minimization and bounds to future network,
navigation, source-map, and snapshot evidence. Output redaction is defense in
depth, not permission to retain arbitrary secrets.

### 5. Capability filtering is implemented
ToolMeta registration and `--toolsets=core,diagnose` are active. Keep one
declaration as the source of schema, capability, side effects, sensitivity, and
deprecation metadata.

### 6. Trace tools have handler-level integration coverage
Nine integration tests cross the registered MCP handler boundary through an
in-memory transport while using a real Chrome. They cover schema decoding,
filtering, export path/permission policy, network evidence, and redaction in
addition to the lower-level recorder tests.

**Action**: Keep handler-boundary coverage mandatory for every public trace
contract added in P2.

## Tool Count Evolution

| Stage | Canonical | Deprecated aliases | Total registered |
|-------|-----------|--------------------|------------------|
| Original audit | 51 | 0 | 51 |
| Governance target before new trace tools | 44 | 5 | 49 |
| Current after trace tools | 47 | 5 | 55 |
| Future `locate_source` candidate | 48 | 5 | 56 |

Current result: 55 registered names = 47 canonical + 5 deprecated compatibility
aliases + 3 trace tools. Count is not the quality target; a compact canonical
surface, explicit compatibility policy, and reliable composition contracts are.
