# Measured scaling and remote surfaces

Status: deferred. Optimize a demonstrated workload rather than prebuilding a platform.

Candidates: incremental fetch with periodic full reconciliation; SQLite FTS/trigram acceleration that preserves short Chinese substring behavior; minimal read-only MCP wrapper over the existing services; remote context handoff; conflict-safe pin synchronization.

Review of 2026-11-04: each query (including every search keystroke in the TUI) opens both SQLite files, scans `instr(search_text, ?)` over bodies, decodes every matching row's JSON, sorts in Go and slices the page; page 2 repeats it. Candidates once measured: a long-lived DB handle on the Service, SQL `ORDER BY`/`LIMIT` when no text rank is needed, and FTS/trigram indexing.

Before each addition, record data volume, query/first-frame/input latency, source limits and the concrete missing workflow. Cached absence must never become remote deletion, and remote identity/authentication must stay explicit. A remote wrapper must not grant access to personal context or all configured sources by default.

A public release also needs a confirmed repository/module path, version provenance, supported installation ownership and upgrade/check behavior. Until then, this project is a local source-built preview: rebuild from a reviewed checkout, with no self-updater.

Indexed from [TODO](../TODO.md).
