# Contributing

Mobile Egress 2 connects workload Clients directly to phones. Read [AGENTS.md](AGENTS.md), the [architecture](docs/architecture.md), [security model](docs/security-model.md) and [wire contract](docs/direct-protocol-v2.md) before changing behavior. The approved implementation uses the existing main checkout.

## Component ownership

| Path | Responsibility |
|---|---|
| `windows-client/cmd/mobile-egress-client` | Windows service / Mac LaunchDaemon entrypoint |
| `windows-client/cmd/mobile-egress-client-app` | Native Client GUI |
| `windows-client/internal/nodeservice` | Direct trust, pairing, endpoint listener and management |
| `windows-client/internal/relayclient` | Reused stream codec and direct accepted-phone tunnel; name retained internally |
| `windows-client/internal/clientapp` | Protected local IPC and embedded HTML/JavaScript GUI |
| `internal/destinationpolicy`, `internal/tunnelwire`, `internal/capacity` | Shared destination policy, wire format and resource budgets |
| `android/`, `ios/` | Native cellular Agents, registry, lifecycle and platform tests |
| `scripts/` | Source gates and guarded Client/mobile release orchestration |
| `docs/` | Current runbooks, acceptance records and explicitly historical evidence |

Controller, AWS management, relay and Tailscale runtime packages are retired. Legacy local record codecs remain for protected migration, and old outbound protocol helpers are test-only. Production entrypoints must never reconnect to a relay. Historical artifacts must not be rebuilt from current source.

## Development tools

Use Go 1.26, Node.js 22+, JDK 17+, Android SDK Platform/Build Tools 35, and the pinned native Mac Go/Xcode toolchain. Windows GUI development requires WebView2. The Client GUI embeds static assets and uses Node's built-in test runner; there is no controller React/npm build. Native Apple builds run on an Apple Silicon Mac.

```powershell
& .\scripts\preflight.ps1 -Components Go,Node,Android
& .\scripts\test-all.ps1 -Components Windows,Android
```

Android SDK resolution uses ANDROID_HOME, then ANDROID_SDK_ROOT, then ignored android/local.properties. A configured JAVA_HOME must itself contain a supported JDK.

Client development helpers are `windows-client/scripts/build.ps1` for unsigned local binaries and `dev.ps1` for the GUI connected to an installed protected service. Neither is a release installer. Native Mac/iOS work follows [the build-server guide](docs/ios-build-server.md).

## Verification

The local gate runs mobile-manifest validation, release contract tests, Go tests/vet/build, Client installer contracts, JavaScript behavior/syntax, and selected Android tests/lint/debug assembly. Run native Mac tests/race checks and iOS package/app builds separately. Cross-language fixtures use disposable public test authorities and validate the actual Go signature/certificate format on both phones.

Concurrency tests must exercise cancellation, ownership and persistence across actual async boundaries. Preserve exact EOF/tail delivery, cellular-only routing and shared queued/in-flight debt. Do not add a throughput throttle or fixed stream ceiling to suppress a bug.

Update both platforms and the [mobile manifest](docs/mobile-feature-manifest.json) with existing source/test evidence. Foreground-only iOS is an explicitly approved exception with a decision reference, not background equivalence. Signed physical acceptance is a separate gate for routing, lifecycle, keep-awake, service boot/logout, upgrades and performance.

## Documentation, generated files and secrets

README owns onboarding; architecture/security/protocol own technical contracts; operations owns recovery; deployment owns distribution; [status](docs/status.md) and [acceptance](docs/direct-acceptance.md) own current evidence. Dated earlier plans and relay benchmarks are historical.

Keep ignored build outputs, private signing/SSH/notary material, device/endpoint identifiers and protected runtime state out of source and diagnostics. Never include real QR invitations, proxy credentials or traffic payloads in reports. Regenerate intentional shared brand changes with `scripts/generate-brand-assets.ps1`; its check mode verifies deterministic assets without recreating retired controller paths.

Use the established signing identities and [guarded release process](docs/deployment.md). Source checks do not authorize signing or publication and do not make a 2.x download physically accepted.
