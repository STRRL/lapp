---
name: lapp-log-analysis
description: "Analyze log files using LAPP (Log Auto Pattern Pipeline). Use this skill when the user wants to investigate logs, find error patterns, diagnose issues from log files, or do any kind of log analysis. Triggers on phrases like 'analyze logs', 'check these logs', 'what's wrong in this log', 'investigate log', 'find errors in logs', or when the user provides a log file and wants to understand what happened."
argument-hint: "[path to log file or description of what to investigate]"
---

# LAPP Log Analysis

Analyze log files by discovering patterns, labeling them semantically, and building a structured workspace that coding agents can explore.

## Prerequisites

- `lapp` binary must be available in PATH (or built via `make build` in the lapp repo, output at `output/lapp`)
- `OPENROUTER_API_KEY` environment variable must be set (for LLM-based semantic labeling and analysis)

## Workflow

### Step 1: Create a workspace (required)

Pick a short, descriptive topic name for the investigation. Topic names are automatically sanitized to lower-kebab-case.

```bash
lapp workspace create <topic>
```

Example: `lapp workspace create k8s-pod-crash`

This creates the directory structure at `~/.lapp/workspaces/<topic>/`.

### Step 2: Ingest log files (required)

Feed one or more log files into the workspace. Each `add-log` triggers a full rebuild: reads ALL files in `logs/`, runs Drain clustering + LLM semantic labeling, and regenerates the `patterns/` and `notes/` directories.

From a file:
```bash
lapp workspace add-log --topic <topic> <logfile>
```

From stdin (useful for piping from kubectl, docker, journalctl, etc.):
```bash
kubectl logs my-pod | lapp workspace add-log --topic <topic> --stdin
```

You can call `add-log` multiple times to add more log files. Each call rebuilds the entire workspace from all ingested logs.

To override the default LLM model:
```bash
lapp workspace add-log --topic <topic> <logfile> --model <model>
```

### Step 3: Explore and analyze

After ingestion, the workspace at `~/.lapp/workspaces/<topic>/` contains a structured breakdown of the logs. There are two ways to analyze:

#### Option A: Let LAPP's built-in AI agent analyze

```bash
lapp workspace analyze --topic <topic> "your question here"
```

The agent has filesystem tools (grep, read_file, execute) and will investigate the workspace to answer your question.

#### Option B: Explore the workspace directly

List all workspaces to find the directory:
```bash
lapp workspace list
```

Then explore the workspace directory structure yourself:

```
~/.lapp/workspaces/<topic>/
├── logs/                    # Raw log files (as ingested)
├── patterns/                # One directory per discovered pattern
│   ├── <semantic-id>/       # e.g. "connection-timeout"
│   │   ├── pattern.md       # Template, count, description, first/last seen
│   │   └── samples.log      # Up to 20 representative log lines
│   └── unmatched/
│       └── samples.log      # Lines that didn't match any pattern
├── notes/
│   ├── summary.md           # Overview: file count, patterns, samples
│   └── errors.md            # Error patterns and error lines
└── AGENTS.md                # Context guide for AI agents
```

Start with `notes/summary.md` for an overview, then drill into specific `patterns/<id>/` directories for details. The `errors.md` file is especially useful for quickly finding error-related patterns.

This approach is ideal for coding agents (Claude Code, Codex, etc.) that can freely navigate the filesystem and form their own investigation strategy.

## Tips

- **Topic naming**: Use descriptive names like `api-gateway-5xx`, `auth-service-oom`, `deploy-2024-03-15`. They become directory names.
- **Multiple log sources**: You can ingest logs from different sources into the same workspace. The pipeline processes all files in `logs/` together, finding cross-file patterns.
- **Iterative investigation**: Add more logs and re-analyze as you narrow down the issue. The workspace rebuilds cleanly each time.
- **Pattern counts**: Patterns with high counts are "normal" behavior. Focus on patterns in `errors.md` or low-count patterns that might indicate anomalies.
