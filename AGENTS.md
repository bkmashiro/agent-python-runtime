# Pysolate development experiments

- Record model-driven development experiments fully by default. Preserve actual provider request/response bodies (including returned reasoning fields), prompts, generated and executed code, ordered tool inputs/results/errors, Guest inputs/results/errors, and the configuration needed to replay them. Retain failed and interrupted attempts; mark partial captures explicitly.
- Do not record transport authorization, cookies, or configured API-key values. Full traces can still contain sensitive task data: keep them in private local files, separate from reviewed public summaries. Never add raw private recordings to Git automatically.
- Before a paid scored cohort, prove that its recording can be read and its captured executions replayed without model/network requests or real tool dispatch. Reject mismatches and unsupported/partial replay instead of inventing data or silently falling back to live calls.
- Distinguish result regrading, recorded-response playback, deterministic execution replay, and a new model run. Old metrics-only logs are not complete recordings, and missing historical data must not be reconstructed as if observed.
- Keep runtime authority, isolation, deadlines and resource limits intact. Record truncation caused by existing limits rather than disabling limits to collect more data. Declare recording overhead in timing comparisons.
