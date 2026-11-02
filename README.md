# lazytodo

**One inbox view, many authoritative sources.** A local-first Go CLI/TUI for browsing, searching and organizing work without replacing its tracker.

首版：**GitHub Issues + GitLab project Work Items（含 labels）**，加上明確指定的 canonical snapshot 檔案。來源唯讀；本機 pins／views 可整理。不做任務寫回、agent 執行或 personal-ops 的決策流程。

## Build and try

Requires **Go 1.26+**. macOS/Linux source-built developer preview; no published module, package manager or self-updater yet.

```sh
go build -o bin/lazytodo ./cmd/lazytodo
python3 scripts/demo.py bin/lazytodo
```

Demo 使用虛構快照與暫存 HOME/XDG，不需帳號、不碰你的真實設定或 cache。日期固定，因此顯示 stale 是預期行為；退出後 demo pins 不保留。

To update this preview, rebuild from a reviewed source checkout. Public installation/versioned upgrades are in [TODO.md](TODO.md).

## Configure real sources

1. Run **`bin/lazytodo init`** in a terminal. Select GitHub/GitLab hosts; existing gh/glab logins are reused. If a tool or login is missing, the wizard explains installation/authentication and manual alternatives; it never installs tools or logs in for you.
2. Discover recent repositories/projects, select only those you want, and review the exact host/scope/config path before saving. No repositories are preselected and no issues are fetched during setup.
3. For manual editing, use `bin/lazytodo config edit`. Both commands respect XDG, `--config PATH` and `LAZYTODO_CONFIG`; existing malformed TOML must be repaired with `config edit`.

Example explicit sources:

```toml
refresh_interval = "5m"
color = "auto"

[[sources]]
id = "github-work"
kind = "github"
host = "github.com"
scope = "owner/repository"

[[sources]]
id = "gitlab-work"
kind = "gitlab"
host = "gitlab.example.com"
scope = "group/project"

[[views]]
name = "Review"
query = 'state:open label:"needs review"'
```

```sh
bin/lazytodo fetch
bin/lazytodo
```

### Editing and authentication

```sh
bin/lazytodo config edit
VISUAL="code --wait" bin/lazytodo config edit
bin/lazytodo --config ./work.toml config edit
```

Editor precedence: `$VISUAL` → `$EDITOR` → `vi`. Quoted arguments are supported without shell expansion. GUI editors need their wait flag. Existing malformed TOML can be repaired; on return the file (including view queries) is validated without environment overrides masking errors. Invalid edits are kept, not reverted. Existing comments are not reserialized. `config edit` requires real input/output terminals, rejects `--json`, and never fetches sources or creates cache/pin databases. Restart an open TUI to load changes.

**Already logged in?** Nothing else to import: lazytodo calls `gh api` / `glab api` with each source's explicit hostname, inheriting the native CLI's credentials, active account and environment. A github.com login does not authenticate another enterprise/GitLab host. No token is copied into lazytodo configuration.

**Access is not scope.** Normal startup never discovers or adds repositories automatically. `init` explicitly lists owned/collaborator/organization-member GitHub repositories and GitLab membership projects, excluding archived or explicitly issue-disabled entries. These are not every readable public repository, watched repositories, or a list of issues assigned to you.

Candidates are ordered by GitHub repository `updated_at` / GitLab project `last_activity_at`, **not issue update time**. Discovery starts with up to 200 eligible entries per host, can be expanded to 1000, and inspects at most 2000 records per host. Partial results/errors are labeled. Selected destinations must be reviewed before local configuration is written. Existing comments/settings are preserved; duplicates—including disabled sources—are skipped, not silently enabled. External edits during setup abort the save rather than being overwritten.

Scriptable equivalents:

```sh
bin/lazytodo sources discover --provider github --host github.com --json
bin/lazytodo sources discover --provider gitlab --host gitlab.example.com --limit 400 --json
bin/lazytodo sources add --provider github --scope owner/repo --scope owner/another --json
bin/lazytodo sources add --provider gitlab --host gitlab.example.com --scope group/project
```

`discover` reads account/repository metadata but does not save configuration or fetch tasks. `add` registers explicitly named scopes without any API/auth probe, so it also works before gh/glab is installed. Both the wizard and `add` generate stable readable source aliases; edit aliases manually if desired. Configurations using inline `sources = []` cannot be safely appended without restructuring; use `config edit` to replace that inline declaration with source tables.

GitHub excludes PRs. GitLab needs the Work Items GraphQL fields used by the adapter; unsupported schemas produce an error, **never an Issues fallback presented as full Work Items**. Group epics and full namespace discovery are not included.

## Daily use

| Key | Action |
|---|---|
| `↑↓` / `jk`, `gg` / `G` | Move, first/last item on this page |
| Tab / Shift+Tab, `hl` / `←→` | Switch list/preview focus |
| `/` | Search cached text; letters remain text while typing |
| Enter | Accept a search; from navigation, inspect the selected item |
| `p` | Toggle a **local** pin |
| `o` | Explicitly open the native source URL |
| `r` | Refresh sources asynchronously |
| `v` | Open / All / Pinned / saved views |
| `s`, then Enter | Focus one repository or All sources; inspect cached status/errors |
| `c` | Focus/revisit the current cwd repository |
| Ctrl+N / Ctrl+P | Next/previous result page |
| `?`, Esc, `q` | Help, back/clear, quit |

