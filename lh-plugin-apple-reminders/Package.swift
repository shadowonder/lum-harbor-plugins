// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "lh-plugin-apple-reminders",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(
            name: "lh-plugin-apple-reminders",
            path: "Sources/lh-plugin-apple-reminders"
        )
    ]
)
