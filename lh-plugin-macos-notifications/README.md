# lh-plugin-macos-notifications

Lum Harbor Plugin Runtime V1 native extension for Apple macOS notification delivery.
Implements the Host-owned typed capability `notification.deliver`.

## Capabilities

- **`notification.deliver`**: Ensures external user-visible delivery for a stable `DeliveryID`.

## Architecture & Semantics

- **Platform-neutral request**: Receives `DeliveryID`, `Title`, `Message`, and `Urgency` (`NORMAL` vs `CRITICAL`).
- **Duplicate Suppression**: Inspects pending and delivered notifications before scheduling. Repeated calls with the same `DeliveryID` do not produce duplicate alerts.
- **Urgency Presentation**:
  - `NORMAL`: Standard macOS system notification with default sound.
  - `CRITICAL`: Heightened presentation with `"Sosumi"` alert sound and `"CRITICAL / SEVERE"` subtitle.
- **Zero Business Truth Mutation**: Never modifies Core DB tables or Notification lifecycle states.

## Building & Packaging

```bash
# Build the artifact directory under dist/lh-plugin-macos-notifications
make build

# Package the canonical .lhplugin distribution archive under dist/
make package
```
