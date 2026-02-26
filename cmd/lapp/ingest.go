package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/strrl/lapp/pkg/ingestor"
	"github.com/strrl/lapp/pkg/parser"
	"github.com/strrl/lapp/pkg/store"
)

func ingestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingest <logfile>",
		Short: "Ingest a log file through the parser pipeline into the store",
		Long:  "Read a log file (or stdin with \"-\"), parse each line through the parser chain (JSON -> Grok -> Drain -> LLM), and store results in DuckDB.",
		Args:  cobra.ExactArgs(1),
		RunE:  runIngest,
	}
	return cmd
}

func runIngest(cmd *cobra.Command, args []string) error {
	logFile := args[0]

	ch, err := ingestor.Ingest(logFile)
	if err != nil {
		return fmt.Errorf("ingest: %w", err)
	}

	grokParser, err := parser.NewGrokParser()
	if err != nil {
		return fmt.Errorf("grok parser: %w", err)
	}
	chain := parser.NewChainParser(
		parser.NewJSONParser(),
		grokParser,
		parser.NewDrainParser(),
		parser.NewLLMParser(),
	)

	s, err := store.NewDuckDBStore(dbPath)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Init(); err != nil {
		return fmt.Errorf("store init: %w", err)
	}

	var count int
	var batch []store.LogEntry
	for line := range ch {
		result := chain.Parse(line.Content)
		entry := store.LogEntry{
			LineNumber: line.LineNumber,
			Timestamp:  time.Now(),
			Raw:        line.Content,
			TemplateID: result.TemplateID,
			Template:   result.Template,
		}
		batch = append(batch, entry)

		if len(batch) >= 500 {
			if err := s.InsertLogBatch(batch); err != nil {
				return fmt.Errorf("insert batch: %w", err)
			}
			batch = batch[:0]
		}
		count++
	}

	if len(batch) > 0 {
		if err := s.InsertLogBatch(batch); err != nil {
			return fmt.Errorf("insert batch: %w", err)
		}
	}

	templates := chain.Templates()
	fmt.Fprintf(os.Stderr, "Ingested %d lines, discovered %d templates\n", count, len(templates))
	fmt.Fprintf(os.Stderr, "Database: %s\n", dbPath)
	return nil
}
