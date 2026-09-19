# Retired pass outcome pipeline

The standalone `runtime/passpipeline` outcome ledger was removed during core
simplification. It had no execution consumer: only its tests and a test-only
semantic adapter constructed records. Its binding lists, duplicate outcome keys,
record limits and repeated hash validation are no longer runtime concepts.

Historical implementation and documentation are available at commit `df191bf1`.
Current source transformations are described in [source-pass-plugins.md](source-pass-plugins.md).