The TUI starts in **Open** (closed items remain available under All). Views and source scope are independent: choose a repo with `s`, then use `v` to see its Open/All/Pinned items. `s` → All sources returns to cross-repo browsing. Each source/view keeps its filter and selection. Rows use readable repository names and issue numbers rather than truncated internal IDs; the footer summarizes freshness/errors, with details in `s`.

Wide terminals show list + preview; narrow terminals show the focused pane. No mouse capture, mandatory icons or Vim insert mode. A refresh follows the selected identity rather than the old row number. Manual `r` refreshes the focused source, or all configured sources when All sources is selected.

```sh
bin/lazytodo query '登入 state:open label:"needs review"'
bin/lazytodo query --view Pinned --json --limit 20
bin/lazytodo show '<ref copied from JSON>' --json
bin/lazytodo sources --json
bin/lazytodo fetch --source github-work --json
bin/lazytodo pin add '<ref>'
bin/lazytodo skill print next
```

Text/quoted phrases and filters are ANDed. Exact label spelling is preserved, including `workflow::todo`; text uses Unicode-normalized, case-folded substring matching, including Chinese. Supported filters: `source:`, `state:open|closed|unknown`, `label:`, `pinned:true|false`. Unknown filter names are errors. There is no translation or semantic search yet.

`query`, `show` and `sources` are **cache-only**. TUI refresh runs only while the process is open; `0s` disables automatic fetch, not manual `r`/`fetch`. An external scheduler can invoke `fetch`; none is installed automatically.

## Current repository context

In the interactive TUI, local Git metadata identifies cwd's repository (including subdirectories/worktrees). A configured, enabled match is initially focused in Open. Detection is asynchronous and does not steal focus after the person begins navigating/typing; `c` focuses or revisits it explicitly.

An unconfigured recognized remote offers **Ignore this time** (default) or **Add + fetch**. Only explicit confirmation writes a local source and fetches that repository. The active TUI adopts the registration without restarting; a pending refresh finishes before the targeted fetch. Ignore is session-only; set `repo_context = false` in TOML to disable detection/suggestions entirely. The fictional demo opts out.

Preference: branch upstream remote → origin → a unique recognized target. Ambiguous remotes require a choice. HTTPS/SSH/scp-like URLs are supported; credentials are discarded and SSH ports are not mistaken for API ports. Custom forge hosts must already be known in sources; unknown hosts/SSH aliases require manual setup rather than guessed providers. No Git fetch, SSH probe, account discovery or Git state change is performed. Outside a Git checkout or without a remote, global browsing remains available. CLI query/show/sources remain cache-only and do not detect cwd.

Enabled placeholder scopes such as `owner/repository` or `group/project` are still ordinary configured sources: disable or replace them in `config edit`. Setup preserves existing entries rather than deleting them. New example blocks explicitly remain disabled even when uncommented.

## Data, privacy and agent use

- Source identity + native immutable ID, not similar titles or bare issue numbers.
- Source-native states remain visible. Open ≠ accepted, pin ≠ claim, closed ≠ verified completion.
- Failed/partial fetch retains the last complete snapshot. Unknown, stale, failed and successful empty are distinct. Coverage is limited to configured scope and current credentials.
- Rebuildable cache and durable pins are separate SQLite databases. Missing items leave discoverable pin hints, not fabricated closed tasks.
- No built-in LLM, MCP server, telemetry or automatic data upload. The embedded skill guides an external agent through the same bounded JSON queries and explicit handoffs.
- personal-ops owns goals/decisions/commitments. lazytodo does not read its private store or invoke its roles automatically.

macOS and Linux both use explicit XDG defaults:

| Content | Default |
|---|---|
| Configuration / saved views | `~/.config/lazytodo/config.toml` |
| Disposable cache | `~/.cache/lazytodo/cache.sqlite` |
| Durable local pins | `~/.local/share/lazytodo/local.sqlite` |
| Fetch coordination | `~/.local/state/lazytodo/fetch.lock` |

`XDG_*_HOME` absolute overrides are honored. Precedence: explicit flags → documented `LAZYTODO_CONFIG`, `LAZYTODO_REFRESH_INTERVAL`, `LAZYTODO_COLOR` → TOML → defaults. `config show` exposes effective non-secret settings. Help/version/skill/example do not initialize storage.

See [design and machine contract](docs/design.md), [source compatibility](docs/sources.md), and [verification evidence](docs/verification.md).

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o bin/lazytodo ./cmd/lazytodo
python3 scripts/pty_smoke.py bin/lazytodo
python3 scripts/config_edit_smoke.py bin/lazytodo
python3 scripts/setup_smoke.py bin/lazytodo
python3 scripts/repo_context_smoke.py bin/lazytodo
python3 scripts/check_todo.py
```

Tests use fictional data/fake provider CLIs and temporary state. No live GitHub/GitLab account was queried for the initial verification. A fixture-tested adapter is not a claim of compatibility with every self-hosted GitLab version.

## Future work

[TODO.md](TODO.md) is the priority/effort index; [backlog/](backlog/) holds research. Linear is the next native-source candidate. Beads/td, Super Productivity, Google Tasks, Reminders, personal-ops exchange, explicit intake/dispatch and optional [translate](https://github.com/daviddwlee84/translate) integration remain separate work. [Translation notes](backlog/translation.md) preserve the English-source/translated-view distinction.
