package logheader

import (
	"testing"
	"time"
)

func TestStrip(t *testing.T) {
	Now = func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }
	defer func() { Now = time.Now }()

	cases := []struct {
		line, body string
		ts         time.Time
	}{
		{"[Sun Dec 04 04:47:44 2005] [notice] workerEnv.init() ok", "[notice] workerEnv.init() ok", time.Date(2005, 12, 4, 4, 47, 44, 0, time.UTC)},
		{"Dec 10 06:55:46 LabSZ sshd[24200]: Invalid user webmaster from 1.2.3.4", "sshd: Invalid user webmaster from 1.2.3.4", time.Date(2025, 12, 10, 6, 55, 46, 0, time.UTC)},
		{"Jun 14 15:16:01 combo sshd(pam_unix)[19939]: check pass; user unknown", "sshd(pam_unix): check pass; user unknown", time.Date(2026, 6, 14, 15, 16, 1, 0, time.UTC)},
		{"081109 203615 148 INFO dfs.DataNode: Receiving block", "INFO dfs.DataNode: Receiving block", time.Date(2008, 11, 9, 20, 36, 15, 0, time.UTC)},
		{"2024-03-28T13:45:30.123Z INFO started", "INFO started", time.Date(2024, 3, 28, 13, 45, 30, 123e6, time.UTC)},
		{"2026-10-26T05:41:47.158Z,api,request done", "api,request done", time.Date(2026, 10, 26, 5, 41, 47, 158e6, time.UTC)},
		{"2026-10-26T05:41:47Z;api;request done", "api;request done", time.Date(2026, 10, 26, 5, 41, 47, 0, time.UTC)},
		{"2026-10-26T05:41:47Z\tapi\trequest done", "api\trequest done", time.Date(2026, 10, 26, 5, 41, 47, 0, time.UTC)},
		{"1792993307158\t2026-10-26T05:41:47.158Z\tcolumn \"x\" does not exist", "column \"x\" does not exist", time.Date(2026, 10, 26, 5, 41, 47, 158e6, time.UTC)},
		{"2026-10-26 05:41:47.158+00,postgres,LOG", "postgres,LOG", time.Date(2026, 10, 26, 5, 41, 47, 158e6, time.UTC)},
		{"\"2026-10-26 05:41:47.158+02\",postgres,LOG", "postgres,LOG", time.Date(2026, 10, 26, 3, 41, 47, 158e6, time.UTC)},
		{"no header here", "no header here", time.Time{}},
	}
	for _, c := range cases {
		got := Strip(c.line)
		if got.Body != c.body || !got.Timestamp.Equal(c.ts) {
			t.Errorf("Strip(%q) = %q @ %v, want %q @ %v", c.line, got.Body, got.Timestamp, c.body, c.ts)
		}
	}
}

func TestMaskInline(t *testing.T) {
	got := MaskInline("connection from 1.2.3.4 () at Sun Jul  3 23:16:09 2005")
	if want := "connection from 1.2.3.4 () at <*>"; got != want {
		t.Errorf("MaskInline = %q, want %q", got, want)
	}
}

func TestStartShape(t *testing.T) {
	cases := []struct{ line, shape string }{
		{"1792993307158\t2026-10-26T05:41:47.158Z\tcolumn \"x\" does not exist", "9\t9-9-9a9:9:9"},
		{"1792993300000\t2026-10-26T05:41:40.000Z\tcheckpoint complete", "9\t9-9-9a9:9:9"},
		{"2026-10-26 05:20:11.123+00 LOG:  checkpoint starting", "9-9-9 9:9:9"},
		{"Oct 26 05:41:47 web01 sshd[2101]: Accepted publickey", "a 9 9:9:9"},
		{"\tFilter: (created_at > '2026-10-26 05:20:11.123+00'::timestamp with time zone)", ""},
		{"Filter: (created_at > '2026-10-26 05:20:11.123+00'::timestamp with time zone)", ""},
	}
	for _, c := range cases {
		if got := StartShape(c.line); got != c.shape {
			t.Errorf("StartShape(%q) = %q, want %q", c.line, got, c.shape)
		}
	}
}

// TestStartShapeIgnoresPrecisionAndZone checks that lines of one RFC 3339
// format share a shape whether or not they carry fractional seconds or a
// zone suffix.
func TestStartShapeIgnoresPrecisionAndZone(t *testing.T) {
	lines := []string{
		"2026-10-26T05:41:47.158Z INFO a",
		"2026-10-26T05:41:48.1Z INFO b",
		"2026-10-26T05:41:49.123456789+00:00 INFO c",
		"2026-10-26T05:41:50Z INFO d",
		"2026-10-26T05:41:51 INFO e",
	}
	want := StartShape(lines[0])
	for _, line := range lines[1:] {
		if got := StartShape(line); got != want {
			t.Errorf("StartShape(%q) = %q, want %q", line, got, want)
		}
	}
}
