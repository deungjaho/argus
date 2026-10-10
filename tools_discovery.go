package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolSummaryInput is the input for the tool_summary discovery tool.
type toolSummaryInput struct {
	Capability string `json:"capability,omitempty"` // filter: core, diagnose, debug, full (default: all)
	Filter     string `json:"filter,omitempty"`
	Toolset    string `json:"toolset,omitempty"`
	Group      string `json:"group,omitempty"`
	Verbose    bool   `json:"verbose,omitempty"` // include full side_effects and descriptions
}

// toolSummaryRow is one row of the compact discovery table.
type toolSummaryRow struct {
	Name        string `json:"name"`
	Capability  string `json:"capability"`
	Sensitivity string `json:"sensitivity"`
	Idempotent  bool   `json:"idempotent"`
	SideEffects string `json:"side_effects"`
	Description string `json:"description,omitempty"` // only when verbose
}

// toolSummaryOutput is the structured output of tool_summary.
type toolSummaryOutput struct {
	Counts  map[string]int     `json:"counts"`
	Total   int                `json:"total"`
	Groups  []toolSummaryGroup `json:"groups"`
	Verbose bool               `json:"verbose"`
}

// toolSummaryGroup is a capability-grouped set of tool rows.
type toolSummaryGroup struct {
	Capability string           `json:"capability"`
	Count      int              `json:"count"`
	Tools      []toolSummaryRow `json:"tools"`
}

func registerDiscoveryTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "tool_summary",
		Description: "Return a compact, categorized list of all registered tools with capability, sensitivity, idempotency, and side effects. Pass capability=core to filter. Pass verbose=true for full side_effects and one-line descriptions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input toolSummaryInput) (*mcp.CallToolResult, any, error) {
		cap := resolveFirstNonEmpty(input.Capability, input.Filter, input.Toolset, input.Group)
		return nil, buildToolSummary(cap, input.Verbose), nil
	})
}

// buildToolSummary constructs the compact tool discovery summary from the
// toolMetaRegistry. It groups tools by capability set, computes canonical/
// deprecated/trace counts, and optionally truncates side_effects to 40 chars
// (non-verbose mode) or includes full side_effects plus descriptions (verbose).
func buildToolSummary(filter string, verbose bool) toolSummaryOutput {
	filterCap := CapabilitySet(strings.TrimSpace(strings.ToLower(filter)))

	// Classify tools for counts. Deprecated tools are those whose
	// description starts with "Deprecated". Trace tools are the three
	// diagnose trace tools (timeline, diagnose, export_trace). Canonical
	// is everything else.
	canonical, deprecated, trace := 0, 0, 0
	traceNames := map[string]bool{
		"timeline":     true,
		"diagnose":     true,
		"export_trace": true,
	}
	for _, m := range toolMetaRegistry {
		switch {
		case strings.HasPrefix(m.Description, "Deprecated"):
			deprecated++
		case traceNames[m.Name]:
			trace++
		default:
			canonical++
		}
	}

	// Group by capability, preserving registry order within each group.
	groupOrder := []CapabilitySet{CapDefault, CapCore, CapDiagnose, CapDebug, CapFull}
	groups := map[CapabilitySet][]toolSummaryRow{}
	for _, m := range toolMetaRegistry {
		if filterCap != "" && filterCap != CapFull && m.Capability != filterCap {
			continue
		}
		row := toolSummaryRow{
			Name:        m.Name,
			Capability:  string(m.Capability),
			Sensitivity: m.Sensitivity,
			Idempotent:  m.Idempotent,
			SideEffects: formatSideEffects(m.SideEffects, verbose),
		}
		if verbose {
			row.Description = m.Description
		}
		groups[m.Capability] = append(groups[m.Capability], row)
	}

	out := toolSummaryOutput{
		Counts: map[string]int{
			"canonical":  canonical,
			"deprecated": deprecated,
			"trace":      trace,
		},
		Total:   len(toolMetaRegistry),
		Verbose: verbose,
	}
	for _, cap := range groupOrder {
		rows := groups[cap]
		if len(rows) == 0 {
			continue
		}
		out.Groups = append(out.Groups, toolSummaryGroup{
			Capability: string(cap),
			Count:      len(rows),
			Tools:      rows,
		})
	}
	return out
}

// formatSideEffects joins side effects into a comma-separated string. In
// non-verbose mode it is truncated to 40 characters; in verbose mode the full
// list is returned.
func formatSideEffects(effects []string, verbose bool) string {
	if len(effects) == 0 {
		return "none"
	}
	joined := strings.Join(effects, ", ")
	if !verbose {
		return truncateRunes(joined, 40)
	}
	return joined
}

// toolSummaryText renders the discovery summary as a compact text table for
// human/agent consumption. Used by tests to verify the byte bound.
func toolSummaryText(summary toolSummaryOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d canonical, %d deprecated, %d trace (%d total)\n",
		summary.Counts["canonical"], summary.Counts["deprecated"],
		summary.Counts["trace"], summary.Total)
	for _, g := range summary.Groups {
		fmt.Fprintf(&b, "\n[%s] (%d)\n", g.Capability, g.Count)
		for _, t := range g.Tools {
			if summary.Verbose {
				fmt.Fprintf(&b, "  %-22s sens=%-6s idem=%-5t effects=%s\n    %s\n",
					t.Name, t.Sensitivity, t.Idempotent, t.SideEffects, t.Description)
			} else {
				fmt.Fprintf(&b, "  %-22s %-8s %-6s idem=%-5t %s\n",
					t.Name, t.Capability, t.Sensitivity, t.Idempotent, t.SideEffects)
			}
		}
	}
	return b.String()
}

// sortedToolNames returns the names of all registered tools sorted
// alphabetically. Used by tests and audits.
func sortedToolNames() []string {
	names := make([]string, 0, len(toolMetaRegistry))
	for _, m := range toolMetaRegistry {
		names = append(names, m.Name)
	}
	sort.Strings(names)
	return names
}
