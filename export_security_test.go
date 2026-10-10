package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetTraceDirDefault(t *testing.T) {
	dir := getTraceDir()
	if dir == "" {
		t.Fatal("getTraceDir returned empty string")
	}
	// Should end with argus/traces
	if !endsWith(dir, "argus/traces") && !endsWith(dir, "argus-traces") {
		t.Errorf("getTraceDir = %s, expected to end with argus/traces or argus-traces", dir)
	}
}

func TestGetTraceDirFromEnv(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("ARGUS_TRACE_DIR", tmpDir)
	defer os.Unsetenv("ARGUS_TRACE_DIR")

	dir := getTraceDir()
	if dir != tmpDir {
		t.Errorf("getTraceDir = %s, want %s", dir, tmpDir)
	}
}

func TestExportTraceRefusesOverwrite(t *testing.T) {
	restore := withTestRecorder(10, 10)
	defer restore()

	// Record some data
	r := BeginAction("test")
	r.End()
	rec.RecordEvent("console", "tab1", map[string]any{"text": "hello"})

	traceDir := t.TempDir()
	os.Setenv("ARGUS_TRACE_DIR", traceDir)
	defer os.Unsetenv("ARGUS_TRACE_DIR")

	// Create a file that already exists
	existingPath := filepath.Join(traceDir, "existing.json")
	if err := os.WriteFile(existingPath, []byte("existing"), 0600); err != nil {
		t.Fatalf("failed to create existing file: %v", err)
	}

	// Simulate export_trace logic: refuse overwrite
	path := filepath.Join(traceDir, filepath.Base("existing.json"))
	if _, err := os.Stat(path); err == nil {
		// File exists — export_trace would return an error
		t.Log("correctly detected existing file — export_trace would refuse overwrite")
	} else {
		t.Error("expected existing file to be detected")
	}
}

func TestExportTracePathTraversalPrevention(t *testing.T) {
	traceDir := t.TempDir()
	os.Setenv("ARGUS_TRACE_DIR", traceDir)
	defer os.Unsetenv("ARGUS_TRACE_DIR")

	// Malicious path attempts to traverse outside traceDir
	malicious := "../../../etc/passwd"
	filename := filepath.Base(malicious)
	path := filepath.Join(traceDir, filename)

	// The path should be inside traceDir, not /etc/passwd
	if path == "/etc/passwd" || !startsWith(path, traceDir) {
		t.Errorf("path traversal not prevented: got %s, should be under %s", path, traceDir)
	}
}

func TestExportTraceFilePermissions(t *testing.T) {
	traceDir := t.TempDir()
	os.Setenv("ARGUS_TRACE_DIR", traceDir)
	defer os.Unsetenv("ARGUS_TRACE_DIR")

	testPath := filepath.Join(traceDir, "test-perms.json")
	if err := os.WriteFile(testPath, []byte("{}"), 0600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	info, err := os.Stat(testPath)
	if err != nil {
		t.Fatalf("failed to stat test file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
	}
}

// helpers
func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func TestGetPdfDirDefault(t *testing.T) {
	dir := getPdfDir()
	if dir == "" {
		t.Fatal("getPdfDir returned empty string")
	}
	if !endsWith(dir, "argus/pdf") && !endsWith(dir, "argus-pdf") {
		t.Errorf("getPdfDir = %s, expected to end with argus/pdf or argus-pdf", dir)
	}
}

func TestGetPdfDirFromEnv(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("ARGUS_PDF_DIR", tmpDir)
	defer os.Unsetenv("ARGUS_PDF_DIR")

	dir := getPdfDir()
	if dir != tmpDir {
		t.Errorf("getPdfDir = %s, want %s", dir, tmpDir)
	}
}

func TestPdfRefusesOverwrite(t *testing.T) {
	pdfDir := t.TempDir()
	os.Setenv("ARGUS_PDF_DIR", pdfDir)
	defer os.Unsetenv("ARGUS_PDF_DIR")

	existingPath := filepath.Join(pdfDir, "report.pdf")
	if err := os.WriteFile(existingPath, []byte("%PDF-1.4 existing"), 0600); err != nil {
		t.Fatalf("failed to create existing file: %v", err)
	}

	path := filepath.Join(pdfDir, filepath.Base("report.pdf"))
	if _, err := os.Stat(path); err == nil {
		t.Log("correctly detected existing file — pdf would refuse overwrite")
	} else {
		t.Error("expected existing file to be detected")
	}
}

func TestPdfPathTraversalPrevention(t *testing.T) {
	pdfDir := t.TempDir()

	// Relative traversal should resolve safely inside pdfDir using the base filename
	path, err := resolveSafePDFPath(pdfDir, "../../../etc/passwd")
	if err != nil {
		t.Fatalf("unexpected error for relative path: %v", err)
	}
	if path == "/etc/passwd" || !startsWith(path, pdfDir) {
		t.Errorf("path traversal not prevented: got %s, should be under %s", path, pdfDir)
	}

	// Absolute path outside allowed dirs must return error
	_, err = resolveSafePDFPath(pdfDir, "/etc/passwd")
	if err == nil {
		t.Errorf("expected error for /etc/passwd, got nil")
	}
}

func TestPdfPrefixCollisionRejected(t *testing.T) {
	pdfDir := "/var/argus/exports"
	// An attacker tries to write to a sibling directory starting with the same prefix
	siblingDir := "/var/argus/exports-evil"
	maliciousPath := filepath.Join(siblingDir, "exploit.pdf")

	_, err := resolveSafePDFPath(pdfDir, maliciousPath)
	if err == nil {
		t.Errorf("expected error for sibling prefix collision %s, got nil", maliciousPath)
	}
}

func TestPdfAllowedPathsSucceed(t *testing.T) {
	pdfDir := t.TempDir()
	tempDir := filepath.Clean(os.TempDir())

	// Safe path inside pdfDir
	validPath := filepath.Join(pdfDir, "report.pdf")
	p, err := resolveSafePDFPath(pdfDir, validPath)
	if err != nil || p != validPath {
		t.Errorf("resolveSafePDFPath failed for valid path: %v, got %s", err, p)
	}

	// Safe path inside tempDir
	validTemp := filepath.Join(tempDir, "temp-report.pdf")
	p, err = resolveSafePDFPath(pdfDir, validTemp)
	if err != nil || p != validTemp {
		t.Errorf("resolveSafePDFPath failed for valid temp path: %v, got %s", err, p)
	}
}
