import Foundation
import XCTest

final class XcodeProjectStructureTests: XCTestCase {
    func testPrivacyManifestDeclaresRetainedRequiredReasonAPIsAndIsPackagedInBothBundles() throws {
        let manifest = try plist(at: "MobileEgressAgent/PrivacyInfo.xcprivacy")
        XCTAssertEqual(Set(manifest.keys), ["NSPrivacyAccessedAPITypes"])
        let entries = try XCTUnwrap(manifest["NSPrivacyAccessedAPITypes"] as? [[String: Any]])
        var reasons: [String: Set<String>] = [:]
        for entry in entries {
            XCTAssertEqual(Set(entry.keys), ["NSPrivacyAccessedAPIType", "NSPrivacyAccessedAPITypeReasons"])
            let category = try XCTUnwrap(entry["NSPrivacyAccessedAPIType"] as? String)
            XCTAssertNil(reasons[category], "Duplicate required-reason category")
            reasons[category] = Set(try XCTUnwrap(entry["NSPrivacyAccessedAPITypeReasons"] as? [String]))
        }
        XCTAssertEqual(reasons, [
            "NSPrivacyAccessedAPICategoryUserDefaults": ["CA92.1", "1C8F.1"],
            "NSPrivacyAccessedAPICategorySystemBootTime": ["35F9.1"],
        ])
        let project = try text(at: "MobileEgressAgent.xcodeproj/project.pbxproj")
        XCTAssertTrue(project.contains("B00000000000000000000018 /* PrivacyInfo.xcprivacy */ = {isa = PBXFileReference; lastKnownFileType = text.xml; path = PrivacyInfo.xcprivacy; sourceTree = \"<group>\"; };"))
        XCTAssertTrue(project.contains("C00000000000000000000015 /* PrivacyInfo.xcprivacy in Resources */ = {isa = PBXBuildFile; fileRef = B00000000000000000000018 /* PrivacyInfo.xcprivacy */; };"))
        let resourcePhase = try XCTUnwrap(project.range(of: "E00000000000000000000003 /* Resources */ = {"))
        let phaseEnd = try XCTUnwrap(project.range(of: "runOnlyForDeploymentPostprocessing", range: resourcePhase.upperBound..<project.endIndex))
        XCTAssertTrue(project[resourcePhase.upperBound..<phaseEnd.lowerBound].contains("C00000000000000000000015 /* PrivacyInfo.xcprivacy in Resources */"))
        XCTAssertTrue(project.contains("C00000000000000000000016 /* PrivacyInfo.xcprivacy in Extension Resources */ = {isa = PBXBuildFile; fileRef = B00000000000000000000018 /* PrivacyInfo.xcprivacy */; };"))
        let extensionPhase = try XCTUnwrap(project.range(of: "E00000000000000000000007 /* Resources */ = {"))
        let extensionEnd = try XCTUnwrap(project.range(of: "runOnlyForDeploymentPostprocessing", range: extensionPhase.upperBound..<project.endIndex))
        XCTAssertTrue(project[extensionPhase.upperBound..<extensionEnd.lowerBound].contains("C00000000000000000000016 /* PrivacyInfo.xcprivacy in Extension Resources */"))
    }

