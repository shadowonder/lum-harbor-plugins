# Lum Harbor Plugins

This repository contains plugins and the reusable Go plugin foundation for Lum Harbor. Each plugin communicates with Lum Harbor through Plugin Runtime V1.

## Contents

| Directory | Purpose | Capability | Platform |
| --- | --- | --- | --- |
| [`lh-plugin-ai-runtime`](./lh-plugin-ai-runtime/) | Provides AI reasoning through an OpenAI-compatible endpoint or a deterministic offline mode. | `ai.runtime` | macOS, Apple silicon |
| [`lh-plugin-apple-reminders`](./lh-plugin-apple-reminders/) | Imports Apple Reminders as a read-only Lum Harbor source. | `source.fetch` | macOS, Apple silicon |
| [`lh-plugin-macos-notifications`](./lh-plugin-macos-notifications/) | Delivers Lum Harbor notifications through macOS Notification Center. | `notification.deliver` | macOS, Apple silicon |
| [`lh-plugin-synthetic-project`](./lh-plugin-synthetic-project/) | Supplies a deterministic sample project for testing source ingestion and read queries. | `source.fetch`, `read.query` | macOS, Apple silicon |
| [`lh-plugin-base`](./lh-plugin-base/) | Go library and example executable for building Plugin Runtime V1 plugins. It is not an end-user integration. | Example `read.query` | Go-supported platforms |

Follow the linked plugin README files for configuration and usage details.

## Requirements

- macOS 13 or later on Apple silicon for the packaged plugins
- Go 1.23 or later for the Go plugins and `lh-plugin-base`
- Swift 5.9 or later and the Xcode Command Line Tools for the Apple plugins
- `make` and `zip`

The macOS Notifications build also uses `sips`, `iconutil`, and `codesign`, which are supplied by macOS and the Xcode Command Line Tools. Its Makefile expects the Lum Harbor application icon at the path configured by `APP_ICON_SOURCE`.

## Build a plugin

Run the commands from the plugin directory you want to build:

```sh
cd lh-plugin-ai-runtime
make test
make package
```

Use the same `make test` and `make package` commands for `lh-plugin-macos-notifications` and `lh-plugin-synthetic-project`.

Apple Reminders does not currently define a test target:

```sh
cd lh-plugin-apple-reminders
make package
```

For any installable plugin, `make build` creates an unpacked artifact under `dist/<plugin-id>/`. `make package` also creates a canonical installable archive at `dist/<plugin-id>.lhplugin` and a versioned archive for distribution.

## Build the Go foundation and example

`lh-plugin-base` is a library with a small compatibility example rather than an installable production plugin:

```sh
cd lh-plugin-base
make test
make build
```

The example artifact is written to `lh-plugin-base/dist/lh-plugin-base/`. To cross-compile its example for Windows on AMD64, run `make build-windows`.

## Clean build output

From any module directory:

```sh
make clean
```
