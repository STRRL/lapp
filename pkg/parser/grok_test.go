package parser

import (
	"testing"
)

func TestGrokParser_Syslog(t *testing.T) {
	p, err := NewGrokParser()
	if err != nil {
		t.Fatalf("NewGrokParser: %v", err)
	}

	line := "Jan  5 14:32:01 myhost sshd[12345]: Accepted password for user from 192.168.1.1 port 22 ssh2"
	result := p.Parse(line)
	if !result.Matched {
		t.Fatal("expected syslog line to match")
	}
	if result.TemplateID != "SYSLOG" {
		t.Errorf("expected template ID 'SYSLOG', got %q", result.TemplateID)
	}
	if result.Params["logsource"] != "myhost" {
		t.Errorf("expected logsource 'myhost', got %q", result.Params["logsource"])
	}
	if result.Params["program"] != "sshd" {
		t.Errorf("expected program 'sshd', got %q", result.Params["program"])
	}
}

func TestGrokParser_ApacheCommon(t *testing.T) {
	p, err := NewGrokParser()
	if err != nil {
		t.Fatalf("NewGrokParser: %v", err)
	}

	line := `127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.0" 200 2326`
	result := p.Parse(line)
	if !result.Matched {
		t.Fatal("expected Apache common log to match")
	}
}

func TestGrokParser_NoMatch(t *testing.T) {
	p, err := NewGrokParser()
	if err != nil {
		t.Fatalf("NewGrokParser: %v", err)
	}

	line := "081109 203615 148 INFO dfs.DataNode: some random log line"
	result := p.Parse(line)
	if result.Matched {
		t.Error("expected non-syslog/non-apache line to not match grok patterns")
	}
}

func TestGrokParser_Templates(t *testing.T) {
	p, err := NewGrokParser()
	if err != nil {
		t.Fatalf("NewGrokParser: %v", err)
	}

	// Before any parsing, no templates should be returned
	templates := p.Templates()
	if len(templates) != 0 {
		t.Errorf("expected 0 templates before parsing, got %d", len(templates))
	}

	// Parse a syslog line
	p.Parse("Jan  5 14:32:01 myhost sshd[12345]: test message")

	templates = p.Templates()
	if len(templates) != 1 {
		t.Errorf("expected 1 template after syslog parse, got %d", len(templates))
	}
	if len(templates) > 0 && templates[0].ID != "SYSLOG" {
		t.Errorf("expected SYSLOG template, got %q", templates[0].ID)
	}
}
