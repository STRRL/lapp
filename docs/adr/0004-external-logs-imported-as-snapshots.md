# External logs are imported as snapshots, not live connections

LAPP can pull logs from external providers (first: GCP Cloud Logging), but an ImportRun always materializes the result as a log file inside the workspace, after which the pipeline treats it exactly like an uploaded file. There is no persistent connection entity, no continuous tailing, and no LAPP-managed provider authentication: each import is a one-off recorded action, and credentials are whatever already exists on the machine (ADC for GCP).

**Considered Options**

- Snapshot import: pull a query + time range, land it as a local file
- Live connection: workspace holds a remote source, discovery and analysis query the provider on demand

**Consequences**

ADR 0001 (local workspaces as source of truth) stays intact, and investigations remain reproducible after the provider's retention window expires. The ImportRun record (provider, project, filter, time range, truncation flag) is the provenance for the imported file and the basis for re-pulling with a different time range; recall of past queries across workspaces is a derived view over these records, not a stored entity. Continuous-monitoring use cases are explicitly out of scope.
