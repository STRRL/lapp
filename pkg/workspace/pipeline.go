package workspace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-errors/errors"
	"github.com/google/uuid"
	"github.com/strrl/lapp/pkg/logheader"
	"github.com/strrl/lapp/pkg/multiline"
	"github.com/strrl/lapp/pkg/ndjson"
	"github.com/strrl/lapp/pkg/pattern"
	"github.com/strrl/lapp/pkg/semantic"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// PatternLabeler assigns stable semantic labels to discovered Drain patterns.
type PatternLabeler func(context.Context, semantic.Config, []semantic.PatternInput) ([]semantic.SemanticLabel, error)

const (
	defaultLabelBatchSize   = 25
	defaultLabelConcurrency = 12
	defaultLabelMaxAttempts = 3
	labelBatchTimeout       = 90 * time.Second
)

type DiscoveryConfig struct {
	Dir              string
	RunID            string
	APIKey           string
	Model            string
	LabelConcurrency int
	LabelMaxAttempts int
	Labeler          PatternLabeler
	// Mask replaces emails, IPs and tokens before samples are sent for naming.
	Mask bool
}

// DiscoveryResult summarizes a completed discovery run.
type DiscoveryResult struct {
	FileCount      int
	LineCount      int
	PatternCount   int
	UnmatchedCount int
	RunID          string
	// TruncatedLineCount counts lines cut at logsource.MaxLineBytes.
	TruncatedLineCount int
}

// ErrFormatUnrecognized marks a log file that is neither plain text nor
// NDJSON, such as one JSON array export or a binary file. Failed runs carry
// it as the FORMAT_UNRECOGNIZED error code.
var ErrFormatUnrecognized = errors.New("format not recognized: expected plain text, NDJSON, or a Grafana export (CSV or JSON)")

type labelProgressEvent string

const (
	labelProgressStarted   labelProgressEvent = "started"
	labelProgressRetrying  labelProgressEvent = "retrying"
	labelProgressCompleted labelProgressEvent = "completed"
)

type labelProgress struct {
	Event          labelProgressEvent
	BatchNumber    int
	BatchCount     int
	BatchSize      int
	Attempt        int
	MaxAttempts    int
	CompletedCount int
}

