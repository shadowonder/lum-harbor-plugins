// notification-macos-bridge
//
// Lum Harbor Plugin Runtime V1 native extension for macOS notification delivery.
// Provides the `notification.deliver` capability.
//
// In accordance with M17 Slice 2:
// - Uses stable DeliveryID as external notification identifier
// - Performs duplicate suppression: inspects pending/delivered notifications
// - Presents NORMAL vs CRITICAL/SEVERE with noticeably distinct behavior (sound, subtitle)
// - Supports both UNUserNotificationCenter and NSUserNotificationCenter
// - Serves the Plugin Runtime V1 newline-framed JSON RPC protocol over stdio

import Foundation
import UserNotifications
import AppKit
import CoreServices
import LumHarborNotificationSupport

// MARK: - Plugin Identity

let extensionID = "lh-plugin-macos-notifications"
let extensionVersion = "0.1.0"
let protocolVersion = 1
let notificationDeliverCapability = "notification.deliver"

// MARK: - Presentation & Mapping Models

struct DeliveryPayload: Codable {
    let deliveryId: String
    let title: String
    let message: String
    let urgency: String // "NORMAL" | "CRITICAL"
    let plannerItemId: String?
    let workItemId: String?
}

struct DeliveryPresentation {
    let identifier: String
    let title: String
    let subtitle: String
    let message: String
    let isCritical: Bool
    let activationURL: URL?
}

func mapPresentation(_ payload: DeliveryPayload) -> DeliveryPresentation {
    let isCritical = payload.urgency.uppercased() == "CRITICAL"
    if isCritical {
        return DeliveryPresentation(
            identifier: payload.deliveryId,
            title: payload.title,
            subtitle: "CRITICAL / SEVERE",
            message: payload.message,
            isCritical: true,
            activationURL: NotificationActivationMetadata(
                notificationId: payload.deliveryId,
                plannerItemId: payload.plannerItemId,
                workItemId: payload.workItemId
            ).activationURL()
        )
    } else {
        return DeliveryPresentation(
            identifier: payload.deliveryId,
            title: payload.title,
            subtitle: "Normal Priority",
            message: payload.message,
            isCritical: false,
            activationURL: NotificationActivationMetadata(
                notificationId: payload.deliveryId,
                plannerItemId: payload.plannerItemId,
                workItemId: payload.workItemId
            ).activationURL()
        )
    }
}

// MARK: - Logging & RPC Helpers

func logStderr(_ message: String) {
    let line = "[lh-plugin-macos-notifications] \(message)\n"
    if let data = line.data(using: .utf8) {
        FileHandle.standardError.write(data)
    }
}

func writeJSONLine(_ object: [String: Any]) {
    guard let data = try? JSONSerialization.data(withJSONObject: object, options: []) else {
        logStderr("failed to encode response: \(object)")
        return
    }
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data([0x0A]))
}

func successResponse(id: String, result: [String: Any] = [:]) -> [String: Any] {
    ["id": id, "result": result]
}

func errorResponse(id: String, code: String, message: String) -> [String: Any] {
    ["id": id, "error": ["code": code, "message": message]]
}

func decodeRequestLine(_ line: String) -> [String: Any]? {
    guard let data = line.data(using: .utf8) else { return nil }
    return (try? JSONSerialization.jsonObject(with: data, options: [])) as? [String: Any]
}

// MARK: - Duplicate Suppression & Platform Delivery

func authorizationState(_ status: UNAuthorizationStatus) -> NotificationAuthorizationState {
    switch status {
    case .notDetermined:
        return .notDetermined
    case .denied:
        return .denied
    case .authorized:
        return .authorized
    case .provisional:
        return .provisional
    case .ephemeral:
        return .ephemeral
    @unknown default:
        return .unknown
    }
}

@MainActor
func openLumHarborActivationURL(_ url: URL) {
    _ = NSWorkspace.shared.open(url)
    if let app = NSRunningApplication.runningApplications(
        withBundleIdentifier: "com.wails.lum-harbor-notifier"
    ).first {
        app.activate(options: [.activateAllWindows, .activateIgnoringOtherApps])
    }
}

