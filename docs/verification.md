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

## Search measurement

`go test ./internal/service -run '^$' -bench BenchmarkQuery10k -benchtime=3x -benchmem`

Synthetic 10,000-item dataset; SQLite predicate scan, projection decode, ranking, sorting and a bounded result page included. On this machine, initial repeated-normalization ranking measured about **114 ms/query, 208 MB allocated/op**. Precomputing rank once, avoiding duplicate filtering and limiting ref scans reduced the measured run to about **34 ms/query, 55 MB allocated/op**. Allocation totals are cumulative per operation, not retained heap size.

This supports the small substring-search baseline, not a claim about 100k-item datasets or long real issue bodies. Performance-driven indexing remains backlog work.

## Not verified here

- Live authenticated GitHub or GitLab service compatibility and actual account coverage; fixture tests do not establish a minimum supported self-hosted GitLab release.
- Linux runtime/PTY and CI execution: a Linux binary was cross-built; the macOS/Linux CI matrix is authored but has not run on a remote service.
- Real browser opening or native authentication flows; no external browser was launched by the verification suite.
- Linear, native Beads/td, Super Productivity, Google Tasks, Reminders, personal-ops exchange or translation: these remain backlog, not tested integrations.
