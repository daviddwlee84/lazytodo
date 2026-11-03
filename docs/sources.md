# Sources and compatibility

Normal fetch contacts configured sources only. Run bare `sources --json` for cached status, not a live probe. Explicit `init` or `sources discover` additionally reads account/repository metadata from the user-selected host, without fetching task content. Run `fetch` explicitly or allow TUI's configured interval after registration. Forge authentication stays with native tools; install/login separately for the exact hostname. Linear and Super Productivity, which have no native CLI, read a token from an environment variable that the config names (`token_env`). No tokens belong in lazytodo TOML.

## Source initialization

`lazytodo init` is an explicit terminal-only wizard: choose hosts → discover candidates → select → review → save. Host defaults come from existing configured sources, recognized host environment variables, or public defaults; the selected hosts remain visible/editable before any network call. Missing tools and failed logins get install/authentication/manual instructions, never an embedded credential form. One failing host does not discard another host's candidates.

GitHub discovery uses `GET /user` and `/user/repos` with owned/collaborator/organization-member affiliations, newest `updated_at` first. GitLab uses `GET /user` and `/projects?membership=true&archived=false&order_by=last_activity_at&sort=desc`. Dates describe repository/project metadata/activity, not the newest issue. The selected native login and credentials remain with gh/glab. No `auth status --show-token`, token extraction, or native login is invoked.

Discovery has a 45-second overall timeout, 64 MiB response budget, maximum 2000 inspected records and 1–1000 eligible candidates per host (wizard/CLI default 200). Limits/errors return labeled partial results; unknown/unavailable is not an empty complete inventory. Archived and explicitly issue-disabled entries are excluded. Other known readable scopes can be registered manually with `sources add`; visibility alone is not blanket registration authorization.

Saving appends source tables to the selected config with a cooperative writer lock, a content/target check and atomic rename. Existing comments, views, settings and file symlinks are preserved. Duplicate targets are skipped, including disabled ones; enabling them remains a separate edit. Direct editors do not honor the cooperative lock, so the file is checked again immediately before replacement. Incompatible inline-array TOML shapes are rejected with a manual-edit instruction instead of being reserialized. Cancel before Save creates no configuration or cache. Setup never fetches issues; use the printed fetch command afterward.

## GitHub

`kind = "github"`, `host` defaults to github.com, `scope = "owner/repository"`.

Uses `gh api --hostname HOST --method GET --include` with the repository Issues endpoint, `state=all`, 100/page, following the pagination Link's page number while retaining the configured host/path. It excludes pull_request records. Labels and assignees are fully represented by REST arrays; delegates remain unknown/unread. Uses native global issue ID, not issue number.

Read/API permissions for the repository are required. Rate limiting, access denied and not-found failures retain old data; not-found is not automatically deletion. No search endpoint cap, auto-repo discovery, Projects fields, comments, PRs or incremental since-based reconciliation.

