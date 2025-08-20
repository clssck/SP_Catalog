package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	_ "modernc.org/sqlite"
)

func TestParseExtSet(t *testing.T) {
	tests := []struct {
		input    string
		expected map[string]struct{}
		name     string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: map[string]struct{}{},
		},
		{
			name:  "single extension with dot",
			input: ".pdf",
			expected: map[string]struct{}{
				".pdf": {},
			},
		},
		{
			name:  "single extension without dot",
			input: "pdf",
			expected: map[string]struct{}{
				".pdf": {},
			},
		},
		{
			name:  "multiple extensions",
			input: ".pdf,.docx,txt",
			expected: map[string]struct{}{
				".pdf":  {},
				".docx": {},
				".txt":  {},
			},
		},
		{
			name:  "extensions with spaces",
			input: " .pdf , .docx , txt ",
			expected: map[string]struct{}{
				".pdf":  {},
				".docx": {},
				".txt":  {},
			},
		},
		{
			name:  "empty extensions ignored",
			input: ".pdf,,,.docx",
			expected: map[string]struct{}{
				".pdf":  {},
				".docx": {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseExtSet(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("parseExtSet(%q) = %v, want %v", tt.input, result, tt.expected)
				return
			}
			for k := range tt.expected {
				if _, exists := result[k]; !exists {
					t.Errorf("parseExtSet(%q) missing expected extension %q", tt.input, k)
				}
			}
		})
	}
}

func TestDetectMIME(t *testing.T) {
	tests := []struct {
		ext      string
		expected string
		name     string
	}{
		{
			name:     "Outlook message file",
			ext:      ".msg",
			expected: "application/vnd.ms-outlook",
		},
		{
			name:     "PDF file",
			ext:      ".pdf",
			expected: "application/pdf",
		},
		{
			name:     "Text file",
			ext:      ".txt",
			expected: "text/plain; charset=utf-8",
		},
		{
			name:     "Unknown extension",
			ext:      ".unknown",
			expected: "application/octet-stream",
		},
		{
			name:     "Empty extension",
			ext:      "",
			expected: "application/octet-stream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectMIME(tt.ext)
			if result != tt.expected {
				t.Errorf("detectMIME(%q) = %q, want %q", tt.ext, result, tt.expected)
			}
		})
	}
}

func TestHashFile(t *testing.T) {
	// Create a temporary file with known content
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := "Hello, World!"

	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Hash the file
	hash := hashFile(testFile)

	// Expected SHA256 hash of "Hello, World!"
	expected := "dffd6021bb2bd5b0af676290809ec3a53191dd81c7f70a4b28688a362182986f"

	if hash != expected {
		t.Errorf("hashFile(%q) = %q, want %q", testFile, hash, expected)
	}

	// Test non-existent file
	nonExistentFile := filepath.Join(tmpDir, "nonexistent.txt")
	emptyHash := hashFile(nonExistentFile)
	if emptyHash != "" {
		t.Errorf("hashFile(%q) = %q, want empty string", nonExistentFile, emptyHash)
	}
}

func TestInitSchema(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Test schema initialization
	if err := initSchema(db); err != nil {
		t.Fatalf("initSchema() failed: %v", err)
	}

	// Verify tables exist
	tables := []string{"folders", "files"}
	for _, table := range tables {
		var count int
		query := "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?"
		if err := db.QueryRow(query, table).Scan(&count); err != nil {
			t.Fatalf("Failed to check for table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("Table %s not found", table)
		}
	}
}

func TestScanAndPersistBasic(t *testing.T) {
	// Create a temporary directory structure
	tmpDir := t.TempDir()

	// Create test directories and files
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	testFiles := []struct {
		path    string
		content string
	}{
		{filepath.Join(tmpDir, "file1.txt"), "content1"},
		{filepath.Join(tmpDir, "file2.pdf"), "content2"},
		{filepath.Join(subDir, "file3.docx"), "content3"},
	}

	for _, tf := range testFiles {
		if err := os.WriteFile(tf.path, []byte(tf.content), 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", tf.path, err)
		}
	}

	// Create database
	dbPath := filepath.Join(tmpDir, "catalog.db")

	// Progress callback for testing
	progressCalls := 0
	progressCallback := func(files, folders int64, last string) tea.Msg {
		progressCalls++
		return progressMsg{files: files, folders: folders, last: last}
	}

	// Test scanning without extension filter
	extFilter := map[string]struct{}{}
	err := scanAndPersist(tmpDir, dbPath, extFilter, false, progressCallback)
	if err != nil {
		t.Fatalf("scanAndPersist() failed: %v", err)
	}

	// Verify database contents
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Check folders
	var folderCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM folders").Scan(&folderCount); err != nil {
		t.Fatalf("Failed to count folders: %v", err)
	}
	// Should have root dir and subdir
	if folderCount < 2 {
		t.Errorf("Expected at least 2 folders, got %d", folderCount)
	}

	// Check files
	var fileCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM files").Scan(&fileCount); err != nil {
		t.Fatalf("Failed to count files: %v", err)
	}
	if fileCount < 3 {
		t.Errorf("Expected at least 3 files, got %d", fileCount)
	}
}

