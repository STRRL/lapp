# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LAPP (Log Auto Pattern Pipeline) discovers log templates from log streams using the Drain algorithm, labels them with semantic IDs via LLM, and stores structured results in DuckDB. It also includes an agentic analyzer that uses LLMs to investigate logs.

## Commands

```bash
make build              # Build binary to output/lapp
make unit-test          # Run unit tests (./pkg/...)
make integration-test   # Run integration tests (requires LOGHUB_PATH)
make test               # Run all tests
make lint               # golangci-lint
make ci                 # fmt + vet + build + lint + unit-test

# Run a single test
go test -v -run TestFunctionName ./pkg/pattern/
```

## CLI Usage

```bash
go run ./cmd/lapp/ ingest <logfile> [--db <path>] [--model <model>]
go run ./cmd/lapp/ templates [--db <path>]
go run ./cmd/lapp/ analyze <logfile> [question]
go run ./cmd/lapp/ debug workspace <logfile> [-o <dir>]
go run ./cmd/lapp/ debug run <workspace-dir> [question] [--model <model>]
```

## Architecture

```
cmd/lapp/                CLI entrypoint (cobra commands)
pkg/logsource/           Read log files → channel of LogLine
pkg/multiline/           Detect log entry boundaries, merge continuation lines
pkg/pattern/             Drain-based log pattern discovery and template matching
pkg/semantic/            LLM-based semantic labeling of Drain patterns
pkg/store/               DuckDB storage (log_entries + patterns tables)
pkg/config/              Model resolution (flag → $MODEL_NAME → default)
pkg/analyzer/            Agentic log analysis via eino ADK + OpenRouter
pkg/analyzer/workspace/  Build workspace files (raw.log, summary.txt, errors.txt) from templates
integration_test/        Integration tests against Loghub-2.0 datasets
```

### 2-Round Ingest Pipeline

The core design: cheap Drain clustering first, then a single batch LLM call for semantic labels.

```
Round 1 (pattern discovery):
  File → logsource.Ingest() → multiline.Merge() → collectLines()
    → pattern.DrainParser.Feed(lines) → pattern.DrainParser.Templates()
    → filter patterns with Count > 1
    → semantic.Label(ctx, cfg, patterns)  ← single LLM batch call
    → store.InsertPatterns()

Round 2 (match & store):
  For each line → pattern.MatchTemplate(line, templates)
    → attach labels {pattern: semantic_id, pattern_id: uuid}
    → store.InsertLogBatch() (500-entry batches)
```

### Multiline Detection

Uses a token graph trained on 70+ timestamp formats. Lines are tokenized (first 60 bytes), matched against a directed graph of valid token transitions, and scored 0.0-1.0. Score > 0.5 means "new log entry". If no timestamps are ever detected, falls back to line-by-line.

### Analyzer

Builds a workspace directory with pre-processed files, then runs an eino ADK agent (15 max iterations) with filesystem tools (grep, read_file, execute) against that workspace.

### DuckDB Schema

- `log_entries`: id, line_number, end_line_number, timestamp, raw, labels (JSON with `pattern` and `pattern_id` keys)
- `patterns`: pattern_id (PK, UUID string), pattern_type, raw_pattern, semantic_id, description
- Joined via `json_extract_string(labels, '$.pattern_id') = patterns.pattern_id`

## Environment Variables

- `OPENROUTER_API_KEY`: Required for `ingest`, `analyze`, and `debug run`
- `MODEL_NAME`: Override default LLM model (default: `google/gemini-3-flash-preview`)
- `.env` file is auto-loaded via godotenv

## Tech Stack

- Go, cobra CLI, go-drain3, DuckDB (duckdb-go/v2), cloudwego/eino ADK + OpenRouter

## Code Style

- `nolint` directives go on the line above the target, not as end-of-line comments
- Compile-time interface guards: `var _ MyInterface = (*MyImpl)(nil)`
