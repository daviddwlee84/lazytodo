# What could I do next?

Use this recipe when the user asks for suggestions, not as permission to execute work.

1. Establish scope with `lazytodo sources --json`. Say what is unavailable, stale, or not connected before saying there is nothing to do.
2. Respect the user's current project or chosen view. Start with `lazytodo query 'pinned:true' --json --limit 10`, or a scoped `state:open` query. Pins are a personal shortlist, not proof of accepted work. If the shortlist is empty, do not invent priorities.
3. Inspect a few candidates using `lazytodo show '<ref>' --json`. Use exact returned references. Expand pages only when needed; bind them to the reported revision.
4. Suggest three to five options, each with a source link, a reason grounded in the observed record, one small next action, and any uncertainty. Separate observations from your recommendations.
5. If choosing requires personal goals, ask for that context or use an already-authorized personal-ops role. Never read a private store just because it exists.
6. Stop at suggestions or a context handoff. Do not create/accept/claim/close tasks, launch agents, translate via a remote service, or send content elsewhere without separate authorization.

A useful handoff contains the exact ref, native URL, scope, observation time, relevant evidence and the user's requested outcome. It is not a new task, a dispatch receipt, or proof that another system accepted work.
