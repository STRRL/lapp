// Package logheader strips the leading timestamp/host/process prefix from
// plain-text log lines so pattern mining sees only the message body, and
// keeps the parsed timestamp for time-based metrics.
package logheader

import (
	"regexp"
	"strings"
	"time"
)

// Result is a log line split into its header facts and message body.
type Result struct {
	Body      string
	Timestamp time.Time // zero when no timestamp could be parsed
}

type format struct {
	re *regexp.Regexp
	// layouts parse the "ts" submatch; tried in order.
	layouts []string
	// noYear marks layouts without a year (syslog); the year is inferred.
	noYear bool
}

// Header formats, most specific first. Each regex must capture "ts" and
// "body"; everything else it matches (host, process, pid) is dropped.
var formats = []format{
	// Apache error log: [Sun Dec 04 04:47:44 2005] [notice] ...
	{
		re:      regexp.MustCompile(`^\[(?P<ts>\w{3} \w{3} +\d{1,2} \d{2}:\d{2}:\d{2}(?:\.\d+)? \d{4})\]\s*(?P<body>.*)$`),
		layouts: []string{"Mon Jan _2 15:04:05 2006", "Mon Jan _2 15:04:05.000000 2006"},
	},
	// ISO-8601 / RFC 3339-ish, optional brackets, optional "host proc[pid]:".
	// The timestamp may be quoted and followed by whitespace or, in CSV-like
	// text, by a "," or ";" separator. A leading Unix epoch and tab (Grafana's
	// text export: "<unix ms>\t<ISO time>\t<body>") is dropped; the ISO time
	// wins.
	{
		re: regexp.MustCompile(`^(?:\d{10,19}\t)?\[?"?(?P<ts>\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}(?::?\d{2})?)?)"?\]?(?:\s+|\s*[,;]\s*)(?P<body>.*)$`),
		layouts: []string{
			time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z0700", "2006-01-02T15:04:05.999999999Z07", "2006-01-02T15:04:05.999999999",
			"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999Z0700", "2006-01-02 15:04:05.999999999Z07", "2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05,999999999", "2006-01-02 15:04:05,999999999Z0700",
		},
	},
	// Syslog: Jun 14 15:16:01 host proc(sub)[pid]: body
	{
		re:      regexp.MustCompile(`^(?P<ts>\w{3} +\d{1,2} \d{2}:\d{2}:\d{2}) \S+ (?P<proc>[^\s:\[]+(?:\([^)]*\))?)(?:\[\d+\])?: ?(?P<body>.*)$`),
		layouts: []string{"Jan _2 15:04:05"},
		noYear:  true,
	},
	// Syslog without process tag: Jun 14 15:16:01 host body
	{
		re:      regexp.MustCompile(`^(?P<ts>\w{3} +\d{1,2} \d{2}:\d{2}:\d{2}) \S+ (?P<body>.*)$`),
		layouts: []string{"Jan _2 15:04:05"},
		noYear:  true,
	},
	// HDFS/Hadoop: 081109 203615 148 INFO ...
	{
		re:      regexp.MustCompile(`^(?P<ts>\d{6} \d{6}) \d+ (?P<body>.*)$`),
		layouts: []string{"060102 150405"},
	},
	// glog / klog: I1020 10:12:12.123456 1234 file.go:12] body
	{
		re:      regexp.MustCompile(`^(?P<lvl>[IWEF])(?P<ts>\d{4} \d{2}:\d{2}:\d{2}\.\d+)\s+\d+ \S+\] (?P<body>.*)$`),
		layouts: []string{"0102 15:04:05.000000"},
		noYear:  true,
	},
	// Slash dates: 2024/04/25 14:57:42 body
	{
		re:      regexp.MustCompile(`^(?P<ts>\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?)\s+(?P<body>.*)$`),
		layouts: []string{"2006/01/02 15:04:05.999999999"},
	},
}

// Now is the reference clock used to infer missing years.
var Now = time.Now

