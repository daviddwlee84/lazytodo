# Design and contracts

## Scope

lazytodo owns a projection, not work-item truth. Trackers own native content/state; personal-ops owns personal goals, decisions, acceptance/evidence policy. Local pins are bookmarks. No write-through or automatic agent dispatch is implemented.

`internal/model` defines canonical identity/projection. `internal/source` reads complete snapshots with native CLI transports or an explicit file. `internal/store` manages SQLite observations and pins; `internal/service` is shared by Cobra and Bubble Tea. `internal/skill` embeds operational instructions. There is no daemon, plugin framework or model runtime.

## Identity and fields

`ref` is `lt1_` plus base64url JSON encoding of `[provider, authority, namespace, immutable native ID]`. Consumers treat it as opaque and copy it from output. GitHub's global issue ID uses namespace `issues`; GitLab uses global WorkItem GID and namespace `work_items`. Repository/project paths and issue number/IID are display/scope metadata, not global identity. A local source needs an explicit durable store namespace, not a checkout path.

Source aliases can change without changing canonical identity. Two registered sources claiming the same identity cannot overwrite each other; the later overlapping snapshot fails atomically. Similar titles are not merged. Snapshot transport preserves original origin identity, not a fictional new task identity.

Native type/state, labels, actors and timestamps remain visible alongside `open|closed|unknown`. Null labels/assignees/delegates mean unavailable/not fetched/unsupported; empty arrays mean confirmed empty. Human assignees and agent delegates are separate. No priority/commitment inference is performed.

## Observation protocol

- A complete source response means all requested outer and required nested pages were read, within the configured scope and credentials. It is not a global inventory or a transactionally consistent remote instant.
- Fetch obtains an OS advisory lock, reads at most two sources concurrently, and commits each snapshot in a short SQLite transaction. Readers use a single read transaction for item rows and their source revisions.
- Partial response, malformed data, pagination limit, GraphQL errors, timeout or cancellation cannot replace last-good data.
- A valid empty snapshot removes previous rows for that source. Absence means not observed in the current projection, not deleted/closed upstream. Pins remain in separate durable storage.
- Source target fingerprints isolate configuration changes. A failed fetch for a changed target cannot relabel old-target rows as current.
- Native fetch has a two-minute source deadline, 64 MiB total response/output budget and 10,000-request safety ceiling. Limit failures are explicit, not truncated success. No immediate blind retries; subsequent attempts honor source retry metadata and at least a one-minute failure backoff.
- TUI ticks check per-source last-attempt cadence; default 5m, zero disables automatic fetch. Snapshot observation age comes from the producer, never from rereading/importing the file.

The snapshot and pin databases use schema version 1. Future schema versions are rejected by old binaries. Read commands do not initialize databases. SQLite may still use filesystem coordination while reading an existing WAL database; “source-read-only” is not a promise of zero filesystem activity.

## Search and pagination

Plain/quoted text and exact filters are ANDed. `source:`, `state:`, `label:`, `pinned:` are the complete initial grammar; unknown fields/incomplete quotes are errors. Text uses NFKC normalization and Unicode case folding. Label filters are exact and preserve native spelling/case. SQLite applies text/source/state/label predicates; the shared service merges pins, computes relevance once, sorts and only then slices a page. No vector/FTS tokenizer assumption is required for Chinese.

Ranking: number of title/reference text hits, then native updated timestamp, then canonical ref. This is retrieval relevance, not a priority recommendation engine. Default 50 and maximum 1000 results/page; TUI pages 100 at a time.

## JSON / exit contract

Success stdout:

```json
{"schema_version":1,"data":{"items":[],"sources":[],"total":0,"returned":0,"offset":0,"limit":50,"truncated":false,"revision":"..."}}
```

Data shape is command-specific. Query includes per-source scope, completeness, revision, observation/attempt/success/retry timestamps and safe errors. Query omits body; show returns `item` plus `sources`. A missing cache yields unknown coverage, not an unexplained empty array.

Pass `--offset` and `--revision` from the first query to continue. Changes in source observations/configuration or pins cause `revision_changed`; restart from offset 0. Revision is a consistency token, not a secret or write authorization.

Error stderr:

```json
{"schema_version":1,"error":{"code":"usage","message":"..."}}
```

Exit: 0 success, 1 runtime/partial fetch failure, 2 usage/config, 130 cancellation. Partial fetch may emit per-source result JSON on stdout and error JSON on stderr. No source raw stderr or progress enters JSON. Successful cache reads do not imply freshness. Machine paths never prompt; explicit `open` is an outward browser action and rejects `--json`.

Static help/version/skill/config-example paths do not parse configuration or create directories. Documentation JSON is an explicit content envelope rather than Markdown mixed into an item list.

## Terminal and trust boundaries

The event loop owns state. I/O runs in effects with cancellation/generation guards. Refresh follows stable refs, text fields own printable keys, and overlays restore the underlying context. Views remember their query/page/selection/scroll. Main UI is list/preview with narrow-pane fallback; no mouse capture or required font.

Native CLIs receive fixed read requests, explicit hostname/scope and JSON variables on stdin, never a shell or interpolated GraphQL. Credentials remain with native tools. External content is sanitized for terminal controls. Only explicit `open` can launch the system browser, only HTTP(S) without userinfo; forge URL host must match origin authority.

The operational skill offers bounded fact retrieval and a 3–5-option next-step recipe. It grants no authority to accept/claim/close tasks, inspect private personal-ops records, upload content or launch another agent. Future dispatch needs an independently reviewed target, permission and receipt contract.