final class NotificationResponseDelegate: NSObject, UNUserNotificationCenterDelegate, NSUserNotificationCenterDelegate {
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        defer { completionHandler() }
        guard let rawURL = response.notification.request.content.userInfo["activationURL"] as? String,
              let url = URL(string: rawURL) else {
            return
        }
        Task { @MainActor in
            openLumHarborActivationURL(url)
        }
    }

    func userNotificationCenter(
        _ center: NSUserNotificationCenter,
        didActivate notification: NSUserNotification
    ) {
        guard let rawURL = notification.userInfo?["activationURL"] as? String,
              let url = URL(string: rawURL) else {
            return
        }
        Task { @MainActor in
            openLumHarborActivationURL(url)
        }
    }

    func userNotificationCenter(
        _ center: NSUserNotificationCenter,
        shouldPresent notification: NSUserNotification
    ) -> Bool {
        true
    }
}

@MainActor
class PlatformDeliveryManager {
    static let shared = PlatformDeliveryManager()
    private let responseDelegate = NotificationResponseDelegate()

    private init() {
        UNUserNotificationCenter.current().delegate = responseDelegate
        NSUserNotificationCenter.default.delegate = responseDelegate
    }

    func checkReadiness() async -> Result<NotificationAuthorizationState, Error> {
        let center = UNUserNotificationCenter.current()
        var state = authorizationState((await center.notificationSettings()).authorizationStatus)
        if readinessAction(for: state) == .requestAuthorization {
            do {
                _ = try await center.requestAuthorization(options: [.alert, .sound, .badge])
                state = authorizationState((await center.notificationSettings()).authorizationStatus)
            } catch {
                return .failure(error)
            }
        }
        return .success(state)
    }
    
    /// Checks whether a notification with this DeliveryID is already pending or visibly delivered
    func isAlreadyPresent(deliveryId: String) async -> Bool {
        // 1. Check NSUserNotificationCenter delivered notifications
        if let centerClass = NSClassFromString("NSUserNotificationCenter") as? NSObject.Type,
           let center = centerClass.perform(NSSelectorFromString("defaultUserNotificationCenter"))?.takeUnretainedValue() {
            if let delivered = center.perform(NSSelectorFromString("deliveredNotifications"))?.takeUnretainedValue() as? [NSObject] {
                for n in delivered {
                    if let id = n.perform(NSSelectorFromString("identifier"))?.takeUnretainedValue() as? String, id == deliveryId {
                        return true
                    }
                }
            }
            if let scheduled = center.perform(NSSelectorFromString("scheduledNotifications"))?.takeUnretainedValue() as? [NSObject] {
                for n in scheduled {
                    if let id = n.perform(NSSelectorFromString("identifier"))?.takeUnretainedValue() as? String, id == deliveryId {
                        return true
                    }
                }
            }
        }
        
        // 2. Check UNUserNotificationCenter if active
        let unCenter = UNUserNotificationCenter.current()
        let pending = await unCenter.pendingNotificationRequests()
        if pending.contains(where: { $0.identifier == deliveryId }) {
            return true
        }
        let delivered = await unCenter.deliveredNotifications()
        if delivered.contains(where: { $0.request.identifier == deliveryId }) {
            return true
        }
        
        return false
    }
    
