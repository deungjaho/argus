package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type pdfInput struct {
	Path            string `json:"path,omitempty"`
	Format          string `json:"format,omitempty"` // A4, Letter, Legal, etc.
	Landscape       bool   `json:"landscape,omitempty"`
	PrintBackground bool   `json:"print_background,omitempty"`
}

func registerMiscTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "pdf",
		Description: "Export the current page as a PDF. Returns the file path. If path is not given, saves to /tmp/argus-*.pdf.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input pdfInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		params := map[string]any{
			"printBackground": input.PrintBackground,
			"landscape":       input.Landscape,
		}
		if format := strings.TrimSpace(input.Format); format != "" {
			params["paperFormat"] = format
		}
		var result struct {
			Data string `json:"data"`
		}
		if err := cdpRequest(ws, "Page.printToPDF", params, &result); err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(result.Data) == "" {
			return nil, nil, fmt.Errorf("browser returned empty PDF")
		}
		path := strings.TrimSpace(input.Path)
		if path == "" {
			path = filepath.Join(os.TempDir(), fmt.Sprintf("argus-pdf-%s.pdf", time.Now().Format("20060102-150405")))
		}
		data, err := base64.StdEncoding.DecodeString(result.Data)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decode PDF: %w", err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"path": path, "bytes": len(data)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "list_downloads",
		Description: "List files downloaded by the browser in this session. Returns filenames, URLs, and download states.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
		ws, err := requireTargetWS()
		if err != nil {
			return nil, nil, err
		}
		// Query download history via CDP
		var result struct {
			Downloads []map[string]any `json:"downloads"`
		}
		if err := cdpRequest(ws, "Browser.getDownloads", map[string]any{}, &result); err != nil {
			// Fallback: check the default download directory
			downloadDir := filepath.Join(homeDir(), "Downloads")
			entries, _ := os.ReadDir(downloadDir)
			files := []map[string]any{}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				info, _ := entry.Info()
				files = append(files, map[string]any{
					"filename": entry.Name(),
					"path":     filepath.Join(downloadDir, entry.Name()),
					"size":     info.Size(),
					"modified": info.ModTime().Format(time.RFC3339),
				})
			}
			return nil, map[string]any{"downloads": files, "count": len(files), "source": "filesystem"}, nil
		}
		return nil, map[string]any{"downloads": result.Downloads, "count": len(result.Downloads), "source": "cdp"}, nil
	})
}
