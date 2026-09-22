# Full-code Tool overlap

This spike tests one narrow optimization against a common Agent script shape:
the complete Python program contains two independent Host reads before local
computation uses both results.

```python
customer = crm.get_profile(customer_id=inputs["customer_id"])
quote = shipping.get_quote(postcode=inputs["postcode"])
result = {
    "customer_tier": customer["tier"],
    "shipping_gbp": quote["amount_gbp"],
}
```

The ordinary path waits for `crm.get_profile` and then starts
`shipping.get_quote`. The full-code path receives the same complete source,
starts both explicitly opted-in reads together, and delivers each outcome at
its original Python assignment. The Guest-visible result and error boundary
remain unchanged.

Run the real-Guest comparison:

```sh
./demos/16-fullcode-overlap.sh
```

The demo uses two deterministic Host fixtures with the same configurable
delay. This isolates overlap from external network variance; it is not a claim
about a particular API's latency. It reports median sequential and full-code
times, speedup, Host call count, and whether preparation was admitted.

## Deliberately small boundary

- The Host must explicitly mark a Tool as safe for early read. Descriptive MCP
  metadata alone does not grant this behavior.
- Only independent, read-only calls are candidates. Dependent calls, writes,
  unknown effects, and ordinary Python computation retain source order.
- The complete script is already available. The demo does not use append-only
  prefix input, source streaming, a runtime `describe()` call, or an LLM.
- Tool values are not cached across Runs and failures are not retried.

This is useful when an Agent gathers several independent records before
combining them, such as profile plus shipping quote, repository metadata plus
issue state, or two search indexes. It does not improve CPU-heavy Python,
NumPy import cost, dependent Tool chains, or a single slow Tool.

The spike currently exercises the existing `RunPLM` implementation so the
behavior can be measured without adding another execution subsystem. If this
shape proves useful, the implementation can be simplified around one
full-source-only API; preserving the current PLM name or prefix machinery is
not a requirement.