    /// Delivers the presentation via available macOS notification mechanisms
    func deliver(_ presentation: DeliveryPresentation) async -> Result<Void, Error> {
        // Check duplicate suppression first
        if await isAlreadyPresent(deliveryId: presentation.identifier) {
            logStderr("duplicate suppressed for DeliveryID \(presentation.identifier) (already pending or delivered)")
            return .success(())
        }
        
        // Attempt UNUserNotificationCenter first if authorized
        let unCenter = UNUserNotificationCenter.current()
        let settings = await unCenter.notificationSettings()
        let state = authorizationState(settings.authorizationStatus)
        // The user turned notifications off for this bundle: macOS silently
        // discards anything handed to either API, so reporting success would
        // make Core record a delivery that never happened.
        if readinessAction(for: state) == .denyDelivery {
            logStderr("delivery refused: macOS notifications are turned off for this bundle (DeliveryID=\(presentation.identifier))")
            return .failure(NSError(domain: "io.lumharbor.notification", code: 2, userInfo: [
                NSLocalizedDescriptionKey: "macOS notifications are turned off for Lum Harbor Notification Bridge in System Settings > Notifications"
            ]))
        }
        if readinessAction(for: state) == .unavailable {
            return .failure(NSError(domain: "io.lumharbor.notification", code: 4, userInfo: [
                NSLocalizedDescriptionKey: "macOS notification permission status is unavailable"
            ]))
        }
        if readinessAction(for: state) == .ready {
            let content = UNMutableNotificationContent()
            let soundPolicy = notificationSoundPolicy(isCritical: presentation.isCritical)
            content.title = presentation.title
            content.subtitle = presentation.subtitle
            content.body = presentation.message
            if let activationURL = presentation.activationURL {
                content.userInfo = ["activationURL": activationURL.absoluteString]
            }
            if presentation.isCritical {
                content.interruptionLevel = .timeSensitive
            } else {
                content.interruptionLevel = .active
            }
            switch soundPolicy.modern {
            case .systemDefault:
                content.sound = .default
            case .bundled(let name):
                content.sound = UNNotificationSound(named: UNNotificationSoundName(name))
            }
            
            let req = UNNotificationRequest(identifier: presentation.identifier, content: content, trigger: nil)
            do {
                try await unCenter.add(req)
                logStderr("delivered via UNUserNotificationCenter (DeliveryID=\(presentation.identifier), urgency=\(presentation.isCritical ? "CRITICAL" : "NORMAL"))")
                return .success(())
            } catch {
                logStderr("UNUserNotificationCenter add failed: \(error); attempting NSUserNotificationCenter fallback")
            }
        }
        
        if state == .notDetermined {
            return .failure(NSError(domain: "io.lumharbor.notification", code: 3, userInfo: [
                NSLocalizedDescriptionKey: "macOS notification permission has not been granted for Lum Harbor Notification Bridge"
            ]))
        }

        // Deliver via NSUserNotificationCenter only as a compatibility fallback
        // after an authorized modern delivery attempt failed.
        if let centerClass = NSClassFromString("NSUserNotificationCenter") as? NSObject.Type,
           let center = centerClass.perform(NSSelectorFromString("defaultUserNotificationCenter"))?.takeUnretainedValue() {
            let soundPolicy = notificationSoundPolicy(isCritical: presentation.isCritical)
            let notification = NSClassFromString("NSUserNotification")!.alloc()
            _ = notification.perform(NSSelectorFromString("init"))
            _ = notification.perform(NSSelectorFromString("setIdentifier:"), with: presentation.identifier)
            _ = notification.perform(NSSelectorFromString("setTitle:"), with: presentation.title)
            _ = notification.perform(NSSelectorFromString("setSubtitle:"), with: presentation.subtitle)
            _ = notification.perform(NSSelectorFromString("setInformativeText:"), with: presentation.message)
            _ = notification.perform(NSSelectorFromString("setSoundName:"), with: soundPolicy.legacyName)
            if let activationURL = presentation.activationURL {
                _ = notification.perform(
                    NSSelectorFromString("setUserInfo:"),
                    with: ["activationURL": activationURL.absoluteString]
                )
            }
            
            _ = center.perform(NSSelectorFromString("deliverNotification:"), with: notification)
            logStderr("delivered via NSUserNotificationCenter (DeliveryID=\(presentation.identifier), sound=\(soundPolicy.legacyName), critical=\(presentation.isCritical))")
            return .success(())
        }
        
        return .failure(NSError(domain: "io.lumharbor.notification", code: 1, userInfo: [
            NSLocalizedDescriptionKey: "Neither UNUserNotificationCenter nor NSUserNotificationCenter was available to deliver notification"
        ]))
    }
}

// MARK: - RPC Protocol Handlers

