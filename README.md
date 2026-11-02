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

1. Install and authenticate `gh` and/or `glab` for the intended host using their own tools. lazytodo neither logs in nor stores credentials.
2. Run `bin/lazytodo config example` for a template; save your edited copy to `~/.config/lazytodo/config.toml` or pass `--config PATH`.
3. Add only the repositories/projects you want to read:

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

No account/repository discovery. GitHub excludes PRs. GitLab needs the Work Items GraphQL fields used by the adapter; unsupported schemas produce an error, **never an Issues fallback presented as full Work Items**. Group epics and full namespace discovery are not included.

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
| `v`, `s` | Saved views / cached source status |
| Ctrl+N / Ctrl+P | Next/previous result page |
| `?`, Esc, `q` | Help, back/clear, quit |

Wide terminals show list + preview; narrow terminals show the focused pane. No mouse capture, mandatory icons or Vim insert mode. A refresh follows the selected identity rather than the old row number.

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
python3 scripts/check_todo.py
```

Tests use fictional data/fake provider CLIs and temporary state. No live GitHub/GitLab account was queried for the initial verification. A fixture-tested adapter is not a claim of compatibility with every self-hosted GitLab version.

## Future work

[TODO.md](TODO.md) is the priority/effort index; [backlog/](backlog/) holds research. Linear is the next native-source candidate. Beads/td, Super Productivity, Google Tasks, Reminders, personal-ops exchange, explicit intake/dispatch and optional [translate](https://github.com/daviddwlee84/translate) integration remain separate work. [Translation notes](backlog/translation.md) preserve the English-source/translated-view distinction.
