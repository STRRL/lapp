package workspace

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/go-errors/errors"
	"github.com/google/uuid"
	"github.com/strrl/lapp/pkg/multiline"
	"github.com/strrl/lapp/pkg/pattern"
	"github.com/strrl/lapp/pkg/semantic"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// PatternLabeler assigns stable semantic labels to discovered Drain patterns.
type PatternLabeler func(context.Context, semantic.Config, []semantic.PatternInput) ([]semantic.SemanticLabel, error)

type DiscoveryConfig struct {
	Dir     string
	RunID   string
	APIKey  string
	Model   string
	Labeler PatternLabeler
}

// DiscoveryResult summarizes a completed discovery run.
type DiscoveryResult struct {
	FileCount      int
	LineCount      int
	PatternCount   int
	UnmatchedCount int
	RunID          string
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

	record := DiscoveryRunRecord{
		ID:              runID,
		State:           DiscoveryRunStateRunning,
		CurrentStep:     DiscoveryStepReadingLogs,
		ProgressMessage: "Reading log files",
		StartedAt:       timeNow(),
	}
	if err := WriteDiscoveryRunRecord(config.Dir, record); err != nil {
		return DiscoveryResult{}, err
	}

	tagged, content, fileCount, err := mergeAllLogs(ctx, config.Dir)
	if err != nil {
		failDiscoveryRun(config.Dir, &record, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DiscoveryResult{}, err
	}

	slog.Info("Processing logs", "files", fileCount, "lines", len(tagged))
	record.LogFileCount = fileCount
	record.LineCount = len(tagged)
	record.CurrentStep = DiscoveryStepMergingEntries
	record.ProgressMessage = "Merging log entries"
	if err := WriteDiscoveryRunRecord(config.Dir, record); err != nil {
		return DiscoveryResult{}, err
	}
	record.CurrentStep = DiscoveryStepDiscoveringPatterns
	record.ProgressMessage = "Discovering log patterns"
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

	record.CurrentStep = DiscoveryStepLabelingPatterns
	record.ProgressMessage = "Labeling discovered patterns"
	if err := WriteDiscoveryRunRecord(config.Dir, record); err != nil {
		return DiscoveryResult{}, err
	}
	labels, err := labelPatterns(ctx, labeler, config, templates, content)
	if err != nil {
		failDiscoveryRun(config.Dir, &record, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DiscoveryResult{}, err
	}

	record.CurrentStep = DiscoveryStepWritingResults
	record.ProgressMessage = "Writing discovery results"
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
	record.ProgressMessage = "Discovery completed"
	if err := WriteDiscoveryRunRecord(config.Dir, record); err != nil {
		return DiscoveryResult{}, err
	}

	result := DiscoveryResult{
		FileCount:      fileCount,
		LineCount:      len(tagged),
		PatternCount:   len(builder.Patterns()),
		UnmatchedCount: len(builder.Unmatched()),
		RunID:          runID,
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

var timeNow = func() time.Time {
	return time.Now().UTC()
}

func failDiscoveryRun(workspaceDir string, record *DiscoveryRunRecord, cause error) {
	finishedAt := timeNow()
	record.State = DiscoveryRunStateFailed
	record.FinishedAt = &finishedAt
	record.ErrorMessage = cause.Error()
	record.ProgressMessage = "Discovery failed"
	_ = WriteDiscoveryRunRecord(workspaceDir, *record)
}

func mergeAllLogs(ctx context.Context, dir string) (tagged []TaggedLine, content []string, fileCount int, err error) {
	allLogs, err := ReadAllLogs(dir)
	if err != nil {
		return nil, nil, 0, errors.Errorf("read all logs: %w", err)
	}

	fileNames := make([]string, 0, len(allLogs))
	for name := range allLogs {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)

	var allTagged []TaggedLine
	var allContent []string
	for _, fileName := range fileNames {
		lines := allLogs[fileName]
		detector, err := multiline.NewDetector(multiline.DetectorConfig{})
		if err != nil {
			return nil, nil, 0, errors.Errorf("multiline detector: %w", err)
		}
		merged := multiline.MergeSlice(ctx, lines, detector)
		for _, m := range merged {
			allTagged = append(allTagged, TaggedLine{
				Content:  m.Content,
				FileName: fileName,
				LineNum:  m.StartLine,
			})
			allContent = append(allContent, m.Content)
		}
	}
	return allTagged, allContent, len(allLogs), nil
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
) ([]semantic.SemanticLabel, error) {
	if len(templates) == 0 {
		return nil, nil
	}

	inputs := buildLabelInputs(ctx, templates, content)
	slog.Info("Labeling patterns", "count", len(inputs))
	labels, err := labeler(ctx, semantic.Config{
		APIKey: config.APIKey,
		Model:  config.Model,
	}, inputs)
	if err != nil {
		return nil, errors.Errorf("label: %w", err)
	}
	return labels, nil
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
