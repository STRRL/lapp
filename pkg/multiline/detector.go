// Copyright 2016-present Datadog, Inc.
// Licensed under the Apache License, Version 2.0
// Originally from github.com/DataDog/datadog-agent
// Modified for use in LAPP: removed Datadog-specific dependencies
// (config, metrics, status, message types), added firstline regex support.

package multiline

import "regexp"

// knownTimestampFormats is the list of known timestamp formats used to build
// the token graph. Adding similar or partial duplicate timestamps does not
// impact accuracy since related tokens are inherently deduped in the graph.
var knownTimestampFormats = []string{
	"2024-03-28T13:45:30.123456Z",
	"28/Mar/2024:13:45:30",
	"Sun, 28 Mar 2024 13:45:30",
	"2024-03-28 13:45:30",
	"2024-03-28 13:45:30,123",
	"02 Jan 06 15:04 MST",
	"2024-03-28T14:33:53.743350Z",
	"2024-03-28T15:19:38.578639+00:00",
	"2024-03-28 15:44:53",
	"2024-08-20'T'13:20:10*633+0000",
	"2024 Mar 03 05:12:41.211 PDT",
	"Jan 21 18:20:11 +0000 2024",
	"19/Apr/2024:06:36:15",
	"Dec 2, 2024 2:39:58 AM",
	"Jun 09 2024 15:28:14",
	"Apr 20 00:00:35 2010",
	"Sep 28 19:00:00 +0000",
	"Mar 16 08:12:04",
	"Jul 1 09:00:55",
	"2024-10-14T22:11:20+0000",
	"2024-07-01T14:59:55.711",
	"2024-07-01T14:59:55.711Z",
	"2024-08-19 12:17:55-0400",
	"2024-06-26 02:31:29,573",
	"2024/04/12*19:37:50",
	"2024 Apr 13 22:08:13.211*PDT",
	"2024 Mar 10 01:44:20.392",
	"2024-03-10 14:30:12,655+0000",
	"2024-02-27 15:35:20.311",
	"2024-07-22'T'16:28:55.444",
	"2024-11-22'T'10:10:15.455",
	"2024-02-11'T'18:31:44",
	"2024-10-30*02:47:33:899",
	"2024-07-04*13:23:55",
	"24-02-11 16:47:35,985 +0000",
	"24-06-26 02:31:29,573",
	"24-04-19 12:00:17",
	"06/01/24 04:11:05",
	"08/10/24*13:33:56",
	"11/24/2024*05:13:11",
	"05/09/2024*08:22:14*612",
	"04/23/24 04:34:22 +0000",
	"2024/04/25 14:57:42",
	"11:42:35.173",
	"11:42:35,173",
	"23/Apr 11:42:35,173",
	"23/Apr/2024:11:42:35",
	"23/Apr/2024 11:42:35",
	"23-Apr-2024 11:42:35",
	"23-Apr-2024 11:42:35.883",
	"23 Apr 2024 11:42:35",
	"23 Apr 2024 10:32:35*311",
	"8/5/2024 3:31:18 AM:234",
	"9/28/2024 2:23:15 PM",
	"2023-03.28T14-33:53-7430Z",
	"2017-05-16_13:53:08",
}

var staticTokenGraph = makeStaticTokenGraph()

const minimumTokenLength = 8

func makeStaticTokenGraph() *tokenGraph {
	tok := newTokenizer(100)
	inputData := make([][]Token, len(knownTimestampFormats))
	for i, format := range knownTimestampFormats {
		tokens, _ := tok.tokenize([]byte(format))
		inputData[i] = tokens
	}
	return newTokenGraph(minimumTokenLength, inputData)
}

// DetectorConfig configures the multiline entry boundary detector.
type DetectorConfig struct {
	// MaxScanBytes is the maximum number of bytes to scan for timestamp
	// detection at the beginning of each line. Default: 60.
	MaxScanBytes int

	// Threshold is the minimum probability for a line to be considered
	// a new log entry. Default: 0.5.
	Threshold float64

	// FirstLineRegex is an optional regex pattern that overrides timestamp
	// detection. If set, lines matching this regex are treated as new entries.
	FirstLineRegex string

	// MaxEntryBytes is the maximum size of a merged log entry in bytes.
	// Entries exceeding this are flushed regardless of detection.
	// Default: 65536 (64KB).
	MaxEntryBytes int
}

func (c *DetectorConfig) defaults() {
	if c.MaxScanBytes == 0 {
		c.MaxScanBytes = 60
	}
	if c.Threshold == 0 {
		c.Threshold = 0.5
	}
	if c.MaxEntryBytes == 0 {
		c.MaxEntryBytes = 65536
	}
}

// Detector determines whether a log line is the start of a new log entry.
type Detector struct {
	tokenizer      *tokenizer
	tokenGraph     *tokenGraph
	threshold      float64
	firstLineRegex *regexp.Regexp
	maxScanBytes   int
	maxEntryBytes  int
}

// NewDetector creates a new multiline entry boundary detector.
func NewDetector(cfg DetectorConfig) (*Detector, error) {
	cfg.defaults()

	var re *regexp.Regexp
	if cfg.FirstLineRegex != "" {
		var err error
		re, err = regexp.Compile(cfg.FirstLineRegex)
		if err != nil {
			return nil, err
		}
	}

	return &Detector{
		tokenizer:      newTokenizer(cfg.MaxScanBytes),
		tokenGraph:     staticTokenGraph,
		threshold:      cfg.Threshold,
		firstLineRegex: re,
		maxScanBytes:   cfg.MaxScanBytes,
		maxEntryBytes:  cfg.MaxEntryBytes,
	}, nil
}

// IsNewEntry returns true if the given line looks like the start of a new
// log entry (i.e. it begins with a timestamp or matches the firstline regex).
func (d *Detector) IsNewEntry(line string) bool {
	if d.firstLineRegex != nil {
		return d.firstLineRegex.MatchString(line)
	}

	scanLen := len(line)
	if scanLen > d.maxScanBytes {
		scanLen = d.maxScanBytes
	}
	if scanLen == 0 {
		return false
	}

	tokens, _ := d.tokenizer.tokenize([]byte(line[:scanLen]))
	return d.tokenGraph.matchProbability(tokens).probability > d.threshold
}

// MaxEntryBytes returns the configured maximum entry size.
func (d *Detector) MaxEntryBytes() int {
	return d.maxEntryBytes
}
