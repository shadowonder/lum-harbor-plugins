# lh-plugin-ai-runtime

`lh-plugin-ai-runtime` is the **Sample AI Runtime Takeover** plugin. It implements the same high-level Runtime contract as Lum Harbor's native runtime for Assistant, Source Processing, and runtime verification.

It declares only `ai.runtime`. The plugin owns reasoning, capability selection, model transport, and final-result generation. Core sends the unified capability catalog, executes requested READs, persists Proposal intent, and remains the sole domain-mutation authority.

## Architecture

- **Capability:** `ai.runtime`
- **Protocol:** bounded `START` → `NEED_CAPABILITIES` → `RESUME` → `FINAL`, with opaque Plugin-owned state and Core-executed results
- **Dual Execution Modes:**
  - **Online Mode:** Uses an OpenAI-compatible `/chat/completions` endpoint as an internal model client while retaining the high-level Runtime loop.
  - **Offline Mode:** `mock_mode: true` deterministically exercises capability requests, Assistant answers/Proposal intent, Source cleaning/interpretation, and verification without network access. Missing online configuration fails closed.

The executable imports the public AI Runtime protocol types from `lh-plugin-base`; it does not import Core packages, repositories, database handles, or domain services. Plugin processes receive isolated data/cache directories from the Host.

## Build and Package

```bash
# Run tests
go test -v ./...

# Build staged artifact (dist/lh-plugin-ai-runtime/)
make build

# Build distribution package (dist/lh-plugin-ai-runtime.lhplugin)
make package
```
