# Sources and compatibility

Normal fetch contacts configured sources only. Run bare `sources --json` for cached status, not a live probe. Explicit `init` or `sources discover` additionally reads account/repository metadata from the user-selected host, without fetching task content. Run `fetch` explicitly or allow TUI's configured interval after registration. Authentication stays with native tools; install/login separately for the exact hostname. No tokens belong in lazytodo TOML.

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

[TODO](../TODO.md) and [adapter research](../backlog/source-adapters.md) cover Linear, Beads/td, Super Productivity, Google Tasks, Reminders and selected personal-ops exchange. Translation is separately opt-in research, not a fetch/display side effect.
