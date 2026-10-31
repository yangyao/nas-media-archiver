// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "NASArchiver",
    platforms: [.macOS("26.0")],
    products: [.executable(name: "NASArchiver", targets: ["NASArchiver"])],
    targets: [
        .executableTarget(name: "NASArchiver"),
        .testTarget(name: "NASArchiverTests", dependencies: ["NASArchiver"])
    ]
)
