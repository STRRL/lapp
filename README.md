# LAPP - Log Auto Pattern Pipeline

LAPP turns raw log files into a structured investigation workspace for humans and AI agents.

It clusters repeated log lines with Drain, asks an LLM to assign semantic IDs to repeated patterns, then writes a file-based workspace with pattern notes, samples, error summaries, and agent instructions.

## Quick Start

```bash
# Build
make build

# Clean, rebuild, and start the local web app
make dev

# Create a workspace
go run ./cmd/lapp/ workspace create app-incident

# Start the local web app
go run ./cmd/lapp/ web

# Add logs to the workspace (pure copy, no discovery)
go run ./cmd/lapp/ workspace add-log --topic app-incident /var/log/syslog

# Run discovery over all log files in the workspace
go run ./cmd/lapp/ workspace discover --topic app-incident

# AI-powered analysis (agent backend via ACP provider)
go run ./cmd/lapp/ workspace analyze --topic app-incident "why are there connection timeouts?" --acp claude
go run ./cmd/lapp/ workspace analyze --topic app-incident "what failed?" --acp codex
```

## How It Works

The current CLI product is a structured file workspace under `~/.lapp/workspaces/<topic>/`.

```
workspace add-log
  -> copy input into logs/ (nothing else)

workspace discover
  -> read all files in logs/
  -> merge multiline entries
  -> discover repeated Drain patterns
  -> label repeated patterns with retried LLM batches
  -> write discovery-runs/<run-id>/patterns/, notes/, and AGENTS.md
```

**Core idea**: Drain clusters logs into templates cheaply (no API cost), then LLM semantifies the templates in bounded batches. This follows the IBM "Label Broadcasting" pattern — cluster first (90%+ volume reduction), apply LLM to representatives, broadcast labels back.

Generated workspace layout:

```text
~/.lapp/workspaces/<topic>/
  logs/                         # copied source logs
  discovery-runs/<run-id>/
    run.json                    # status, progress, summary counts
    patterns/<semantic-id>/
      pattern.md                # metadata, counts, source line refs
      samples.log               # representative matching lines
    patterns/unmatched/samples.log
    notes/summary.md            # frequency-sorted overview
    notes/errors.md             # error/warning-focused view
    AGENTS.md                   # instructions for AI-assisted investigation
  AGENTS.md                     # initial workspace note before discovery
```

DiscoveryRuns are one-time local tasks. If `lapp web` starts and finds a previous `QUEUED` or `RUNNING` run on disk, it marks that run as failed because no worker is still attached to it.

## Environment Variables

- `OPENROUTER_API_KEY`: Required for semantic labeling in `workspace discover`
- `MODEL_NAME`: Override default LLM model (default: `google/gemini-3-flash-preview`)
- Provider-specific auth for ACP agent CLI (for example Claude/Codex/Gemini CLI login credentials)
- `.env` file is auto-loaded

## Commands

| Command | Description |
|---|---|
| `workspace create <topic>` | Create a workspace under `~/.lapp/workspaces/` |
| `workspace list` | List all workspace topics |
| `workspace add-log --topic <topic> <file>` | Copy a log file into the workspace |
| `workspace import gcp --topic <topic> --project <p> --since 1h` | Import a snapshot from GCP Cloud Logging |
| `workspace discover --topic <topic>` | Run pattern discovery over all log files |
| `workspace analyze --topic <topic> [question]` | Run AI analysis (`--acp claude|codex|gemini`) |

## Event Schema

The initial normalized event contract is defined in [proto/lapp/event/v1/event.proto](proto/lapp/event/v1/event.proto) and documented in [docs/event-schema-v1.md](docs/event-schema-v1.md). Representative fixtures live under `fixtures/events/v1/` for JSON, logfmt, `key=value`, and plain text logs.

The event parser and DuckDB store packages are library-level building blocks. They are covered by tests and integration tests, but `workspace discover` currently writes DiscoveryRun results directly into the local workspace instead of persisting through DuckDB first.

## Development

```bash
make build                           # Build embedded web assets and output/lapp
make clean                           # Remove generated build artifacts
make dev                             # Clean, build, and start lapp web on 127.0.0.1:8080
make proto-gen                       # Generate protobuf/Connect code
make test                            # Run unit and integration tests
make check                           # Run formatting, linting, type checks, build, and unit tests

LOGHUB_PATH=/path/to/2k_dataset \
  go test -v ./integration_test/...  # Integration tests (14 Loghub-2.0 datasets)
```

Override the local web address with `WEB_ADDR=127.0.0.1:3000 make dev`.

## Roadmap

See [Issue #2](https://github.com/STRRL/lapp/issues/2) for full vision and progress.

### Current

Structured workspace generation, multiline merging, Drain pattern discovery, LLM semantic labeling, ACP-backed agent analysis, event parsing, DuckDB storage primitives, and Loghub integration tests.

### Next: Log Viewer

Color-coded log viewer with template filtering. Each semantic template gets a distinct color. Unmatched/leftover logs shown in gray.

### Future

- Persist discovery inputs/results through the event store
- Iterative refinement (re-discover patterns from leftover)
- Per-template statistics and trend detection
- Real-time discovery progress
- MCP server for LLM agent access

## License

MIT
