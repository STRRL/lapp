package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/gcplog"
	"github.com/strrl/lapp/pkg/workspace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

var importGCPTopic string
var importGCPProject string
var importGCPFilter string
var importGCPSince string
var importGCPFrom string
var importGCPTo string
var importGCPLimit int

func workspaceImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import logs from an external provider into the workspace",
	}
	cmd.AddCommand(workspaceImportGCPCmd())
	return cmd
}

func workspaceImportGCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gcp",
		Short: "Import logs from GCP Cloud Logging",
		Long: `Pull matching entries from GCP Cloud Logging into the workspace as one
NDJSON log file under logs/, and record the ImportRun under import-runs/.

Uses Application Default Credentials; LAPP never manages provider auth.
Exactly one time range form is required: --since, or --from together with --to.
This never starts discovery. Run 'lapp workspace discover --topic <topic>'
afterwards.`,
		Args: cobra.NoArgs,
		RunE: runWorkspaceImportGCP,
	}
	cmd.Flags().StringVar(&importGCPTopic, "topic", "", "workspace topic (required)")
	cmd.Flags().StringVar(&importGCPProject, "project", "", "GCP project id (required)")
	cmd.Flags().StringVar(&importGCPFilter, "filter", "", "Cloud Logging filter, combined with the time range")
	cmd.Flags().StringVar(&importGCPSince, "since", "", "import entries from the last duration, e.g. 1h or 30m")
	cmd.Flags().StringVar(&importGCPFrom, "from", "", "start of time range, RFC3339, e.g. 2026-07-25T00:00:00Z")
	cmd.Flags().StringVar(&importGCPTo, "to", "", "end of time range, RFC3339")
	cmd.Flags().IntVar(&importGCPLimit, "limit", 100000, "maximum number of entries to import")
	_ = cmd.MarkFlagRequired("topic")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

// resolveImportTimeRange validates the mutually exclusive time range flags
// and resolves them to a concrete UTC from/to pair.
func resolveImportTimeRange(since, from, to string, now time.Time) (fromTime, toTime time.Time, err error) {
	hasSince := since != ""
	hasFromTo := from != "" || to != ""
	if hasSince && hasFromTo {
		return time.Time{}, time.Time{}, errors.New("--since and --from/--to are mutually exclusive; use one time range form")
	}
	if !hasSince && !hasFromTo {
		return time.Time{}, time.Time{}, errors.New("a time range is required: use --since <duration>, or --from and --to (RFC3339)")
	}

	if hasSince {
		duration, parseErr := time.ParseDuration(since)
		if parseErr != nil {
			return time.Time{}, time.Time{}, errors.Errorf("invalid --since duration %q: %w", since, parseErr)
		}
		if duration <= 0 {
			return time.Time{}, time.Time{}, errors.Errorf("--since must be a positive duration, got %q", since)
		}
		toTime = now.UTC()
		return toTime.Add(-duration), toTime, nil
	}

	if from == "" || to == "" {
		return time.Time{}, time.Time{}, errors.New("--from and --to must be provided together")
	}
	fromTime, err = time.Parse(time.RFC3339, from)
	if err != nil {
		return time.Time{}, time.Time{}, errors.Errorf("invalid --from timestamp %q, expected RFC3339: %w", from, err)
	}
	toTime, err = time.Parse(time.RFC3339, to)
	if err != nil {
		return time.Time{}, time.Time{}, errors.Errorf("invalid --to timestamp %q, expected RFC3339: %w", to, err)
	}
	if fromTime.After(toTime) {
		return time.Time{}, time.Time{}, errors.Errorf("--from %s is after --to %s", from, to)
	}
	return fromTime.UTC(), toTime.UTC(), nil
}

func runWorkspaceImportGCP(cmd *cobra.Command, _ []string) error {
	dir, err := topicToDir(importGCPTopic)
	if err != nil {
		return err
	}

	// Validate workspace exists
	if _, err := os.Stat(filepath.Join(dir, "logs")); os.IsNotExist(err) {
		hint := availableWorkspacesHint()
		return errors.Errorf("not a workspace: %s (no logs/ directory)%s", dir, hint)
	}

	if strings.TrimSpace(importGCPProject) == "" {
		return errors.New("--project must not be blank")
	}

	from, to, err := resolveImportTimeRange(importGCPSince, importGCPFrom, importGCPTo, time.Now())
	if err != nil {
		return err
	}
	if importGCPLimit <= 0 {
		return errors.Errorf("--limit must be positive, got %d", importGCPLimit)
	}

	// Validation is done; anything past this point is a run failure, so the
	// usage help would only be noise.
	cmd.SilenceUsage = true

	ctx, span := otel.Tracer("lapp/cmd").Start(cmd.Context(), "cmd.WorkspaceImportGCP")
	defer span.End()

	result, err := workspace.RunImport(ctx, workspace.ImportConfig{
		Dir:      dir,
		Provider: "gcp",
		Project:  importGCPProject,
		Filter:   importGCPFilter,
		From:     from,
		To:       to,
		Limit:    importGCPLimit,
		Fetcher: func(fetchCtx context.Context, req workspace.ImportRequest) (workspace.ImportFetchResult, error) {
			fetched, err := gcplog.FetchLines(fetchCtx, gcplog.FetchRequest{
				Project: req.Project,
				Filter:  req.Filter,
				From:    req.From,
				To:      req.To,
				Limit:   req.Limit,
			})
			if err != nil {
				return workspace.ImportFetchResult{}, err
			}
			return workspace.ImportFetchResult{
				Lines:     fetched.Lines,
				Truncated: fetched.Truncated,
			}, nil
		},
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	topic := filepath.Base(dir)
	if result.EntryCount == 0 {
		fmt.Printf("No entries matched. ImportRun %s succeeded with 0 entries; no log file was written.\n", result.RunID)
		span.SetStatus(codes.Ok, "")
		return nil
	}
	if result.Truncated {
		fmt.Printf("Warning: result truncated at --limit %d; narrow the time range or filter to import everything.\n", importGCPLimit)
	}
	fmt.Printf("Imported %d entries into logs/%s.\nDiscovery has not run yet. Run it with:\n\n  lapp workspace discover --topic %s\n", result.EntryCount, result.LogFileName, topic)
	span.SetStatus(codes.Ok, "")
	return nil
}
