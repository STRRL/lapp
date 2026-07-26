# LAPP Web App Plan

LAPP Web is a local web app for working with local LAPP workspaces. It starts from `lapp web`, serves an embedded React/Vite/TypeScript frontend, and reads/writes the same local workspace files used by the CLI.

## Product Shape

- Dashboard is a simple entry point for listing, creating, opening, and permanently deleting workspaces.
- Workspace detail is the primary analysis surface.
- Discovery is explicit. Uploading or deleting log files does not automatically start discovery.
- Discovery runs are asynchronous, one time task executions. Create returns a standard long running Operation. A completed run is not synchronized with later log file changes.
- Workspace views default to the latest successful discovery run.
- DiscoveryRun creation requires at least one log file. A parallel run request returns `ABORTED`.
- If the web server starts and finds local `QUEUED` or `RUNNING` DiscoveryRuns from a previous process, it marks them as failed because no worker is still attached to them.
- Semantic labeling runs in batches of 25 patterns with default concurrency 12, a per-batch timeout, and up to 3 attempts per batch.
- DiscoveryRun records store structured `progress` and `error` fields. Backend code records facts; frontend code renders user-facing text.

## Implementation Shape

- Frontend state is managed with Zustand.
- React components render selected state and dispatch actions; workspace/detail/result loading rules live in the store.
- Server state still comes from the local Connect RPC API and the local workspace files under `~/.lapp/workspaces`.
- `make build` generates embedded frontend assets before compiling `output/lapp`.
- Vite writes hashed production assets under `pkg/webapp/static/app/`; that directory is ignored by git and can be removed with `make clean`.
- `pkg/webapp/static/index.html` is a stable fallback kept in git so Go embedding still works before production assets are generated.

## First Scope

### Dashboard

- List workspaces.
- Create an empty workspace.
- Open a workspace.
- Permanently delete a workspace after confirmation.

### Workspace Logs

- List log files.
- Upload log files.
- Reject uploads when a log file with the same name already exists.
- Delete log files.
- Start a discovery run.
- Show latest discovery status and progress.
- Disable log upload, log deletion, and starting another discovery while a discovery run is running.

### Patterns

- List patterns from the selected or latest successful discovery run.
- Open a pattern detail view.
- Show semantic ID, description, template, counts, and samples.

### Errors View

- Show error-like patterns and unmatched error lines as a view over discovery results.
- Do not model errors as independent resources.

## Out of Scope For First Version

- AI Ask / AnalysisRun UI.
- Raw log full viewer.
- Cross-workspace statistics.
- Editing generated results.
- User annotations.
- Discovery cancellation.
- Streaming discovery progress.
- Trash/archive for deleted workspaces.
