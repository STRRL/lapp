package workspace

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strrl/lapp/pkg/ndjson"
)

// TestTagFileLinesSplitsEntries checks entry boundaries for plain text
// fixtures under testdata/split/. Each file is normalized as it would be on
// landing in logs/, then split: timestamped lines begin entries and the
// lines without one join the entry before them, unless too few lines carry
// a timestamp, in which case every line is an entry.
func TestTagFileLinesSplitsEntries(t *testing.T) {
	tests := []struct {
		fixture        string
		entries        int
		wantSplitMode  SplitMode
		wantTimestamps bool
	}{
		// Grafana text export: summary lines dropped, each plan one entry.
		{fixture: "grafana-text-export.log", entries: 4, wantSplitMode: SplitModeMerged, wantTimestamps: true},
		// Postgres auto_explain plans with indented continuation lines.
		{fixture: "postgres-plans.log", entries: 3, wantSplitMode: SplitModeMerged, wantTimestamps: true},
		{fixture: "syslog.log", entries: 4, wantSplitMode: SplitModeMerged, wantTimestamps: true},
		{fixture: "untimestamped.log", entries: 6, wantSplitMode: SplitModePerLine},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			lines := readNormalizedFixture(t, filepath.Join("testdata", "split", tt.fixture))
			tagged, split, err := tagFileLines(context.Background(), tt.fixture, LogFileLines{Lines: lines})
			if err != nil {
				t.Fatalf("tagFileLines: %v", err)
			}
			if len(tagged) != tt.entries {
				var got []string
				for _, tl := range tagged {
					got = append(got, tl.Content)
				}
				t.Fatalf("entries = %d, want %d:\n%s", len(tagged), tt.entries, strings.Join(got, "\n---\n"))
			}
			if split.mode != tt.wantSplitMode || split.cappedEntries != 0 {
				t.Fatalf("split = %+v, want mode %q and no capped entries", split, tt.wantSplitMode)
			}
			if !tt.wantTimestamps {
				return
			}
			for _, tl := range tagged {
				if tl.Timestamp == nil {
					t.Fatalf("entry without timestamp: %q", tl.Content)
				}
			}
		})
	}
}

// TestTagFileLinesCapsMergedEntries checks that a merged entry stops at
// maxEntryLines: the overflow starts a new entry, cutting again at the cap
// while no timestamped line comes, and each cut entry is counted.
func TestTagFileLinesCapsMergedEntries(t *testing.T) {
	lines := []string{"2026-10-26 05:41:40.000 duration: 1.0 ms  plan:"}
	for i := 0; i < 2*maxEntryLines+9; i++ {
		lines = append(lines, "    ->  Seq Scan on orders")
	}
	// Enough timestamped lines that the file is merged, not split per line.
	for i := 0; i < 60; i++ {
		lines = append(lines, "2026-10-26 05:41:41.000 LOG:  checkpoint complete")
	}

	tagged, split, err := tagFileLines(context.Background(), "plans.log", LogFileLines{Lines: lines})
	if err != nil {
		t.Fatalf("tagFileLines: %v", err)
	}
	// 1 + 1009 plan lines: 500 + 500 + 10, then the 60 checkpoints.
	if len(tagged) != 63 || split.cappedEntries != 2 || split.physicalLines != len(lines) {
		t.Fatalf("entries = %d, split = %+v; want 63 entries, 2 capped, %d lines", len(tagged), split, len(lines))
	}
	for i, want := range []int{maxEntryLines, maxEntryLines, 10, 1} {
		if got := strings.Count(tagged[i].Content, "\n") + 1; got != want {
			t.Fatalf("entry %d has %d lines, want %d", i, got, want)
		}
	}
}

func readNormalizedFixture(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()
	normalized, err := ndjson.Normalize(file)
	if err != nil {
		t.Fatalf("normalize %s: %v", path, err)
	}
	data, err := io.ReadAll(normalized)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}
