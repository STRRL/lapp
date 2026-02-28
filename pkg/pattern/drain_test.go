package pattern

import (
	"testing"

	"github.com/google/uuid"
)

func TestDrainParser_FeedAndTemplates(t *testing.T) {
	p, err := NewDrainParser()
	if err != nil {
		t.Fatalf("NewDrainParser: %v", err)
	}

	lines := []string{
		"081109 203615 148 INFO dfs.DataNode$PacketResponder: PacketResponder 1 for block blk_38865049064139660 terminating",
		"081109 203615 149 INFO dfs.DataNode$PacketResponder: PacketResponder 2 for block blk_-6952295868487656571 terminating",
		"081109 203615 150 INFO dfs.DataNode$PacketResponder: PacketResponder 0 for block blk_752555892853339066 terminating",
		"081109 204005 35 INFO dfs.FSNamesystem: BLOCK* NameSystem.allocateBlock: /mnt/hadoop/mapred/system/job_200811092030_0001/job.jar. blk_-1608999687919862906",
		"081109 204005 36 INFO dfs.FSNamesystem: BLOCK* NameSystem.allocateBlock: /mnt/hadoop/mapred/system/job_200811092030_0002/job.jar. blk_5260569883199042858",
	}

	if err := p.Feed(lines); err != nil {
		t.Fatalf("Feed: %v", err)
	}

	templates, err := p.Templates()
	if err != nil {
		t.Fatalf("Templates: %v", err)
	}

	if len(templates) == 0 {
		t.Fatal("expected at least one template after feeding lines")
	}

	// Similar lines should cluster together, so we expect fewer templates than lines
	if len(templates) >= len(lines) {
		t.Errorf("expected fewer templates than lines, got %d templates for %d lines", len(templates), len(lines))
	}

	for _, tmpl := range templates {
		if tmpl.ID == uuid.Nil {
			t.Error("expected non-nil UUID")
		}
		if tmpl.Count <= 0 {
			t.Errorf("expected positive Count, got %d for template %s", tmpl.Count, tmpl.ID)
		}
	}

	// Verify total count across templates matches input lines
	totalCount := 0
	for _, tmpl := range templates {
		totalCount += tmpl.Count
	}
	if totalCount != len(lines) {
		t.Errorf("expected total count %d, got %d", len(lines), totalCount)
	}
}

func TestDrainParser_EmptyInput(t *testing.T) {
	p, err := NewDrainParser()
	if err != nil {
		t.Fatalf("NewDrainParser: %v", err)
	}

	templates, err := p.Templates()
	if err != nil {
		t.Fatalf("Templates: %v", err)
	}
	if len(templates) != 0 {
		t.Errorf("expected 0 templates before any input, got %d", len(templates))
	}
}

func TestMatchTemplate(t *testing.T) {
	id1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	id2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	templates := []DrainCluster{
		{ID: id1, Pattern: "INFO server started on port <*>"},
		{ID: id2, Pattern: "ERROR connection <*> to <*>"},
	}

	// Should match first template
	matched, ok := MatchTemplate("INFO server started on port 8080", templates)
	if !ok {
		t.Fatal("expected match for server started line")
	}
	if matched.ID != id1 {
		t.Errorf("expected template %s, got %s", id1, matched.ID)
	}

	// Should match second template
	matched, ok = MatchTemplate("ERROR connection lost to db-host", templates)
	if !ok {
		t.Fatal("expected match for error line")
	}
	if matched.ID != id2 {
		t.Errorf("expected template %s, got %s", id2, matched.ID)
	}

	// Should not match
	_, ok = MatchTemplate("DEBUG something else entirely", templates)
	if ok {
		t.Error("expected no match for unrelated line")
	}
}
