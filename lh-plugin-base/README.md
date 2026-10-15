# lh-plugin-base

`lh-plugin-base` is the reusable Go foundation for Lum Harbor Plugin Runtime V1 plugins. It keeps protocol plumbing out of plugin business logic while preserving the existing Core ↔ subprocess wire contract.

It owns:

- Protocol V1 request, response, handshake, invocation, source, and read-query types.
- Strict JSON decoding and canonical protocol error envelopes.
- Newline-delimited JSON stdio serving, handshake validation, capability dispatch, EOF handling, and graceful shutdown.
- Small helpers for environment values supplied by Lum Harbor.

It deliberately does not own plugin configuration models, persistence, HTTP clients, logging frameworks, dependency injection, or capability-specific business logic.

## Minimal plugin

```go
package main

import (
    "log"
    "os"

    "lh-plugin-base/protocol"
    pluginruntime "lh-plugin-base/runtime"
)

func main() {
    logger := log.New(os.Stderr, "my-plugin: ", 0)

    runtime, err := pluginruntime.New(pluginruntime.Metadata{
        ID: "my-plugin", Version: "0.1.0",
    }, logger)
    if err != nil {
        logger.Fatal(err)
    }

    err = runtime.Register("read.query", func(params protocol.CapabilityInvokeParams) protocol.CapabilityResult {
        var input protocol.ReadQueryRequest
        if err := protocol.Decode(params.Input, &input); err != nil {
            return protocol.Failure("invalid_read_query", err.Error())
        }
        return protocol.Success(protocol.ReadQueryResult{
            Summary: input.Query,
            Facts: []protocol.ReadFact{},
        })
    })
    if err != nil {
        logger.Fatal(err)
    }

    if err := runtime.Serve(os.Stdin, os.Stdout); err != nil {
        logger.Fatal(err)
    }
}
```

Declare the same ID, version, executable, protocol version, and capabilities in `plugin.json`. Lum Harbor launches the executable, supplies plugin-owned directories through environment variables, performs `system.handshake`, invokes registered capabilities through `capability.invoke`, and ends the lifecycle with `system.shutdown` or stdin EOF. Stdout is reserved for protocol JSON; diagnostics belong on stderr.

The workspace uses local Go module replacements during development. See `lh-plugin-ai-runtime/go.mod` and `lh-plugin-synthetic-project/go.mod`. A new sibling plugin can use the same pattern:

```go
require lh-plugin-base v0.0.0
replace lh-plugin-base => ../lh-plugin-base
```

## Packages

- `protocol`: stable Protocol V1 wire types, capability result helpers, strict decoding, and canonical plumbing error codes.
- `runtime`: capability registration and the stdio lifecycle loop.
- `env`: accessors for host-provided plugin environment values.
- `cmd/example-plugin`: a deliberately small compatibility/example executable, not a production integration.

The full Sample AI Runtime and Sample Synthetic Project plugins are the primary real examples. Their OpenAI-compatible behavior and simulation behavior remain local to their own modules.

## Build, test, and package the example

```sh
go test ./...
make clean
make
```

The example build creates the compatibility artifact used by Core's existing plugin-runtime tests:

```text
dist/lh-plugin-base/
  plugin.json
  lh-plugin-base
```

Production sample packages are built from their own directories with `make package`.
