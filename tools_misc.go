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
	FilePath        string `json:"file_path,omitempty"`
	File            string `json:"file,omitempty"`
	Filename        string `json:"filename,omitempty"`
	Output          string `json:"output,omitempty"`
	Format          string `json:"format,omitempty"` // A4, Letter, Legal, etc.
	Landscape       bool   `json:"landscape,omitempty"`
	PrintBackground bool   `json:"print_background,omitempty"`
	Target          string `json:"target,omitempty"`
	TargetID        string `json:"target_id,omitempty"`
	TabID           string `json:"tab_id,omitempty"`
}

func registerMiscTools(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        "pdf",
		Description: "Export the current page as a PDF. Returns the file path. If path is not given, saves to /tmp/argus-*.pdf.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input pdfInput) (*mcp.CallToolResult, any, error) {
		if targetID := resolveFirstNonEmpty(input.TargetID, input.TabID, input.Target); targetID != "" {
			setCurrentTargetID(targetID)
		}
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

		pdfDir := getPdfDir()
		if err := os.MkdirAll(pdfDir, 0700); err != nil {
			return nil, nil, fmt.Errorf("failed to create pdf directory %s: %w", pdfDir, err)
		}

		rawPath := resolveInputPath(input.Path, input.FilePath, input.File, input.Filename, input.Output)
		path, err := resolveSafePDFPath(pdfDir, rawPath)
		if err != nil {
			return nil, nil, err
		}

		// Refuse to overwrite existing files
		if _, err := os.Stat(path); err == nil {
			return nil, nil, fmt.Errorf("pdf file already exists: %s (refusing to overwrite)", path)
		}

		data, err := base64.StdEncoding.DecodeString(result.Data)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decode PDF: %w", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"path": path, "bytes": len(data)}, nil
	})

	addTool(server, &mcp.Tool{
		Name:        "list_downloads",
		Description: "List files downloaded by the browser in this session. Returns filenames, URLs, and download states.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ tabScopeInput) (*mcp.CallToolResult, any, error) {
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

// getPdfDir returns the controlled directory for PDF exports.
// Priority: ARGUS_PDF_DIR env > ~/.local/share/argus/pdf/
func getPdfDir() string {
	if dir := os.Getenv("ARGUS_PDF_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "argus-pdf")
	}
	return filepath.Join(home, ".local", "share", "argus", "pdf")
}

// resolveSafePDFPath validates and returns a clean, safe path for writing PDF output.
// It ensures the target is strictly inside pdfDir or os.TempDir().
func resolveSafePDFPath(pdfDir, rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		filename := fmt.Sprintf("argus-pdf-%s.pdf", time.Now().Format("20060102-150405"))
		return filepath.Join(pdfDir, filename), nil
	}
	cleanPath := filepath.Clean(rawPath)
	if filepath.IsAbs(cleanPath) {
		tempDir := filepath.Clean(os.TempDir())
		cleanPdfDir := filepath.Clean(pdfDir)
		if !isWithinDir(cleanPdfDir, cleanPath) && !isWithinDir(tempDir, cleanPath) {
			return "", fmt.Errorf("pdf path %s is outside allowed directories (%s)", cleanPath, pdfDir)
		}
		return cleanPath, nil
	}
	filename := filepath.Base(cleanPath)
	if filename == "" || filename == "." || filename == ".." {
		filename = fmt.Sprintf("argus-pdf-%s.pdf", time.Now().Format("20060102-150405"))
	}
	return filepath.Join(pdfDir, filename), nil
}

// isWithinDir reports whether targetPath is strictly inside dir.
func isWithinDir(dir, targetPath string) bool {
	cleanDir := filepath.Clean(dir)
	cleanTarget := filepath.Clean(targetPath)
	rel, err := filepath.Rel(cleanDir, cleanTarget)
	if err != nil {
		return false
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return false
	}
	return true
}
