# MCP Host integration

Pysolate keeps MCP transport, authentication, process lifetime, schemas and credentials on the Host. The Guest receives only generated Python functions backed by the existing single JSON Host-call ABI.

## Official Go SDK adapter

`mcpadapter/gosdk` adapts an initialized `github.com/modelcontextprotocol/go-sdk/mcp.ClientSession` to the narrow `mcpadapter.Client` interface. The repository pins the official SDK at v1.7.0. It is a Host dependency and does not change `dist/pysolate.wasm`.

For a trusted local stdio server:

```go
cmd := exec.Command("trusted-mcp-server", "--stdio")
client, err := gosdk.ConnectCommand(ctx, cmd, time.Second)
if err != nil { return err }
defer client.Close()

provider := mcpadapter.Provider{
    Client:             client,
    CanonicalNamespace: "mcp.catalog",
    PythonNamespace:    "catalog",
    AllowEarlyRead: func(tool mcpadapter.Tool) bool {
        return tool.Annotations.ReadOnlyHint && !tool.Annotations.OpenWorldHint
    },
}
manifest, err := pysolate.ManifestFromProviders(ctx, provider)
```

For durable execution, discovery uses the same `Capability` definitions but
requires an explicit Host policy for each one:

```go
tools, err := durable.ToolsFromProviders(ctx, func(
    ctx context.Context,
    capability pysolate.Capability,
) (durable.CapabilityPolicy, error) {
    if !capability.Spec.Annotations.ReadOnlyHint {
        return durable.CapabilityPolicy{}, errors.New("capability not approved")
    }
    return durable.CapabilityPolicy{
        Version:    "catalog-v1",
        Recovery:   durable.RetrySafe,
        Scheduling: durable.ExternalIO,
    }, nil
}, provider)
```

The callback is Host policy. MCP annotations can inform it but never approve
recovery, speculative execution or authority by themselves.

`ConnectCommand` starts the subprocess, performs MCP initialization through the official SDK, and owns the resulting session until `Close`. `mcpadapter.Provider` then paginates `tools/list`, validates the normalized manifest and binds each generated Python function to `tools/call`.

The resulting one-shot Guest code is ordinary Python:

```python
reply = catalog.lookup(sku="A-1")
price = reply["structuredContent"]["price"]
```

MCP result envelopes are preserved, including `content`, `structuredContent` and `isError`. A tool-level MCP error therefore remains data visible to Python; transport and protocol errors remain Host-call failures. Arguments must be JSON objects.

## Executable acceptance

```sh
./demos/11-mcp-stdio.sh
# Equivalent:
go run ./examples/mcp-stdio -guest dist/pysolate.wasm
```

The example uses the official SDK on both sides of a real newline-delimited stdio transport:

1. the Host starts a local MCP subprocess;
2. client and server complete MCP initialization;
3. `tools/list` discovers a read-only local catalog tool and its generated schema;
4. the provider exposes it as canonical `mcp.catalog.lookup` and Python `catalog.lookup`;
5. a real Wasm CPython Guest calls the generated function;
6. the Host performs `tools/call`, and Python consumes the structured MCP result.

The subprocess is the same compiled example in server mode, so the demonstration needs no Node.js, Python MCP package or external network service.

For a deterministic two-call MCP chain followed by reviewable workspace edits,
run `./demos/12-mcp-workspace-scheduling.sh`. The corresponding
[`workflow spike`](mcp-workspace-scheduling-spike.md) records the current
durable/workspace lifecycle boundary and the measured inline versus
`ExternalIO` result.

## Authority and lifecycle boundaries

- The command passed to `ConnectCommand` is trusted Host configuration. Guest code cannot choose or spawn it.
- MCP annotations are descriptive. `readOnlyHint` never grants PLM/early-read authority unless the Host's `AllowEarlyRead` policy explicitly accepts it.
- Protocol-default annotation values are preserved: absent `destructiveHint` and `openWorldHint` are treated as true.
- Credentials and environment variables supplied to the subprocess remain Host-owned. Do not pass secrets that the selected MCP server should not receive.
- Discovery happens before Runner construction. Refreshing a catalog requires a new Runner or prepared service state, but never a Guest artifact rebuild.
- MCP tool names still need a valid configured Python path. The adapter does not silently rewrite remote identities.
- Closing the adapter closes the official SDK session and its command transport; Runner shutdown should happen before client shutdown when calls may still be active.
