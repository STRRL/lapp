package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-errors/errors"
	"github.com/strrl/lapp/pkg/multiline"
	"github.com/strrl/lapp/pkg/pattern"
	"github.com/strrl/lapp/pkg/semantic"
	"github.com/strrl/lapp/pkg/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func collectLines(ctx context.Context, merged <-chan multiline.MergeResult) ([]multiline.MergedLine, error) {
	_, span := otel.Tracer("lapp/pipeline").Start(ctx, "pipeline.CollectLines")
	defer span.End()

	var lines []multiline.MergedLine
	for rr := range merged {
		if rr.Err != nil {
			span.RecordError(rr.Err)
			span.SetStatus(codes.Error, rr.Err.Error())
			return nil, errors.Errorf("read log: %w", rr.Err)
		}
		lines = append(lines, *rr.Value)
	}

	span.SetAttributes(attribute.Int("line.count", len(lines)))
	return lines, nil
}

func discoverAndSavePatterns(
	ctx context.Context,
	s *store.DuckDBStore,
	dp *pattern.DrainParser,
	lines []string,
	labelCfg semantic.Config,
) (semanticIDMap map[string]string, patternCount, templateCount int, err error) {
	ctx, span := otel.Tracer("lapp/pipeline").Start(ctx, "pipeline.DiscoverAndSavePatterns")
	defer span.End()

	if err := dp.Feed(ctx, lines); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, 0, 0, errors.Errorf("drain feed: %w", err)
	}

	templates, err := dp.Templates(ctx)
	if err != nil {
		return nil, 0, 0, errors.Errorf("drain templates: %w", err)
	}

	// Filter out single-match patterns (not generalized)
	var filtered []pattern.DrainCluster
	for _, t := range templates {
		if t.Count <= 1 {
			continue
		}
		filtered = append(filtered, t)
	}

	semanticIDMap = make(map[string]string)

	if len(filtered) == 0 {
		return semanticIDMap, 0, len(templates), nil
	}

	// Build labeler inputs with sample lines from in-memory data
	inputs := buildLabelInputs(ctx, filtered, lines)

	slog.Info("Labeling patterns", "count", len(inputs))

	labels, err := semantic.Label(ctx, labelCfg, inputs)
	if err != nil {
		return nil, 0, 0, errors.Errorf("label: %w", err)
	}

	// Index labels by pattern UUID for lookup
	labelMap := make(map[string]semantic.SemanticLabel, len(labels))
	for _, l := range labels {
		labelMap[l.PatternUUIDString] = l
	}

	// Build store patterns with semantic labels and populate semanticIDMap
	var patterns []store.Pattern
	for _, t := range filtered {
		p := store.Pattern{
			PatternUUIDString: t.ID.String(),
			PatternType:       "drain",
			RawPattern:        t.Pattern,
		}
		if l, ok := labelMap[t.ID.String()]; ok {
			p.SemanticID = l.SemanticID
			p.Description = l.Description
			semanticIDMap[t.ID.String()] = l.SemanticID
		}
		patterns = append(patterns, p)
	}

	if err := s.InsertPatterns(ctx, patterns); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, 0, 0, errors.Errorf("insert patterns: %w", err)
	}

	span.SetAttributes(
		attribute.Int("pattern.count", len(patterns)),
		attribute.Int("template.count", len(templates)),
	)
	return semanticIDMap, len(patterns), len(templates), nil
}

func storeLogsWithLabels(
	ctx context.Context,
	s *store.DuckDBStore,
	mergedLines []multiline.MergedLine,
	templates []pattern.DrainCluster,
	semanticIDMap map[string]string,
) error {
	ctx, span := otel.Tracer("lapp/pipeline").Start(ctx, "pipeline.StoreLogsWithLabels")
	defer span.End()

	span.SetAttributes(attribute.Int("line.count", len(mergedLines)))

	var batchCount int
	var batch []store.LogEntry
	for _, ml := range mergedLines {
		entry := store.LogEntry{
			LineNumber:    ml.StartLine,
			EndLineNumber: ml.EndLine,
			Timestamp:     time.Now(),
			Raw:           ml.Content,
		}

		if tpl, ok := pattern.MatchTemplate(ml.Content, templates); ok {
			if sid, found := semanticIDMap[tpl.ID.String()]; found {
				entry.Labels = map[string]string{
					"pattern":    sid,
					"pattern_id": tpl.ID.String(),
				}
			}
		}

		batch = append(batch, entry)

		if len(batch) >= 500 {
			if err := s.InsertLogBatch(ctx, batch); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return errors.Errorf("insert batch: %w", err)
			}
			batch = batch[:0]
			batchCount++
		}
	}

	if len(batch) > 0 {
		if err := s.InsertLogBatch(ctx, batch); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return errors.Errorf("insert batch: %w", err)
		}
		batchCount++
	}

	span.SetAttributes(attribute.Int("batch.count", batchCount))
	return nil
}

func buildLabelInputs(ctx context.Context, templates []pattern.DrainCluster, lines []string) []semantic.PatternInput {
	_, span := otel.Tracer("lapp/pipeline").Start(ctx, "pipeline.BuildLabelInputs")
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
