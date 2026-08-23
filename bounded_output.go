package main

// TruncationMeta describes whether and how a tool's output was bounded.
// High-frequency tools (snapshot, find, screenshot, network_requests, timeline)
// attach this metadata so agents can detect that more data exists beyond what
// was returned, rather than assuming the returned slice is complete.
type TruncationMeta struct {
	Truncated  bool   `json:"truncated"`
	TotalCount int    `json:"total_count,omitempty"`
	ShownCount int    `json:"shown_count,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// newTruncationMeta builds a TruncationMeta from a total and a shown count.
// When total <= shown, the result is not truncated.
func newTruncationMeta(total, shown int, reason string) TruncationMeta {
	return TruncationMeta{
		Truncated:  total > shown,
		TotalCount: total,
		ShownCount: shown,
		Reason:     reason,
	}
}

// truncationFromMap extracts a TruncationMeta from a map[string]any result
// (the common shape returned by cdpEvaluate-based tools). Returns the meta
// and whether the field was present. Handles both a typed TruncationMeta
// value (attached by attachTruncation) and a raw map[string]any (as would
// arrive from a JSON-decoded CDP result).
func truncationFromMap(m map[string]any) (TruncationMeta, bool) {
	if m == nil {
		return TruncationMeta{}, false
	}
	raw, ok := m["truncation"]
	if !ok {
		return TruncationMeta{}, false
	}
	// Typed struct attached in-process by attachTruncation.
	if tm, ok := raw.(TruncationMeta); ok {
		return tm, true
	}
	// Raw map from a JSON-decoded result.
	if tm, ok := raw.(map[string]any); ok {
		meta := TruncationMeta{}
		if v, ok := tm["truncated"].(bool); ok {
			meta.Truncated = v
		}
		if v, ok := numberValue(tm["total_count"]); ok {
			meta.TotalCount = int(v)
		}
		if v, ok := numberValue(tm["shown_count"]); ok {
			meta.ShownCount = int(v)
		}
		if v, ok := tm["reason"].(string); ok {
			meta.Reason = v
		}
		return meta, true
	}
	return TruncationMeta{}, false
}

// attachTruncation adds a TruncationMeta to a map[string]any result in place.
// If the map is nil, it is a no-op.
func attachTruncation(m map[string]any, meta TruncationMeta) {
	if m == nil {
		return
	}
	m["truncation"] = meta
}
