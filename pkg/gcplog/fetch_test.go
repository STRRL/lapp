package gcplog

import (
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/logging"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestBuildFilter(t *testing.T) {
	since := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	until := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		cfg  FetchConfig
		want string
	}{
		{
			name: "since only",
			cfg:  FetchConfig{Since: since},
			want: `timestamp>="2026-07-01T10:00:00Z"`,
		},
		{
			name: "since and until",
			cfg:  FetchConfig{Since: since, Until: until},
			want: `timestamp>="2026-07-01T10:00:00Z" AND timestamp<="2026-07-01T11:00:00Z"`,
		},
		{
			name: "with user filter",
			cfg:  FetchConfig{Since: since, Filter: `severity>=ERROR OR resource.type="k8s_container"`},
			want: `timestamp>="2026-07-01T10:00:00Z" AND (severity>=ERROR OR resource.type="k8s_container")`,
		},
		{
			name: "user filter is trimmed",
			cfg:  FetchConfig{Since: since, Filter: "  severity>=ERROR  "},
			want: `timestamp>="2026-07-01T10:00:00Z" AND (severity>=ERROR)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildFilter(tt.cfg); got != tt.want {
				t.Errorf("BuildFilter() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderEntry(t *testing.T) {
	ts := time.Date(2026, 7, 1, 10, 30, 45, 123000000, time.UTC)

	jsonPayload, err := structpb.NewStruct(map[string]any{
		"message": "request failed",
		"code":    500,
	})
	if err != nil {
		t.Fatal(err)
	}
	noMessagePayload, err := structpb.NewStruct(map[string]any{
		"code": 500,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		entry *logging.Entry
		want  string
	}{
		{
			name:  "text payload",
			entry: &logging.Entry{Timestamp: ts, Severity: logging.Error, Payload: "connection refused"},
			want:  "2026-07-01T10:30:45.123Z ERROR connection refused",
		},
		{
			name:  "json payload with message field",
			entry: &logging.Entry{Timestamp: ts, Severity: logging.Warning, Payload: jsonPayload},
			want:  "2026-07-01T10:30:45.123Z WARNING request failed",
		},
		{
			name:  "json payload without message field",
			entry: &logging.Entry{Timestamp: ts, Severity: logging.Info, Payload: noMessagePayload},
			want:  `2026-07-01T10:30:45.123Z INFO {"code":500}`,
		},
		{
			name:  "nil payload",
			entry: &logging.Entry{Timestamp: ts, Severity: logging.Default},
			want:  "2026-07-01T10:30:45.123Z DEFAULT ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderEntry(tt.entry); got != tt.want {
				t.Errorf("RenderEntry() = %q, want %q", got, tt.want)
			}
		})
	}
}

type fakeIterator struct {
	entries []*logging.Entry
	index   int
}

func (f *fakeIterator) Next() (*logging.Entry, error) {
	if f.index >= len(f.entries) {
		return nil, iterator.Done
	}
	entry := f.entries[f.index]
	f.index++
	return entry, nil
}

func TestDrainEntries(t *testing.T) {
	ts := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	makeEntries := func(n int) []*logging.Entry {
		entries := make([]*logging.Entry, n)
		for i := range entries {
			entries[i] = &logging.Entry{Timestamp: ts, Severity: logging.Info, Payload: "line"}
		}
		return entries
	}

	t.Run("drains all entries under limit", func(t *testing.T) {
		var sb strings.Builder
		count, err := drainEntries(&fakeIterator{entries: makeEntries(3)}, FetchConfig{Limit: 10}, &sb)
		if err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Errorf("count = %d, want 3", count)
		}
		if lines := strings.Count(sb.String(), "\n"); lines != 3 {
			t.Errorf("lines = %d, want 3", lines)
		}
	})

	t.Run("stops at limit", func(t *testing.T) {
		var sb strings.Builder
		count, err := drainEntries(&fakeIterator{entries: makeEntries(10)}, FetchConfig{Limit: 4}, &sb)
		if err != nil {
			t.Fatal(err)
		}
		if count != 4 {
			t.Errorf("count = %d, want 4", count)
		}
	})

	t.Run("reports progress", func(t *testing.T) {
		var sb strings.Builder
		var reported []int
		cfg := FetchConfig{
			Limit: progressInterval * 2,
			OnProgress: func(fetched int) {
				reported = append(reported, fetched)
			},
		}
		count, err := drainEntries(&fakeIterator{entries: makeEntries(progressInterval * 2)}, cfg, &sb)
		if err != nil {
			t.Fatal(err)
		}
		if count != progressInterval*2 {
			t.Errorf("count = %d, want %d", count, progressInterval*2)
		}
		if len(reported) != 2 || reported[0] != progressInterval || reported[1] != progressInterval*2 {
			t.Errorf("reported = %v, want [%d %d]", reported, progressInterval, progressInterval*2)
		}
	})
}

func TestFetchConfigValidate(t *testing.T) {
	since := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		cfg     FetchConfig
		wantErr bool
	}{
		{name: "valid", cfg: FetchConfig{ProjectID: "p", Since: since, Limit: 10}, wantErr: false},
		{name: "missing project", cfg: FetchConfig{Since: since, Limit: 10}, wantErr: true},
		{name: "missing since", cfg: FetchConfig{ProjectID: "p", Limit: 10}, wantErr: true},
		{name: "zero limit", cfg: FetchConfig{ProjectID: "p", Since: since}, wantErr: true},
		{name: "until before since", cfg: FetchConfig{ProjectID: "p", Since: since, Until: since.Add(-time.Hour), Limit: 10}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.validate(); (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
