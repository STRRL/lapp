# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Coherence

Read this first: it is the reason behind the coding rules in this file.

- **What it is.** Decoherence is the accumulation of locally valid but mutually incompatible or redundant models and patterns in one system: four ways to handle errors, three ways to load config, two data models for the same thing. Each one works on its own; together they make every change expensive.
- **Why it compounds.** Every extra way of doing something has to be understood, and translated to and from, by every later change. Paths get longer, connections multiply, and each change switches between paradigms, so the cost of change grows faster than linearly with decoherence. It grows with change, not with time: the faster the code changes, the faster it decays.
- **Why AI agents must care most.** An agent has no memory across sessions. Each session re-infers the conventions from the small slice of code it read, and small differences between those inferences pile up into parallel patterns. Agents also change code fast, which makes them the largest source of decoherence. Left unchecked, the system slides into a state that only agents can change and that agents can no longer untangle (Decoherence Collapse).
- **So:** consistency beats local optimality. Fit new code into the existing patterns, even when another approach looks slightly better in isolation. When you touch an area with duplicate patterns, unify one small instance in the same PR and say so in the description (recohere). A PR that lowers coherence is a defect, even if it works.
- Source: [The Decoherence Trap](https://maevyn.codes/decoherence/).

The sections below (Standard patterns, Find the existing pattern first, Review checklist) apply this one.

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
go run ./cmd/lapp/ <logfile> --no-open [--name <name>] [--addr 127.0.0.1:0]
go run ./cmd/lapp/ workspace create <topic>
go run ./cmd/lapp/ workspace add-log --topic <topic> <logfile>
go run ./cmd/lapp/ workspace add-log --topic <topic> --stdin
go run ./cmd/lapp/ workspace import gcp --topic <topic> --project <project> [--filter <filter>] (--since <duration> | --from <ts> --to <ts>) [--limit <n>]
go run ./cmd/lapp/ workspace import victorialogs --topic <topic> --url <base-url> [--query <LogsQL>] (--since <duration> | --from <ts> --to <ts>) [--limit <n>] [--account-id <id> --project-id <id>]
go run ./cmd/lapp/ workspace discover --topic <topic> [--model <model>]
go run ./cmd/lapp/ workspace analyze --topic <topic> [question] [--model <model>]
go run ./cmd/lapp/ web [--addr 127.0.0.1:0]
```

`lapp <logfile>` (or a pipe into `lapp`) opens the page in the default browser unless `--no-open` is given. Agents must always pass `--no-open`: the browser is the user's, and pages popping up while they work are disruptive. Open the printed URL with a headless browser instead.

Topic names are sanitized to lower-kebab-case. Workspaces live under `~/.lapp/workspaces/<topic>/`.

## Architecture

```
cmd/lapp/                CLI entrypoint (cobra commands: workspace create/add-log/import/discover/analyze)
pkg/logsource/           Read log files → channel of LogLine
pkg/multiline/           Detect log entry boundaries, merge continuation lines
pkg/pattern/             Drain-based log pattern discovery and template matching
pkg/semantic/            LLM-based semantic labeling of Drain patterns
pkg/workspace/           DiscoveryRun execution and run-scoped file writer
pkg/gcplog/              GCP Cloud Logging fetch for ImportRuns
pkg/victorialogs/        VictoriaLogs LogsQL fetch and stream listing for ImportRuns
pkg/store/               DuckDB storage primitives (not yet on the CLI discovery path)
pkg/config/              Model resolution (flag → $MODEL_NAME → default)
pkg/analyzer/            Agentic log analysis via eino ADK + ACP providers
integration_test/        Integration tests against Loghub-2.0 datasets
```

### DiscoveryRun (discover)

`add-log` copies the file into `logs/` (Grafana Explore CSV/JSON exports are normalized to NDJSON on the way in, ADR 0006) and never triggers discovery. `workspace import gcp` pulls a snapshot from GCP Cloud Logging through ADC credentials, lands it as enveloped NDJSON in `logs/`, and records provenance under `import-runs/<run-id>/record.json`; it never triggers discovery either. `workspace import victorialogs` lists the matching streams, maps each stream to a Service (its `service.name`, `service`, `app` or `job` label, else a short stream hash), and runs one ImportRun per Service into workspace `<topic>-<service>`, writing the raw VictoriaLogs NDJSON lines (`_msg`, `_time`) and the Service in the workspace `service` file; the ImportRun filter is narrowed to that Service's `_stream_id`s so it can be re-pulled. Each `workspace discover` starts a DiscoveryRun: reads ALL files in `logs/`, runs fresh Drain + semantic labeling, and writes run-scoped `patterns/` and `notes/`.
When `lapp web` starts, it marks any previous `QUEUED` or `RUNNING` DiscoveryRuns and ImportRuns as failed because those local workers no longer exist.
DiscoveryRun records persist structured `progress` and `error` fields; frontend code renders those facts into user-facing text.
The main page imports from VictoriaLogs into one new workspace per Service (`/api/demo/import-victorialogs`). The classic web Imports tab starts GCP or VictoriaLogs imports into the selected workspace through standard Operations, shows ImportRun history and final facts, and fills project and filter fields from recent queries across all workspaces.

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

Uses a token graph trained on 70+ timestamp formats. Lines are tokenized (first 60 bytes), matched against a directed graph of valid token transitions, and scored 0.0-1.0. Score > 0.5 means "new log entry". In the pipeline, only lines whose leading timestamp has the file's dominant `logheader.StartShape` start entries; entries are capped at 2000 lines or 1 MB. When fewer than 5% of a text file's lines (or fewer than 3) start an entry, the file is split line-by-line and the DiscoveryRun records `split_mode: PER_LINE` (ADR 0006).

### Analyzer

Runs an eino ADK agent (15 max iterations) with filesystem tools (grep, read_file, execute) against a structured workspace directory.

## Environment Variables

- `OPENROUTER_API_KEY`: Required for semantic labeling in `workspace discover`
- GCP ADC (`gcloud auth application-default login`): Required for `workspace import gcp`
- `VICTORIALOGS_BEARER_TOKEN`, or `VICTORIALOGS_USERNAME` + `VICTORIALOGS_PASSWORD`: Optional credentials for VictoriaLogs imports (CLI and web); never part of the URL or the ImportRun record
- `MODEL_NAME`: Override default LLM model (default: `google/gemini-3-flash-preview`)
- ACP provider credentials/login: Required for `workspace analyze` through the selected provider
- `.env` file is auto-loaded via godotenv

## Tech Stack

- Go, cobra CLI, go-drain3, DuckDB (duckdb-go/v2), cloudwego/eino ADK, OpenRouter semantic labeling, ACP providers

## Code Style

- `nolint` directives go on the line above the target, not as end-of-line comments
- Compile-time interface guards: `var _ MyInterface = (*MyImpl)(nil)`
- Always use `make build` to verify compilation, never bare `go build` (it drops a binary in the project root)

## Real-data acceptance

Unit tests on hand-made data have missed real problems: very long lines, file names, header lines, and multi-line entries in actual exports. So every new log source or input format (an import provider, an export format such as Grafana CSV or JSON, plain-text variants) is accepted only after an end-to-end run on a real sample:

- Run import → time range → volume chart → patterns → noise suggestions on a real export, redacted where needed, and post the results as a PR comment (not in the description, which stays short): expected vs actual entry count, time range, pattern count.
- Never commit real samples to this repository, even redacted. Keep them local. Test fixtures in the repo are hand-made with the same structure (headers, line endings, quoting, multi-line bodies, timestamp formats).

## Standard patterns

| Concern | Use |
|---|---|
| Error handling | `github.com/go-errors/errors` with `errors.Errorf("<verb> <object>: %w", err)` (`fmt.Errorf` is lint-forbidden); see `pkg/workspace/importrun.go`; RPC handlers return `connect.NewError(connect.Code…, err)`; see `pkg/webapp/service.go` |
| Config & env | Read env vars at the CLI edge and pass them down in `Config` structs; `.env` loaded once; see `cmd/lapp/main.go`, `cmd/lapp/web.go`; model via `config.ResolveModel`; see `pkg/config/model.go` |
| Persistence | Workspace files are the source of truth (ADR 0001): JSON records written via temp file + rename; see `pkg/workspace/importrun.go`. Legacy: DuckDB `pkg/store/duckdb.go` (only `pkg/ingest` and integration tests use it); do not extend |
| Workspace paths & IDs | Run paths: helpers/constants in `pkg/workspace` (`DiscoveryRunDir`, `ImportRunRecordPath`, `*DirName`); see `pkg/workspace/discovery.go`. Not yet unified (duplicate implementations, no shared entry point yet): the workspace root `~/.lapp/workspaces`, workspace creation, and ID sanitizing exist in both `cmd/lapp/workspace.go` and `pkg/webapp/service.go`; consolidate into one when touched, don't add a third |
| Logging | Current: `log/slog` with key/value attrs; see `pkg/workspace/pipeline.go`; Legacy: stdlib `log.Printf` in `pkg/tape/eino_handler.go`; migrate when touched |
| Tracing | `otel.Tracer("lapp/<pkg>").Start` + `span.RecordError`; see `pkg/semantic/labeler.go`; setup in `pkg/tracing/otel.go` |
| Retries & timeouts | LLM batches: bounded attempts, per-attempt `context.WithTimeout`, overridable delay var; see `pkg/workspace/pipeline.go`; Google APIs: `gax.WithRetry`/`gax.WithTimeout`; see `pkg/gcplog/fetch.go` |
| HTTP | Injectable `*http.Client`, default `otelhttp` transport with timeout; see `pkg/semantic/labeler.go`; handlers wrapped in `otelhttp.NewHandler`; see `pkg/webapp/server.go` |
| LLM / agent calls | Labeling: eino OpenRouter model behind `workspace.PatternLabeler`; see `pkg/semantic/labeler.go`; analysis: eino ADK + ACP; see `pkg/analyzer/analyzer.go` |
| Structured log lines | Current: NDJSON detect + message extraction (ADR 0006); see `pkg/ndjson/extract.go`; Legacy: `pkg/event/parser.go` (only `pkg/ingest` uses it); do not extend |
| Run progress & errors | Current: structured `Progress`/`Error`, text rendered in the frontend; see `pkg/workspace/discovery.go`, `frontend/src/main.tsx`; Legacy: deprecated `progress_message`/`error_message` in `proto/lapp/web/v1/web.proto`; stop writing when touched |
| Long-running work | Create returns `google.longrunning.Operation` (AIP 151), client polls `GetOperation`; see `pkg/webapp/operations_service.go`, `frontend/src/store.ts` |
| Frontend | Generated Connect clients from `frontend/src/api.ts`; state in the zustand store `frontend/src/store.ts` |
| Testing | stdlib `testing` (no testify), `t.TempDir()`, fakes via function-typed config fields; see `pkg/webapp/import_service_test.go`; golden pairs `testdata/<case>.input.*` + `.expected.txt`; see `pkg/ndjson/extract_test.go` |

## Find the existing pattern first

- Before writing new code, search the repo for how the same kind of thing is already done (the table above is the starting point) and follow it.
- Introducing a new pattern requires a justification in the PR description. If it replaces or overlaps an existing one, the same change must migrate all existing callers and delete the old pattern; no side-by-side "new way" plus "old way". Genuinely new resources or capabilities with no existing counterpart are not affected.
- Every PR description includes a line `Reused pattern: <pattern> (see <path>)`, or `Reused pattern: none; <justification>`.

## Review checklist

Reviewers must check whether the PR introduces:

- a new concept (a domain term not in `CONTEXT.md`, a new resource, a new package with overlapping responsibility),
- a new way of doing something the repo already does (errors, logging, config, persistence, retries, HTTP, LLM calls, tests), or
- a duplicate model or type (a struct, proto message, or TS type mirroring an existing one).

If any answer is yes and the new thing replaces or overlaps an existing one without migrating all callers and deleting the old way, request changes.
