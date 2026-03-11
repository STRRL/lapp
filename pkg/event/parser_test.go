package event

import (
	"testing"
	"time"
)

func TestParseLine_JSON(t *testing.T) {
	line := `{"ts":"2026-03-10T21:00:00Z","level":"ERROR","service":"payments-api","env":"prod","request_id":"req_123","message":"checkout failed"}`

	parsed := ParseLine(line)

	if parsed.SourceFormat != SourceFormatJSON {
		t.Fatalf("expected %s, got %s", SourceFormatJSON, parsed.SourceFormat)
	}
	assertTimestamp(t, parsed.Event.Timestamp, "2026-03-10T21:00:00Z")
	assertAttr(t, parsed.Event.Attrs, "level", "error")
	assertAttr(t, parsed.Event.Attrs, "service", "payments-api")
	assertAttr(t, parsed.Event.Attrs, "env", "prod")
	assertAttr(t, parsed.Event.Attrs, "request_id", "req_123")
	assertAttr(t, parsed.Event.Attrs, "message", "checkout failed")
	if parsed.Event.Text != line {
		t.Fatalf("expected raw line to be preserved, got %q", parsed.Event.Text)
	}
}

func TestParseLine_Logfmt(t *testing.T) {
	line := `ts=2026-03-10T21:01:12Z level=INFO service=auth-api env=staging request_id=req_456 msg="user user_123 authenticated"`

	parsed := ParseLine(line)

	if parsed.SourceFormat != SourceFormatLogfmt {
		t.Fatalf("expected %s, got %s", SourceFormatLogfmt, parsed.SourceFormat)
	}
	assertTimestamp(t, parsed.Event.Timestamp, "2026-03-10T21:01:12Z")
	assertAttr(t, parsed.Event.Attrs, "level", "info")
	assertAttr(t, parsed.Event.Attrs, "service", "auth-api")
	assertAttr(t, parsed.Event.Attrs, "env", "staging")
	assertAttr(t, parsed.Event.Attrs, "request_id", "req_456")
	assertAttr(t, parsed.Event.Attrs, "msg", "user user_123 authenticated")
}

func TestParseLine_KeyValue(t *testing.T) {
	line := "timestamp=2026-03-10T21:02:45Z severity=WARN service_name=billing-worker environment=prod correlation_id=corr_123 action=retrying"

	parsed := ParseLine(line)

	if parsed.SourceFormat != SourceFormatKeyValue {
		t.Fatalf("expected %s, got %s", SourceFormatKeyValue, parsed.SourceFormat)
	}
	assertTimestamp(t, parsed.Event.Timestamp, "2026-03-10T21:02:45Z")
	assertAttr(t, parsed.Event.Attrs, "level", "warn")
	assertAttr(t, parsed.Event.Attrs, "service", "billing-worker")
	assertAttr(t, parsed.Event.Attrs, "env", "prod")
	assertAttr(t, parsed.Event.Attrs, "correlation_id", "corr_123")
	assertAttr(t, parsed.Event.Attrs, "action", "retrying")
}

func TestParseLine_PrefixFallback(t *testing.T) {
	line := "2026-03-10T21:03:45Z ERROR worker pool stalled after 3 retries"

	parsed := ParseLine(line)

	if parsed.SourceFormat != SourceFormatPlainText {
		t.Fatalf("expected %s, got %s", SourceFormatPlainText, parsed.SourceFormat)
	}
	assertTimestamp(t, parsed.Event.Timestamp, "2026-03-10T21:03:45Z")
	assertAttr(t, parsed.Event.Attrs, "level", "error")
	if parsed.Event.Text != line {
		t.Fatalf("expected raw line to be preserved, got %q", parsed.Event.Text)
	}
}

func TestParseLine_PlainTextFallback(t *testing.T) {
	line := "worker pool stalled after 3 retries while draining queue payments"

	parsed := ParseLine(line)

	if parsed.SourceFormat != SourceFormatPlainText {
		t.Fatalf("expected %s, got %s", SourceFormatPlainText, parsed.SourceFormat)
	}
	if parsed.Event.Timestamp != nil {
		t.Fatalf("expected no timestamp, got %s", parsed.Event.Timestamp.Format(time.RFC3339))
	}
	if len(parsed.Event.Attrs) != 0 {
		t.Fatalf("expected no attrs, got %#v", parsed.Event.Attrs)
	}
	if parsed.Event.Text != line {
		t.Fatalf("expected raw line to be preserved, got %q", parsed.Event.Text)
	}
}

func TestParseLine_OrderedParsers(t *testing.T) {
	t.Run("json wins before key value-like content", func(t *testing.T) {
		line := `{"level":"INFO","message":"status=ok"}`

		parsed := ParseLine(line)

		if parsed.SourceFormat != SourceFormatJSON {
			t.Fatalf("expected %s, got %s", SourceFormatJSON, parsed.SourceFormat)
		}
		assertAttr(t, parsed.Event.Attrs, "level", "info")
		assertAttr(t, parsed.Event.Attrs, "message", "status=ok")
	})

	t.Run("logfmt wins before key value when quotes are present", func(t *testing.T) {
		line := `level=INFO msg="status ok"`

		parsed := ParseLine(line)

		if parsed.SourceFormat != SourceFormatLogfmt {
			t.Fatalf("expected %s, got %s", SourceFormatLogfmt, parsed.SourceFormat)
		}
		assertAttr(t, parsed.Event.Attrs, "level", "info")
		assertAttr(t, parsed.Event.Attrs, "msg", "status ok")
	})

	t.Run("invalid quoted logfmt falls back to plain text", func(t *testing.T) {
		line := `level=INFO msg="status ok`

		parsed := ParseLine(line)

		if parsed.SourceFormat != SourceFormatPlainText {
			t.Fatalf("expected %s, got %s", SourceFormatPlainText, parsed.SourceFormat)
		}
		if len(parsed.Event.Attrs) != 0 {
			t.Fatalf("expected no attrs, got %#v", parsed.Event.Attrs)
		}
	})
}

func assertAttr(t *testing.T, attrs map[string]string, key, want string) {
	t.Helper()

	got, ok := attrs[key]
	if !ok {
		t.Fatalf("missing attr %q", key)
	}
	if got != want {
		t.Fatalf("expected attr %q=%q, got %q", key, want, got)
	}
}

func assertTimestamp(t *testing.T, ts *time.Time, want string) {
	t.Helper()

	if ts == nil {
		t.Fatal("expected timestamp, got nil")
	}
	if ts.UTC().Format(time.RFC3339) != want {
		t.Fatalf("expected timestamp %s, got %s", want, ts.UTC().Format(time.RFC3339))
	}
}
