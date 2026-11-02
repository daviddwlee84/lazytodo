# Optional translation and repository language conventions

Status: deferred — explicitly requested for later, not part of v0.1.

## Need

Some repositories require issues to be authored in English, while the person prefers to read or draft in Chinese. Evaluate reuse of [daviddwlee84/translate](https://github.com/daviddwlee84/translate); its actual CLI/API, provider, offline behavior and cost have not been verified here.

## Two separate surfaces

1. Read translation: toggle original/translated preview. English source remains authoritative; never write a displayed translation back automatically.
2. Future write preparation: preserve the original draft and produce an English candidate according to an explicitly selected repo convention. Review before dispatch through the authorized native write path.

## Entry criteria

- Verify the reusable translator's structured interface and cancellation/error behavior; invoke without shell interpolation.
- Define opt-in consent before private issue bodies leave the machine. Opening preview alone must not trigger model requests or cost.
- Cache by source content hash, target language and translator/version identity; invalidate on upstream changes. Keep translation cache disposable, separate from pins/source facts.
- Preserve code blocks, identifiers, terminology and Markdown links. Clearly mark translated text, failures and original-text fallback.
- Decide whether translations participate in search; do not silently change exact labels or the original-language query contract.

No translation runtime, provider credentials or automatic upload is implemented. Indexed from [TODO](../TODO.md).
