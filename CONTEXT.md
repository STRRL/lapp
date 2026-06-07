# LAPP

LAPP helps people investigate logs by turning raw log files into organized investigation material for humans and AI agents.

## Language

**Investigation Workspace**:
A container for one log investigation. It groups uploaded log files, discovered log patterns, error-focused notes, representative samples, and AI analysis context.
_Avoid_: Project, incident

**Dashboard**:
The main entry screen where people find and open investigation workspaces. It is an entry point, not the primary analysis surface.

**DiscoveryRun**:
One execution that turns the current log files in a workspace into discovered patterns and generated investigation material. It covers both the first discovery and later rediscovery after log files change.
_Avoid_: Build, rebuild
