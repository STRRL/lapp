# Go local server with embedded frontend assets

LAPP Web is started by `lapp web`, which runs a local Go HTTP server and serves a React, Vite, and TypeScript web UI from frontend assets embedded in the LAPP binary. This keeps the user-facing distribution as a single local tool while allowing the web UI to call the same workspace and discovery code used by the CLI.

**Considered Options**

- Go local server with embedded frontend assets
- Separate frontend and backend processes at runtime
- Desktop shell around the web UI
- Server-rendered HTML without a client-side app

**Consequences**

Release builds must include the frontend build output in the Go binary. Development may still use a separate frontend dev server, but production use should not require Node.js or a second long-running process.