func handleHandshake(id: String, params: [String: Any]) {
    guard
        let hostProtocolVersion = params["protocolVersion"] as? Int,
        let hostExtensionID = params["extensionId"] as? String,
        let hostExtensionVersion = params["extensionVersion"] as? String,
        let hostCapabilities = params["capabilities"] as? [String]
    else {
        writeJSONLine(errorResponse(id: id, code: "invalid_handshake", message: "malformed handshake params"))
        return
    }
    
    let matches = hostProtocolVersion == protocolVersion
        && hostExtensionID == extensionID
        && hostExtensionVersion == extensionVersion
        && Set(hostCapabilities) == Set([notificationDeliverCapability])
        
    guard matches else {
        writeJSONLine(errorResponse(id: id, code: "incompatible_handshake", message: "host handshake parameters do not match extension manifest"))
        return
    }
    
    writeJSONLine(successResponse(id: id, result: [
        "protocolVersion": protocolVersion,
        "extensionId": extensionID,
        "extensionVersion": extensionVersion,
        "capabilities": [notificationDeliverCapability],
    ]))
}

func handleCapabilityInvoke(id: String, params: [String: Any]) async {
    guard let capabilityID = params["capabilityId"] as? String else {
        writeJSONLine(errorResponse(id: id, code: "invalid_invocation", message: "missing capabilityId"))
        return
    }
    guard capabilityID == notificationDeliverCapability else {
        writeJSONLine(errorResponse(id: id, code: "capability_not_supported", message: "unsupported capability: \(capabilityID)"))
        return
    }
    
    guard let input = params["input"] as? [String: Any] else {
        writeJSONLine(errorResponse(id: id, code: "invalid_request", message: "missing notification.deliver input"))
        return
    }

    let operation = (input["operation"] as? String ?? "DELIVER").uppercased()
    if operation == "CHECK_READINESS" {
        let result = await PlatformDeliveryManager.shared.checkReadiness()
        switch result {
        case .success(let state):
            writeJSONLine(successResponse(id: id, result: ["authorizationStatus": state.rawValue]))
        case .failure(let error):
            writeJSONLine(errorResponse(id: id, code: "authorization_check_failed", message: error.localizedDescription))
        }
        return
    }
    guard operation == "DELIVER" else {
        writeJSONLine(errorResponse(id: id, code: "invalid_request", message: "unsupported notification.deliver operation"))
        return
    }

    guard
          let deliveryID = input["deliveryId"] as? String, !deliveryID.trimmingCharacters(in: .whitespaces).isEmpty,
          let title = input["title"] as? String, !title.trimmingCharacters(in: .whitespaces).isEmpty,
          let message = input["message"] as? String, !message.trimmingCharacters(in: .whitespaces).isEmpty,
          let urgency = input["urgency"] as? String, !urgency.trimmingCharacters(in: .whitespaces).isEmpty
    else {
        writeJSONLine(errorResponse(id: id, code: "invalid_request", message: "malformed notification.deliver payload"))
        return
    }
    
    let payload = DeliveryPayload(
        deliveryId: deliveryID.trimmingCharacters(in: .whitespaces),
        title: title,
        message: message,
        urgency: urgency,
        plannerItemId: input["plannerItemId"] as? String,
        workItemId: input["workItemId"] as? String
    )
    
    let presentation = mapPresentation(payload)
    let result = await PlatformDeliveryManager.shared.deliver(presentation)
    switch result {
    case .success:
        writeJSONLine(successResponse(id: id, result: [:]))
    case .failure(let error):
        writeJSONLine(errorResponse(id: id, code: "delivery_failed", message: error.localizedDescription))
    }
}

// MARK: - Server Loop

func runPluginServer() async {
    logStderr("started protocol server")
    while let line = readLine(strippingNewline: true) {
        guard let request = decodeRequestLine(line) else {
            continue
        }
        guard
            let id = request["id"] as? String, !id.isEmpty,
            let method = request["method"] as? String, !method.isEmpty
        else {
            continue
        }
        
        let params = request["params"] as? [String: Any] ?? [:]
        switch method {
        case "system.handshake":
            handleHandshake(id: id, params: params)
        case "capability.invoke":
            await handleCapabilityInvoke(id: id, params: params)
        case "system.shutdown":
            writeJSONLine(successResponse(id: id, result: ["accepted": true]))
            logStderr("shutdown accepted")
            return
        default:
            writeJSONLine(errorResponse(id: id, code: "method_not_supported", message: "unsupported method: \(method)"))
        }
    }
    logStderr("stdin closed")
}

// MARK: - CLI Debug Modes

