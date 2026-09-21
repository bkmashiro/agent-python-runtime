# WSL Linux scheduling quick matrix

This directory records a bounded validation run for the scheduling harness. It is not the full matrix and does not establish production defaults.

## Envelope

- Source: `0df54dc1b09dca03cbe7654b3c8680c4b3e1651e`
- Host: WSL2, Linux `6.18.33.2-microsoft-standard-WSL2`, amd64
- Go: `go1.27.1 linux/amd64`
- Guest: 34,683,734 bytes, SHA-256 `ef49a628d71ab43c2523712fc47bb59ca8ebc3bfd52cfd46cbc4f493721ee8e4`
- Tool delay: 50 ms
- Phase case: `read-finish`, three samples per arm
- Phase preparation: copy and Linux COW
- Competing-Run fixture: two Runs, one running slot, two resident slots, two Tool slots
- Private heap fixtures: 0 and 8 MiB
- Queue samples: one separate process per arm

The Guest was an existing WSL artifact identified above; it was not rebuilt for this run. The source tree was a clean clone at the recorded revision. `dist/` is ignored, so `go run` did not embed VCS settings in the phase metadata; this README binds the raw files to the source revision used to execute them.

## Observations

Single-Run `read-finish` medians were:

- copy Inline: 188.99 ms;
- copy `ExternalIO`: 182.74 ms;
- COW Inline: 80.68 ms;
- COW `ExternalIO`: 81.18 ms.

The single-Run arms provide phase and backend validation. They do not offer another runnable task while the Tool waits, so they should not show a material live-I/O scheduling gain.

For the two-Run queue fixture:

- 0 MiB private heap: 172.61 ms Inline versus 107.32 ms `ExternalIO`, a 37.8% reduction; peak Host waits changed from one to two.
- 8 MiB private heap: 169.04 ms Inline versus 110.92 ms `ExternalIO`, a 34.4% reduction; peak Host waits changed from one to two.

Sampled process RSS was 387,400 versus 412,428 KiB for the 0 MiB pair and 418,424 versus 415,064 KiB for the 8 MiB pair. Each value comes from one process, includes the runtime and prepared image, and is sampled at harness boundaries. The opposite deltas across the two heap fixtures are insufficient for a memory conclusion. A repeated full Linux matrix is still required before selecting resident limits or a park threshold.
