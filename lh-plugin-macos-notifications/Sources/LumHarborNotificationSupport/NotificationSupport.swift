import Foundation

public enum NotificationAuthorizationState: String, Equatable {
    case notDetermined = "NOT_DETERMINED"
    case denied = "DENIED"
    case authorized = "AUTHORIZED"
    case provisional = "PROVISIONAL"
    case ephemeral = "EPHEMERAL"
    case unknown = "UNKNOWN"
}

public enum NotificationReadinessAction: Equatable {
    case requestAuthorization
    case ready
    case denyDelivery
    case unavailable
}

public func readinessAction(for state: NotificationAuthorizationState) -> NotificationReadinessAction {
    switch state {
    case .notDetermined:
        return .requestAuthorization
    case .denied:
        return .denyDelivery
    case .authorized, .provisional, .ephemeral:
        return .ready
    case .unknown:
        return .unavailable
    }
}

public enum ModernNotificationSound: Equatable {
    case systemDefault
    case bundled(String)
}

public struct NotificationSoundPolicy: Equatable {
    public let modern: ModernNotificationSound
    public let legacyName: String

    public init(modern: ModernNotificationSound, legacyName: String) {
        self.modern = modern
        self.legacyName = legacyName
    }
}

public func notificationSoundPolicy(isCritical: Bool) -> NotificationSoundPolicy {
    if isCritical {
        return NotificationSoundPolicy(modern: .bundled("Sosumi.aiff"), legacyName: "Sosumi")
    }
    return NotificationSoundPolicy(
        modern: .systemDefault,
        legacyName: "NSUserNotificationDefaultSoundName"
    )
}

public struct NotificationActivationMetadata: Equatable {
    public let notificationId: String
    public let plannerItemId: String?
    public let workItemId: String?

    public init(
        notificationId: String,
        plannerItemId: String?,
        workItemId: String?
    ) {
        self.notificationId = notificationId
        self.plannerItemId = plannerItemId
        self.workItemId = workItemId
    }

    public func activationURL() -> URL? {
        var components = URLComponents()
        components.scheme = "lumharbor"
        components.host = "notification"
        components.path = "/activate"
        var queryItems = [URLQueryItem(name: "notificationId", value: notificationId)]
        if let plannerItemId, !plannerItemId.isEmpty {
            queryItems.append(URLQueryItem(name: "plannerItemId", value: plannerItemId))
        }
        if let workItemId, !workItemId.isEmpty {
            queryItems.append(URLQueryItem(name: "workItemId", value: workItemId))
        }
        components.queryItems = queryItems
        return components.url
    }
}
