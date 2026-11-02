# Additional source adapters

Status: deferred. GitHub Issues and project-scoped GitLab Work Items are the first native readers; the existing canonical snapshot reader is a transport bridge, not native compatibility with every export format.

## Next: Linear

Preserve workspace/team scope, native status and labels. [Assignment documentation](https://linear.app/docs/assigning-issues) distinguishes human responsibility from agent delegation. `assignees` and `delegates` must remain separate; this documentation does not establish API field names. Verify GraphQL schema, pagination, authentication, rate limiting and archived coverage before implementation.

## Beads / td

First add explicit-file converters for Beads JSONL exports and td JSON exports. Require a persistent store namespace and honest export observation time/completeness. Do not guess freshness from `.beads/issues.jsonl` existence, execute exporters automatically, or open either authoritative database.

Static research on 2026-11-02: installed td 0.69.0 corresponds to `359fd41c17729cff3e5c23ba5478b44cc162096f`; ordinary list/show/export open a writable DB and migrate, with possible autosync/analytics. Disabling sync/analytics does not eliminate DB-side effects. Beads 1.3.2 (`9b65b9e198acbcc98e7fd3a87a5d55baa065a572`) has a useful `--readonly` mode, but it rejects proxied-server mode and differs from newer revisions. Native polling requires hermetic before/after filesystem and backend/version tests, not just a command named list. See upstream `cmd/root.go`/`internal/db` (td) and `cmd/bd/main.go` (Beads).

Export semantics differ: td export JSON wraps issue plus logs/handoffs/dependencies/files; Beads export is typed JSONL (issue or memory). `--all` has different meanings between list and export. Preserve declared coverage and ignore non-work-item records explicitly.

## Super Productivity

[Local REST API](https://github.com/super-productivity/super-productivity/wiki/3.01-API#3-local-rest-api): opt-in Electron desktop service at loopback port 3876, Bearer token, GET tasks/projects/tags. Not web/mobile. Include completed/archive coverage deliberately; a closed desktop app means unavailable plus retained cache, not empty. Confirm loopback security/token storage and response limits.

## Google Tasks / Apple Reminders

Google: request tasks.readonly scope; tasklist-scoped IDs, all pages, completed/hidden/deleted flags. No labels; due is date-only, not a precise deadline.

Apple: research macOS EventKit bridge and actual Reminders permissions/packaging. Do not promise generic iCloud/iOS REST access. No account data was queried or imported during this work.

## personal-ops

Only an explicitly selected, minimal store/export reader. Preserve schema_version/store_id/item ID and native state. Do not import its complete private context via overview just to render task titles; do not change its schema or treat roles as shell APIs.

Every adapter needs fixtures for pagination, unknown vs empty fields, stale preservation, and source-safe reads before being advertised. Indexed from [TODO](../TODO.md).
