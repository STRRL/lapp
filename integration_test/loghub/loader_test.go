package loghub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDataset(t *testing.T) {
	loghubPath := os.Getenv("LOGHUB_PATH")
	if loghubPath == "" {
		t.Skip("LOGHUB_PATH not set")
	}

	csvPath := filepath.Join(loghubPath, "HDFS", "HDFS_2k.log_structured_corrected.csv")

	entries, err := LoadDataset(csvPath)
	if err != nil {
		t.Fatalf("LoadDataset returned error: %v", err)
	}

	if len(entries) < 2000 {
		t.Fatalf("expected at least 2000 entries, got %d", len(entries))
	}

	first := entries[0]
	if first.Content == "" {
		t.Error("first entry has empty Content")
	}
	if first.EventTemplate == "" {
		t.Error("first entry has empty EventTemplate")
	}
}

func TestLoadDatasetMissingFile(t *testing.T) {
	_, err := LoadDataset("/nonexistent/path.csv")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestLoadDatasetInlineCSV(t *testing.T) {
	tmpDir := t.TempDir()
	csvFile := filepath.Join(tmpDir, "test.csv")

	content := `LineId,Content,EventId,EventTemplate
1,"Starting NameNode, args = [-format]",E1,Starting NameNode args = <*>
2,"Shutting down NameNode at host/10.0.0.1",E2,Shutting down NameNode at <*>
`

	err := os.WriteFile(csvFile, []byte(content), 0o644)
	if err != nil {
		t.Fatalf("failed to write test csv: %v", err)
	}

	entries, err := LoadDataset(csvFile)
	if err != nil {
		t.Fatalf("LoadDataset returned error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Content != "Starting NameNode, args = [-format]" {
		t.Errorf("unexpected Content: %q", entries[0].Content)
	}
	if entries[0].EventID != "E1" {
		t.Errorf("unexpected EventID: %q", entries[0].EventID)
	}
	if entries[1].EventTemplate != "Shutting down NameNode at <*>" {
		t.Errorf("unexpected EventTemplate: %q", entries[1].EventTemplate)
	}
}
