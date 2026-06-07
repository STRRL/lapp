# Protobuf schema-first API with Connect and AIP resources

LAPP Web uses protobuf service definitions as the API contract, served through Connect RPC and consumed by generated Go and TypeScript code. API shapes should follow AIP resource-oriented design where it fits, while using custom methods for actions such as starting a discovery run that do not fit standard CRUD semantics.

The first API resources use simple names: `Workspace`, `LogFile`, `DiscoveryRun`, and `Pattern`. Pattern resources belong to the discovery run that produced them. Pattern resource IDs use the generated `semantic_id`, which must be unique within a discovery run; duplicate semantic IDs are resolved with a numeric suffix.

Discovery runs are started with `CreateDiscoveryRun`. Progress steps use user-facing stage names: reading logs, merging entries, discovering patterns, labeling patterns, and writing results.

Uploading or deleting log files does not automatically start discovery. Discovery is an explicit action and can be started whenever the user chooses.

Each workspace may have at most one running discovery run at a time because discovery writes generated workspace results.

Log files cannot be uploaded or deleted while a discovery run is running for the same workspace.

Uploading a log file with a name that already exists in the workspace is rejected. The app does not auto-rename or overwrite log files.

Discovery run history is retained so failures and prior runs can be inspected. The first UI may show only the latest run by default.

DiscoveryRun IDs use UUIDv7. Unlike Pattern IDs, DiscoveryRun IDs are operational record identifiers, not user-facing semantic handles.

The first version reports discovery progress through polling `GetDiscoveryRun`, not streaming.

Discovery results belong to the discovery run that produced them. Workspace views may present a selected discovery run's results instead of treating generated results as anonymous global workspace state.

By default, workspace result views show the latest successful discovery run. Users may later select a different discovery run to inspect its results.

DiscoveryRun is a one-time task execution, not a synchronizer. Changes to workspace log files after a run completes do not update that run or its results; users start another DiscoveryRun to produce new results from the current workspace inputs.

Discovery runs do not record input log file snapshots. Historical run inspection is based on the run status and produced results, not on reconstructing the exact input file state at run time.

Deleting a log file does not delete prior discovery run results. The deletion affects future discovery runs only.

Workspace status uses a small first-version set: empty, discovering, ready, and failed. The first version does not expose a stale status.

Errors are not separate API resources. Error views are filters or summaries over patterns and unmatched log lines.

Raw log lines are not separate API resources. They are returned by log file reading and search methods with file name, line number, content, and optional matched pattern context.

**Considered Options**

- Protobuf schema-first API with Connect RPC and AIP-style resources
- Hand-written REST JSON handlers
- RPC endpoints without resource-oriented naming

**Consequences**

API changes start in `.proto` files, not in ad hoc HTTP handlers. Resource names, standard methods, and custom methods need to be designed deliberately because they become the contract between the embedded frontend and the local Go server.
