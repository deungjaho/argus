package main

// --- Typed identifiers and shared types ---
//
// Opaque string types for the IDs that flow between MCP tools and the
// flight recorder. Typed so they cannot be silently cross-assigned.

// ActionID identifies a recorded agent action (click, type, navigate, etc.).
// Used to correlate events (console, network, exceptions) with the action
// that triggered them. Format: "act_<counter>".
type ActionID string

// EventID identifies a recorded browser event (console message, network
// request, exception, navigation). Format: "evt_<counter>".
type EventID string

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

// ToolError is the structured error recorded on action records and
// returned by MCP tools where a plain error string would be ambiguous.
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
//   default:  compact interaction loop (configure/status/snapshot/click/fill/
//             navigate/eval/screenshot/find/get_element/wait_for/exists/
//             tool_summary). Always enabled.
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
