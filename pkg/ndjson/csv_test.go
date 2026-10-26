package ndjson

import (
	"strings"
	"testing"
)

func TestReadCSVKeepsQuotedNewlinesInOneRecord(t *testing.T) {
	content := "\ufeffDate,Line,level\r\n" +
		"2026-10-26T05:41:50Z,\"duration: 1.0 ms  plan:\r\nQuery Text: SELECT 1\",info\r\n" +
		"2026-10-26T05:41:51Z,say \"hi\",info\r\n"
	header, records, err := ReadCSV(strings.NewReader(content))
	if err != nil {
		t.Fatalf("ReadCSV: %v", err)
	}
	if strings.Join(header, "|") != "Date|Line|level" {
		t.Fatalf("header = %q", header)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %q", len(records), records)
	}
	if got, want := records[0][1], "duration: 1.0 ms  plan:\nQuery Text: SELECT 1"; got != want {
		t.Fatalf("multi-line body = %q, want %q", got, want)
	}
	if got, want := records[1][1], `say "hi"`; got != want {
		t.Fatalf("lazy-quoted body = %q, want %q", got, want)
	}
}
