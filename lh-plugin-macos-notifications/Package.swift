// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "lh-plugin-macos-notifications",
    platforms: [.macOS(.v13)],
    products: [
        .library(name: "LumHarborNotificationSupport", targets: ["LumHarborNotificationSupport"])
    ],
    targets: [
        .target(
            name: "LumHarborNotificationSupport",
            path: "Sources/LumHarborNotificationSupport"
        ),
        .executableTarget(
            name: "lh-plugin-macos-notifications",
            dependencies: ["LumHarborNotificationSupport"],
            path: "Sources/lh-plugin-macos-notifications"
        ),
        .executableTarget(
            name: "notification-support-tests",
            dependencies: ["LumHarborNotificationSupport"],
            path: "Tests/LumHarborNotificationSupportTests"
        ),
    ]
)
