package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strrl/lapp/pkg/parser"
)

func TestBuildWorkspace(t *testing.T) {
	lines := []string{
		`081109 204655 148 INFO dfs.DataNode$DataXceiver: Receiving block blk_-1608999687919862906 src: /10.251.73.220:42557 dest: /10.251.73.220:50010`,
		`081109 204655 148 INFO dfs.DataNode$DataXceiver: Receiving block blk_-1608999687919862906 src: /10.251.73.220:42558 dest: /10.251.73.220:50010`,
		`081109 204655 149 ERROR dfs.DataNode$DataXceiver: Failed to transfer block blk_123 to /10.251.73.220:50010`,
		`081109 204656 150 WARN dfs.DataNode$DataXceiver: Timeout waiting for block blk_456`,
	}

	dir := t.TempDir()

	drainParser, err := parser.NewDrainParser()
	if err != nil {
		t.Fatalf("NewDrainParser: %v", err)
	}
	chain := parser.NewChainParser(
		drainParser,
	)

	err = BuildWorkspace(dir, lines, chain)
	if err != nil {
		t.Fatalf("BuildWorkspace: %v", err)
	}

	// Check raw.log exists and has all lines
	rawData, err := os.ReadFile(filepath.Join(dir, "raw.log"))
	if err != nil {
		t.Fatalf("read raw.log: %v", err)
	}
	rawContent := string(rawData)
	for _, line := range lines {
		if !strings.Contains(rawContent, line) {
			t.Errorf("raw.log missing line: %s", line)
		}
	}

	// Check summary.txt exists and has content
	summaryData, err := os.ReadFile(filepath.Join(dir, "summary.txt"))
	if err != nil {
		t.Fatalf("read summary.txt: %v", err)
	}
	summary := string(summaryData)
	if !strings.Contains(summary, "Log Template Summary") {
		t.Error("summary.txt missing header")
	}
	if !strings.Contains(summary, "occurrences") {
		t.Error("summary.txt missing occurrence counts")
	}

	// Check errors.txt exists and has error patterns
	errorsData, err := os.ReadFile(filepath.Join(dir, "errors.txt"))
	if err != nil {
		t.Fatalf("read errors.txt: %v", err)
	}
	errorsContent := string(errorsData)
	if !strings.Contains(errorsContent, "Error and Warning") {
		t.Error("errors.txt missing header")
	}
}

func TestBuildWorkspace_NoErrors(t *testing.T) {
	lines := []string{
		`INFO server started`,
		`INFO request handled`,
	}

	dir := t.TempDir()
	drainParser, err := parser.NewDrainParser()
	if err != nil {
		t.Fatalf("NewDrainParser: %v", err)
	}
	chain := parser.NewChainParser(
		drainParser,
	)

	err = BuildWorkspace(dir, lines, chain)
	if err != nil {
		t.Fatalf("BuildWorkspace: %v", err)
	}

	errorsData, err := os.ReadFile(filepath.Join(dir, "errors.txt"))
	if err != nil {
		t.Fatalf("read errors.txt: %v", err)
	}
	if !strings.Contains(string(errorsData), "No error or warning patterns detected") {
		t.Error("expected 'no error' message when no errors present")
	}
}
