# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LAPP (Log Auto Pattern Pipeline) is a tool that automatically discovers log templates from log streams using multi-strategy parsing (JSON, Drain) and stores structured results in DuckDB for querying. It also includes an agentic analyzer that uses LLMs to investigate logs.

## Commands (Makefile)

```bash
# Build
make build

# Run unit tests
make unit-test

# Run integration tests (requires LOGHUB_PATH)
make integration-test

# Run all tests (unit + integration)
make test

# Run a single test
go test -v -run TestFunctionName ./pkg/pattern/

# Lint
make lint

# CI checks (fmt + vet + build + lint + unit-test)
make ci
```

## CLI Usage

```bash
go run ./cmd/lapp/ ingest <logfile> [--db <path>]
go run ./cmd/lapp/ templates [--db <path>]
go run ./cmd/lapp/ analyze <logfile> [question]

# Debug subcommands (build workspace without LLM, or run agent on existing workspace)
go run ./cmd/lapp/ debug workspace <logfile> [-o <dir>]
go run ./cmd/lapp/ debug run <workspace-dir> [question] [--model <model>]

```

## Architecture

See `ARCHITECTURE.md` for full module design. Key modules:

```
cmd/lapp/           CLI entrypoint (cobra commands)
pkg/logsource/      Read log files → stream of LogLine
pkg/pattern/        Drain-based log pattern discovery and template matching
pkg/store/          DuckDB storage for log entries and templates
pkg/analyzer/       Agentic log analysis: builds workspace files, runs LLM agent via eino ADK
integration_test/   Integration tests and test assets (Loghub-2.0 CSV loader)
```

**Parser Chain:** JSON → Drain (first match wins, via `ChainParser`)

**Data Flow:** CLI → Ingestor → Parser Chain → Store

**Analyzer Flow:** Logs → Parser Chain → Workspace (raw.log, summary.txt, errors.txt) → eino ADK agent with filesystem tools → analysis result

## Key Interfaces

- `pattern.DrainCluster` / `pattern.MatchTemplate` (`pkg/pattern/parser.go`): Pattern discovery and template matching
- `store.Store` (`pkg/store/store.go`): Storage interface with `Init`, `InsertLog`, `InsertLogBatch`, `QueryByTemplate`, etc.

## Environment Variables

- `OPENROUTER_API_KEY`: Required for the `analyze` and `debug run` commands (LLM-powered analysis)
- `MODEL_NAME`: Override default LLM model (default: `google/gemini-3-flash-preview`)
- `.env` file is auto-loaded via godotenv

## Tech Stack

- Language: Go
- CLI: cobra
- Parser: go-drain3
- Storage: DuckDB (via duckdb-go/v2)
- LLM Agent: cloudwego/eino ADK + OpenRouter API

## Code Style

- `nolint` directives should be placed on the line above the target, not as end-of-line comments
- For every Go interface implementation, add a compile-time interface guard: `var _ MyInterface = (*MyImpl)(nil)`
