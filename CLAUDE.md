# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LAPP (Log Auto Pattern Pipeline) discovers log templates from log streams using the Drain algorithm, labels them with semantic IDs via LLM, and builds structured file-based workspaces for AI-assisted log investigation. It also includes an agentic analyzer that uses LLMs to investigate logs.

## Commands

```bash
make build              # Build embedded frontend assets, then output/lapp
make clean              # Remove generated build artifacts
make dev                # Clean, build, and start lapp web on 127.0.0.1:8080
make proto-gen          # Generate protobuf/Connect code
make test               # Run unit and integration tests
make check              # Run formatting, linting, type checks, build, and unit tests

# Run a single test
go test -v -run TestFunctionName ./pkg/pattern/
```

## CLI Usage

```bash
go run ./cmd/lapp/ workspace create <topic>
go run ./cmd/lapp/ workspace add-log --topic <topic> <logfile>
go run ./cmd/lapp/ workspace add-log --topic <topic> --stdin
go run ./cmd/lapp/ workspace import gcp --topic <topic> --project <project> [--filter <filter>] (--since <duration> | --from <ts> --to <ts>) [--limit <n>]
go run ./cmd/lapp/ workspace discover --topic <topic> [--model <model>]
go run ./cmd/lapp/ workspace analyze --topic <topic> [question] [--model <model>]
go run ./cmd/lapp/ web [--addr 127.0.0.1:0]
```

Topic names are sanitized to lower-kebab-case. Workspaces live under `~/.lapp/workspaces/<topic>/`.

## Architecture

```
cmd/lapp/                CLI entrypoint (cobra commands: workspace create/add-log/import/discover/analyze)
pkg/logsource/           Read log files → channel of LogLine
pkg/multiline/           Detect log entry boundaries, merge continuation lines
pkg/pattern/             Drain-based log pattern discovery and template matching
pkg/semantic/            LLM-based semantic labeling of Drain patterns
pkg/workspace/           DiscoveryRun execution and run-scoped file writer
pkg/store/               DuckDB storage primitives (not yet on the CLI discovery path)
pkg/config/              Model resolution (flag → $MODEL_NAME → default)
pkg/analyzer/            Agentic log analysis via eino ADK + ACP providers
integration_test/        Integration tests against Loghub-2.0 datasets
```

### DiscoveryRun (discover)

`add-log` is a pure copy into `logs/` and never triggers discovery. `workspace import gcp` pulls a snapshot from GCP Cloud Logging through ADC credentials, lands it as enveloped NDJSON in `logs/`, and records provenance under `import-runs/<run-id>/record.json`; it never triggers discovery either. Each `workspace discover` starts a DiscoveryRun: reads ALL files in `logs/`, runs fresh Drain + semantic labeling, and writes run-scoped `patterns/` and `notes/`.
When `lapp web` starts, it marks any previous `QUEUED` or `RUNNING` DiscoveryRuns as failed because those local workers no longer exist.
DiscoveryRun records persist structured `progress` and `error` fields; frontend code renders those facts into user-facing text.

```
workspace.Discover(ctx, cfg)
  → Read all logs/ files → multiline.MergeSlice() per file → tagged lines
  → pattern.DrainParser.Feed(all content) → Templates() → filter Count > 1
  → semantic.Label(ctx, cfg, patterns)  ← LLM batches with per-batch retry
  → workspace.NewBuilder(...).BuildAll()
    → discovery-runs/<run-id>/patterns/<semantic-id>/pattern.md + samples.log
    → discovery-runs/<run-id>/patterns/unmatched/samples.log
    → discovery-runs/<run-id>/notes/summary.md + errors.md
    → discovery-runs/<run-id>/AGENTS.md
```

### Multiline Detection

Uses a token graph trained on 70+ timestamp formats. Lines are tokenized (first 60 bytes), matched against a directed graph of valid token transitions, and scored 0.0-1.0. Score > 0.5 means "new log entry". If no timestamps are ever detected, falls back to line-by-line.

### Analyzer

Runs an eino ADK agent (15 max iterations) with filesystem tools (grep, read_file, execute) against a structured workspace directory.

## Environment Variables

- `OPENROUTER_API_KEY`: Required for semantic labeling in `workspace discover`
- GCP ADC (`gcloud auth application-default login`): Required for `workspace import gcp`
- `MODEL_NAME`: Override default LLM model (default: `google/gemini-3-flash-preview`)
- ACP provider credentials/login: Required for `workspace analyze` through the selected provider
- `.env` file is auto-loaded via godotenv

## Tech Stack

- Go, cobra CLI, go-drain3, DuckDB (duckdb-go/v2), cloudwego/eino ADK, OpenRouter semantic labeling, ACP providers

## Code Style

- `nolint` directives go on the line above the target, not as end-of-line comments
- Compile-time interface guards: `var _ MyInterface = (*MyImpl)(nil)`
- Always use `make build` to verify compilation, never bare `go build` (it drops a binary in the project root)