func runStatus() async {
    var nsCount = 0
    if let centerClass = NSClassFromString("NSUserNotificationCenter") as? NSObject.Type,
       let center = centerClass.perform(NSSelectorFromString("defaultUserNotificationCenter"))?.takeUnretainedValue(),
       let delivered = center.perform(NSSelectorFromString("deliveredNotifications"))?.takeUnretainedValue() as? [NSObject] {
        nsCount = delivered.count
    }
    let unCenter = UNUserNotificationCenter.current()
    let settings = await unCenter.notificationSettings()
    let unDelivered = await unCenter.deliveredNotifications()
    
    let statusObj: [String: Any] = [
        "extensionId": extensionID,
        "extensionVersion": extensionVersion,
        "unAuthorizationStatus": settings.authorizationStatus.rawValue,
        "unSoundSetting": settings.soundSetting.rawValue,
        "unDeliveredCount": unDelivered.count,
        "nsDeliveredCount": nsCount
    ]
    if let data = try? JSONSerialization.data(withJSONObject: statusObj, options: [.prettyPrinted, .sortedKeys]),
       let str = String(data: data, encoding: .utf8) {
        print(str)
    }
}

func runList() async {
    var identifiers: [String] = []
    if let centerClass = NSClassFromString("NSUserNotificationCenter") as? NSObject.Type,
       let center = centerClass.perform(NSSelectorFromString("defaultUserNotificationCenter"))?.takeUnretainedValue(),
       let delivered = center.perform(NSSelectorFromString("deliveredNotifications"))?.takeUnretainedValue() as? [NSObject] {
        for n in delivered {
            if let id = n.perform(NSSelectorFromString("identifier"))?.takeUnretainedValue() as? String {
                identifiers.append(id)
            }
        }
    }
    let unCenter = UNUserNotificationCenter.current()
    let unDelivered = await unCenter.deliveredNotifications()
    for d in unDelivered {
        identifiers.append(d.request.identifier)
    }
    
    let listObj: [String: Any] = [
        "deliveredCount": identifiers.count,
        "identifiers": identifiers
    ]
    if let data = try? JSONSerialization.data(withJSONObject: listObj, options: [.prettyPrinted, .sortedKeys]),
       let str = String(data: data, encoding: .utf8) {
        print(str)
    }
}

func runDeliverCLI(args: [String]) async {
    guard args.count >= 4 else {
        print("Usage: lh-plugin-macos-notifications deliver <id> <title> <message> [NORMAL|CRITICAL]")
        exit(1)
    }
    let deliveryID = args[1]
    let title = args[2]
    let message = args[3]
    let urgency = args.count > 4 ? args[4] : "NORMAL"
    
    let payload = DeliveryPayload(
        deliveryId: deliveryID,
        title: title,
        message: message,
        urgency: urgency,
        plannerItemId: nil,
        workItemId: nil
    )
    let presentation = mapPresentation(payload)
    let result = await PlatformDeliveryManager.shared.deliver(presentation)
    switch result {
    case .success:
        print("SUCCESS: Delivered notification \(deliveryID)")
    case .failure(let error):
        print("ERROR: Failed to deliver notification \(deliveryID): \(error)")
        exit(1)
    }
}

// MARK: - Entry Point

// Self-register app bundle with LaunchServices if running inside an .app bundle
let bundleURL = Bundle.main.bundleURL
if bundleURL.pathExtension == "app" {
    let regStatus = LSRegisterURL(bundleURL as CFURL, true)
    if regStatus != noErr {
        logStderr("LSRegisterURL returned status \(regStatus)")
    }
}

let arguments = CommandLine.arguments
let mode = arguments.count > 1 ? arguments[1] : "serve"

switch mode {
case "serve":
    _ = PlatformDeliveryManager.shared
    if ProcessInfo.processInfo.environment["LUM_HARBOR_PLUGIN_DATA_DIR"] == nil,
       Bundle.main.bundleURL.pathExtension == "app" {
        NSApplication.shared.run()
    } else {
        await runPluginServer()
    }
case "status":
    await runStatus()
case "list":
    await runList()
case "deliver":
    await runDeliverCLI(args: Array(arguments.dropFirst()))
default:
    FileHandle.standardError.write("unknown mode \(mode); expected (no args / serve)|status|list|deliver\n".data(using: .utf8)!)
    exit(1)
}
