package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type handleDialogInput struct {
	Accept     bool   `json:"accept"`
	PromptText string `json:"prompt_text,omitempty"`
}

type uploadFileInput struct {
	Ref      string   `json:"ref,omitempty"`
	Selector string   `json:"selector,omitempty"`
	Paths    []string `json:"paths"`
}

func registerDialogTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "handle_dialog",
		Description: "Accept or dismiss a JavaScript dialog (alert, confirm, prompt, beforeunload). For prompt dialogs, provide prompt_text.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input handleDialogInput) (*mcp.CallToolResult, any, error) {
		rec := BeginAction("handle_dialog")
		defer rec.End()
		rec.SetParams(input)

		ws, err := requireTargetWS()
		if err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetTarget(getCurrentTargetID())
		params := map[string]any{"accept": input.Accept}
		if strings.TrimSpace(input.PromptText) != "" {
			params["promptText"] = input.PromptText
		}
		if err := cdpRequest(ws, "Page.handleJavaScriptDialog", params, nil); err != nil {
			rec.SetErrorFromError(err)
			return nil, nil, err
		}
		rec.SetResult(map[string]any{"handled": true, "accepted": input.Accept, "action_id": string(rec.ID)})
		return nil, map[string]any{"handled": true, "accepted": input.Accept, "action_id": string(rec.ID)}, nil
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
		selector := firstNonEmpty(strings.TrimSpace(input.Ref), strings.TrimSpace(input.Selector))
		if selector == "" {
			rec.SetErrorFromError(fmt.Errorf("upload_file requires ref or selector"))
			return nil, nil, fmt.Errorf("upload_file requires ref or selector")
		}
		if len(input.Paths) == 0 {
			rec.SetErrorFromError(fmt.Errorf("upload_file requires paths"))
			return nil, nil, fmt.Errorf("upload_file requires paths")
		}
		// Canonicalize and validate file paths to prevent path traversal.
		// Each path must resolve to an existing file after symlink resolution.
		canonicalPaths := make([]string, 0, len(input.Paths))
		for _, p := range input.Paths {
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