Reference: [GitHub Issues REST](https://docs.github.com/en/rest/issues/issues?apiVersion=2022-11-28).

## GitLab

`kind = "gitlab"`, explicit `host`, `scope = "group/subgroup/project"`.

Uses `glab api ... graphql --method POST --input - --include` for fixed queries. Project `workItems(first:100, state:all, after:...)` supplies core fields plus WorkItemWidgetLabels and WorkItemWidgetAssignees. Remaining widget pages use the same item's global WorkItem ID. Missing optional widgets remain null (unknown/unsupported), not confirmed-empty collections. HTTP 200 GraphQL errors invalidate the whole attempted snapshot. Native URL is constructed as `https://HOST/SCOPE/-/work_items/IID`.

**Required capabilities, not a blanket version promise:** project WorkItems connection, all-state filtering, WorkItem global IDs, core fields, widget fragments, and pageInfo/cursors. Missing schema capability reports unsupported and preserves cache. There is no REST Issues fallback. Group-level epics and discovery across namespaces are not included. Native CLI should be authenticated for that deployment's configured protocol/TLS; the initial URL opener assumes HTTPS.

Fixture tests verify the actual fixed query/data shapes against researched official declarations, not a live GitLab deployment. Validate your deployment before relying on its coverage; unsupported server/CLI versions remain a visible source failure.

References: [Project declaration](https://gitlab.com/gitlab-org/gitlab/-/raw/master/app/graphql/types/project_type.rb), [WorkItem type](https://gitlab.com/gitlab-org/gitlab/-/raw/master/app/graphql/types/work_item_type.rb), [label widget](https://gitlab.com/gitlab-org/gitlab/-/raw/master/app/graphql/types/work_items/widgets/labels_type.rb).

## Linear

```toml
[[sources]]
id = "linear-eng"
kind = "linear"
scope = "workspace-url-key/ENG"   # workspace URL key / team key
token_env = "LINEAR_API_KEY"      # the variable's NAME; export the key in your shell
```

`host` defaults to and must be `linear.app`. Create a personal API key in Linear (Settings → Security & access) and export it under the named variable before `fetch` or the TUI. The key is read only for an explicit or interval fetch: never by query/show/bare commands, never written to config, cache or errors. A missing variable reports `auth_missing` and sends no request.

**Limit what the key can do.** lazytodo only reads, but the key carries whatever permissions it was created with. Every process that inherits the variable can use it: other programs and coding agents started from the same shell, and the `gh`/`glab` subprocesses lazytodo starts, which inherit lazytodo's environment. If Linear's key form offers the options, create the key with read-only permission, restricted to the teams you configure. Prefer setting the variable only for lazytodo over a global `export` in a shell profile, for example:

```sh
LINEAR_API_KEY="$(security find-generic-password -s lazytodo-linear -w)" bin/lazytodo
```

A team-restricted key works as long as it can read the configured team. A key that can't read it reports `not_found`.

lazytodo posts two fixed GraphQL queries to `https://api.linear.app/graphql` with `Authorization: <key>` and refuses redirects. First it reads `viewer.organization` and resolves the team by key. A key from a different workspace reports `scope_mismatch`, and an unknown or renamed team key reports `not_found`; neither is treated as an empty team. Then it reads `issues(filter: team id, includeArchived: true, orderBy: createdAt, first: 50)` and follows cursors.

- **Coverage:** every issue in the team, including completed, canceled and archived issues. Trashed issues are skipped. Sub-issues are separate issues. Projects, cycles, comments and custom views are not read.
- **Identity:** `linear` / `linear.app` / organization ID / issue UUID. `ENG-123` is only the display ref.
- **State:** the native workflow state name is kept (for example "In Review"). The open/closed mapping comes from the state type: triage, backlog, unstarted and started are open; completed, canceled and duplicate are closed.
- **People:** the human `assignee` and the agent `delegate` are kept separate (`Assignees` and `Delegates`). A null value is known-empty.
- **Labels:** up to 50 per issue, with native names and colors. An issue with more labels fails the fetch rather than silently truncating.

Rate limits (HTTP 429 or a `RATELIMITED` GraphQL code) honor `Retry-After` or Linear's millisecond reset headers. GraphQL errors, schema-validation errors, malformed data, a repeated cursor or a duplicate issue ID invalidate the whole attempt and keep the previous snapshot. The 64 MiB and page limits are shared with forge sources.

Fixture tests follow Linear's published SDK schema (`packages/sdk/src/schema.graphql`, checked 2026-11-04: `Issue.delegate`, `trashed`, `includeArchived` and `PaginationOrderBy`). They do not exercise a live workspace. There is no discovery wizard yet; add Linear sources with `config edit`.

References: [Linear GraphQL API](https://linear.app/developers/graphql), [rate limiting](https://linear.app/developers/rate-limiting), [assigning and delegating issues](https://linear.app/docs/assigning-issues).

## Super Productivity

```toml
[[sources]]
id = "super-productivity"
kind = "super-productivity"
token_env = "SP_API_TOKEN"   # the variable's NAME; export the Access Token in your shell
```

Desktop (Electron) app only. In the app, enable **Settings → Misc → Enable local REST API** (it is per computer and not synced) and copy the **Access Token** shown there into the named variable. `host` is fixed to `127.0.0.1:3876` (the app's non-configurable loopback port) and `scope` to `local`, the whole desktop profile. There is one source per configuration.

Each fetch reads `GET /health` without a token, then `/projects`, `/tags` and `/tasks?source=all&includeDone=true` with `Authorization: Bearer <token>`. Requests bypass proxy settings and refuse redirects. The API does not paginate, so one response is the complete task list, still bounded by the shared 64 MiB budget.

A closed app, a disabled API, a renderer that isn't ready, or a renderer timeout (503/504) reports `unavailable` and keeps the previous snapshot, never an empty list. A wrong token reports `authentication`. An app without these routes reports `unsupported_schema`.

- **Coverage:** active, done and archived tasks, including tasks in archived projects. Subtasks are separate items with native type `Subtask`. Notes, repeat configs, counters and time tracking are not read.
- **Identity:** `super-productivity` / `127.0.0.1:3876` / `tasks` / task ID. The display ref is the project title, or "Super Productivity" for tasks without a project, so text search matches project names.
- **State:** `todo`, `done` or `won't do` (a done task whose `wontDoOn` equals `doneOn`). Done and won't-do map to closed.
- **Labels:** tags with their native titles and colors. A tag ID that `/tags` does not return is kept as a label named by that ID, not dropped. Assignees and delegates stay unknown (the app is single-user). There is no web URL, so open-in-browser is unavailable for these items.

The same key-hygiene advice as Linear applies: any process as your user that can read the variable can call the full read/write local API. Super Productivity's own docs note that the API does not authenticate itself to clients, so while the app is closed another local process could bind the port and receive the token.

Fixture tests follow the app's source at v19.3.1 (`electron/local-rest-api.ts`, `local-rest-api-task-routes.service.ts`, plugin `Task`/`Tag`/`Project` types), not a running app.

Reference: [Local REST API](https://github.com/super-productivity/super-productivity/wiki/3.01-API#3-local-rest-api).

## Canonical snapshot file

```toml
[[sources]]
id = "local-export"
kind = "snapshot"
path = "/absolute/path/to/snapshot.json"
# scope = "optional/expected-scope"
```

Relative file paths resolve against the selected config file's directory. The reader accepts a regular JSON file up to 64 MiB, rejects unknown schema fields, null/missing items, duplicate IDs, mismatched refs/scopes, incomplete snapshots and implausible observation timestamps. See the complete fictional [example](../examples/snapshot.json).

Envelope: `schema_version:1`, nonempty `scope`, RFC3339 `observed_at`, `complete:true`, `items:[]`. Each item needs origin `{provider,authority,namespace,id}`, title and state `open|closed|unknown`; optional original fields follow `model.Item`. An omitted ref is derived. Null collections mean unknown; empty arrays mean known empty. One snapshot represents one declared scope. Origin identities must not overlap a live source.

Producer should atomically rename a fully written file into place. Observation time is the export observation, not file mtime or import time. A complete export does not certify tracker replication or current remote state.

lazytodo never runs exporters, initializes/migrates/syncs trackers, or reads Beads/td authoritative stores. Raw Beads JSONL and td export JSON need future explicit converters and **are not accepted as canonical snapshots today**.

## Deferred

[TODO](../TODO.md) and [adapter research](../backlog/source-adapters.md) cover Linear discovery, Beads/td, Google Tasks, Reminders and selected personal-ops exchange. Translation is separately opt-in research, not a fetch/display side effect.
