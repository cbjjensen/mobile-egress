// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "MobileEgressCore",
    platforms: [
        .iOS(.v17),
        .macOS(.v13),
    ],
    products: [
        .library(name: "MobileEgressCore", targets: ["MobileEgressCore"]),
    ],
    targets: [
        .target(name: "MobileEgressCore"),
        .testTarget(name: "MobileEgressCoreTests", dependencies: ["MobileEgressCore"]),
    ]
)
