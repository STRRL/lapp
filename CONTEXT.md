# LAPP

LAPP helps people investigate logs by turning raw log files into organized investigation material for humans and AI agents.

## Language

**Investigation Workspace**:
A container for one log investigation. It groups uploaded log files, discovered log patterns, error-focused notes, representative samples, and AI analysis context.
_Avoid_: Project, incident

**Dashboard**:
The main entry screen where people find and open investigation workspaces. It is an entry point, not the primary analysis surface.

**Run**:
One tracked background execution inside a workspace, with a queued, running, then succeeded or failed lifecycle. DiscoveryRun and ImportRun are kinds of Run. Runs never trigger each other; people decide what to run next.
_Avoid_: Task, job

**DiscoveryRun**:
One execution that turns the current log files in a workspace into discovered patterns and generated investigation material. It covers both the first discovery and later rediscovery after log files change. It starts only when a person asks for it.
_Avoid_: Build, rebuild

**ImportRun**:
One execution that pulls logs from an external provider using a query and time range, and materializes the result as a log file in the workspace. The workspace keeps the import record for provenance and re-pull; there is no persistent connection to the provider.
_Avoid_: Sync, connection, integration, LogImport

**Provider**:
An external logging service LAPP can import from, such as GCP Cloud Logging or Vercel. LAPP uses credentials already present on the machine and never manages provider authentication itself.
_Avoid_: Source, backend

**Projection**:
The text line derived from a structured log entry for pattern discovery. Discovery reads the projection; investigation material keeps the full structured entry.
_Avoid_: Flattening, rendering
