package victorialogs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var (
	testFrom = time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	testTo   = time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)
)

func TestFetchStreamsLinesAndMarksTruncation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prefix/select/logsql/query" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("query"); got != "error" {
			t.Errorf("query = %q", got)
		}
		if r.Form.Get("start") != "2026-07-25T00:00:00Z" || r.Form.Get("end") != "2026-07-25T01:00:00Z" || r.Form.Get("limit") != "3" {
			t.Errorf("form = %v", r.Form)
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("AccountID") != "12" || r.Header.Get("ProjectID") != "34" {
			t.Errorf("headers = %v", r.Header)
		}
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, `{"_msg":"line %d","_time":"2026-07-25T00:00:0%dZ"}`+"\n", i, i)
		}
	}))
	defer server.Close()

	var lines []string
	client := &Client{Auth: Auth{BearerToken: "token"}}
	result, err := client.Fetch(context.Background(), FetchRequest{
		URL: server.URL + "/prefix/", Tenant: "12:34", Query: "error", From: testFrom, To: testTo, Limit: 2,
	}, func(line string) error {
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !result.Truncated || len(lines) != 2 || lines[1] != `{"_msg":"line 1","_time":"2026-07-25T00:00:01Z"}` {
		t.Fatalf("result = %+v, lines = %q", result, lines)
	}
}

func TestFetchRetriesTransientStatusAndUsesBasicAuth(t *testing.T) {
	defer func(old func(int) time.Duration) { retryDelay = old }(retryDelay)
	retryDelay = func(int) time.Duration { return 0 }
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if user, pass, ok := r.BasicAuth(); !ok || user != "reader" || pass != "secret" {
			t.Errorf("basic auth = %q %q %v", user, pass, ok)
		}
		if calls == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, `{"_msg":"ok"}`)
	}))
	defer server.Close()

	count := 0
	client := &Client{Auth: Auth{Username: "reader", Password: "secret"}}
	result, err := client.Fetch(context.Background(), FetchRequest{URL: server.URL, From: testFrom, To: testTo, Limit: 10}, func(string) error {
		count++
		return nil
	})
	if err != nil || calls != 2 || count != 1 || result.Truncated {
		t.Fatalf("err = %v, calls = %d, count = %d, result = %+v", err, calls, count, result)
	}
}

func TestFetchUnauthorizedDoesNotRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := (&Client{}).Fetch(context.Background(), FetchRequest{URL: server.URL, From: testFrom, To: testTo}, func(string) error { return nil })
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "VICTORIALOGS_BEARER_TOKEN") {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}

func TestStreamsParsesStatsRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("query"); got != "(*) | stats by (_stream_id, _stream) count() hits" {
			t.Errorf("query = %q", got)
		}
		fmt.Fprintln(w, `{"_stream_id":"a1","_stream":"{app=\"api\"}","hits":"20"}`)
		fmt.Fprintln(w, `{"_stream_id":"b2","_stream":"{app=\"web\"}","hits":"5"}`)
	}))
	defer server.Close()

	streams, err := (&Client{}).Streams(context.Background(), FetchRequest{URL: server.URL, From: testFrom, To: testTo})
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	if fmt.Sprint(streams) != `[{a1 {app="api"} 20} {b2 {app="web"} 5}]` {
		t.Fatalf("streams = %v", streams)
	}
}

func TestFetchRequestValidate(t *testing.T) {
	tests := []struct {
		name string
		req  FetchRequest
		ok   bool
	}{
		{name: "plain url", req: FetchRequest{URL: "https://logs.example.com/vl", From: testFrom, To: testTo}, ok: true},
		{name: "tenant", req: FetchRequest{URL: "http://localhost:9428", Tenant: "0:7", From: testFrom, To: testTo}, ok: true},
		{name: "missing url", req: FetchRequest{From: testFrom, To: testTo}},
		{name: "credentials in url", req: FetchRequest{URL: "http://u:p@localhost:9428", From: testFrom, To: testTo}},
		{name: "query in url", req: FetchRequest{URL: "http://localhost:9428?token=x", From: testFrom, To: testTo}},
		{name: "non numeric tenant", req: FetchRequest{URL: "http://localhost:9428", Tenant: "a:b", From: testFrom, To: testTo}},
		{name: "from after to", req: FetchRequest{URL: "http://localhost:9428", From: testTo, To: testFrom}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.req.Validate(); (err == nil) != test.ok {
				t.Fatalf("Validate() = %v, want ok %v", err, test.ok)
			}
		})
	}
}

func TestServiceLabelAndCombinedQuery(t *testing.T) {
	labels := []struct{ stream, want string }{
		{`{app="api",env="prod"}`, "api"},
		{`{app="api",service.name="checkout"}`, "checkout"},
		{`{env="prod",job="node \"exporter\""}`, `node "exporter"`},
	}
	for _, test := range labels {
		if got := ServiceLabel(test.stream); got != test.want {
			t.Errorf("ServiceLabel(%s) = %q, want %q", test.stream, got, test.want)
		}
	}
	if got := ServiceLabel(`{env="prod"}`); got != ServiceLabel(`{env="prod"}`) || !strings.HasPrefix(got, "stream-") {
		t.Errorf("hash fallback = %q", got)
	}

	queries := []struct {
		query string
		ids   []string
		want  string
	}{
		{"*", []string{"a", "b"}, "_stream_id:in(a,b)"},
		{"error OR warn", []string{"a"}, "_stream_id:in(a) (error OR warn)"},
		{"", nil, "*"},
	}
	for _, test := range queries {
		if got := CombinedQuery(test.query, test.ids); got != test.want {
			t.Errorf("CombinedQuery(%q, %v) = %q, want %q", test.query, test.ids, got, test.want)
		}
	}
}
