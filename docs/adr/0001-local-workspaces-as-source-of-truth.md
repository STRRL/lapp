# Local workspaces are the source of truth

LAPP stores investigation data in local workspace directories under the user's LAPP workspace root. The web app is a local application that reads and writes those workspaces directly instead of introducing a separate application database, so the CLI and web UI share the same durable investigation records.

**Considered Options**

- Local workspace directories as the source of truth
- A web-app-owned database with the workspace files as exports or derived views

**Consequences**

The web app must treat the workspace file structure as a product contract, not an incidental CLI detail. Schema changes to workspace files need compatibility handling because they affect both CLI and web UI users.

Discovery run history is stored inside each workspace under `discovery-runs/`. Each run owns its generated results under its own directory, for example `discovery-runs/<run-id>/patterns/` and `discovery-runs/<run-id>/notes/`.

New CLI and web writes use the DiscoveryRun structure. Existing top-level `patterns/` and `notes/` directories may be read for compatibility during migration, but they are not the long-term primary generated result location.
