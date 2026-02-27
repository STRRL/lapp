package parser

import (
	"testing"
)

func TestDrainParser_Parse(t *testing.T) {
	p := NewDrainParser()

	lines := []string{
		"081109 203615 148 INFO dfs.DataNode$PacketResponder: PacketResponder 1 for block blk_38865049064139660 terminating",
		"081109 203615 149 INFO dfs.DataNode$PacketResponder: PacketResponder 2 for block blk_-6952295868487656571 terminating",
		"081109 203615 150 INFO dfs.DataNode$PacketResponder: PacketResponder 0 for block blk_752555892853339066 terminating",
		"081109 204005 35 INFO dfs.FSNamesystem: BLOCK* NameSystem.allocateBlock: /mnt/hadoop/mapred/system/job_200811092030_0001/job.jar. blk_-1608999687919862906",
		"081109 204005 36 INFO dfs.FSNamesystem: BLOCK* NameSystem.allocateBlock: /mnt/hadoop/mapred/system/job_200811092030_0002/job.jar. blk_5260569883199042858",
	}

	for _, line := range lines {
		result := p.Parse(line)
		if !result.Matched {
			t.Errorf("expected line to match, got unmatched: %s", line)
		}
		if result.PatternID == "" {
			t.Error("expected non-empty PatternID")
		}
		if result.Pattern == "" {
			t.Error("expected non-empty Pattern")
		}
	}

	templates := p.Templates()
	if len(templates) == 0 {
		t.Fatal("expected at least one template after feeding lines")
	}

	// Similar lines should cluster together, so we expect fewer templates than lines
	if len(templates) >= len(lines) {
		t.Errorf("expected fewer templates than lines, got %d templates for %d lines", len(templates), len(lines))
	}
}

func TestDrainParser_EmptyInput(t *testing.T) {
	p := NewDrainParser()

	templates := p.Templates()
	if len(templates) != 0 {
		t.Errorf("expected 0 templates before any input, got %d", len(templates))
	}
}
