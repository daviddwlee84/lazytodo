# Additional source adapters

Status: deferred. GitHub Issues, project-scoped GitLab Work Items and team-scoped Linear issues are the native readers; the existing canonical snapshot reader is a transport bridge, not native compatibility with every export format.

## Linear (shipped 2026-11-04)

Current behavior lives in [docs/sources.md](../docs/sources.md#linear). Schema facts were checked against Linear's published SDK schema (`linear/linear` `packages/sdk/src/schema.graphql`, master, 2026-11-04): `Issue.delegate: User` (the agent delegated to work on an issue), `Issue.trashed`, `includeArchived` and `orderBy: PaginationOrderBy {createdAt, updatedAt}` on `issues`, `StringComparator.eqIgnoreCase`, and `WorkflowState.type` ∈ triage/backlog/unstarted/started/completed/canceled/duplicate. Personal keys use `Authorization: <key>` without Bearer. Rate limits: 429 + `RATELIMITED`, `Retry-After` seconds, `X-RateLimit-*-Reset` in epoch milliseconds.

Open follow-ups: team discovery and setup (TODO "Linear discover and setup"); per-issue label overflow beyond 50 currently fails the fetch; an OAuth or keychain credential source would need a separate review; a live-workspace check is still pending. Incremental `updatedAt` deltas belong with "Incremental native fetch".

## Beads / td

First add explicit-file converters for Beads JSONL exports and td JSON exports. Require a persistent store namespace and honest export observation time/completeness. Do not guess freshness from `.beads/issues.jsonl` existence, execute exporters automatically, or open either authoritative database.

Static research on 2026-11-02: installed td 0.69.0 corresponds to `359fd41c17729cff3e5c23ba5478b44cc162096f`; ordinary list/show/export open a writable DB and migrate, with possible autosync/analytics. Disabling sync/analytics does not eliminate DB-side effects. Beads 1.3.2 (`9b65b9e198acbcc98e7fd3a87a5d55baa065a572`) has a useful `--readonly` mode, but it rejects proxied-server mode and differs from newer revisions. Native polling requires hermetic before/after filesystem and backend/version tests, not just a command named list. See upstream `cmd/root.go`/`internal/db` (td) and `cmd/bd/main.go` (Beads).

Export semantics differ: td export JSON wraps issue plus logs/handoffs/dependencies/files; Beads export is typed JSONL (issue or memory). `--all` has different meanings between list and export. Preserve declared coverage and ignore non-work-item records explicitly.

## Super Productivity (shipped 2026-11-04)

Current behavior lives in [docs/sources.md](../docs/sources.md#super-productivity). Facts checked against the app source at v19.3.1 (2026-11-04): the listener binds `127.0.0.1:3876` with a Host-header allowlist and rejects web Origins; `GET /tasks` filters `source` (active|archived|all) and `includeDone` in memory with no pagination; envelope `{ok,data}` / `{ok:false,error:{code}}`; the renderer times out at 15 s; `/projects` returns `projectService.list$` and `/tags` returns `tagService.tags$`. Task timestamps are epoch milliseconds (`created`, `updated` or legacy `modified`, `doneOn`, `wontDoOn`).

Open follow-ups: a per-project scope (`scope = <projectId>`) if one profile mixes work and personal tasks; due/deadline dates have no model field yet; a live check against a running app is still pending. The API is read/write, so lazytodo uses only the four GET routes above.

## Google Tasks / Apple Reminders

Google: request tasks.readonly scope; tasklist-scoped IDs, all pages, completed/hidden/deleted flags. No labels; due is date-only, not a precise deadline.

Apple: research macOS EventKit bridge and actual Reminders permissions/packaging. Do not promise generic iCloud/iOS REST access. No account data was queried or imported during this work.

## personal-ops

Only an explicitly selected, minimal store/export reader. Preserve schema_version/store_id/item ID and native state. Do not import its complete private context via overview just to render task titles; do not change its schema or treat roles as shell APIs.

## GitHub / GitLab refresh cost and paging drift

Review of 2026-11-04: every refresh re-reads complete history; GitHub `/issues` also returns pull requests (filtered client-side), with no `since=`. A repository with tens of thousands of issues+PRs needs hundreds of requests per refresh and may never finish within the two-minute/64 MiB per-source budget, leaving the source unknown with a generic deadline error. GitHub paging now uses `sort=created&direction=asc`, so new issues append rather than duplicating a boundary item (which failed snapshot validation); a deletion/transfer during a fetch can still shift one item past a page boundary and be skipped while the snapshot is marked complete. Entry criteria for an incremental design: `since=`-style deltas with periodic full reconciliation (deletions are invisible to deltas), a PR-free read path (search API or GraphQL `issues` connection with cursors), and timeout errors that name the source and limit. Indexed from [TODO](../TODO.md) "Incremental native fetch".

Every adapter needs fixtures for pagination, unknown vs empty fields, stale preservation, and source-safe reads before being advertised. Indexed from [TODO](../TODO.md).
