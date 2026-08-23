package main

// automator_snapshot.go — WeChat Mini Program snapshot with interactive elements.
//
// The snapshot returns a compact page summary and bounded interactive
// elements. Each useful element has:
//   - opaque ref (wx:... — pass directly to click/fill)
//   - tag/role
//   - concise visible text/value
//   - useful id/class summary
//   - disabled/hidden state when available
//   - selector/source hint only for diagnostics
//
// The snapshot does NOT dump complete WXML or complete page data by default.
// Those are on-demand via the wxml and page_data tools.
//
// Default bounds: ~2-4 KiB output, <= 30-40 interactive elements.
// Includes truncation, rpc_count, and duration_ms metadata.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// wxSnapshotElement is one interactive element in the snapshot output.
type wxSnapshotElement struct {
	Ref      string `json:"ref"`
	Tag      string `json:"tag"`
	Text     string `json:"text,omitempty"`
	Name     string `json:"name,omitempty"`
	Value    string `json:"value,omitempty"`
	ID       string `json:"id,omitempty"`
	Class    string `json:"class,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	Hidden   bool   `json:"hidden,omitempty"`
	Selector string `json:"selector,omitempty"` // diagnostic hint
}

// wxSnapshotResult is the structured snapshot output.
type wxSnapshotResult struct {
	Backend    string              `json:"backend"`
	PageID     int                 `json:"page_id"`
	Path       string              `json:"path"`
	StackDepth int                 `json:"stack_depth,omitempty"`
	Elements   []wxSnapshotElement `json:"elements"`
	Truncation TruncationMeta      `json:"truncation,omitempty"`
	RPCCount   int                 `json:"rpc_count"`
	DurationMs int64               `json:"duration_ms"`
}

// automatorSnapshot builds a compact snapshot of the current Mini Program
// page: page path, bounded interactive elements with refs, and metadata.
// It does NOT dump complete WXML or page data.
//
// Element discovery uses a single Page.getElements call with a broad
// interactive selector, then registers each element in the ref registry.
// If Page.getElements is not available, it falls back to individual
// Page.getElement calls for common interactive tags (bounded).
func automatorSnapshot(maxElements int) (map[string]any, error) {
	start := time.Now()
	if maxElements <= 0 {
		maxElements = 30
	}
	if maxElements > 80 {
		maxElements = 80
	}

	rpcCount := 0

	// Get current page.
	path, pageID, err := automatorCurrentPage()
	if err != nil {
		return nil, err
	}
	rpcCount++

	// Get page stack depth.
	stackDepth := 0
	if stack, err := automatorPageStack(); err == nil {
		stackDepth = len(stack)
		rpcCount++
	}

	// Snapshot generation for ref registration.
	snapGen := wxRefs.nextSnapshotGeneration()

	// Discover interactive elements.
	// We use a broad selector that matches common interactive Mini Program
	// components: button, input, textarea, navigator, view[bindtap], etc.
	// The WeChat automator selector syntax supports tag names and classes.
	elements, rpcs := discoverInteractiveElements(pageID, maxElements, snapGen)
	rpcCount += rpcs

	// Build result.
	result := wxSnapshotResult{
		Backend:    BackendWeChat,
		PageID:     pageID,
		Path:       path,
		StackDepth: stackDepth,
		Elements:   elements,
		RPCCount:   rpcCount,
		DurationMs: time.Since(start).Milliseconds(),
	}

	// Check if we hit the element limit.
	totalElements := len(elements)
	result.Truncation = newTruncationMeta(totalElements, len(elements), "max_elements")
	if result.Truncation.Truncated {
		// We don't know the true total without a count query; mark as truncated.
		result.Truncation.TotalCount = totalElements
	}

	// Convert to map[string]any for the MCP handler.
	out := snapshotResultToMap(result)
	return out, nil
}

// discoverInteractiveElements finds interactive elements on the current page
// and registers them in the ref registry. Returns the snapshot elements and
// the number of RPC calls made.
//
// Strategy: try Page.getElements with a broad selector first (1 RPC).
// If that fails or returns nothing, fall back to per-tag Page.getElement
// calls with a small concurrency cap (bounded RPCs).
func discoverInteractiveElements(pageID int, maxElements int, snapGen int64) ([]wxSnapshotElement, int) {
	return discoverInteractiveElementsBudget(pageID, maxElements, snapGen, snapshotEnrichMaxRPCs)
}

// discoverInteractiveElementsBudget is like discoverInteractiveElements but
// allows a custom enrichment budget (used by find for broader text search).
func discoverInteractiveElementsBudget(pageID int, maxElements int, snapGen int64, enrichBudget int) ([]wxSnapshotElement, int) {
	// Try broad selector first — this is the most efficient (1 RPC).
	// WeChat automator selectors support comma-separated tag names.
	broadSelector := "button,input,textarea,navigator,switch,slider,scroll-view,swiper,image,view,text"
	refs, err := automatorGetElements(pageID, broadSelector)
	if err == nil && len(refs) > 0 {
		snaps, enrichRPCs := buildSnapshotElements(pageID, refs, maxElements, snapGen, enrichBudget)
		return snaps, 1 + enrichRPCs
	}

	// Fallback: try individual tags. Bounded to avoid N unbounded RPCs.
	// We try the most common interactive tags first.
	tags := []string{"button", "input", "textarea", "navigator", "switch"}
	rpcs := 0
	var allRefs []wxElementRef
	for _, tag := range tags {
		if len(allRefs) >= maxElements {
			break
		}
		refs, err := automatorGetElements(pageID, tag)
		rpcs++
		if err == nil {
			allRefs = append(allRefs, refs...)
		}
	}
	if len(allRefs) == 0 && err != nil {
		// Page.getElements might not be supported; try Page.getElement
		// for a few common selectors.
		for _, tag := range tags {
			if len(allRefs) >= maxElements {
				break
			}
			elem, err := automatorGetElement(pageID, tag)
			rpcs++
			if err == nil {
				allRefs = append(allRefs, elem)
			}
		}
	}
	snaps, enrichRPCs := buildSnapshotElements(pageID, allRefs, maxElements, snapGen, enrichBudget)
	return snaps, rpcs + enrichRPCs
}

// snapshotEnrichMaxRPCs is the strict maximum number of Element.getWXML
// RPCs that buildSnapshotElements will issue to enrich elements. This bounds
// the total RPC cost of a snapshot regardless of element count.
const snapshotEnrichMaxRPCs = 20

// findEnrichMaxRPCs is the enrichment budget for find operations. Higher
// than snapshot because find needs to search through more elements by text.
const findEnrichMaxRPCs = 100

// findMaxElements is the maximum number of elements to discover for find
// operations. Higher than snapshot to handle lists with many rows.
const findMaxElements = 200

// snapshotMaxTextLen bounds the text/value/class fields in snapshot elements
// to prevent a single element from consuming excessive output bytes.
const snapshotMaxTextLen = 80

// snapshotMaxClassLen bounds the class string to a concise summary.
const snapshotMaxClassLen = 60

// buildSnapshotElements converts wxElementRef list to wxSnapshotElement list,
// registering each in the ref registry. Truncates to maxElements.
//
// Enrichment: for each element (up to snapshotEnrichMaxRPCs), issues one
// Element.getAttributes RPC to fetch id, class, value, disabled, name, and
// other metadata. Elements beyond the RPC budget are included without
// enrichment (tag + ref only). Text is extracted from the WXML inner content
// via a lightweight getWXML call only for the first few elements (bounded).
//
// All enrichment is bounded by:
//   - maxElements (element count cap)
//   - snapshotEnrichMaxRPCs (WXML RPC cap)
//   - snapshotMaxTextLen / snapshotMaxClassLen (per-field byte caps)
//   - bounded concurrency (max 4 concurrent RPCs)
func buildSnapshotElements(pageID int, refs []wxElementRef, maxElements int, snapGen int64, enrichBudget int) ([]wxSnapshotElement, int) {
	count := len(refs)
	if count > maxElements {
		count = maxElements
	}

	// Allocate result slice and register refs first (no RPCs).
	snaps := make([]wxSnapshotElement, count)
	for i := 0; i < count; i++ {
		ref := wxRefs.register(pageID, refs[i], snapGen)
		snaps[i] = wxSnapshotElement{
			Ref:      ref,
			Tag:      refs[i].TagName,
			Selector: refs[i].TagName,
		}
	}

	// Enrich up to snapshotEnrichMaxRPCs elements with WXML.
	// Use bounded concurrency to avoid overwhelming the automator.
	enrichCount := count
	if enrichCount > enrichBudget {
		enrichCount = enrichBudget
	}

	type enrichResult struct {
		index int
		wxml  string
		err   error
	}

	results := make([]enrichResult, enrichCount)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // max 4 concurrent RPCs

	for i := 0; i < enrichCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// Use outer WXML to get attributes (class, name, value, etc.)
			wxml, err := automatorGetWXML(pageID, refs[idx], "outer")
			results[idx] = enrichResult{index: idx, wxml: wxml, err: err}
		}(i)
	}
	wg.Wait()

	// Apply enrichment results by parsing WXML attributes.
	for _, r := range results {
		if r.err != nil || r.wxml == "" {
			continue
		}
		s := &snaps[r.index]
		attrs := parseWxmlAttributes(r.wxml)
		if v, ok := attrs["class"]; ok {
			s.Class = truncateString(v, snapshotMaxClassLen)
		}
		if v, ok := attrs["id"]; ok {
			s.ID = truncateString(v, snapshotMaxTextLen)
		}
		if v, ok := attrs["value"]; ok {
			s.Value = truncateString(v, snapshotMaxTextLen)
		}
		if v, ok := attrs["name"]; ok {
			s.Name = truncateString(v, snapshotMaxTextLen)
		}
		if v, ok := attrs["placeholder"]; ok {
			// Use placeholder as text for input/textarea elements.
			if s.Text == "" {
				s.Text = truncateString(v, snapshotMaxTextLen)
			}
		}
		if _, ok := attrs["disabled"]; ok {
			s.Disabled = true
		}
		// Extract inner text content from the WXML.
		inner := extractWxmlInnerText(r.wxml)
		if inner != "" && s.Text == "" {
			s.Text = truncateString(inner, snapshotMaxTextLen)
		} else if inner != "" {
			// If we already have placeholder as text, keep inner text as well
			// by appending it (bounded).
			combined := s.Text + " | " + inner
			s.Text = truncateString(combined, snapshotMaxTextLen)
		}
	}

	return snaps, enrichCount
}

// parseWxmlAttributes extracts key-value attributes from the opening tag
// of a WXML string. e.g. <textarea class="reason-input" name="原因描述">
// returns map{"class": "reason-input", "name": "原因描述"}.
func parseWxmlAttributes(wxml string) map[string]string {
	attrs := make(map[string]string)
	// Find the opening tag
	gt := strings.Index(wxml, ">")
	if gt < 0 {
		return attrs
	}
	tag := wxml[:gt]
	// Remove the tag name
	firstSpace := strings.IndexAny(tag, " \t")
	if firstSpace < 0 {
		return attrs
	}
	attrStr := tag[firstSpace+1:]
	// Parse key="value" pairs
	i := 0
	for i < len(attrStr) {
		// Skip whitespace
		for i < len(attrStr) && (attrStr[i] == ' ' || attrStr[i] == '\t' || attrStr[i] == '\n') {
			i++
		}
		if i >= len(attrStr) {
			break
		}
		// Find key
		eq := strings.Index(attrStr[i:], "=")
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(attrStr[i : i+eq])
		i += eq + 1
		// Expect a quote
		if i >= len(attrStr) || (attrStr[i] != '"' && attrStr[i] != '\'') {
			break
		}
		quote := attrStr[i]
		i++ // skip opening quote
		end := strings.IndexByte(attrStr[i:], quote)
		if end < 0 {
			break
		}
		value := attrStr[i : i+end]
		i += end + 1
		if key != "" {
			attrs[key] = value
		}
	}
	return attrs
}

// extractWxmlInnerText extracts the text content between opening and closing
// tags of a WXML string. e.g. <button>Submit</button> returns "Submit".
// For self-closing tags or empty content, returns "".
func extractWxmlInnerText(wxml string) string {
	gt := strings.Index(wxml, ">")
	if gt < 0 {
		return ""
	}
	// Check if it's a self-closing tag
	if gt > 0 && wxml[gt-1] == '/' {
		return ""
	}
	rest := wxml[gt+1:]
	// Find the closing tag
	lt := strings.Index(rest, "</")
	if lt < 0 {
		return ""
	}
	inner := rest[:lt]
	// Strip nested tags to get pure text
	stripped := stripWxmlTags(inner)
	return strings.TrimSpace(stripped)
}

// stripWxmlTags removes all <...> tags from a string, leaving only text.
func stripWxmlTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, ch := range s {
		if ch == '<' {
			inTag = true
			continue
		}
		if ch == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// snapshotResultToMap converts a wxSnapshotResult to a map[string]any,
// omitting empty fields for token efficiency.
func snapshotResultToMap(r wxSnapshotResult) map[string]any {
	out := map[string]any{
		"backend":     r.Backend,
		"page_id":     r.PageID,
		"path":        r.Path,
		"elements":    r.Elements,
		"rpc_count":   r.RPCCount,
		"duration_ms": r.DurationMs,
	}
	if r.StackDepth > 0 {
		out["stack_depth"] = r.StackDepth
	}
	if r.Truncation.Truncated {
		out["truncation"] = r.Truncation
	}
	return out
}

// --- Click via ref ---

// automatorClickByRef performs a tap on an element identified by ref or
// selector. It waits for the element to exist before tapping, and unless
// noWait is true, waits for an observable post-action settlement (route
// change or a short bounded quiet period). Returns before/after page
// identity and timing.
func automatorClickByRef(refOrSelector string, noWait bool) (map[string]any, error) {
	start := time.Now()

	// Resolve ref or selector to page ID + element.
	pageID, elem, err := resolveRefOrSelector(refOrSelector)
	if err != nil {
		return nil, err
	}

	// Record before-route for settlement detection.
	beforePath, _, _ := automatorCurrentPage()

	// Tap the element.
	if err := automatorTapElement(pageID, elem); err != nil {
		return nil, err
	}

	result := map[string]any{
		"backend":     BackendWeChat,
		"tapped":      refOrSelector,
		"element_id":  elem.ElementID,
		"tag":         elem.TagName,
		"before_path": beforePath,
		"duration_ms": time.Since(start).Milliseconds(),
	}

	// Post-action settlement: unless noWait, check for route change.
	if !noWait {
		afterPath, err := waitForRouteSettlementShort(beforePath)
		if err == nil {
			result["after_path"] = afterPath
			if afterPath != beforePath {
				result["route_changed"] = true
				// Invalidate refs for the old page.
				wxRefs.invalidatePage(pageID)
			}
		}
	}

	return result, nil
}

// --- Find via text/regex ---

// automatorFind searches the current Mini Program page for elements matching
// text or a regex pattern. Takes a snapshot (with enriched element metadata)
// and filters elements by matching text, id, class, name, or value against
// the search pattern. Returns a bounded list of matching elements with refs.
//
// Text matching is case-insensitive substring match. Regex matching uses
// Go regexp against the element's text/id/class/name/value fields.
func automatorFind(text, regex string) (map[string]any, error) {
	return automatorFindBudget(text, regex, findEnrichMaxRPCs, findMaxElements)
}

// automatorFindBudget is like automatorFind but with a custom enrichment
// budget and max element count. Used by wait_for for lightweight polling.
func automatorFindBudget(text, regex string, enrichBudget, maxElems int) (map[string]any, error) {
	text = strings.TrimSpace(text)
	regex = strings.TrimSpace(regex)
	if text == "" && regex == "" {
		return nil, &ToolError{
			Code:    ErrInvalidInput,
			Op:      "find",
			Message: "find requires text or regex",
		}
	}

	// Compile regex if provided.
	var re *regexp.Regexp
	var regexErr error
	if regex != "" {
		re, regexErr = regexp.Compile(regex)
		if regexErr != nil {
			return nil, &ToolError{
				Code:    ErrInvalidInput,
				Op:      "find",
				Message: fmt.Sprintf("invalid regex %q: %v", regex, regexErr),
			}
		}
	}

	// Take a snapshot to get current elements with enriched metadata.
	// Use a higher enrichment budget for find to search through more elements.
	pageID, err := resolveCurrentPageID()
	if err != nil {
		return nil, err
	}
	snapGen := wxRefs.nextSnapshotGeneration()
	elements, rpcCount := discoverInteractiveElementsBudget(pageID, maxElems, snapGen, enrichBudget)
	path, _, _ := automatorCurrentPage()

	// Filter elements by text/regex match against metadata fields.
	matches := make([]map[string]any, 0, len(elements))
	for _, elem := range elements {
		if !elementMatchesPattern(elem, text, re) {
			continue
		}
		match := map[string]any{
			"ref": elem.Ref,
			"tag": elem.Tag,
		}
		if elem.Text != "" {
			match["text"] = elem.Text
		}
		if elem.ID != "" {
			match["id"] = elem.ID
		}
		if elem.Class != "" {
			match["class"] = elem.Class
		}
		if elem.Name != "" {
			match["name"] = elem.Name
		}
		if elem.Value != "" {
			match["value"] = elem.Value
		}
		matches = append(matches, match)
	}

	result := map[string]any{
		"backend":   BackendWeChat,
		"path":      path,
		"matches":   matches,
		"count":     len(matches),
		"rpc_count": rpcCount,
	}

	// Apply truncation metadata.
	const maxMatches = 50
	totalMatches := len(matches)
	shown := totalMatches
	if shown > maxMatches {
		shown = maxMatches
		matches = matches[:maxMatches]
		result["matches"] = matches
		result["count"] = shown
	}
	result["truncation"] = newTruncationMeta(totalMatches, shown, "max_matches")

	return result, nil
}

// elementMatchesPattern checks if an element's metadata fields match the
// given text (case-insensitive substring) or regex pattern.
func elementMatchesPattern(elem wxSnapshotElement, text string, re *regexp.Regexp) bool {
	// Collect all searchable text from the element.
	fields := []string{elem.Text, elem.ID, elem.Class, elem.Name, elem.Value, elem.Tag}

	if text != "" {
		lowerText := strings.ToLower(text)
		for _, f := range fields {
			if strings.Contains(strings.ToLower(f), lowerText) {
				return true
			}
		}
	}
	if re != nil {
		for _, f := range fields {
			if re.MatchString(f) {
				return true
			}
		}
	}
	return false
}

// --- Wait for condition ---

// automatorWaitFor polls the WeChat Mini Program page for a condition.
// Supports selector presence and URL/path matching. Text-based conditions
// are degraded because WeChat does not expose a JS evaluation equivalent
// for arbitrary page text — the result explicitly notes this.
func automatorWaitFor(ctx context.Context, input waitForInput) (map[string]any, error) {
	timeout := time.Duration(input.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	poll := time.Duration(input.PollMs) * time.Millisecond
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}

	selector := strings.TrimSpace(input.Selector)
	urlMatch := strings.TrimSpace(input.URLMatches)
	textCond := strings.TrimSpace(input.Text)
	textAbsent := strings.TrimSpace(input.TextAbsent)

	if selector == "" && urlMatch == "" && textCond == "" && textAbsent == "" {
		return nil, &ToolError{
			Code:    ErrInvalidInput,
			Op:      "wait_for",
			Message: "wait_for requires text, text_absent, selector, or url_matches",
		}
	}

	// Determine the condition type for the result.
	conditionType := "selector"
	if urlMatch != "" {
		conditionType = "url_matches"
	} else if textCond != "" {
		conditionType = "text"
	} else if textAbsent != "" {
		conditionType = "text_absent"
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return map[string]any{
				"backend":    BackendWeChat,
				"satisfied":  false,
				"cancelled":  true,
				"condition":  conditionType,
				"timeout_ms": timeout.Milliseconds(),
			}, ctx.Err()
		default:
		}

		satisfied := false

		if selector != "" {
			// Poll for element presence via Page.getElement.
			pageID, err := resolveCurrentPageID()
			if err == nil {
				_, err := automatorGetElement(pageID, selector)
				if err == nil {
					satisfied = true
				}
			}
		} else if urlMatch != "" {
			// Poll for path match via App.getCurrentPage.
			path, _, err := automatorCurrentPage()
			if err == nil {
				// Normalize: strip leading "/" from both for comparison.
				normalizedPath := strings.TrimPrefix(path, "/")
				normalizedMatch := strings.TrimPrefix(urlMatch, "/")
				if strings.Contains(path, urlMatch) || strings.Contains(normalizedPath, normalizedMatch) {
					satisfied = true
				}
			}
		} else if textCond != "" {
			// Poll for text presence. Use a fast two-phase check:
			// 1. Page.getData (1 RPC) — check if text is in reactive data.
			//    This covers all text rendered via {{}} bindings.
			// 2. automatorFindBudget (snapshot + enrichment) — fallback for
			//    text that only appears in WXML, not in page data.
			//    Uses a small budget (10 RPCs) for speed during polling.
			if textInPageData(textCond) {
				satisfied = true
			} else {
				findResult, err := automatorFindBudget(textCond, "", 10, 30)
				if err == nil {
					if count, ok := findResult["count"].(int); ok && count > 0 {
						satisfied = true
					}
				}
			}
		} else if textAbsent != "" {
			// Poll for text absence. Same two-phase approach.
			if !textInPageData(textAbsent) {
				findResult, err := automatorFindBudget(textAbsent, "", 10, 30)
				if err == nil {
					if count, ok := findResult["count"].(int); ok && count == 0 {
						satisfied = true
					}
				}
			}
		}

		if satisfied {
			return map[string]any{
				"backend":   BackendWeChat,
				"satisfied": true,
				"condition": conditionType,
			}, nil
		}

		select {
		case <-ctx.Done():
			return map[string]any{
				"backend":   BackendWeChat,
				"satisfied": false,
				"cancelled": true,
				"condition": conditionType,
			}, ctx.Err()
		case <-time.After(poll):
		}
	}

	return map[string]any{
		"backend":    BackendWeChat,
		"satisfied":  false,
		"timed_out":  true,
		"condition":  conditionType,
		"timeout_ms": timeout.Milliseconds(),
	}, nil
}

// waitForRouteSettlementShort polls for route change with a short timeout
// (2s). Returns the final route. If the route doesn't change, returns the
// current route (same-page interaction).
func waitForRouteSettlementShort(beforeRoute string) (string, error) {
	const (
		maxWait   = 2 * time.Second
		pollEvery = 200 * time.Millisecond
	)
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		path, _, err := automatorCurrentPage()
		if err != nil {
			return "", err
		}
		if path != beforeRoute {
			return path, nil
		}
		time.Sleep(pollEvery)
	}
	path, _, err := automatorCurrentPage()
	if err != nil {
		return "", err
	}
	return path, nil
}

// textInPageData is a fast (1 RPC) check for whether a text string appears
// in the current page's reactive data. Used by wait_for as a quick pre-check
// before the slower snapshot+enrichment find. Returns false on any error.
func textInPageData(text string) bool {
	pageID, err := resolveCurrentPageID()
	if err != nil {
		return false
	}
	data, err := automatorPageData(pageID, "")
	if err != nil {
		return false
	}
	return strings.Contains(fmt.Sprintf("%v", data), text)
}

// --- Fill via ref ---

// automatorFillByRef fills an input/textarea element identified by ref or
// selector. Uses the official Element.callFunction protocol. Rejects
// non-input/textarea elements with an actionable error.
func automatorFillByRef(refOrSelector, value string) (map[string]any, error) {
	pageID, elem, err := resolveRefOrSelector(refOrSelector)
	if err != nil {
		return nil, err
	}
	if err := automatorFillElement(pageID, elem, value); err != nil {
		return nil, err
	}
	return map[string]any{
		"backend":      BackendWeChat,
		"filled":       refOrSelector,
		"element_id":   elem.ElementID,
		"tag":          elem.TagName,
		"value_length": len([]rune(value)),
	}, nil
}

// --- Exists via ref ---

// automatorExistsByRef checks whether an element matching ref or selector
// exists on the current page. Returns (found, error).
func automatorExistsByRef(refOrSelector string) (bool, error) {
	if strings.HasPrefix(refOrSelector, "wx:") {
		pageID, err := resolveCurrentPageID()
		if err != nil {
			return false, err
		}
		_, err = wxRefs.resolve(refOrSelector, pageID)
		return err == nil, nil
	}
	// Plain selector — try Page.getElement.
	pageID, err := resolveCurrentPageID()
	if err != nil {
		return false, err
	}
	_, err = automatorGetElement(pageID, refOrSelector)
	if err != nil {
		// Element not found is not a hard failure for exists.
		return false, nil
	}
	return true, nil
}

// --- Helpers ---

// min returns the smaller of a and b.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// truncateString returns s truncated to maxLen runes, with "..." appended
// if truncation occurred. Returns s unchanged if shorter.
func truncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "..."
}

// formatWxError wraps an error as a ToolError if it isn't already one.
func formatWxError(op string, err error) *ToolError {
	if te, ok := err.(*ToolError); ok {
		return te
	}
	return &ToolError{
		Code:    ErrUnsupported,
		Op:      op,
		Message: err.Error(),
	}
}

// jsonString marshals v to a JSON string, returning "" on error.
func jsonString(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

// refSummary returns a short summary of a ref for diagnostics.
func refSummary(ref string) string {
	pageID, elemID, _, _, ok := decodeWxRef(ref)
	if !ok {
		return ref
	}
	return fmt.Sprintf("wx:page=%d,elem=%s", pageID, elemID)
}
