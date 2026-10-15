# lh-plugin-apple-reminders

**Status: Lum Harbor Plugin Runtime V1 native extension for Apple Reminders.**
This Swift executable implements the Lum Harbor Plugin Runtime V1 protocol (`plugin.json` manifest, `system.handshake`, newline-JSON stdio RPC) directly. It declares and serves the `source.fetch` capability, reading Apple Reminders through EventKit and mapping them to structured Reminder records (`internal/plannersync/payload.go` in `lum-harbor-notifier`). It is read-only: it never creates, edits, completes or deletes a Reminder, and exposes no write capability.

## Building & Packaging

```sh
# Build the artifact directory under dist/lh-plugin-apple-reminders
make build

# Package the canonical .lhplugin distribution archive under dist/
make package

make clean
```

`dist/lh-plugin-apple-reminders/` is an unpacked Lum Harbor Plugin artifact directory.
`dist/lh-plugin-apple-reminders.lhplugin` is the packaged distribution archive installable through Lum Harbor.

## Run as a Plugin (Host entry point)

With no arguments, the binary serves the Plugin Runtime V1 protocol on stdio — this is the mode the Plugin Host invokes:

```sh
dist/lh-plugin-apple-reminders/lh-plugin-apple-reminders
```

It reads newline-delimited JSON RPC requests from stdin and writes newline-delimited JSON responses to stdout; stderr carries diagnostics. See `docs/contracts/plugin-runtime-contract-v1.md` for the protocol specification.

## Debug commands (manual diagnostics only, not used by the Plugin protocol)

```sh
dist/lh-plugin-apple-reminders/lh-plugin-apple-reminders status   # print current TCC authorization status
dist/lh-plugin-apple-reminders/lh-plugin-apple-reminders request  # request full Reminders access
dist/lh-plugin-apple-reminders/lh-plugin-apple-reminders list     # dump raw EventKit evidence
```
