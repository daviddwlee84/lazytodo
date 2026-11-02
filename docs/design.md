# Design and contracts

## Scope

lazytodo owns a projection, not work-item truth. Trackers own native content/state; personal-ops owns personal goals, decisions, acceptance/evidence policy. Local pins are bookmarks. No write-through or automatic agent dispatch is implemented.

`internal/model` defines canonical identity/projection. `internal/source` reads complete snapshots with native CLI transports or an explicit file. `internal/store` manages SQLite observations and pins; `internal/service` is shared by Cobra and Bubble Tea. `internal/skill` embeds operational instructions. There is no daemon, plugin framework or model runtime.

## Explicit source setup

`init` is a standalone, terminal-only three-stage wizard, not an automatic startup scan. It discovers installed native CLIs locally, shows editable host choices, and only starts account/repository metadata requests after Enter. Existing native credentials are reused; missing tools/login lead to guidance, never automatic installation or authentication.

Repository/project candidates are bounded, newest-first and opt-in. A partial failure remains visible without erasing another host's usable choices. Text input owns printable keys; filtering does not discard selected items, and Review lists all selections. Review defaults to Back, not Save. Only explicit Save creates/appends configuration, via the same SourceDraft/SourcePlan used by `sources add`. Cancellation before Save creates no config/cache; cancellation during Save reconciles the actual atomic-write outcome. Existing file comments/preferences and symlinks are preserved; concurrent changes abort rather than being overwritten.

`sources discover --provider ... --host ... --json` is the scriptable metadata read; `sources add --provider ... --scope ... --json` is explicit offline local registration. Bare `sources` remains cache-only. Setup does not certify the server's Work Items capabilities or task access; the subsequent explicit fetch verifies those.

## Cwd context and source-focused browsing

TUI startup schedules a bounded local Git read after rendering. `internal/repoctx` recognizes safe forge URLs, resolving nested checkouts/worktrees and choosing branch upstream, origin, or a unique recognized target. There are no network/SSH probes; credentials never become display fields. Unknown hosts/aliases and ambiguous targets are not guessed. `repo_context=false` opts out; non-TUI commands never run this detection.

Configured enabled cwd matches focus the Open view only before user interaction. Otherwise `c` provides an explicit path. Missing sources offer Ignore this time by default or a reviewed Add + fetch. Registration returns a new immutable Service/configuration snapshot instead of mutating the one in use by ongoing effects; the targeted fetch queues behind the TUI's current refresh. A committed registration is not rolled back or called uncommitted if fetching/cancellation later fails. Disabled sources require explicit manual enabling.

Views (Open/All/Pinned/custom) and source scope are separate. Scope changes preserve each view's query/selection/scroll and use the same service predicates as the CLI. `s` is a source picker plus cached diagnostics, not a discovery probe. Native scopes/numbers are primary human labels; internal aliases and canonical references remain stable machine identifiers.

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

`config edit` resolves the same selected path independently of parsing, then invokes `$VISUAL`, `$EDITOR`, or `vi` with safely split arguments and the absolute file path. It requires a real TTY and rejects JSON before any file creation. Missing files get a private example template; existing content/comments are left to the editor. Validation on return checks the file itself, including view syntax, without env/flag overrides masking errors. Invalid or failed-editor changes are never silently reverted; restart an existing TUI to load edits. No source discovery/fetch or cache initialization occurs.

Static help/version/skill/config-example paths do not parse configuration or create directories. Documentation JSON is an explicit content envelope rather than Markdown mixed into an item list.

## Terminal and trust boundaries

The event loop owns state. I/O runs in effects with cancellation/generation guards. Refresh follows stable refs, text fields own printable keys, and overlays restore the underlying context. Views remember their query/page/selection/scroll. Main UI is list/preview with narrow-pane fallback; no mouse capture or required font.

Native CLIs receive fixed read requests, explicit hostname/scope and JSON variables on stdin, never a shell or interpolated GraphQL. Credentials remain with native tools. External content is sanitized for terminal controls. Only explicit `open` can launch the system browser, only HTTP(S) without userinfo; forge URL host must match origin authority.

The operational skill offers bounded fact retrieval and a 3–5-option next-step recipe. It grants no authority to accept/claim/close tasks, inspect private personal-ops records, upload content or launch another agent. Future dispatch needs an independently reviewed target, permission and receipt contract.
