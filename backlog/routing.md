# Explicit intake and dispatch

Status: deferred. v0.1 offers source opening and versioned JSON handoff only.

lazytodo may become a unified front door, but personal-ops still owns decisions/commitments and trackers own their records. Begin with one explicit destination, not a universal create_task/update_task abstraction.

A reviewed request must name destination, source reference or original capture text, scope, requested operation and authority. Require native identity in the receipt, idempotency/reconciliation where supported, cancellation semantics and an outcome-unknown state. Accepted dispatch is not completed work. Cross-source copy/move is not an atomic transaction.

Do not invent a second capture store, launch an agent from refresh, or interpret a pin/assignment as authorization. When using an external skill/MCP/API, verify its real contract and record results at the authoritative source. personal-ops currently has no dispatch CLI/API to invoke automatically.

Entry gate: a concrete repeated workflow, its owner, privacy boundary and receipt/verification mechanism. Indexed from [TODO](../TODO.md).
