# Funnel — Task Context

This folder contains context for continuing implementation across AI model sessions.

## Files

| File | Description |
|------|-------------|
| `README.md` | This file — navigation guide |
| `context.md` | Full project context: architecture, existing code, decisions made |
| `plan.md` | What needs to be built — detailed implementation plan |
| `codebase-snapshot.md` | Key existing files with their signatures/content |

## How to Use

1. Read `context.md` first — understand what exists and why
2. Read `plan.md` — understand what needs to be built and in what order
3. Reference `codebase-snapshot.md` when you need exact signatures of existing code

## Current Status

- **Phase 0 (DONE)**: CLI + IPC daemon fully implemented
- **Phase 1 (DONE)**: Queue management, pause/resume/stop/remove, state persistence
- **Phase 2 (DONE)**: StorageInfo in status, version command, CI/CD, short-ID resolution
- **Phase 3 (TODO)**: `funneld` standalone, `funnel-manager`, `funnel-worker` — see `plan.md`
