package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// ToolStat aggregates metrics for a single tool.
type ToolStat struct {
	Name        string
	Calls       int
	Successes   int
	Errors      int
	TotalTimeMs int64
	ArgKeys     map[string]int
	CommonError map[string]int
}

// UsageSummary aggregates all historical agent call telemetry.
type UsageSummary struct {
	TotalCalls     int
	TotalSuccesses int
	TotalErrors    int
	TotalTimeMs    int64
	StartTime      time.Time
	EndTime        time.Time
	Tools          map[string]*ToolStat
	RecentCalls    []AgentCallRecord
}

// AnalyzeUsageLog reads a JSON Lines telemetry file and computes usage habits.
func AnalyzeUsageLog(r io.Reader) (*UsageSummary, error) {
	scanner := bufio.NewScanner(r)
	summary := &UsageSummary{
		Tools: make(map[string]*ToolStat),
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec AgentCallRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}

		summary.TotalCalls++
		summary.TotalTimeMs += rec.DurationMs
		if rec.Success {
			summary.TotalSuccesses++
		} else {
			summary.TotalErrors++
		}

		if t, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil {
			if summary.StartTime.IsZero() || t.Before(summary.StartTime) {
				summary.StartTime = t
			}
			if summary.EndTime.IsZero() || t.After(summary.EndTime) {
				summary.EndTime = t
			}
		}

		st, exists := summary.Tools[rec.Tool]
		if !exists {
			st = &ToolStat{
				Name:        rec.Tool,
				ArgKeys:     make(map[string]int),
				CommonError: make(map[string]int),
			}
			summary.Tools[rec.Tool] = st
		}
		st.Calls++
		st.TotalTimeMs += rec.DurationMs
		if rec.Success {
			st.Successes++
		} else {
			st.Errors++
			if rec.Error != "" {
				// Shorten/sanitize error for summary grouping
				errKey := rec.Error
				if len(errKey) > 80 {
					errKey = errKey[:80] + "..."
				}
				st.CommonError[errKey]++
			}
		}

		for k := range rec.Arguments {
			st.ArgKeys[k]++
		}

		// Keep a sliding window of recent 10 calls
		summary.RecentCalls = append(summary.RecentCalls, rec)
		if len(summary.RecentCalls) > 10 {
			summary.RecentCalls = summary.RecentCalls[1:]
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return summary, nil
}

// PrintUsageReport parses and prints a formatted terminal report from the specified or default file.
func PrintUsageReport(path string) error {
	if path == "" {
		path = defaultUsageLogPath()
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("No usage telemetry recorded yet (log file not found at: %s)\n", path)
			fmt.Println("Usage logs are automatically created as agents invoke MCP tools.")
			return nil
		}
		return fmt.Errorf("failed to read telemetry file %s: %w", path, err)
	}
	defer f.Close()

	summary, err := AnalyzeUsageLog(f)
	if err != nil {
		return fmt.Errorf("failed to parse telemetry log: %w", err)
	}

	if summary.TotalCalls == 0 {
		fmt.Printf("Usage log file is empty: %s\n", path)
		return nil
	}

	fmt.Println("================================================================================")
	fmt.Println("                     ARGUS MCP AGENT USAGE & HABIT REPORT                       ")
	fmt.Println("================================================================================")
	fmt.Printf("Log File:         %s\n", path)
	fmt.Printf("Total Calls:      %d\n", summary.TotalCalls)
	fmt.Printf("Overall Success:  %.1f%% (%d ok, %d errors)\n", float64(summary.TotalSuccesses)/float64(summary.TotalCalls)*100, summary.TotalSuccesses, summary.TotalErrors)
	avgMs := float64(summary.TotalTimeMs) / float64(summary.TotalCalls)
	fmt.Printf("Average Latency:  %.1f ms\n", avgMs)
	if !summary.StartTime.IsZero() && !summary.EndTime.IsZero() {
		fmt.Printf("Time Horizon:     %s -> %s\n", summary.StartTime.Format("2006-01-02 15:04:05"), summary.EndTime.Format("2006-01-02 15:04:05"))
	}
	fmt.Println("--------------------------------------------------------------------------------")

	// Sort tools by calls descending
	var tools []*ToolStat
	for _, t := range summary.Tools {
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Calls > tools[j].Calls
	})

	fmt.Println("\n[1] TOOL FREQUENCY & SUCCESS RANKING")
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("%-18s %8s %8s %10s %12s\n", "TOOL NAME", "CALLS", "SHARE", "SUCCESS", "AVG LATENCY")
	fmt.Println("--------------------------------------------------------------------------------")
	for _, t := range tools {
		share := float64(t.Calls) / float64(summary.TotalCalls) * 100
		successRate := float64(t.Successes) / float64(t.Calls) * 100
		avgToolMs := float64(t.TotalTimeMs) / float64(t.Calls)
		fmt.Printf("%-18s %8d %7.1f%% %9.1f%% %10.1f ms\n", t.Name, t.Calls, share, successRate, avgToolMs)
	}

	fmt.Println("\n[2] AGENT ARGUMENT HABITS & ALIAS PREFERENCES")
	fmt.Println("--------------------------------------------------------------------------------")
	foundHabits := false
	for _, t := range tools {
		if len(t.ArgKeys) == 0 {
			continue
		}
		// Sort argument keys by count
		type kv struct {
			k string
			v int
		}
		var kvs []kv
		for k, v := range t.ArgKeys {
			kvs = append(kvs, kv{k, v})
		}
		sort.Slice(kvs, func(i, j int) bool {
			return kvs[i].v > kvs[j].v
		})

		var parts []string
		for _, item := range kvs {
			pct := float64(item.v) / float64(t.Calls) * 100
			parts = append(parts, fmt.Sprintf("%s (%d, %.0f%%)", item.k, item.v, pct))
		}
		fmt.Printf("• %-16s -> Preferred parameters: %s\n", t.Name, strings.Join(parts, ", "))
		foundHabits = true
	}
	if !foundHabits {
		fmt.Println("  (No parameter details recorded)")
	}

	fmt.Println("\n[3] TOP FRICTION POINTS & ERRORS")
	fmt.Println("--------------------------------------------------------------------------------")
	foundErrors := false
	for _, t := range tools {
		if t.Errors == 0 {
			continue
		}
		foundErrors = true
		fmt.Printf("• %s: %d errors (%.1f%% failure rate)\n", t.Name, t.Errors, float64(t.Errors)/float64(t.Calls)*100)
		for errStr, count := range t.CommonError {
			fmt.Printf("    [%dx] %s\n", count, errStr)
		}
	}
	if !foundErrors {
		fmt.Println("  ✓ 100% clean: zero errors recorded across all tools!")
	}

	fmt.Println("\n[4] RECENT INVOCATIONS (SLIDING WINDOW)")
	fmt.Println("--------------------------------------------------------------------------------")
	for i := len(summary.RecentCalls) - 1; i >= 0; i-- {
		r := summary.RecentCalls[i]
		statusStr := "OK "
		if !r.Success {
			statusStr = "ERR"
		}
		argsJSON, _ := json.Marshal(r.Arguments)
		argStr := string(argsJSON)
		if len(argStr) > 50 {
			argStr = argStr[:47] + "..."
		}
		tStr := r.Timestamp
		if len(tStr) > 19 {
			tStr = tStr[11:19]
		}
		fmt.Printf("[%s] %s | %-14s | %4dms | args: %s\n", tStr, statusStr, r.Tool, r.DurationMs, argStr)
	}

	fmt.Println("================================================================================")
	return nil
}
