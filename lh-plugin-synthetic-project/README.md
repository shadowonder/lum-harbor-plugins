# lh-plugin-synthetic-project

`lh-plugin-synthetic-project` is the **Sample Synthetic Project** plugin. Its stable plugin ID remains `lh-plugin-synthetic-project`; only its product display name identifies it as a sample.

It simulates a multi-workflow agile project with evolving epics, stories, statuses, and story points, enabling end-to-end verification of Source Layer ingestion, revision evolution, canonical hash deduplication, and bounded host-mediated `read.query` lookups without external network dependencies.

## Capabilities

- `source.fetch`: Emits structured `synthetic.project.story` records with stable composite identity (`workflow-XXX/story-YYY`), realistic payloads (title, description, status, storyPoints, priority, version, updatedAt), and cursor tracking (`tick-0`, `tick-1`, ...).
  - Unchanged items retain identical payload bytes, enabling Core Source Layer canonical content hash deduplication.
  - Advancing ticks update active stories and introduce new stories, proving revision evolution.
- `read.query`: Answers bounded queries against the simulation state:
  - `get_story:<id>`: Look up story details and metadata facts.
  - `list_active`: List all non-DONE stories.
  - `search:<term>`: Query stories by title or description text.
  - Default: Simulator overview (tick, workflow count, story count).

## Durable State

Simulation state is persisted under `LUM_HARBOR_PLUGIN_DATA_DIR/simulation.json` across process lifetimes.

The executable imports `lh-plugin-base` for Protocol V1 types, capability dispatch, the stdio lifecycle, canonical plumbing errors, and host environment access. Simulation state, persistence, `source.fetch`, and `read.query` business behavior remain in this sample.

## Build and Package

```bash
# Run tests
go test -v ./...

# Build staged artifact (dist/lh-plugin-synthetic-project/)
make build

# Build distribution package (dist/lh-plugin-synthetic-project.lhplugin)
make package
```
