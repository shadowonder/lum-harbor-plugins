import Foundation
import LumHarborNotificationSupport

func expect(_ condition: @autoclosure () -> Bool, _ message: String) {
    if !condition() {
        fatalError(message)
    }
}

expect(readinessAction(for: .notDetermined) == .requestAuthorization, "notDetermined must request permission")
expect(readinessAction(for: .denied) == .denyDelivery, "denied must fail delivery")
expect(readinessAction(for: .authorized) == .ready, "authorized must be ready")
expect(readinessAction(for: .provisional) == .ready, "provisional must be ready")
expect(readinessAction(for: .ephemeral) == .ready, "ephemeral must be ready")
expect(readinessAction(for: .unknown) == .unavailable, "unknown status must fail closed without becoming denied")

let normalSound = notificationSoundPolicy(isCritical: false)
expect(normalSound.modern == .systemDefault, "NORMAL must use the modern system default sound")
expect(
    normalSound.legacyName == "NSUserNotificationDefaultSoundName",
    "NORMAL must use the legacy default notification sound"
)

let criticalSound = notificationSoundPolicy(isCritical: true)
expect(
    criticalSound.modern == .bundled("Sosumi.aiff"),
    "CRITICAL must use the packaged Sosumi sound on the modern path"
)
expect(criticalSound.legacyName == "Sosumi", "CRITICAL must use Sosumi on the legacy path")

let metadata = NotificationActivationMetadata(
    notificationId: "notification-1",
    plannerItemId: "todo / 1",
    workItemId: nil
)
guard let url = metadata.activationURL(),
      let components = URLComponents(url: url, resolvingAgainstBaseURL: false) else {
    fatalError("activation metadata must produce a URL")
}
let values = Dictionary(uniqueKeysWithValues: (components.queryItems ?? []).map { ($0.name, $0.value) })
expect(components.scheme == "lumharbor", "unexpected activation scheme")
expect(components.host == "notification", "unexpected activation host")
expect(components.path == "/activate", "unexpected activation path")
expect(values["notificationId"] == "notification-1", "notification identity was not preserved")
expect(values["plannerItemId"] == "todo / 1", "planner target was not preserved")
expect(values["workItemId"] == nil, "absent work item target must stay absent")

print("notification support tests passed")
