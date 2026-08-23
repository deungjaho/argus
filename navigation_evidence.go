package main

// --- Typed navigation evidence model ---
//
// P2-B: Navigation and frame lifecycle events are captured as typed
// evidence records and fed into the existing recorder/timeline. This
// file defines the typed structure and bounds for navigation evidence.
//
// Design (mirrors NetworkEvidence in network_evidence.go):
//   - NavigationEvidence is stored in EventRecord.Data for "navigation"
//     events, the same way NetworkEvidence is stored for "network" events.
//   - The connection epoch is carried on EventRecord.Epoch (B1 foundation),
//     not duplicated inside the evidence struct.
//   - URLs are sanitized at ingestion time via sanitizeURL (removes query
//     secrets, applies byte limit). Redaction at output is defense in depth.
//   - Frame IDs, loader IDs, and parent frame IDs are short CDP identifiers
//     and are truncated to maxCDPIDBytes as a safety bound.
//   - Each CDP Page-domain event produces one NavigationEvidence with a
//     distinct Phase identifying the source event.

const (
	// maxCDPIDBytes limits stored CDP identifier strings (frameId,
	// loaderId, parentFrameId). These are typically short (< 64 chars)
	// but are bounded as a safety measure against malformed input.
	maxCDPIDBytes = 256
	// maxNavTypeBytes limits the navigation type and lifecycle event
	// name strings stored in evidence.
	maxNavTypeBytes = 64
)

// NavigationEvidence is the typed data stored in EventRecord.Data for
// navigation and frame lifecycle events. It is designed to fit within
// maxEventDataBytes (16KB) after JSON serialization.
//
// Fields preserve the exact CDP identifiers (frameId, loaderId,
// parentFrameId) needed to link navigation evidence to network requests
// and actions across the timeline. B3 will use these for exact CDP-ID
// attribution; B2 captures them only.
type NavigationEvidence struct {
	// FrameID is the CDP frame identifier. Present on all frame events.
	FrameID string `json:"frame_id,omitempty"`
	// LoaderID is the CDP loader identifier for this navigation.
	// Present on frameNavigated and lifecycleEvent.
	LoaderID string `json:"loader_id,omitempty"`
	// ParentFrameID is the parent frame's CDP identifier. Non-empty
	// for iframe navigations, empty for the top-level frame.
	ParentFrameID string `json:"parent_frame_id,omitempty"`
	// URL is the sanitized navigation URL. Present on frameNavigated
	// and navigatedWithinDocument.
	URL string `json:"url,omitempty"`
	// NavigationType distinguishes normal navigations from same-document
	// navigations. Values: "Navigation" (full page navigation),
	// "SameDocument" (URL change without full reload), or "" for
	// non-navigation frame events.
	NavigationType string `json:"navigation_type,omitempty"`
	// LifecycleEvent is the lifecycle milestone name from
	// Page.lifecycleEvent (e.g. "DOMContentLoaded", "load",
	// "networkIdle"). Empty for non-lifecycle events.
	LifecycleEvent string `json:"lifecycle_event,omitempty"`
	// Reason is the frame detach reason from Page.frameDetached.
	// Values: "remove" (frame removed from DOM) or "swap" (frame
	// swapped, e.g. for BFCache). Empty for non-detach events.
	Reason string `json:"reason,omitempty"`
	// MimeType is the MIME type of the navigated resource, from
	// Page.frameNavigated. Empty for non-navigation events.
	MimeType string `json:"mime_type,omitempty"`
	// Phase identifies which CDP Page-domain event produced this
	// evidence: "frameNavigated", "frameDetached",
	// "frameStartedLoading", "frameStoppedLoading",
	// "navigatedWithinDocument", or "lifecycleEvent".
	Phase string `json:"phase"`
}

// truncateCDPID bounds a CDP identifier string to maxCDPIDBytes using
// UTF-8-safe truncation. CDP IDs are normally short ASCII strings; this
// is a defensive bound against malformed or oversized input.
func truncateCDPID(s string) string {
	return truncateUTF8Safe(s, maxCDPIDBytes)
}

// truncateNavType bounds a navigation type or lifecycle event name to
// maxNavTypeBytes. These are short enum-like strings from CDP; this is
// a defensive bound.
func truncateNavType(s string) string {
	return truncateUTF8Safe(s, maxNavTypeBytes)
}

// navigationEvidenceFromMap converts a map[string]any (from JSON
// round-trip) back to NavigationEvidence. Used when event data has been
// through truncation or JSON serialization, matching the
// networkEvidenceFromMap pattern.
func navigationEvidenceFromMap(m map[string]any) NavigationEvidence {
	return NavigationEvidence{
		FrameID:        getString(m, "frame_id"),
		LoaderID:       getString(m, "loader_id"),
		ParentFrameID:  getString(m, "parent_frame_id"),
		URL:            getString(m, "url"),
		NavigationType: getString(m, "navigation_type"),
		LifecycleEvent: getString(m, "lifecycle_event"),
		Reason:         getString(m, "reason"),
		MimeType:       getString(m, "mime_type"),
		Phase:          getString(m, "phase"),
	}
}

// navigationEvidenceToMap converts a NavigationEvidence to a
// map[string]any for redaction processing, matching the
// networkEvidenceToMap pattern.
func navigationEvidenceToMap(e NavigationEvidence) map[string]any {
	m := map[string]any{
		"phase": e.Phase,
	}
	if e.FrameID != "" {
		m["frame_id"] = e.FrameID
	}
	if e.LoaderID != "" {
		m["loader_id"] = e.LoaderID
	}
	if e.ParentFrameID != "" {
		m["parent_frame_id"] = e.ParentFrameID
	}
	if e.URL != "" {
		m["url"] = e.URL
	}
	if e.NavigationType != "" {
		m["navigation_type"] = e.NavigationType
	}
	if e.LifecycleEvent != "" {
		m["lifecycle_event"] = e.LifecycleEvent
	}
	if e.Reason != "" {
		m["reason"] = e.Reason
	}
	if e.MimeType != "" {
		m["mime_type"] = e.MimeType
	}
	return m
}
