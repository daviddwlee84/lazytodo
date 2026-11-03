# TODO

Future candidates, not commitments. Current behavior lives in [README.md](README.md) and [docs/](docs/). Priority and effort are independent; `P?` requires evaluation before scheduling.

## P1
- [ ] **[M] Beads / td snapshot converters** — Explicit-file conversion only; preserve namespace, observation time and coverage. → [research](backlog/source-adapters.md)
- [ ] **[L] Linear reader** — Preserve human assignee versus agent delegate; verify GraphQL and workspace/team scope. → [research](backlog/source-adapters.md)

## P2
- [ ] **[M] Super Productivity reader** — Opt-in desktop loopback REST, completed/archive coverage and stale offline cache. → [research](backlog/source-adapters.md)
- [ ] **[M] personal-ops selected-store reader** — Explicit minimal file/export contract; do not load private context or activate roles. → [research](backlog/source-adapters.md)
- [ ] **[L] Explicit intake and dispatch** — One reviewed destination, idempotency, native receipt and uncertain-outcome handling. → [research](backlog/routing.md)
- [ ] **[L] Google Tasks reader** — Read-only OAuth, tasklist identities, hidden/completed/deleted coverage, date-only due. → [research](backlog/source-adapters.md)
- [ ] **[M] Public installation and upgrades** — Confirm module/remote, version provenance, supported installation and upgrade/check path. → [research](backlog/scaling.md)
- [ ] **[M] Incremental native fetch** — Every refresh re-reads full history (GitHub /issues also returns PRs) under a 2-minute/64 MiB budget; evaluate since=/cursor paging, PR-free reads and timeout errors that name the limit. → [research](backlog/source-adapters.md)
- [ ] **[M] Search grammar: assignee, negation, OR** — Assignees are fetched but not searchable; repeated state:/source: filters silently AND to empty. Keep exact native spelling and shared service predicates.
- [ ] **[S] fetch --force past failure backoff** — After re-login an explicit fetch still reports backoff until the retry window ends; report backoff skips separately from failures.

## P3
- [ ] **[L] Measured scaling and remote surfaces** — Evaluate incremental fetch, indexing (long-lived DB handle, SQL ORDER/LIMIT, FTS for per-keystroke search), a thin MCP wrapper and pin sync only against demonstrated needs. → [research](backlog/scaling.md)
- [ ] **[S] Built-in view resolution in service** — All/Open/Pinned are resolved in both internal/cli/root.go and internal/tui/model.go; move to service and reserve Open consistently in config validation.
- [ ] **[S] Explicit cache prune** — Snapshot writes no longer retire sources absent from the writer's config (shared cache across --config files); offer an explicit, reviewed prune of unconfigured cached sources.

## P?
- [ ] **[?/L] Native Beads / td readers** — Version/backend and filesystem side-effect trials before any periodic native invocation. → [research](backlog/source-adapters.md)
- [ ] **[?/L] Apple Reminders bridge** — Research macOS EventKit permissions and packaging; not a generic iCloud REST API. → [research](backlog/source-adapters.md)
- [ ] **[?/M] Optional translation and repo language conventions** — Evaluate daviddwlee84/translate for local display or reviewed English drafts; preserve original and require opt-in for external processing. → [research](backlog/translation.md)

## Done
