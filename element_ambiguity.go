package main

// AmbiguityInfo describes whether an element selector matched multiple
// elements and, if so, provides distinguishing attributes so the agent can
// refine the selector. It is attached to find/inspect results when a
// selector matches more than one element.
type AmbiguityInfo struct {
	Ambiguous  bool             `json:"ambiguous"`
	MatchCount int              `json:"match_count"`
	Matches    []AmbiguousMatch `json:"matches,omitempty"` // up to 5
	Hint       string           `json:"hint,omitempty"`
}

// AmbiguousMatch is a compact description of one matched element, used to
// help the agent distinguish between multiple matches.
type AmbiguousMatch struct {
	Tag   string `json:"tag"`
	ID    string `json:"id,omitempty"`
	Class string `json:"class,omitempty"`
	Text  string `json:"text,omitempty"`  // snippet, <= 100 chars
	XPath string `json:"xpath,omitempty"` // simplified XPath
	Ref   string `json:"ref,omitempty"`   // CSS selector ref
}

// ambiguityFromMap extracts an AmbiguityInfo from a map[string]any result
// (the shape returned by cdpEvaluate-based tools like find). Returns the
// info and whether the ambiguity fields were present.
func ambiguityFromMap(m map[string]any) (AmbiguityInfo, bool) {
	if m == nil {
		return AmbiguityInfo{}, false
	}
	ambiguous, hasAmb := m["ambiguous"].(bool)
	if !hasAmb {
		return AmbiguityInfo{}, false
	}
	info := AmbiguityInfo{Ambiguous: ambiguous}
	if v, ok := numberValue(m["match_count"]); ok {
		info.MatchCount = int(v)
	}
	if hint, ok := m["hint"].(string); ok {
		info.Hint = hint
	}
	if rawMatches, ok := m["matches"].([]any); ok {
		for _, raw := range rawMatches {
			mm, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			match := AmbiguousMatch{}
			if v, ok := mm["tag"].(string); ok {
				match.Tag = v
			}
			if v, ok := mm["id"].(string); ok {
				match.ID = v
			}
			if v, ok := mm["className"].(string); ok {
				match.Class = v
			} else if v, ok := mm["class"].(string); ok {
				match.Class = v
			}
			if v, ok := mm["text"].(string); ok {
				runes := []rune(v)
				if len(runes) > 100 {
					v = string(runes[:100])
				}
				match.Text = v
			}
			if v, ok := mm["xPath"].(string); ok {
				match.XPath = v
			} else if v, ok := mm["xpath"].(string); ok {
				match.XPath = v
			}
			if v, ok := mm["ref"].(string); ok {
				match.Ref = v
			}
			info.Matches = append(info.Matches, match)
			if len(info.Matches) >= 5 {
				break
			}
		}
	}
	return info, true
}

// ambiguityHint returns the standard hint string for a given match count.
func ambiguityHint(matchCount int) string {
	switch {
	case matchCount == 0:
		return "No elements matched. Try a different text/regex, or use snapshot to inspect the page."
	case matchCount == 1:
		return ""
	default:
		return "Selector matched multiple elements. Refine with id, text, or nth index."
	}
}
