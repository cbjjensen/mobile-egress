# Current status

This is the current validation and limitations ledger. It does not turn a missing workflow into a supported procedure. Read [deployment](deployment.md), [operations](operations.md), and the component guides before acting on a relay or device.

## Current validation

| Automated coverage | What the check establishes | Manual acceptance still required |
| --- | --- | --- |
| `scripts/test-all.ps1` | Runs Go tests, vet, and build; Windows frontend typecheck/build; Compose configuration validation; and, when the required JDK and Android SDK are available, Android JVM tests, lint, and debug assembly. | Run the [required physical-device checklist](deployment.md#required-physical-device-checklist-still-required-not-executed-by-automated-verification) with a relay, Windows device, and Android phone. |
| Android JVM and debug coverage | Exercises Android pairing parsing, address policy, cellular-only/foreground state, protocol behavior, and the debug build without a physical radio or device lifecycle. | Complete the [Android physical-device smoke checklist](../android/README.md#physical-device-smoke-checklist), including the cellular-loss no-fallback check. |
| Guarded release scripts | Require explicit release invocation and validate local signing-input handling before Android packaging; they do not publish artifacts. | Install and exercise the signed APK and Windows artifact through the owner-controlled acceptance steps in [deployment](deployment.md#windows-and-android-releases). |

Automated checks do not prove physical relay ingress, Windows runtime behavior, Android cellular binding, QR handling on a real device, or release distribution. The linked acceptance work remains required.

## Known limitations

| Limitation | Current boundary | Relevant guide |
| --- | --- | --- |
| Additional Windows Client enrollment | The shipped Windows UI does not create or import an additional Windows Client identity. Multi-Windows deployment is not a supported app-first workflow; it requires maintainership intervention. | [Windows pairing identities](../windows-client/README.md#pairing-identities) and [operations](operations.md#local-windows-client-recovery) |
| Lost-Agent targeted revocation | Android does not display its certificate serial and relay v1 has no identity-list endpoint. Routine Agent re-pairing does not revoke the prior Agent identity; targeted recovery for a lost phone requires maintainership intervention. | [Android security boundaries](../android/README.md#security-boundaries) and [operations](operations.md#health-and-rollback) |
| Owner invitation renewal | The initial Owner invitation has no Owner self-service renewal path. Expiry, loss, or failed initial bootstrap requires relay-administrator intervention. | [Relay bootstrap](operations.md#relay-bootstrap) and [secure relay initialization](deployment.md#secure-relay-initialization) |
| Compose rollback | The supplied Compose setup builds from the current source checkout. Selecting a pinned image tag for rollback is not an implemented workflow. | [Rollback and revocation](deployment.md#rollback-and-revocation) |
| Stream capacity | One Windows Client has a four-stream local limit. Eight streams is the Agent and relay-wide capacity, not a promise that one shipped Windows Client can create eight streams. | [Windows client](../windows-client/README.md) and [Android limits](../android/README.md#local-build) |
| CI and publishing | There is no CI, automated publishing, Android instrumentation run, Wails runtime test, or end-to-end physical-device proof of a release. | [Current validation](#current-validation), [Android release guidance](../android/README.md#release-signing-and-sideloading), and [Windows development](../windows-client/README.md#development) |

Related documents: [architecture](architecture.md), [security model](security-model.md), [protocol](protocol.md), [deployment](deployment.md), and [operations](operations.md).
