# Verification evidence

Initial preview verification: **2026-11-02**, macOS Darwin/arm64, Apple M4, Go 1.27.0; minimum Go 1.26.0 checked separately. All provider/runtime fixtures are fictional and use temporary HOME/XDG directories. No live account or personal-ops private store was read.

## Automated checks

Passed:

- `go test ./...`
- `go vet ./...`
- `go test -race ./...`
- `GOTOOLCHAIN=go1.26.0 go test ./...`
- `go build -o bin/lazytodo ./cmd/lazytodo`
- `GOOS=linux GOARCH=amd64 go build -o bin/lazytodo-linux-amd64 ./cmd/lazytodo`
- `python3 scripts/check_todo.py` and the installed knowledge-harness TODO validator.

Coverage includes native source pagination/late-page failure, GitHub PR exclusion, GitLab partial errors and nested label/assignee pages, unknown versus empty fields, safe provider errors, snapshot validation, cache-only/static paths, JSON exits, revision-bound paging, source fingerprint changes, overlapping identities, independent source backoff, pin persistence and uninitialized/newer database handling. TUI tests cover late success/error rejection, focus/input ownership, per-view state, paging and Unicode/resize boundaries.

The first `go mod tidy` attempt hit a checksum mirror HTTP/2 transport error. Retrying with the official `GOSUMDB=sum.golang.org` succeeded; checksum verification was not disabled and no global Go configuration was changed.

## Real PTY

`python3 scripts/pty_smoke.py bin/lazytodo` passed using a real OS PTY with xterm-256color protocol and fake native gh responses:

- Initial cached display, Chinese search, printable `jkhql/?` in input, Enter semantics, bracketed multiline paste.
- Preview/back, help, views and cached Sources overlay.
- Provider deliberately held pending while navigation and local pin completed; cache-only CLI verified the pin before releasing provider I/O.
- Failed refresh retained all last-good rows; raw private-error sentinel never appeared.
- Resize at 120×30, 80×24, 36×12 and 1×1 without crash.
- `q` and raw Ctrl+C restored terminal modes and left the alternate screen.

One run observed the first cached frame at approximately **0.11 s** and completed the pin assertion during blocked fetch in approximately **0.18 s**. These include the test harness and are observations, not universal latency guarantees.

