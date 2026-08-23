// Package wazero runs bounded CPython/WASI Guests with the Wazero backend.
//
// New and Factory construct ordinary fresh runners. PreparedFamily is an
// explicit embedding API for one bounded Host-owned NumPy input and a finite
// set of fresh, single-use consumers. A family may reuse qualified immutable
// physical backing, but it never shares mutable Python state, capability
// authority, invocation identity, cancellation state, or workspace bindings.
//
// Prepared families do not schedule consumers, retry work, select workspace
// roots, or publish effects. Embedding Hosts may compose them with the existing
// subagent and workspace packages. The apyrun CLI and HTTP service do not select
// this path implicitly.
//
// On Linux, PreparedFamilyAuto may select a qualified private-COW image. Other
// platforms and ineligible Linux artifacts use the private-copy reference path.
// Explicit PreparedFamilyPrivateCOW requests fail rather than silently
// downgrading.
package wazero
