# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LAPP (Log Auto Pattern Pipeline) discovers log templates from log streams using the Drain algorithm, labels them with semantic IDs via LLM, and builds structured file-based workspaces for AI-assisted log investigation. It also includes an agentic analyzer that uses LLMs to investigate logs.

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
go run ./cmd/lapp/ workspace create <topic>
go run ./cmd/lapp/ workspace add-log --topic <topic> <logfile> [--model <model>]
go run ./cmd/lapp/ workspace add-log --topic <topic> --stdin [--model <model>]
go run ./cmd/lapp/ workspace analyze --topic <topic> [question] [--model <model>]
```

Topic names are sanitized to lower-kebab-case. Workspaces live under `~/.lapp/workspaces/<topic>/`.

## Architecture

```
cmd/lapp/                CLI entrypoint (cobra commands: workspace create/add-log/analyze)
pkg/logsource/           Read log files → channel of LogLine
pkg/multiline/           Detect log entry boundaries, merge continuation lines
pkg/pattern/             Drain-based log pattern discovery and template matching
pkg/semantic/            LLM-based semantic labeling of Drain patterns
pkg/workspace/           Structured workspace builder (patterns/, notes/, AGENTS.md)
pkg/store/               DuckDB storage (log_entries + patterns tables)
pkg/config/              Model resolution (flag → $MODEL_NAME → default)
pkg/analyzer/            Agentic log analysis via eino ADK + OpenRouter
integration_test/        Integration tests against Loghub-2.0 datasets
```

### Workspace Pipeline (add-log)

Full rebuild on each `add-log`: reads ALL files in `logs/`, runs fresh Drain + semantic labeling, regenerates `patterns/` and `notes/` entirely.

```
Read all logs/ files → multiline.MergeSlice() per file → tagged lines
  → pattern.DrainParser.Feed(all content) → Templates() → filter Count > 1
  → semantic.Label(ctx, cfg, patterns)  ← single LLM batch call
  → workspace.NewBuilder(...).BuildAll()
    → patterns/<semantic-id>/pattern.md + samples.log
    → patterns/unmatched/samples.log
    → notes/summary.md + errors.md
    → AGENTS.md
```

### Multiline Detection

Uses a token graph trained on 70+ timestamp formats. Lines are tokenized (first 60 bytes), matched against a directed graph of valid token transitions, and scored 0.0-1.0. Score > 0.5 means "new log entry". If no timestamps are ever detected, falls back to line-by-line.

### Analyzer

Runs an eino ADK agent (15 max iterations) with filesystem tools (grep, read_file, execute) against a structured workspace directory.

## Environment Variables

- `OPENROUTER_API_KEY`: Required for `workspace add-log` and `workspace analyze`
- `MODEL_NAME`: Override default LLM model (default: `google/gemini-3-flash-preview`)
- `.env` file is auto-loaded via godotenv

## Tech Stack

- Go, cobra CLI, go-drain3, DuckDB (duckdb-go/v2), cloudwego/eino ADK + OpenRouter

## Code Style

- `nolint` directives go on the line above the target, not as end-of-line comments
- Compile-time interface guards: `var _ MyInterface = (*MyImpl)(nil)`
- Always use `make build` to verify compilation, never bare `go build` (it drops a binary in the project root)
