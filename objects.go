package main

// --- Unified Object Model ---
//
// These types define the stable handles that flow between MCP tools.
// The goal is: tool A's output can be directly used as tool B's input,
// without the agent guessing which map[string]any fields are relevant.
//
// Design principles:
//   - Handles are opaque strings (IDs), not full objects.
//   - Each handle has a clear lifecycle: created, valid, stale, gone.
//   - Tools accept handles where possible, not raw coordinates or selectors.
//   - When a handle is stale, tools return ErrStaleReference, not silent re-match.

// --- Handle types ---
//
// These are typed strings to prevent accidental cross-use.
// They are JSON-serialized as plain strings.

// TargetID identifies a browser tab/target. Stable across URL changes
// within the same tab. Becomes invalid when the tab is closed or Chrome
// restarts. Primary key for all target-scoped operations.
type TargetID string

// SnapshotID identifies a page snapshot at a point in time. Used to detect
// stale element references: if the page has changed since the snapshot,
// element refs from that snapshot are invalid.
// Format: "<targetID>:<generation>" where generation increments on navigation
// or significant DOM mutation.
type SnapshotID string

// ElementRef is a CSS selector or snapshot-relative reference to a DOM element.
// When derived from a snapshot, it includes the snapshot generation so stale
// refs can be detected. When passed as a raw CSS selector, generation is 0.
// Format: "<selector>" or "<selector>@<generation>"
type ElementRef string

// ActionID identifies a recorded agent action (click, type, navigate, etc.).
// Used to correlate events (console, network, exceptions) with the action
// that triggered them. Format: "act_<counter>" or "act_<uuid>".
type ActionID string

// EventID identifies a recorded browser event (console message, network
// request, exception, navigation). Used to reference specific events in
// trace exports and source location lookups.
type EventID string

// TraceID identifies a trace export. Used to reference a packaged set of
// evidence (screenshots, console logs, network requests, DOM snapshots)
// associated with a debugging session or failed test.
type TraceID string

// ArtifactID identifies a stored artifact (screenshot file, PDF, trace
// bundle). Used to read artifact contents without passing full binary data
// through MCP.
type ArtifactID string

// SessionID identifies an MCP client session. Reserved for future multi-
// session isolation. Currently there is one implicit session.
type SessionID string

// ConnectionEpoch identifies a connection generation for a target.
//
// CDP assigns fresh object IDs (requestId, loaderId, frameId, etc.) on
// every WebSocket connection. After a reconnect, IDs from the old
// connection are meaningless on the new one, and vice versa. The epoch
// makes that boundary explicit so evidence consumers can detect when an
// ID set changed and refuse to join across it without an explicit
// crossing.
//
// Lifecycle:
//   - Minted by the recorder when a target's first connection is
//     established, and again on every reconnect (old connection died,
//     new one dialed).
//   - Monotonically increasing per target: epoch_0, epoch_1, ...
//   - Carried by every EventRecord recorded under that connection.
//   - Stale (but still queryable) once a new epoch is minted; consumers
//     must check the epoch before joining IDs across records.
//
// Format: "epoch_<counter>".
type ConnectionEpoch string

// --- Source location ---

// SourceLocation maps a browser runtime location (stack frame, error,
// CSS rule) to a workspace source file. Produced by source map resolution.
type SourceLocation struct {
	Path       string  `json:"path"`               // workspace-relative source file path
	Line       int     `json:"line"`               // 1-based line number
	Column     int     `json:"column"`             // 0-based column number
	Function   string  `json:"function,omitempty"` // function name, if known
	Confidence float64 `json:"confidence"`         // 0.0–1.0, how certain the mapping is
	Source     string  `json:"source,omitempty"`   // source map URL or script URL
}

// --- Error categories ---
//
// Stable error codes that agents can branch on. Every tool error should
// include one of these categories so the agent can decide whether to retry,
// re-snapshot, reconfigure, or give up.

type ErrorCode string

const (
	ErrInvalidInput        ErrorCode = "invalid_input"        // caller provided bad parameters
	ErrTargetNotFound      ErrorCode = "target_not_found"     // selected tab no longer exists
	ErrBrowserDisconnected ErrorCode = "browser_disconnected" // Chrome crashed or CDP unreachable
	ErrTimeout             ErrorCode = "timeout"              // operation exceeded deadline
	ErrCanceled            ErrorCode = "canceled"             // context canceled (shutdown, client disconnect)
	ErrStaleReference      ErrorCode = "stale_reference"      // element ref from old snapshot
	ErrElementNotFound     ErrorCode = "element_not_found"    // selector/text didn't match anything
	ErrUnsupported         ErrorCode = "unsupported"          // feature not available in this browser/mode
)

// ToolError is the structured error returned by MCP tools.
// It wraps the error code with context for agent consumption.
type ToolError struct {
	Code      ErrorCode `json:"code"`
	Op        string    `json:"op"` // operation name, e.g. "click", "navigate"
	TargetID  string    `json:"target_id,omitempty"`
	Message   string    `json:"message"`         // human-readable detail
	Cause     string    `json:"cause,omitempty"` // root cause, if different from message
	Retryable bool      `json:"retryable"`       // can the agent reasonably retry?
}

func (e *ToolError) Error() string {
	prefix := e.Op
	if e.Code != "" {
		prefix = string(e.Code) + ": " + e.Op
	}
	if e.Cause != "" {
		return prefix + ": " + e.Message + " (" + e.Cause + ")"
	}
	return prefix + ": " + e.Message
}

// --- Capability sets ---
//
// Tools are grouped into capability sets so agents can load only what
// they need. This reduces context window usage and selection cost.
//
// Current sets:
//   core:     target + observe + act + wait (minimum for browser control)
//   default:  compact interaction loop (configure/status/snapshot/click/fill/
//             navigate/eval/screenshot/find/get_element/wait_for/exists/
//             tool_summary + page_data). What agents see by default.
//   core:     full internal core (all target/observe/act/wait/navigate/
//             emulate/storage tools). Use --toolsets=core to enable.
//   diagnose: network + trace (evidence collection)
//   debug:    debugger + source (breakpoints, source mapping)
//   full:     all tools (deprecated aliases + escape hatches + everything)

type CapabilitySet string

const (
	CapDefault  CapabilitySet = "default"
	CapCore     CapabilitySet = "core"
	CapDiagnose CapabilitySet = "diagnose"
	CapDebug    CapabilitySet = "debug"
	CapFull     CapabilitySet = "full"
)

// ToolMeta describes a tool's capability set, side effects, and sensitivity.
// Used for tool registration, capability filtering, and documentation.
type ToolMeta struct {
	Name        string        `json:"name"`
	Capability  CapabilitySet `json:"capability"`
	SideEffects []string      `json:"side_effects,omitempty"` // e.g. "navigates", "writes_storage", "intercepts_network"
	Sensitivity string        `json:"sensitivity,omitempty"`  // "low", "medium", "high" (high = may expose cookies/tokens)
	Idempotent  bool          `json:"idempotent"`             // can be safely retried with same params?
	Description string        `json:"description,omitempty"`  // one-line summary for tool discovery
}

// toolRegistry tracks all registered tools with their metadata.
// Used for capability filtering and tool audit.
var toolRegistry = []ToolMeta{}