The PTY initially expected different count text (`1 item` versus the UI's page count); its assertion was corrected. On macOS, restoration can raise `PENDIN` (0x20000000), transient kernel pending-input bookkeeping. The test masks only that flag while comparing all other termios fields, including ECHO/ICANON/ISIG/IXON; it does not weaken raw-mode or alternate-screen restoration checks.

No screenshot/emulator result is treated as proof of Unicode glyph rendering. Deterministic view tests measure terminal-cell widths and preserve text; this PTY verifies actual key parsing and cleanup.

## Configuration editor follow-up

`config edit` was verified with `python3 scripts/config_edit_smoke.py bin/lazytodo` using a real PTY and a disposable editor executable whose path contains spaces. The editor received real terminal input/output and quoted `--wait` arguments. Repairing invalid TOML, retaining invalid saved changes with exit 2, flag/env/XDG path precedence, first-file creation, and JSON rejection even under a PTY all passed. No source fetch or cache/pin initialization occurred. Unit tests also cover editor failure, missing executables, cancelled entry, quote parsing, comment preservation and file validation independent of environment overrides. Actual user editors/authentication were not launched.

## Source wizard follow-up

`python3 scripts/setup_smoke.py bin/lazytodo` passed in a real PTY with fictional gh/glab executables. Covered: no metadata calls until explicit discovery; recent-first selection; failed GitLab authentication alongside successful GitHub candidates; printable filter keys; safe default Back in Review; explicit Save through the real atomic config writer; no task fetch/cache/pin initialization; missing-tool install/manual guidance; all-unauthenticated native-login guidance; tiny/normal resize; cancellation without file creation; JSON rejection even under a PTY; terminal restoration and private-error suppression.

Unit/fixture tests cover paginated metadata reads, memberships/affiliations, filtering, discovery bounds/partial results/timeouts, deferred and stale UI effects, hidden selections, configured/disabled targets, safe review controls, save outcome reconciliation, configuration preservation/idempotency/symlinks and concurrent-editor conflicts. No real account discovery or authentication was performed. CI commands were added; remote CI/Linux PTY execution remains unverified here.

## Cwd context / scoped UI follow-up

`python3 scripts/repo_context_smoke.py bin/lazytodo` passed using disposable Git checkouts, nested cwd, fake gh and isolated XDG. Verified configured-repo focus with default Open, All preserving source scope, returning to All sources, explicit `c`, no API calls from detection, safe default Ignore without config/cache changes, confirmed Add + targeted fetch without restarting, disabled-source preservation and terminal restoration. The existing general TUI harness and fictional demo explicitly disable repo context so their real checkout cannot trigger registration/network behavior.

Read-only Git tests cover nested/worktree roots, URL credential stripping, SSH versus API ports, known custom hosts, unsupported aliases, precedence/dedup/ambiguity, output limits, missing Git and cancellation. Model/service tests cover late detection not stealing input, scope/view state, safe/visible confirmation, single registration, queued targeted fetch and immutable service configuration. New template tests prove uncommented example scopes remain disabled. No actual user config was modified or real provider queried by these tests.

## TUI source editor / labels follow-up

`python3 scripts/tui_sources_tags_smoke.py bin/lazytodo` passed with a fictional snapshot and a disposable editor executable. It verifies label badges and exact tag filtering; real terminal release to an editor with canonical/echo mode; visible editor result acknowledgement; reacquisition and valid config reload; per-source priority override changes without re-fetching; raw invalid-file detection even when launch flags could otherwise mask it; refresh pause; repair; disable/remove/re-add of the last source from the same TUI; retained explicit launch flags; and terminal restoration. The initial badge check exposed a narrow-column case; compact priority badges were added and the PTY test rerun successfully.

Unit tests additionally cover source-service immutability, per-scope/view restoration, retarget/disable invalidation, cancelling an edit request while refresh stops, stale replies, exact label query round trips (including quotes/backslashes/CJK), unknown versus empty labels, stable picker targets, per-source nil/empty/custom priority rules, safe RGB colors and bounded cell widths. No real editor/config/source labels were modified during verification.

## Search measurement

`go test ./internal/service -run '^$' -bench BenchmarkQuery10k -benchtime=3x -benchmem`

Synthetic 10,000-item dataset; SQLite predicate scan, projection decode, ranking, sorting and a bounded result page included. On this machine, initial repeated-normalization ranking measured about **114 ms/query, 208 MB allocated/op**. Precomputing rank once, avoiding duplicate filtering and limiting ref scans reduced the measured run to about **34 ms/query, 55 MB allocated/op**. Allocation totals are cumulative per operation, not retained heap size.

This supports the small substring-search baseline, not a claim about 100k-item datasets or long real issue bodies. Performance-driven indexing remains backlog work.

## Not verified here

- Live authenticated GitHub or GitLab service compatibility and actual account coverage; fixture tests do not establish a minimum supported self-hosted GitLab release.
- Linux runtime/PTY and CI execution: a Linux binary was cross-built; the macOS/Linux CI matrix is authored but has not run on a remote service.
- Real browser opening or native authentication flows; no external browser was launched by the verification suite.
- Linear, native Beads/td, Super Productivity, Google Tasks, Reminders, personal-ops exchange or translation: these remain backlog, not tested integrations.

## Mouse, preview copy and markdown follow-up

Verified **2026-11-04**, macOS Darwin/arm64. `go test ./...`, `go test -race ./...`, `go vet ./...` and all PTY smoke scripts passed.

- `scripts/pty_smoke.py` now sends SGR mouse input in a real PTY: it asserts the cell-motion/SGR modes (`?1002h`, `?1006h`) are requested, then sends a list click, wheel over list and preview, a preview drag (an OSC 52 write appears after release; on macOS a fake `pbcopy` placed first on `PATH` receives the text, so the real clipboard is never touched), a footer-hint click that opens Views, and `M` toggling capture off/on. On exit it asserts capture is released.
- `scripts/setup_smoke.py` asserts the wizard requests and releases mouse capture and survives wheel input. Wizard checkbox/button clicks are covered by unit tests rather than the PTY, because locating rows in incremental renderer output would be brittle.
- Unit tests cover row select and inspect on a second click, wheel targeting, picker/footer/header clicks, quit never being clickable, the tiny layout and disabled capture, search acceptance on an outside click, drag text extraction/auto-scroll/Esc clearing, two-click repo registration and wizard Save, keyboard `y`/`Y`, and SGR-only renderer output, including a hostile OSC 8/CSI/DCS/C1 body and render caching.
- A manual tmux 3.6a session (fictional snapshot, isolated HOME/XDG) confirmed rendered headings, tables, lists and code, the preview position title, reverse-video drag highlight and the copy notice. tmux measures the fixture's ZWJ emoji row differently from Bubble Tea's grapheme widths and shifted that row's border; this is a pre-existing emulator width difference, not new behavior.

Not verified here: iTerm2/Terminal.app/Linux terminal emulators driven by a real pointer, OSC 52 acceptance by a particular terminal or by tmux `set-clipboard`, and Linux native clipboard helpers (xclip/xsel/wl-copy). Linux CI is expected to exercise the OSC 52-only path.

Backend fixes in the same change are covered by new tests: shared-cache writes from two configurations keep each other's rows; a renamed alias yields to the same identity while configured overlaps still fail; a failed refresh keeps the paging revision; case-variant duplicate forge targets are rejected. The GitHub request now includes `sort=created&direction=asc` (fixture path updated).

