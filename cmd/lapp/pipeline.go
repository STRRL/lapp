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
)

func collectLines(merged <-chan multiline.MergeResult) ([]multiline.MergedLine, error) {
	var lines []multiline.MergedLine
	for rr := range merged {
		if rr.Err != nil {
			return nil, errors.Errorf("read log: %w", rr.Err)
		}
		lines = append(lines, *rr.Value)
	}
	return lines, nil
}

func discoverAndSavePatterns(
	ctx context.Context,
	s *store.DuckDBStore,
	dp *pattern.DrainParser,
	lines []string,
	labelCfg semantic.Config,
) (semanticIDMap map[string]string, patternCount, templateCount int, err error) {
	if err := dp.Feed(lines); err != nil {
		return nil, 0, 0, errors.Errorf("drain feed: %w", err)
	}

	templates, err := dp.Templates()
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
	inputs := buildLabelInputs(filtered, lines)

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
		return nil, 0, 0, errors.Errorf("insert patterns: %w", err)
	}

	return semanticIDMap, len(patterns), len(templates), nil
}

func storeLogsWithLabels(
	ctx context.Context,
	s *store.DuckDBStore,
	mergedLines []multiline.MergedLine,
	templates []pattern.DrainCluster,
	semanticIDMap map[string]string,
) error {
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
				return errors.Errorf("insert batch: %w", err)
			}
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := s.InsertLogBatch(ctx, batch); err != nil {
			return errors.Errorf("insert batch: %w", err)
		}
	}
	return nil
}

func buildLabelInputs(templates []pattern.DrainCluster, lines []string) []semantic.PatternInput {
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
