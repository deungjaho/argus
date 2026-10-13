package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolSuggestMiddleware rewrites "unknown tool" errors from the MCP layer
// into suggestions, so a hallucinated or renamed tool name still points the
// agent at the closest real tools instead of dead-ending.
func toolSuggestMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		if err == nil || method != "tools/call" || !strings.Contains(err.Error(), "unknown tool") {
			return res, err
		}
		name := ""
		if ctr, ok := req.(*mcp.CallToolRequest); ok && ctr.Params != nil {
			name = ctr.Params.Name
		}
		if name == "" {
			return res, err
		}
		if sugg := suggestTools(name, enabledTools); len(sugg) > 0 {
			return res, fmt.Errorf("unknown tool %q — did you mean: %s? (available: %s)",
				name, strings.Join(sugg, ", "), strings.Join(enabledTools, ", "))
		}
		return res, fmt.Errorf("unknown tool %q (available: %s)", name, strings.Join(enabledTools, ", "))
	}
}

// knownRenames maps observed hallucinated or pre-refactor tool names to
// their closest canonical tool. Covers semantic renames that pure string
// similarity cannot recover (e.g. "open" -> "navigate").
var knownRenames = map[string]string{
	"open":            "navigate",
	"goto":            "navigate",
	"url":             "navigate",
	"fill_form":       "fill",
	"input_text":      "fill",
	"press_key":       "type",
	"key":             "type",
	"console":         "console_messages",
	"logs":            "console_messages",
	"console_log":     "console_messages",
	"network":         "network_requests",
	"browser_preview": "look",
	"screenshot_grid": "screenshot",
	"click_element":   "click",
	"dom_snapshot":    "snapshot",
	"query_selector":  "get_element",
}

// suggestTools returns up to 3 registered tools most similar to name:
// known renames first, then substring hits, then closest normalized
// edit distance (<= 0.6).
func suggestTools(name string, candidates []string) []string {
	lname := strings.ToLower(name)
	var hits []string
	addHit := func(c string) {
		for _, h := range hits {
			if h == c {
				return
			}
		}
		hits = append(hits, c)
	}
	if mapped, ok := knownRenames[lname]; ok {
		for _, c := range candidates {
			if c == mapped {
				addHit(c)
			}
		}
	}
	for _, c := range candidates {
		lc := strings.ToLower(c)
		if strings.Contains(lc, lname) || strings.Contains(lname, lc) {
			addHit(c)
		}
	}
	type scored struct {
		name  string
		score float64
	}
	var near []scored
	for _, c := range candidates {
		lc := strings.ToLower(c)
		d := levenshtein(lname, lc)
		maxLen := len(lname)
		if len(lc) > maxLen {
			maxLen = len(lc)
		}
		score := float64(d) / float64(maxLen)
		if d > 0 && score <= 0.6 {
			near = append(near, scored{c, score})
		}
	}
	sort.Slice(near, func(i, j int) bool { return near[i].score < near[j].score })
	for _, n := range near {
		if len(hits) >= 3 {
			break
		}
		addHit(n.name)
	}
	if len(hits) > 3 {
		hits = hits[:3]
	}
	return hits
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
