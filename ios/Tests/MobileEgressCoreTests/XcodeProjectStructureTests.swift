import Foundation
import XCTest

final class XcodeProjectStructureTests: XCTestCase {
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
        XCTAssertFalse((appInfo["NSCameraUsageDescription"] as? String ?? "").isEmpty)
        XCTAssertEqual(extensionInfo["CFBundlePackageType"] as? String, "XPC!")

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
        for configuration in [debug, release] {
            XCTAssertEqual(configuration["IPHONEOS_DEPLOYMENT_TARGET"], "17.0")
            XCTAssertEqual(configuration["SWIFT_VERSION"], "6.0")
            XCTAssertEqual(configuration["MOBILE_EGRESS_PROVIDER_BUNDLE_IDENTIFIER"], "com.mobileegress.agent.tunnel")
            XCTAssertEqual(configuration["MOBILE_EGRESS_APP_GROUP_IDENTIFIER"], "group.com.mobileegress.agent")
            XCTAssertEqual(configuration["MOBILE_EGRESS_KEYCHAIN_GROUP_SUFFIX"], "com.mobileegress.agent.shared")
            XCTAssertNil(configuration["DEVELOPMENT_TEAM"])
        }
        XCTAssertEqual(debug["SWIFT_ACTIVE_COMPILATION_CONDITIONS"], "$(inherited) DEBUG")
        XCTAssertEqual(release["SWIFT_COMPILATION_MODE"], "wholemodule")
    }

    func testWorkspaceAssetCatalogAndSourceInventoryAreComplete() throws {
        let workspace = try text(at: "MobileEgressAgent.xcodeproj/project.xcworkspace/contents.xcworkspacedata")
        XCTAssertTrue(workspace.contains("location = \"self:\""))
        _ = try plist(at: "MobileEgressAgent.xcodeproj/project.xcworkspace/xcshareddata/IDEWorkspaceChecks.plist")

        let scheme = try text(at: "MobileEgressAgent.xcodeproj/xcshareddata/xcschemes/MobileEgressAgent.xcscheme")
        XCTAssertTrue(scheme.contains("BlueprintName = \"MobileEgressAgent\""))
        XCTAssertTrue(scheme.contains("BlueprintName = \"MobileEgressTunnelExtension\""))

        let noRoutes = try text(at: "MobileEgressTunnelExtension/NoRoutesTunnelSettings.swift")
        XCTAssertEqual(noRoutes.occurrences(of: "includedRoutes = []"), 2)
        XCTAssertFalse(noRoutes.contains("NEIPv4Route.default"))
        XCTAssertFalse(noRoutes.contains("NEIPv6Route.default"))
        XCTAssertFalse(noRoutes.contains("0.0.0.0"))
        XCTAssertFalse(noRoutes.contains("::/0"))

        let manager = try text(at: "MobileEgressAgent/TunnelManager.swift")
        XCTAssertTrue(manager.contains("providerBundleIdentifier = configuration.providerBundleIdentifier"))
        XCTAssertTrue(manager.contains("disconnectOnSleep = false"))
        XCTAssertTrue(manager.contains("NEOnDemandRuleConnect()"))
        XCTAssertTrue(manager.contains("manager.isOnDemandEnabled = onDemandEnabled"))

        let catalog = try json(at: "Assets/AppAssets.xcassets/Contents.json")
        let accent = try json(at: "Assets/AppAssets.xcassets/AccentColor.colorset/Contents.json")
        let appIcon = try json(at: "Assets/AppAssets.xcassets/AppIcon.appiconset/Contents.json")
        XCTAssertNotNil(catalog["info"])
        XCTAssertNotNil(accent["colors"])
        let images = try XCTUnwrap(appIcon["images"] as? [[String: Any]])
        XCTAssertTrue(images.contains {
            $0["idiom"] as? String == "universal" &&
                $0["platform"] as? String == "ios" &&
                $0["size"] as? String == "1024x1024"
        })

        XCTAssertEqual(try swiftFiles(in: "MobileEgressAgent"), Set(expectedAppSources))
        XCTAssertEqual(try swiftFiles(in: "MobileEgressTunnelExtension"), Set(expectedExtensionSources))
    }

    func testAppleStatusRefreshUsesFiniteLastDisconnectErrorState() throws {
        let manager = try text(at: "MobileEgressAgent/TunnelManager.swift")
        let viewModel = try text(at: "MobileEgressAgent/AgentViewModel.swift")
        let provider = try text(at: "MobileEgressTunnelExtension/PacketTunnelProvider.swift")

        XCTAssertTrue(manager.contains("fetchLastDisconnectError"))
        XCTAssertTrue(manager.contains("TunnelProviderErrorClass.classifyDisconnectError"))
        XCTAssertTrue(viewModel.contains("connectionState.startRequested()"))
        XCTAssertTrue(viewModel.contains("connectionState.stopRequested()"))
        XCTAssertTrue(viewModel.contains("connectionState.restorePersistentIntent("))
        XCTAssertTrue(viewModel.contains("connectionState.observe("))
        XCTAssertTrue(provider.contains("TunnelProviderErrorClass.providerErrorDomain"))
        XCTAssertTrue(provider.contains("providerErrorCode"))
        XCTAssertFalse(manager.contains("error.localizedDescription"))
        XCTAssertFalse(manager.contains("nsError.localizedDescription"))
        XCTAssertFalse(viewModel.contains("error.localizedDescription"))
    }

    func testAppleStatusNotificationsPreserveCurrentAttemptLifecycleEvidence() throws {
        let manager = try text(at: "MobileEgressAgent/TunnelManager.swift")
        let viewModel = try text(at: "MobileEgressAgent/AgentViewModel.swift")

        XCTAssertTrue(manager.contains("NotificationCenter.default.addObserver("))
        XCTAssertTrue(manager.contains(".NEVPNStatusDidChange"))
        XCTAssertTrue(manager.contains("func statusUpdates()"))
        XCTAssertTrue(viewModel.contains("tunnelManager.statusUpdates()"))
        XCTAssertTrue(viewModel.contains("connectionStatusTask"))
        XCTAssertTrue(viewModel.contains("refresh(observedPhase: phase)"))
    }

    func testAppleRefreshRejectsReentrantStaleAsyncResults() throws {
        let viewModel = try text(at: "MobileEgressAgent/AgentViewModel.swift")

        XCTAssertTrue(viewModel.contains(
            "let observationToken = connectionState.observationToken"
        ))
        XCTAssertTrue(viewModel.contains("matching: observationToken"))
        XCTAssertEqual(
            viewModel.occurrences(
                of: "guard connectionState.isCurrent(providerStatusToken) else { return }"
            ),
            2
        )
    }

    func testAgentViewModelReportsBothExplicitStopTransactionOutcomes() throws {
        let viewModel = try text(at: "MobileEgressAgent/AgentViewModel.swift")

        XCTAssertTrue(viewModel.contains(
            "connectionState.stopTransactionCompleted(persistenceSucceeded: true)"
        ))
        XCTAssertTrue(viewModel.contains(
            "connectionState.stopTransactionCompleted(persistenceSucceeded: false)"
        ))
    }

    func testAppCommandPresentationAndActionConsumePortableDecision() throws {
        let viewModel = try text(at: "MobileEgressAgent/AgentViewModel.swift")
        let dashboard = try text(at: "MobileEgressAgent/AgentDashboardView.swift")

        XCTAssertTrue(viewModel.contains("var tunnelCommandDecision: TunnelCommandDecision"))
        XCTAssertTrue(viewModel.contains("TunnelCommandDecision.resolve("))
        XCTAssertTrue(viewModel.contains("tunnelCommandDecision.isEnabled"))
        XCTAssertTrue(viewModel.contains("let decision = tunnelCommandDecision"))
        XCTAssertTrue(viewModel.contains("changeTunnelState(command: decision.command)"))
        XCTAssertTrue(viewModel.contains("switch command"))
        XCTAssertFalse(viewModel.contains("if isTunnelActive"))
        XCTAssertTrue(dashboard.contains("let commandDecision = model.tunnelCommandDecision"))
        XCTAssertTrue(dashboard.contains("commandDecision.isDestructive"))
        XCTAssertTrue(dashboard.contains("commandDecision.command == .stop"))
        XCTAssertTrue(dashboard.contains(".disabled(!model.canToggleTunnel)"))
    }

    func testAppleManagerConsumesPortablePreferenceTransaction() throws {
        let manager = try text(at: "MobileEgressAgent/TunnelManager.swift")

        XCTAssertTrue(manager.contains("TunnelManager: TunnelPreferenceSession"))
        XCTAssertTrue(manager.contains("TunnelPreferenceTransaction.start(using: self)"))
        XCTAssertTrue(manager.contains("TunnelPreferenceTransaction.stop(using: self)"))
        XCTAssertTrue(manager.contains("func loadPreferences() async throws"))
        XCTAssertTrue(manager.contains("func applyConfiguration(onDemandEnabled: Bool)"))
        XCTAssertTrue(manager.contains("func savePreferences() async throws"))
        XCTAssertTrue(manager.contains("func startTunnelSession() throws"))
        XCTAssertTrue(manager.contains("func stopTunnelSession()"))
    }

    func testExtensionConsumesPortableRuntimeOwnershipController() throws {
        let project = try text(at: "MobileEgressAgent.xcodeproj/project.pbxproj")
        let provider = try text(at: "MobileEgressTunnelExtension/PacketTunnelProvider.swift")

        XCTAssertTrue(provider.contains("private let runtimeController = TunnelRuntimeController()"))
        XCTAssertFalse(project.contains("TunnelRuntimeController.swift"))
    }

    func testExtensionCancelsCurrentProviderGenerationForTerminalRuntimeFailure() throws {
        let provider = try text(at: "MobileEgressTunnelExtension/PacketTunnelProvider.swift")

        XCTAssertTrue(provider.contains("makeRuntime(generation: generation)"))
        XCTAssertTrue(provider.contains("terminalFailureHandler: { [weak self] failure in"))
        XCTAssertTrue(provider.contains("handleTerminalFailure(failure, generation: generation)"))
        XCTAssertTrue(provider.contains("cancelTunnelWithError("))
        XCTAssertTrue(provider.contains("TunnelProviderErrorClass.runtimeUnavailable.providerNSError"))
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
