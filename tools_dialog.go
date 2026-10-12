package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type handleDialogInput struct {
	Accept     *bool  `json:"accept,omitempty"`
	PromptText string `json:"prompt_text,omitempty"`
	Action     string `json:"action,omitempty"`
	Text       string `json:"text,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	Policy     string `json:"policy,omitempty"`
	TimeoutMs  int    `json:"timeout_ms,omitempty"`
	Timeout    int    `json:"timeout,omitempty"`
}

type uploadFileInput struct {
	Ref      string   `json:"ref,omitempty"`
	Selector string   `json:"selector,omitempty"`
	Target   string   `json:"target,omitempty"`
	CSS      string   `json:"css,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Path     string   `json:"path,omitempty"`
	Files    []string `json:"files,omitempty"`
	File     string   `json:"file,omitempty"`
}

func registerDialogTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "handle_dialog",
		Description: "Inspect, accept, dismiss, or configure auto-handling for JavaScript dialogs (alert, confirm, prompt, beforeunload). Supports non-blocking queries and anti-deadlock controls.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input handleDialogInput) (*mcp.CallToolResult, any, error) {
		ar := BeginAction("handle_dialog")
		defer ar.End()
		ar.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			ar.SetErrorFromError(err)
			return nil, nil, err
		}
		targetID := getCurrentTargetID()
		if targetID == "" {
			targetID = targetIDFromWSURL(ws)
		}
		ar.SetTarget(targetID)
		state := dialogMgr.GetOrCreate(targetID, ws)

		action := strings.ToLower(strings.TrimSpace(input.Action))
		promptText := resolveFirstNonEmpty(input.PromptText, input.Text, input.Prompt)

		// 1. Status / Inspect query (non-blocking)
		if action == "status" || action == "inspect" || action == "get" {
			pending := state.Pending()
			policy, curPrompt := state.GetPolicy()
			res := map[string]any{
				"action_id":   string(ar.ID),
				"target_id":   targetID,
				"has_dialog":  pending != nil,
				"policy":      string(policy),
				"prompt_text": curPrompt,
			}
			if pending != nil {
				res["dialog"] = pending
			}
			if last := state.LastHandled(); last != nil {
				res["last_dialog"] = last
			}
			ar.SetResult(res)
			return nil, res, nil
		}

		// 2. Policy configuration
		if action == "policy" || action == "set_policy" || input.Policy != "" {
			polStr := strings.ToLower(strings.TrimSpace(resolveFirstNonEmpty(input.Policy, action)))
			var newPolicy DialogPolicy
			switch polStr {
			case "accept", "auto_accept":
				newPolicy = DialogPolicyAccept
			case "none", "manual":
				newPolicy = DialogPolicyNone
			default:
				newPolicy = DialogPolicyDismiss
			}
			state.SetPolicy(newPolicy, promptText)
			res := map[string]any{
				"action_id":      string(ar.ID),
				"target_id":      targetID,
				"policy_updated": true,
				"policy":         string(newPolicy),
				"prompt_text":    promptText,
			}
			ar.SetResult(res)
			return nil, res, nil
		}

		// 3. History query
		if action == "history" {
			hist := state.History()
			res := map[string]any{
				"action_id": string(ar.ID),
				"target_id": targetID,
				"history":   hist,
				"count":     len(hist),
			}
			ar.SetResult(res)
			return nil, res, nil
		}

		// 4. Accept or Dismiss
		accept := true
		if input.Accept != nil {
			accept = *input.Accept
		} else if action == "dismiss" || action == "cancel" || action == "no" {
			accept = false
		} else if action == "accept" || action == "ok" || action == "yes" {
			accept = true
		}

		timeoutMs := resolveTimeout(input.TimeoutMs, input.Timeout)
		if !state.HasPending() && timeoutMs > 0 {
			state.DrainNotify()
			deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
			for !state.HasPending() && time.Now().Before(deadline) {
				remaining := time.Until(deadline)
				if remaining <= 0 {
					break
				}
				timer := time.NewTimer(remaining)
				select {
				case <-state.notifyCh:
					timer.Stop()
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					ar.SetErrorFromError(ctx.Err())
					return nil, nil, ctx.Err()
				}
			}
		}

		pending := state.Pending()
		params := map[string]any{"accept": accept}
		if promptText != "" {
			params["promptText"] = promptText
		} else if pending != nil && pending.DefaultPrompt != "" {
			params["promptText"] = pending.DefaultPrompt
		}

		if err := cdpRequest(ws, "Page.handleJavaScriptDialog", params, nil); err != nil {
			ar.SetErrorFromError(err)
			return nil, nil, err
		}
		state.MarkHandled(accept, promptText, false)
		rec.RecordEventWithAction("dialog", targetID, ar.ID, "", map[string]any{
			"status":      "handled",
			"accepted":    accept,
			"prompt_text": promptText,
		})

		res := map[string]any{
			"action_id": string(ar.ID),
			"handled":   true,
			"accepted":  accept,
			"target_id": targetID,
		}
		if pending != nil {
			res["dialog"] = pending
		}
		ar.SetResult(res)
		return nil, res, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "upload_file",
		Description: "Upload one or more files to a file input element. Specify the input by ref/selector and provide file paths.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input uploadFileInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("upload_file")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		selector := resolveSelector(input.Ref, input.Selector, input.CSS, input.Target, "", "")
		if selector == "" {
			rec.SetErrorFromError(fmt.Errorf("upload_file requires ref or selector"))
			return nil, nil, fmt.Errorf("upload_file requires ref or selector")
		}
		paths := input.Paths
		if len(paths) == 0 && len(input.Files) > 0 {
			paths = input.Files
		}
		if len(paths) == 0 {
			if p := resolveFirstNonEmpty(input.Path, input.File); p != "" {
				paths = []string{p}
			}
		}
		if len(paths) == 0 {
			rec.SetErrorFromError(fmt.Errorf("upload_file requires paths"))
			return nil, nil, fmt.Errorf("upload_file requires paths")
		}
		// Canonicalize and validate file paths to prevent path traversal.
		// Each path must resolve to an existing file after symlink resolution.
		canonicalPaths := make([]string, 0, len(paths))
		for _, p := range paths {
			abs, err := filepath.Abs(p)
			if err != nil {
				rec.SetErrorFromError(fmt.Errorf("cannot resolve path %q: %w", p, err))
				return nil, nil, fmt.Errorf("cannot resolve path %q: %w", p, err)
			}
			resolved, err := filepath.EvalSymlinks(abs)
			if err != nil {
				rec.SetErrorFromError(fmt.Errorf("path %q does not exist or cannot be resolved: %w", p, err))
				return nil, nil, fmt.Errorf("path %q does not exist or cannot be resolved: %w", p, err)
			}
			info, err := os.Stat(resolved)
			if err != nil {
				rec.SetErrorFromError(fmt.Errorf("cannot stat path %q: %w", p, err))
				return nil, nil, fmt.Errorf("cannot stat path %q: %w", p, err)
			}
			if info.IsDir() {
				rec.SetErrorFromError(fmt.Errorf("path %q is a directory, not a file", p))
				return nil, nil, fmt.Errorf("path %q is a directory, not a file", p)
			}
			canonicalPaths = append(canonicalPaths, resolved)
		}
		// Find the file input via DOM and set files using CDP
		value, err := cdpEvaluate(ws, fmt.Sprintf(`(() => {
const selector = %s;
const el = document.querySelector(selector);
if (!el) throw new Error('element not found');
if (el.tagName !== 'INPUT' || el.type !== 'file') throw new Error('element is not a file input');
return {found: true, multiple: el.multiple, backendNodeId: 'needs_cdp'};
})()`, jsString(selector)), true)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		_ = value
		// Use CDP DOM.setFileInputFiles — need to get backendNodeId first
		// Simpler approach: use Runtime to get the element and call setFiles
		// For now, use the Interact.setFileInputFiles via a workaround
		result, err := setFileInputFiles(ws, selector, canonicalPaths)
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		result["action_id"] = string(rec.ID)
		rec.SetResult(result)
		return nil, result, nil
	})
}

// setFileInputFiles uses CDP to set files on a file input element.
// Uses the connection pool for WebSocket communication.
func setFileInputFiles(ws, selector string, paths []string) (map[string]any, error) {
	// Enable DOM domain (idempotent)
	_ = cdpRequest(ws, "DOM.enable", map[string]any{}, nil)

	// Query selector to get node ID
	var queryResult struct {
		NodeID int `json:"nodeId"`
	}
	if err := cdpRequest(ws, "DOM.querySelector", map[string]any{
		"selector": selector,
	}, &queryResult); err != nil {
		return nil, fmt.Errorf("DOM.querySelector failed: %w", err)
	}
	if queryResult.NodeID == 0 {
		return nil, fmt.Errorf("element not found: %s", selector)
	}

	// Set files on the node
	if err := cdpRequest(ws, "DOM.setFileInputFiles", map[string]any{
		"nodeId": queryResult.NodeID,
		"files":  paths,
	}, nil); err != nil {
		return nil, fmt.Errorf("DOM.setFileInputFiles failed: %w", err)
	}

	return map[string]any{"uploaded": true, "selector": selector, "files": paths, "count": len(paths)}, nil
}
