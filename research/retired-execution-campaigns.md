# Retired legacy execution campaigns

The retained-prefix Guest executor and independent semantic pre-dispatch controller are removed from the current runtime. The following drivers were coupled to those execution owners and are retired with them:

- `releasereadiness` early-read and live-stream campaigns;
- the mechanismcampaign unified-day-trip executor;
- the effectgraph semantic-predispatch experiment;
- workflowbench source-prefix-overlap;
- semanticspeculation eager/semantic comparison executors and phase-3/phase-4 campaign commands.

Their implementations remain available at Git revision `df191bf1` (use each evidence artifact's recorded revision for exact historical reproduction). No frozen evidence files or digest anchors are changed by this retirement.

The research viewer retains the mechanismcampaign record schema, validation, and deterministic source reconstruction. It no longer links the retired executor. The semanticspeculation serial baseline and independent phase-5 prepared-region operations remain available for current tests. Historical record codecs and aggregators remain separate from runtime execution.

Current prefix execution uses PLM's Run-owned split-phase table. The runtime reader's guide is [Execution core](../docs/execution-core.md).
