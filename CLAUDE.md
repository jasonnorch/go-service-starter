@AGENTS.md

## Claude-specific notes

- `make verify` is the gate. Run it and paste the real output; "should pass" is
  not a result.
- This is a template repo, so comments carry more weight than usual: someone
  reading a forked copy has no access to the PR that explains it. Write the
  *why* above a decision, not a narration of the line below it.
- When a change touches an opinion listed in `docs/decisions.md`, update that
  file in the same commit. A decision doc that lags the code is worse than none,
  because it is trusted.