func TestScanAndPersistWithExtFilter(t *testing.T) {
	// Create a temporary directory structure
	tmpDir := t.TempDir()

	testFiles := []struct {
		path    string
		content string
	}{
		{filepath.Join(tmpDir, "file1.txt"), "content1"},
		{filepath.Join(tmpDir, "file2.pdf"), "content2"},
		{filepath.Join(tmpDir, "file3.docx"), "content3"},
	}

	for _, tf := range testFiles {
		if err := os.WriteFile(tf.path, []byte(tf.content), 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", tf.path, err)
		}
	}

	// Create database
	dbPath := filepath.Join(tmpDir, "catalog.db")

	// Progress callback for testing
	progressCallback := func(files, folders int64, last string) tea.Msg {
		return progressMsg{files: files, folders: folders, last: last}
	}

	// Test scanning with extension filter (only .pdf files)
	extFilter := map[string]struct{}{".pdf": {}}
	err := scanAndPersist(tmpDir, dbPath, extFilter, false, progressCallback)
	if err != nil {
		t.Fatalf("scanAndPersist() failed: %v", err)
	}

	// Verify database contents
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Check files - should only have 1 PDF file
	var fileCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM files").Scan(&fileCount); err != nil {
		t.Fatalf("Failed to count files: %v", err)
	}
	if fileCount != 1 {
		t.Errorf("Expected 1 file (PDF only), got %d", fileCount)
	}

	// Verify it's the PDF file
	var ext string
	if err := db.QueryRow("SELECT ext FROM files LIMIT 1").Scan(&ext); err != nil {
		t.Fatalf("Failed to get file extension: %v", err)
	}
	if ext != ".pdf" {
		t.Errorf("Expected .pdf extension, got %s", ext)
	}
}

func TestScanAndPersistWithHashing(t *testing.T) {
	// Create a temporary directory structure
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := "Hello, World!"
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create database
	dbPath := filepath.Join(tmpDir, "catalog.db")

	// Progress callback for testing
	progressCallback := func(files, folders int64, last string) tea.Msg {
		return progressMsg{files: files, folders: folders, last: last}
	}

	// Test scanning with hashing enabled
	extFilter := map[string]struct{}{}
	err := scanAndPersist(tmpDir, dbPath, extFilter, true, progressCallback)
	if err != nil {
		t.Fatalf("scanAndPersist() failed: %v", err)
	}

	// Verify database contents
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Check that hash was computed
	var hash sql.NullString
	if err := db.QueryRow("SELECT sha256 FROM files LIMIT 1").Scan(&hash); err != nil {
		t.Fatalf("Failed to get file hash: %v", err)
	}
	if !hash.Valid || hash.String == "" {
		t.Error("Expected hash to be computed and stored")
	}

	// Verify hash value matches expected length and format (SHA256 is 64 hex chars)
	if len(hash.String) != 64 {
		t.Errorf("Expected hash to be 64 characters long, got %d: %s", len(hash.String), hash.String)
	}
	// Just verify it's a valid hex string
	for _, c := range hash.String {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("Hash contains non-hex character: %c", c)
			break
		}
	}
}

// Benchmark tests
func BenchmarkParseExtSet(b *testing.B) {
	input := ".pdf,.docx,.txt,.xlsx,.pptx,.jpg,.png,.gif,.mp4,.avi"
	for i := 0; i < b.N; i++ {
		parseExtSet(input)
	}
}

func BenchmarkHashFile(b *testing.B) {
	// Create a test file
	tmpDir := b.TempDir()
	testFile := filepath.Join(tmpDir, "benchmark.txt")
	content := make([]byte, 1024*1024) // 1MB of data
	for i := range content {
		content[i] = byte(i % 256)
	}

	if err := os.WriteFile(testFile, content, 0644); err != nil {
		b.Fatalf("Failed to create test file: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hashFile(testFile)
	}
}