// Discover starts a DiscoveryRun and writes generated results under discovery-runs/<run-id>/.
func Discover(ctx context.Context, config DiscoveryConfig) (DiscoveryResult, error) {
	ctx, span := otel.Tracer("lapp/workspace").Start(ctx, "workspace.Discover")
	defer span.End()

	if config.Dir == "" {
		return DiscoveryResult{}, errors.New("workspace dir is required")
	}
	runID, err := discoveryRunID(config.RunID)
	if err != nil {
		return DiscoveryResult{}, err
	}
	labeler := discoveryLabeler(config.Labeler)
	if config.Labeler == nil && strings.TrimSpace(config.APIKey) == "" {
		// No key: still show patterns, named by their raw template text.
		labeler = unnamedLabeler
	}

	record := DiscoveryRunRecord{
		ID:          runID,
		State:       DiscoveryRunStateRunning,
		CurrentStep: DiscoveryStepReadingLogs,
		Progress:    discoveryStepProgress(DiscoveryStepReadingLogs),
		StartedAt:   timeNow(),
	}
	slog.Info("DiscoveryRun step", "run", runID, "step", record.CurrentStep)
	if err := WriteDiscoveryRunRecord(config.Dir, record); err != nil {
		return DiscoveryResult{}, err
	}

	tagged, content, fileCount, split, err := mergeAllLogs(ctx, config.Dir)
	if err != nil {
		failDiscoveryRun(config.Dir, &record, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DiscoveryResult{}, err
	}

	slog.Info("Processing logs", "files", fileCount, "lines", len(tagged))
	if split.truncatedLines > 0 {
		slog.Warn(TruncatedLinesMessage(split.truncatedLines), "run", runID)
	}
	record.LogFileCount = fileCount
	record.LineCount = len(tagged)
	split.record(&record)
	setDiscoveryStep(&record, DiscoveryStepMergingEntries)
	slog.Info("DiscoveryRun step", "run", runID, "step", record.CurrentStep, "files", fileCount, "lines", len(tagged))
	if err := WriteDiscoveryRunRecord(config.Dir, record); err != nil {
		return DiscoveryResult{}, err
	}
	setDiscoveryStep(&record, DiscoveryStepDiscoveringPatterns)
	slog.Info("DiscoveryRun step", "run", runID, "step", record.CurrentStep)
	if err := WriteDiscoveryRunRecord(config.Dir, record); err != nil {
		return DiscoveryResult{}, err
	}

	templates, err := discoverRepeatedPatterns(ctx, content)
	if err != nil {
		failDiscoveryRun(config.Dir, &record, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DiscoveryResult{}, err
	}

	setDiscoveryStep(&record, DiscoveryStepLabelingPatterns)
	slog.Info("DiscoveryRun step", "run", runID, "step", record.CurrentStep, "patterns", len(templates))
	if err := WriteDiscoveryRunRecord(config.Dir, record); err != nil {
		return DiscoveryResult{}, err
	}
	labels, err := labelPatterns(ctx, labeler, config, templates, content, func(progress labelProgress) error {
		setDiscoveryLabelProgress(&record, progress)
		return WriteDiscoveryRunRecord(config.Dir, record)
	})
	if err != nil {
		failDiscoveryRun(config.Dir, &record, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DiscoveryResult{}, err
	}

	setDiscoveryStep(&record, DiscoveryStepWritingResults)
	slog.Info("DiscoveryRun step", "run", runID, "step", record.CurrentStep)
	if err := WriteDiscoveryRunRecord(config.Dir, record); err != nil {
		return DiscoveryResult{}, err
	}
	runDir := DiscoveryRunDir(config.Dir, runID)
	if err := resetGeneratedDirs(runDir); err != nil {
		failDiscoveryRun(config.Dir, &record, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DiscoveryResult{}, err
	}

	builder := NewBuilder(runDir, tagged, templates, labels)
	if err := builder.BuildAll(); err != nil {
		err = errors.Errorf("build workspace: %w", err)
		failDiscoveryRun(config.Dir, &record, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DiscoveryResult{}, err
	}

	finishedAt := timeNow()
	record.State = DiscoveryRunStateSucceeded
	record.FinishedAt = &finishedAt
	record.PatternCount = len(builder.Patterns())
	record.UnmatchedCount = len(builder.Unmatched())
	record.Patterns = builder.Patterns()
	record.Unmatched = builder.Unmatched()
	if err := WriteDiscoveryRunRecord(config.Dir, record); err != nil {
		return DiscoveryResult{}, err
	}

	result := DiscoveryResult{
		FileCount:      fileCount,
		LineCount:      len(tagged),
		PatternCount:   len(builder.Patterns()),
		UnmatchedCount: len(builder.Unmatched()),
		RunID:          runID,

		TruncatedLineCount: split.truncatedLines,
	}
	span.SetAttributes(
		attribute.Int("workspace.files", result.FileCount),
		attribute.Int("workspace.lines", result.LineCount),
		attribute.Int("workspace.patterns", result.PatternCount),
	)
	span.SetStatus(codes.Ok, "")
	return result, nil
}

func discoveryRunID(value string) (string, error) {
	if value != "" {
		return value, nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", errors.Errorf("create discovery run id: %w", err)
	}
	return id.String(), nil
}

func discoveryLabeler(labeler PatternLabeler) PatternLabeler {
	if labeler != nil {
		return labeler
	}
	return semantic.Label
}

// unnamedLabeler leaves every pattern without a semantic name; the builder
// falls back to the template text.
func unnamedLabeler(_ context.Context, _ semantic.Config, _ []semantic.PatternInput) ([]semantic.SemanticLabel, error) {
	return nil, nil
}

func labelConcurrency(value int) int {
	if value > 0 {
		return value
	}
	return defaultLabelConcurrency
}

func labelMaxAttempts(value int) int {
	if value > 0 {
		return value
	}
	return defaultLabelMaxAttempts
}

var labelRetryDelay = func(attempt int) time.Duration {
	return time.Duration(attempt) * time.Second
}

var timeNow = func() time.Time {
	return time.Now().UTC()
}

func discoveryStepProgress(step DiscoveryStep) *DiscoveryRunProgress {
	return &DiscoveryRunProgress{Step: step}
}

func setDiscoveryStep(record *DiscoveryRunRecord, step DiscoveryStep) {
	record.CurrentStep = step
	record.Progress = discoveryStepProgress(step)
}

func setDiscoveryLabelProgress(record *DiscoveryRunRecord, progress labelProgress) {
	record.CurrentStep = DiscoveryStepLabelingPatterns
	record.Progress = &DiscoveryRunProgress{
		Step: DiscoveryStepLabelingPatterns,
		LabelBatch: &DiscoveryLabelBatchProgress{
			Event:          string(progress.Event),
			BatchNumber:    progress.BatchNumber,
			BatchCount:     progress.BatchCount,
			BatchSize:      progress.BatchSize,
			Attempt:        progress.Attempt,
			MaxAttempts:    progress.MaxAttempts,
			CompletedCount: progress.CompletedCount,
		},
	}
}

func failDiscoveryRun(workspaceDir string, record *DiscoveryRunRecord, cause error) {
	finishedAt := timeNow()
	record.State = DiscoveryRunStateFailed
	record.FinishedAt = &finishedAt
	record.Error = &DiscoveryRunError{
		Code:    discoveryErrorCode(record.CurrentStep, cause),
		Message: cause.Error(),
		Step:    record.CurrentStep,
	}
	record.ErrorMessage = cause.Error()
	slog.Error("DiscoveryRun failed", "run", record.ID, "step", record.CurrentStep, "error", cause)
	_ = WriteDiscoveryRunRecord(workspaceDir, *record)
}

func discoveryErrorCode(step DiscoveryStep, cause error) string {
	if errors.Is(cause, ErrFormatUnrecognized) {
		return "FORMAT_UNRECOGNIZED"
	}
	switch step {
	case DiscoveryStepReadingLogs:
		return "READ_LOGS_FAILED"
	case DiscoveryStepMergingEntries:
		return "MERGE_ENTRIES_FAILED"
	case DiscoveryStepDiscoveringPatterns:
		return "DISCOVER_PATTERNS_FAILED"
	case DiscoveryStepLabelingPatterns:
		return "LABEL_PATTERNS_FAILED"
	case DiscoveryStepWritingResults:
		return "WRITE_RESULTS_FAILED"
	default:
		return "DISCOVERY_FAILED"
	}
}

// TruncatedLinesMessage is the warning shown when lines were cut at
// logsource.MaxLineBytes; the web app renders the same sentence.
func TruncatedLinesMessage(n int) string {
	if n == 1 {
		return "1 line was too long and was cut at 16 MB."
	}
	return fmt.Sprintf("%d lines were too long and were cut at 16 MB.", n)
}

// splitStats describes how the files in logs/ were read and split into
// entries.
type splitStats struct {
	mode           SplitMode
	physicalLines  int
	cappedEntries  int
	truncatedLines int
}

// add folds one file's stats in; a per-line split anywhere wins over a
// merged one so the page can say lines were not merged.
func (s *splitStats) add(file splitStats) {
	s.physicalLines += file.physicalLines
	s.cappedEntries += file.cappedEntries
	s.truncatedLines += file.truncatedLines
	if s.mode != SplitModePerLine && file.mode != "" {
		s.mode = file.mode
	}
}

// record stores the stats on the run record and warns about capped entries.
func (s splitStats) record(record *DiscoveryRunRecord) {
	record.SplitMode = s.mode
	record.PhysicalLineCount = s.physicalLines
	record.CappedEntryCount = s.cappedEntries
	record.TruncatedLineCount = s.truncatedLines
	if s.cappedEntries > 0 {
		slog.Warn("Some log entries were too long and were cut", "entries", s.cappedEntries, "max_lines", maxEntryLines, "max_bytes", maxEntryBytes)
	}
}

func mergeAllLogs(ctx context.Context, dir string) (tagged []TaggedLine, content []string, fileCount int, split splitStats, err error) {
	allLogs, err := ReadAllLogs(dir)
	if err != nil {
		return nil, nil, 0, splitStats{}, errors.Errorf("read all logs: %w", err)
	}

	fileNames := make([]string, 0, len(allLogs))
	for name := range allLogs {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)

	var allTagged []TaggedLine
	var allContent []string
	for _, fileName := range fileNames {
		file := allLogs[fileName]
		fileTagged, fileSplit, err := tagFileLines(ctx, fileName, file)
		if err != nil {
			return nil, nil, 0, splitStats{}, err
		}
		split.add(fileSplit)
		for _, tl := range fileTagged {
			allTagged = append(allTagged, tl)
			allContent = append(allContent, tl.DrainLine())
		}
	}
	return allTagged, allContent, len(allLogs), split, nil
}

// maxEntryLines and maxEntryBytes cap one merged text entry; the line that
// would exceed either starts a new entry.
const (
	maxEntryLines = 2000
	maxEntryBytes = 1 << 20
)

// tagFileLines converts one log file into tagged entries. NDJSON files keep
// the raw JSON line as Content and carry an extracted text line for pattern
// mining; one record is one entry even when its message spans several lines.
// Plain text files go through multiline merging unchanged; the returned
// stats say whether a text file was merged or split one entry per line and
// how many merged entries hit the cap. The format is detected from complete
// lines only, so a line cut at logsource.MaxLineBytes never changes how the
// rest of the file is read.
func tagFileLines(ctx context.Context, fileName string, file LogFileLines) ([]TaggedLine, splitStats, error) {
	lines := file.Lines
	split := splitStats{physicalLines: len(lines), truncatedLines: len(file.Truncated)}
	complete := lines
	if len(file.Truncated) > 0 {
		complete = make([]string, 0, len(lines))
		for i, line := range lines {
			if !file.Truncated[i] {
				complete = append(complete, line)
			}
		}
	}
	switch ndjson.DetectFormat(complete) {
	case ndjson.FormatNDJSON:
		return tagNDJSONLines(fileName, file), split, nil
	case ndjson.FormatUnrecognized:
		return nil, splitStats{}, errors.Errorf("read %s: %w", fileName, ErrFormatUnrecognized)
	}
	detector, err := multiline.NewDetector(multiline.DetectorConfig{
		MaxEntryBytes: maxEntryBytes,
		MaxEntryLines: maxEntryLines,
		IsEntryStart:  dominantEntryStart(lines),
	})
	if err != nil {
		return nil, splitStats{}, errors.Errorf("multiline detector: %w", err)
	}
	var merged []multiline.MergedLine
	if hasTimestampedEntries(lines, detector) {
		split.mode = SplitModeMerged
		merged = multiline.MergeSlice(ctx, lines, detector)
	} else {
		// Logs with too few timestamps at line starts (e.g. `kubectl logs`
		// without --timestamps) have no reliable entry boundaries: keep one
		// entry per line.
		split.mode = SplitModePerLine
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			merged = append(merged, multiline.MergedLine{StartLine: i + 1, EndLine: i + 1, Content: line})
		}
	}
	tagged := make([]TaggedLine, 0, len(merged))
	for _, m := range merged {
		if m.Capped {
			split.cappedEntries++
		}
		tl := TaggedLine{
			Content:   m.Content,
			FileName:  fileName,
			LineNum:   m.StartLine,
			Truncated: anyTruncated(file.Truncated, m.StartLine, m.EndLine),
		}
		// Mine patterns on the message body only: timestamps, hosts and pids
		// in the header would split one event into many patterns.
		first, rest, multi := strings.Cut(m.Content, "\n")
		h := logheader.Strip(first)
		if body := logheader.MaskInline(h.Body); body != first {
			tl.ExtractedLine = body
			if multi {
				tl.ExtractedLine += "\n" + rest
			}
		}
		if !h.Timestamp.IsZero() {
			ts := h.Timestamp
			tl.Timestamp = &ts
		}
		tagged = append(tagged, tl)
	}
	return tagged, split, nil
}

// dominantEntryStart returns a matcher for the file's dominant entry start:
// the most common logheader.StartShape among its lines. Only lines with that
// exact shape begin entries, so a continuation line that mentions a
// timestamp mid-line, or starts with a timestamp in another format (inside a
// query plan, say), stays in the entry before it. It returns nil when no line
// starts with a header logheader knows; the detector's token graph decides
// then.
func dominantEntryStart(lines []string) func(string) bool {
	counts := map[string]int{}
	dominant := ""
	for _, line := range lines {
		shape := logheader.StartShape(line)
		if shape == "" {
			continue
		}
		counts[shape]++
		if counts[shape] > counts[dominant] || (counts[shape] == counts[dominant] && shape < dominant) {
			dominant = shape
		}
	}
	if dominant == "" {
		return nil
	}
	return func(line string) bool { return logheader.StartShape(line) == dominant }
}

// minTimestampedPercent is the share of non-blank lines that must start
// with a timestamp for timestamps to mark entry boundaries. Continuation
// lines (stack traces, multi-line query plans) often outnumber entry lines
// several times over; a Postgres plan export has about one timestamped line
// in seven, so the bar is low.
const minTimestampedPercent = 5

// minTimestampedLines is how many timestamped lines a file needs, unless
// every non-blank line is timestamped.
const minTimestampedLines = 3

// hasTimestampedEntries reports whether enough lines start with a timestamp
// that a timestamped line can begin each entry and the lines without one can
// join the entry before them.
func hasTimestampedEntries(lines []string, detector *multiline.Detector) bool {
	nonBlank, timestamped := 0, 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonBlank++
		if detector.IsNewEntry(line) {
			timestamped++
		}
	}
	if timestamped == 0 || timestamped*100 < nonBlank*minTimestampedPercent {
		return false
	}
	return timestamped >= minTimestampedLines || timestamped == nonBlank
}

// anyTruncated reports whether a line in the 1-based range start..end was cut.
func anyTruncated(truncated map[int]bool, start, end int) bool {
	for n := start; n <= end && len(truncated) > 0; n++ {
		if truncated[n-1] {
			return true
		}
	}
	return false
}

// tagNDJSONLines tags each NDJSON line. A line cut at logsource.MaxLineBytes
// is no longer valid JSON; ndjson.Extract then keeps its raw text.
func tagNDJSONLines(fileName string, file LogFileLines) []TaggedLine {
	var tagged []TaggedLine
	for i, line := range file.Lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		extracted, severity, ts := ndjson.Extract(line)
		tl := TaggedLine{
			Content:       line,
			FileName:      fileName,
			LineNum:       i + 1,
			ExtractedLine: extracted,
			Severity:      severity,
			Truncated:     file.Truncated[i],
		}
		if !ts.IsZero() {
			tl.Timestamp = &ts
		}
		tagged = append(tagged, tl)
	}
	return tagged
}

func discoverRepeatedPatterns(ctx context.Context, content []string) ([]pattern.DrainCluster, error) {
	drainParser, err := pattern.NewDrainParser()
	if err != nil {
		return nil, errors.Errorf("drain parser: %w", err)
	}
	if err := drainParser.Feed(ctx, content); err != nil {
		return nil, errors.Errorf("drain feed: %w", err)
	}
	templates, err := drainParser.Templates(ctx)
	if err != nil {
		return nil, errors.Errorf("drain templates: %w", err)
	}

	var repeated []pattern.DrainCluster
	for _, t := range templates {
		if t.Count > 1 {
			repeated = append(repeated, t)
		}
	}
	return repeated, nil
}

func labelPatterns(
	ctx context.Context,
	labeler PatternLabeler,
	config DiscoveryConfig,
	templates []pattern.DrainCluster,
	content []string,
	onProgress func(labelProgress) error,
) ([]semantic.SemanticLabel, error) {
	if len(templates) == 0 {
		return nil, nil
	}

	inputs := buildLabelInputs(ctx, templates, content)
	concurrency := labelConcurrency(config.LabelConcurrency)
	maxAttempts := labelMaxAttempts(config.LabelMaxAttempts)
	batches := labelBatches(inputs)
	slog.Info(
		"Labeling patterns",
		"count", len(inputs),
		"batch_size", defaultLabelBatchSize,
		"batches", len(batches),
		"concurrency", concurrency,
		"attempts", maxAttempts,
		"timeout", labelBatchTimeout.String(),
	)

	ctx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()

	sem := make(chan struct{}, concurrency)
	errCh := make(chan error, 1)
	labelsByBatch := make([][]semantic.SemanticLabel, len(batches))
	var wg sync.WaitGroup
	var progressMu sync.Mutex
	completed := 0

	for i, batch := range batches {
		wg.Add(1)
		go func(index int, batch []semantic.PatternInput) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			batchNumber := index + 1
			batchCount := len(batches)
			batchLabels, err := labelPatternBatch(ctx, labeler, config, batch, batchNumber, batchCount, func() int {
				progressMu.Lock()
				defer progressMu.Unlock()
				return completed
			}, func(progress labelProgress) error {
				return reportLabelProgress(&progressMu, onProgress, progress)
			})
			if err != nil {
				sendLabelError(errCh, cancelAll, err)
				return
			}

			progressMu.Lock()
			completed++
			completedCount := completed
			progressMu.Unlock()
			if err := reportLabelProgress(&progressMu, onProgress, labelProgress{
				Event:          labelProgressCompleted,
				BatchNumber:    batchNumber,
				BatchCount:     batchCount,
				BatchSize:      len(batch),
				CompletedCount: completedCount,
			}); err != nil {
				sendLabelError(errCh, cancelAll, err)
				return
			}

			slog.Info("Labeled pattern batch", "batch", batchNumber, "batches", batchCount, "labels", len(batchLabels))
			labelsByBatch[index] = batchLabels
		}(i, batch)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case err := <-errCh:
		cancelAll()
		<-done
		return nil, err
	}

	select {
	case err := <-errCh:
		return nil, err
	default:
	}

	var labels []semantic.SemanticLabel
	for _, batchLabels := range labelsByBatch {
		labels = append(labels, batchLabels...)
	}
	return labels, nil
}

func labelPatternBatch(
	ctx context.Context,
	labeler PatternLabeler,
	config DiscoveryConfig,
	batch []semantic.PatternInput,
	batchNumber int,
	batchCount int,
	completedCount func() int,
	onProgress func(labelProgress) error,
) ([]semantic.SemanticLabel, error) {
	maxAttempts := labelMaxAttempts(config.LabelMaxAttempts)
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := onProgress(labelProgress{
			Event:          labelProgressStarted,
			BatchNumber:    batchNumber,
			BatchCount:     batchCount,
			BatchSize:      len(batch),
			Attempt:        attempt,
			MaxAttempts:    maxAttempts,
			CompletedCount: completedCount(),
		}); err != nil {
			return nil, err
		}

		batchCtx, cancel := context.WithTimeout(ctx, labelBatchTimeout)
		slog.Info(
			"Labeling pattern batch",
			"batch", batchNumber,
			"batches", batchCount,
			"patterns", len(batch),
			"attempt", attempt,
			"attempts", maxAttempts,
		)
		labels, err := labeler(batchCtx, semantic.Config{
			APIKey: config.APIKey,
			Model:  config.Model,
			Mask:   config.Mask,
		}, batch)
		cancel()
		if err == nil {
			if attempt > 1 {
				slog.Info(
					"Labeling pattern batch retry succeeded",
					"batch", batchNumber,
					"batches", batchCount,
					"attempt", attempt,
					"attempts", maxAttempts,
					"labels", len(labels),
				)
			}
			return labels, nil
		}

		lastErr = err
		if attempt == maxAttempts {
			break
		}

		nextAttempt := attempt + 1
		delay := labelRetryDelay(attempt)
		slog.Warn(
			"Labeling pattern batch failed; retrying",
			"batch", batchNumber,
			"batches", batchCount,
			"attempt", attempt,
			"attempts", maxAttempts,
			"next_attempt", nextAttempt,
			"delay", delay.String(),
			"error", err,
		)
		if err := onProgress(labelProgress{
			Event:          labelProgressRetrying,
			BatchNumber:    batchNumber,
			BatchCount:     batchCount,
			BatchSize:      len(batch),
			Attempt:        nextAttempt,
			MaxAttempts:    maxAttempts,
			CompletedCount: completedCount(),
		}); err != nil {
			return nil, err
		}
		if err := waitLabelRetry(ctx, delay); err != nil {
			return nil, errors.Errorf("label batch %d/%d retry wait: %w", batchNumber, batchCount, err)
		}
	}
	return nil, errors.Errorf("label batch %d/%d failed after %d attempts: %w", batchNumber, batchCount, maxAttempts, lastErr)
}

func waitLabelRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func labelBatches(inputs []semantic.PatternInput) [][]semantic.PatternInput {
	batches := make([][]semantic.PatternInput, 0, (len(inputs)+defaultLabelBatchSize-1)/defaultLabelBatchSize)
	for start := 0; start < len(inputs); start += defaultLabelBatchSize {
		end := min(start+defaultLabelBatchSize, len(inputs))
		batches = append(batches, inputs[start:end])
	}
	return batches
}

func reportLabelProgress(mu *sync.Mutex, onProgress func(labelProgress) error, progress labelProgress) error {
	if onProgress == nil {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	return onProgress(progress)
}

func sendLabelError(errCh chan<- error, cancel context.CancelFunc, err error) {
	select {
	case errCh <- err:
		cancel()
	default:
	}
}

func resetGeneratedDirs(dir string) error {
	for _, sub := range []string{"patterns", "notes"} {
		subDir := filepath.Join(dir, sub)
		if err := os.RemoveAll(subDir); err != nil {
			return errors.Errorf("remove %s: %w", sub, err)
		}
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			return errors.Errorf("create %s: %w", sub, err)
		}
	}
	return nil
}

func buildLabelInputs(ctx context.Context, templates []pattern.DrainCluster, lines []string) []semantic.PatternInput {
	_, span := otel.Tracer("lapp/workspace").Start(ctx, "workspace.BuildLabelInputs")
	defer span.End()

	span.SetAttributes(attribute.Int("template.count", len(templates)))

	var inputs []semantic.PatternInput
	for _, t := range templates {
		var samples []string
		for _, line := range lines {
			if _, ok := pattern.MatchTemplate(line, []pattern.DrainCluster{t}); ok {
				samples = append(samples, line)
				if len(samples) >= 3 {
					break
				}
			}
		}
		inputs = append(inputs, semantic.PatternInput{
			PatternUUIDString: t.ID.String(),
			Pattern:           t.Pattern,
			Samples:           samples,
		})
	}
	return inputs
}

// LoadTaggedLines reads every log file in a workspace and returns the
// entries exactly as discovery sees them (merged, header-stripped).
func LoadTaggedLines(ctx context.Context, dir string) ([]TaggedLine, error) {
	tagged, _, _, _, err := mergeAllLogs(ctx, dir)
	return tagged, err
}

// LabelPreview returns the pattern inputs that discovery would send to the
// LLM for naming, batched exactly as Discover batches them.
func LabelPreview(ctx context.Context, dir string) ([][]semantic.PatternInput, error) {
	_, content, _, _, err := mergeAllLogs(ctx, dir)
	if err != nil {
		return nil, err
	}
	templates, err := discoverRepeatedPatterns(ctx, content)
	if err != nil {
		return nil, err
	}
	return labelBatches(buildLabelInputs(ctx, templates, content)), nil
}