// Strip splits a log line into header facts and body. Lines with no
// recognised header are returned unchanged with a zero Timestamp.
func Strip(line string) Result {
	for _, f := range formats {
		m := f.re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ts := m[f.re.SubexpIndex("ts")]
		body := m[f.re.SubexpIndex("body")]
		if i := f.re.SubexpIndex("proc"); i >= 0 && m[i] != "" {
			// Keep the program name (sshd, ftpd, kernel): it is part of the
			// event identity. Drop host and pid.
			body = m[i] + ": " + body
		}
		if i := f.re.SubexpIndex("lvl"); i >= 0 && m[i] != "" {
			body = glogLevel(m[i]) + " " + body
		}
		return Result{Body: strings.TrimSpace(body), Timestamp: parse(ts, f)}
	}
	return Result{Body: line}
}

// StartShape returns the shape of the timestamp a line starts with: the
// line up to the end of its leading timestamp (including any epoch prefix,
// bracket, or quote), without fractional seconds or zone suffix, with each
// run of digits written as "9" and each run of letters as "a". It is empty when the line does not start with a recognized
// header. Lines of one log format share a shape, so a file's entry starts
// can be told apart from continuation lines that start with another format.
func StartShape(line string) string {
	for _, f := range formats {
		m := f.re.FindStringSubmatchIndex(line)
		if m == nil {
			continue
		}
		tsStart, tsEnd := m[2*f.re.SubexpIndex("ts")], m[2*f.re.SubexpIndex("ts")+1]
		ts := shapeFraction.ReplaceAllString(line[tsStart:tsEnd], "")
		ts = shapeZone.ReplaceAllString(ts, "")
		return collapseShape(line[:tsStart] + ts)
	}
	return ""
}

// shapeFraction and shapeZone drop the parts of a timestamp whose presence
// varies between lines of one format (fractional seconds, zone suffix), so
// "05:41:47Z" and "05:41:47.158+00:00" share a shape.
var (
	shapeFraction = regexp.MustCompile(`[.,]\d+`)
	shapeZone     = regexp.MustCompile(`(?:Z|[+-]\d{2}(?::?\d{2})?)$`)
)

func collapseShape(prefix string) string {
	var b strings.Builder
	var last byte
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		switch {
		case c >= '0' && c <= '9':
			c = '9'
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			c = 'a'
		}
		if (c == '9' || c == 'a') && c == last {
			continue
		}
		b.WriteByte(c)
		last = c
	}
	return b.String()
}

func parse(ts string, f format) time.Time {
	ts = strings.Join(strings.Fields(ts), " ")
	for _, layout := range f.layouts {
		layout = strings.Join(strings.Fields(strings.ReplaceAll(layout, "_", "")), " ")
		t, err := time.Parse(layout, ts)
		if err != nil {
			continue
		}
		if f.noYear {
			now := Now()
			t = t.AddDate(now.Year(), 0, 0)
			// A month in the future most likely belongs to last year.
			if t.After(now.AddDate(0, 1, 0)) {
				t = t.AddDate(-1, 0, 0)
			}
		}
		return t.UTC()
	}
	return time.Time{}
}

func glogLevel(l string) string {
	switch l {
	case "W":
		return "WARN"
	case "E":
		return "ERROR"
	case "F":
		return "FATAL"
	}
	return "INFO"
}

// inlineTimestamp matches date-times embedded in a message body, e.g.
// "at Sun Jul 3 23:16:09 2005" or "2024-03-28T13:45:30Z".
var inlineTimestamp = regexp.MustCompile(`\b(?:(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun) )?(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) +\d{1,2} \d{2}:\d{2}:\d{2}(?: \d{4})?\b|\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)

// MaskInline replaces date-times inside a message body with a single
// wildcard token so they don't split patterns.
func MaskInline(body string) string {
	return inlineTimestamp.ReplaceAllString(body, "<*>")
}