    func testProjectHasExactlyTwoProductsWithLocalCoreLinkageAndExtensionEmbedding() throws {
        let project = try text(at: "MobileEgressAgent.xcodeproj/project.pbxproj")

        XCTAssertEqual(project.occurrences(of: "isa = PBXNativeTarget;"), 2)
        XCTAssertEqual(project.occurrences(of: "productType = \"com.apple.product-type.application\";"), 1)
        XCTAssertEqual(project.occurrences(of: "productType = \"com.apple.product-type.app-extension\";"), 1)
        XCTAssertTrue(project.contains("name = MobileEgressAgent;"))
        XCTAssertTrue(project.contains("name = MobileEgressTunnelExtension;"))
        XCTAssertEqual(project.occurrences(of: "productName = MobileEgressCore;"), 2)
        XCTAssertTrue(project.contains("isa = XCLocalSwiftPackageReference;"))
        XCTAssertTrue(project.contains("relativePath = .;"))
        XCTAssertTrue(project.contains("name = \"Embed App Extensions\";"))
        XCTAssertTrue(project.contains("dstSubfolderSpec = 13;"))
        XCTAssertTrue(project.contains("MobileEgressTunnelExtension.appex in Embed App Extensions"))
        XCTAssertFalse(project.contains("com.apple.product-type.bundle.unit-test"))
        XCTAssertFalse(project.contains("DEVELOPMENT_TEAM"))

        for source in expectedAppSources + expectedExtensionSources {
            XCTAssertTrue(project.contains("\(source) in Sources"), "Missing Sources build entry for \(source)")
        }
        XCTAssertTrue(project.contains("CODE_SIGN_ENTITLEMENTS = MobileEgressAgent/MobileEgressAgent.entitlements;"))
        XCTAssertTrue(project.contains("CODE_SIGN_ENTITLEMENTS = MobileEgressTunnelExtension/MobileEgressTunnelExtension.entitlements;"))
        XCTAssertTrue(project.contains("INFOPLIST_FILE = MobileEgressAgent/Info.plist;"))
        XCTAssertTrue(project.contains("INFOPLIST_FILE = MobileEgressTunnelExtension/Info.plist;"))
        XCTAssertTrue(project.contains("APPLICATION_EXTENSION_API_ONLY = YES;"))

        let candidatePattern = try NSRegularExpression(pattern: #"\b[A-Z0-9]{24,25}\b"#)
        let validIdentifierPattern = try NSRegularExpression(pattern: #"^[0-9A-F]{24}$"#)
        let range = NSRange(project.startIndex..., in: project)
        let candidates = candidatePattern.matches(in: project, range: range).compactMap { match in
            Range(match.range, in: project).map { String(project[$0]) }
        }
        XCTAssertFalse(candidates.isEmpty)
        for candidate in candidates {
            let candidateRange = NSRange(candidate.startIndex..., in: candidate)
            XCTAssertNotNil(
                validIdentifierPattern.firstMatch(in: candidate, range: candidateRange),
                "Invalid Xcode object identifier: \(candidate)"
            )
        }
    }

    func testPlistsEntitlementsAndResolvedConfigurationsAgreeWithoutTeamIdentifiers() throws {
        let appInfo = try plist(at: "MobileEgressAgent/Info.plist")
        let extensionInfo = try plist(at: "MobileEgressTunnelExtension/Info.plist")
        let appEntitlements = try plist(at: "MobileEgressAgent/MobileEgressAgent.entitlements")
        let extensionEntitlements = try plist(at: "MobileEgressTunnelExtension/MobileEgressTunnelExtension.entitlements")

        XCTAssertEqual(appInfo["CFBundlePackageType"] as? String, "APPL")
        XCTAssertEqual(appInfo["CFBundleDisplayName"] as? String, "ZFNF Mobile Egress")
        XCTAssertFalse((appInfo["NSCameraUsageDescription"] as? String ?? "").isEmpty)
        XCTAssertEqual(extensionInfo["CFBundlePackageType"] as? String, "XPC!")
        XCTAssertEqual(
            extensionInfo["CFBundleDisplayName"] as? String,
            "ZFNF Mobile Egress Agent"
        )
        for info in [appInfo, extensionInfo] {
            XCTAssertEqual(info["CFBundleShortVersionString"] as? String, "$(MARKETING_VERSION)")
            XCTAssertEqual(info["CFBundleVersion"] as? String, "$(CURRENT_PROJECT_VERSION)")
        }

        XCTAssertEqual(
            appInfo["UISupportedInterfaceOrientations"] as? [String],
            [
                "UIInterfaceOrientationPortrait",
                "UIInterfaceOrientationLandscapeLeft",
                "UIInterfaceOrientationLandscapeRight",
            ]
        )
        XCTAssertEqual(
            Set(appInfo["UISupportedInterfaceOrientations~ipad"] as? [String] ?? []),
            Set([
                "UIInterfaceOrientationPortrait",
                "UIInterfaceOrientationPortraitUpsideDown",
                "UIInterfaceOrientationLandscapeLeft",
                "UIInterfaceOrientationLandscapeRight",
            ])
        )

        let expectedProvider = "$(MOBILE_EGRESS_PROVIDER_BUNDLE_IDENTIFIER)"
        let expectedAppGroup = "$(MOBILE_EGRESS_APP_GROUP_IDENTIFIER)"
        let expectedKeychain = "$(AppIdentifierPrefix)$(MOBILE_EGRESS_KEYCHAIN_GROUP_SUFFIX)"
        for info in [appInfo, extensionInfo] {
            XCTAssertEqual(info["MobileEgressProviderBundleIdentifier"] as? String, expectedProvider)
            XCTAssertEqual(info["MobileEgressAppGroupIdentifier"] as? String, expectedAppGroup)
            XCTAssertEqual(info["MobileEgressKeychainAccessGroup"] as? String, expectedKeychain)
        }

        let extensionPoint = try XCTUnwrap(extensionInfo["NSExtension"] as? [String: Any])
        XCTAssertEqual(extensionPoint["NSExtensionPointIdentifier"] as? String, "com.apple.networkextension.packet-tunnel")
        XCTAssertEqual(extensionPoint["NSExtensionPrincipalClass"] as? String, "$(PRODUCT_MODULE_NAME).PacketTunnelProvider")

        let expectedNetworkExtension = ["packet-tunnel-provider"]
        let expectedAppGroups = [expectedAppGroup]
        let expectedKeychainGroups = [expectedKeychain]
        for entitlements in [appEntitlements, extensionEntitlements] {
            XCTAssertEqual(entitlements["com.apple.developer.networking.networkextension"] as? [String], expectedNetworkExtension)
            XCTAssertEqual(entitlements["com.apple.security.application-groups"] as? [String], expectedAppGroups)
            XCTAssertEqual(entitlements["keychain-access-groups"] as? [String], expectedKeychainGroups)
        }

        let debug = try resolvedXCConfig(at: "Configuration/Debug.xcconfig")
        let release = try resolvedXCConfig(at: "Configuration/Release.xcconfig")
        XCTAssertTrue(try text(at: "Configuration/Debug.xcconfig").contains(#"#include "Shared.xcconfig""#))
        XCTAssertTrue(try text(at: "Configuration/Release.xcconfig").contains(#"#include "Shared.xcconfig""#))
        for configuration in [debug, release] {
            XCTAssertEqual(configuration["IPHONEOS_DEPLOYMENT_TARGET"], "17.0")
            XCTAssertEqual(configuration["SWIFT_VERSION"], "6.0")
            XCTAssertEqual(configuration["MARKETING_VERSION"], "2.0.0")
            XCTAssertEqual(configuration["CURRENT_PROJECT_VERSION"], "4")
            XCTAssertEqual(configuration["MOBILE_EGRESS_PROVIDER_BUNDLE_IDENTIFIER"], "com.mobileegress.agent.tunnel")
            XCTAssertEqual(configuration["MOBILE_EGRESS_APP_GROUP_IDENTIFIER"], "group.com.mobileegress.agent")
            XCTAssertEqual(configuration["MOBILE_EGRESS_KEYCHAIN_GROUP_SUFFIX"], "com.mobileegress.agent.shared")
            XCTAssertNil(configuration["DEVELOPMENT_TEAM"])
        }
        XCTAssertEqual(debug["SWIFT_ACTIVE_COMPILATION_CONDITIONS"], "$(inherited) DEBUG")
        XCTAssertEqual(release["SWIFT_COMPILATION_MODE"], "wholemodule")

        let project = try text(at: "MobileEgressAgent.xcodeproj/project.pbxproj")
        XCTAssertEqual(project.occurrences(of: "baseConfigurationReference = B00000000000000000000031 /* Debug.xcconfig */;"), 3)
        XCTAssertEqual(project.occurrences(of: "baseConfigurationReference = B00000000000000000000032 /* Release.xcconfig */;"), 3)
        XCTAssertFalse(project.contains("MARKETING_VERSION ="))
        XCTAssertFalse(project.contains("CURRENT_PROJECT_VERSION ="))
    }

    func testWorkspaceAssetCatalogAndSourceInventoryAreComplete() throws {
        let workspace = try text(at: "MobileEgressAgent.xcodeproj/project.xcworkspace/contents.xcworkspacedata")
        XCTAssertTrue(workspace.contains("location = \"self:\""))
        _ = try plist(at: "MobileEgressAgent.xcodeproj/project.xcworkspace/xcshareddata/IDEWorkspaceChecks.plist")

        let scheme = try text(at: "MobileEgressAgent.xcodeproj/xcshareddata/xcschemes/MobileEgressAgent.xcscheme")
        XCTAssertTrue(scheme.contains("BlueprintName = \"MobileEgressAgent\""))
        XCTAssertTrue(scheme.contains("BlueprintName = \"MobileEgressTunnelExtension\""))

        let catalog = try json(at: "Assets/AppAssets.xcassets/Contents.json")
        let accent = try json(at: "Assets/AppAssets.xcassets/AccentColor.colorset/Contents.json")
        let appIcon = try json(at: "Assets/AppAssets.xcassets/AppIcon.appiconset/Contents.json")
        let header = try json(at: "Assets/AppAssets.xcassets/ZFNFHeader.imageset/Contents.json")
        XCTAssertNotNil(catalog["info"])
        XCTAssertNotNil(accent["colors"])
        let images = try XCTUnwrap(appIcon["images"] as? [[String: Any]])
        let universalIcon = try XCTUnwrap(images.first {
            $0["idiom"] as? String == "universal" &&
                $0["platform"] as? String == "ios" &&
                $0["size"] as? String == "1024x1024"
        })
        let iconFilename = try XCTUnwrap(universalIcon["filename"] as? String)
        let iconData = try data(at: "Assets/AppAssets.xcassets/AppIcon.appiconset/\(iconFilename)")
        XCTAssertEqual(Array(iconData.prefix(8)), [0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A])
        XCTAssertEqual(iconData.pngDimension(at: 16), 1_024)
        XCTAssertEqual(iconData.pngDimension(at: 20), 1_024)
        XCTAssertEqual(iconData[safe: 24], 8, "AppIcon must use 8-bit color channels")
        XCTAssertEqual(iconData[safe: 25], 2, "AppIcon must be opaque RGB without an alpha channel")

        let headerImages = try XCTUnwrap(header["images"] as? [[String: Any]])
        let universalHeader = try XCTUnwrap(headerImages.first {
            $0["idiom"] as? String == "universal" && $0["scale"] as? String == "1x"
        })
        let headerFilename = try XCTUnwrap(universalHeader["filename"] as? String)
        let headerData = try data(at: "Assets/AppAssets.xcassets/ZFNFHeader.imageset/\(headerFilename)")
        XCTAssertEqual(Array(headerData.prefix(8)), [0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A])
        XCTAssertEqual(headerData.pngDimension(at: 16), 256)
        XCTAssertEqual(headerData.pngDimension(at: 20), 256)
        XCTAssertEqual(headerData[safe: 24], 8, "Header image must use 8-bit color channels")
        XCTAssertEqual(headerData[safe: 25], 6, "Header image must preserve an alpha channel")

        XCTAssertEqual(try swiftFiles(in: "MobileEgressAgent"), Set(expectedAppSources))
        XCTAssertEqual(try swiftFiles(in: "MobileEgressTunnelExtension"), Set(expectedExtensionSources))
    }

    private let expectedAppSources = [
        "AgentDashboardView.swift",
        "AgentViewModel.swift",
        "MobileEgressAgentApp.swift",
        "MobileEgressDependencies.swift",
        "QRScannerView.swift",
        "TunnelManager.swift",
    ]

    private let expectedExtensionSources = [
        "ExtensionConfiguration.swift",
        "NoRoutesTunnelSettings.swift",
        "PacketTunnelProvider.swift",
    ]

    private var iosRoot: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
    }

    private func data(at relativePath: String) throws -> Data {
        let url = iosRoot.appendingPathComponent(relativePath)
        guard FileManager.default.fileExists(atPath: url.path) else {
            XCTFail("Missing required project artifact: \(relativePath)")
            throw ProjectFixtureError.missingArtifact
        }
        return try Data(contentsOf: url)
    }

    private func text(at relativePath: String) throws -> String {
        guard let value = String(data: try data(at: relativePath), encoding: .utf8) else {
            throw ProjectFixtureError.invalidArtifact
        }
        return value
    }

    private func plist(at relativePath: String) throws -> [String: Any] {
        let value = try PropertyListSerialization.propertyList(from: data(at: relativePath), format: nil)
        guard let dictionary = value as? [String: Any] else { throw ProjectFixtureError.invalidArtifact }
        return dictionary
    }

    private func json(at relativePath: String) throws -> [String: Any] {
        let value = try JSONSerialization.jsonObject(with: data(at: relativePath))
        guard let dictionary = value as? [String: Any] else { throw ProjectFixtureError.invalidArtifact }
        return dictionary
    }

    private func resolvedXCConfig(at relativePath: String) throws -> [String: String] {
        var visited = Set<String>()
        return try resolvedXCConfig(at: relativePath, visited: &visited)
    }

    private func resolvedXCConfig(
        at relativePath: String,
        visited: inout Set<String>
    ) throws -> [String: String] {
        guard visited.insert(relativePath).inserted else { throw ProjectFixtureError.invalidArtifact }
        let contents = try text(at: relativePath)
        var settings: [String: String] = [:]
        let directory = (relativePath as NSString).deletingLastPathComponent

        for rawLine in contents.split(whereSeparator: \.isNewline) {
            let line = rawLine.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("#include \"") && line.hasSuffix("\"") {
                let name = String(line.dropFirst("#include \"".count).dropLast())
                let includePath = (directory as NSString).appendingPathComponent(name)
                settings.merge(try resolvedXCConfig(at: includePath, visited: &visited)) { _, new in new }
                continue
            }
            guard !line.isEmpty, !line.hasPrefix("//"), let separator = line.firstIndex(of: "=") else {
                continue
            }
            let key = line[..<separator].trimmingCharacters(in: .whitespaces)
            let value = line[line.index(after: separator)...].trimmingCharacters(in: .whitespaces)
            settings[key] = value
        }
        return settings
    }

    private func swiftFiles(in relativePath: String) throws -> Set<String> {
        let directory = iosRoot.appendingPathComponent(relativePath)
        let files = try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)
        return Set(files.filter { $0.pathExtension == "swift" }.map(\.lastPathComponent))
    }
}

private enum ProjectFixtureError: Error {
    case missingArtifact
    case invalidArtifact
}

private extension String {
    func occurrences(of needle: String) -> Int {
        components(separatedBy: needle).count - 1
    }
}

private extension Data {
    subscript(safe index: Int) -> UInt8? {
        indices.contains(index) ? self[index] : nil
    }

    func pngDimension(at offset: Int) -> UInt32? {
        guard count >= offset + 4 else { return nil }
        return self[offset..<(offset + 4)].reduce(0) { ($0 << 8) | UInt32($1) }
    }
}
