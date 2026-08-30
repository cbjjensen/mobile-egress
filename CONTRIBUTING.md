# Contributing

Related documents: [project overview](README.md), [current validation and limitations](docs/status.md), [architecture](docs/architecture.md), [security model](docs/security-model.md), and [protocol reference](docs/protocol.md).

## Repository and platform scope

| Path | Contributor responsibility |
| --- | --- |
| `relay/` | Go relay service, enrollment, policy, session routing, persistence, and tests |
| `pairing/` | Shared Go invitation parsing and validation |
| `windows-client/` | Windows Wails application, Owner/Client state, local SOCKS listener, React frontend, and tests |
| `android/` | Kotlin/Compose cellular Agent, Gradle project, and JVM tests |
| `deploy/` | Docker Compose and relay-container inputs |
| `scripts/` | PowerShell prerequisite, validation, build, and guarded release entry points |
| `docs/` | Canonical system references, runbooks, status, and historical design/plan records |

The complete repository workflow is Windows-first: the shipped desktop uses Windows APIs, Wails development requires Windows and WebView2, and the canonical prerequisite/build gate is PowerShell. The relay Go packages, frontend npm commands, Docker Compose configuration, and Android Gradle project can be worked on with their own tools on other supported hosts, but that does not establish Windows runtime or physical-device behavior.

## Documentation authority

Update the narrowest canonical document that owns a fact instead of copying procedures between documents.

| Document | Owns |
| --- | --- |
| [README](README.md) | Product boundary, safety summary, terms, prerequisites, and task navigation |
| [Architecture](docs/architecture.md) | Components, topology, data flow, persistence boundaries, defaults, and observability |
| [Security model](docs/security-model.md) | Threat, trust, enrollment, secret-storage, routing, and revocation boundaries |
| [Protocol](docs/protocol.md) | Normative v1 endpoints, framing, schemas, state rules, limits, and errors |
| [Deployment](docs/deployment.md) | Relay readiness, initialization, artifacts, backup/recovery, and cross-device acceptance |
| [Operations](docs/operations.md) | Daily operation, health interpretation, troubleshooting, and recovery boundaries |
| [Windows guide](windows-client/README.md) | Windows development and runtime behavior |
| [Android guide](android/README.md) | Android development, install/release behavior, lifecycle, and device-only validation |
| [Current status](docs/status.md) | Current automated coverage, required manual acceptance, and known limitations |
| `CONTRIBUTING.md` | Bootstrap, validation scope, dependency/secret rules, and component ownership |

Dated files below `docs/superpowers/`, plus [analysis](docs/analysis.md) and [plan](docs/plan.md), are historical records rather than current behavior. For a cross-component protocol or security change, update the implementation, its tests, and every affected canonical document in the same change. Do not document a capability until the source and shipped UI perform it.

## Prerequisites and bootstrap

Install the tools required by the component you will change:

- Go 1.26 or later (`go.mod` declares Go 1.26).
- Node.js 22 or later.
- Docker Engine with Docker Compose v2 for Compose validation or relay-container work.
- Windows 10/11 and Microsoft Edge WebView2 Evergreen Runtime for Wails development and packaging.
- JDK 17 or later, Android SDK Platform 35, and Android Build-Tools 35 for Android checks.

Install the locked frontend dependencies before running the frontend or full validation gate:

```powershell
Set-Location .\windows-client\frontend
npm ci
Set-Location ..\..
```

From the repository root, the read-only prerequisite detector reports missing tools as `MISSING:` and unusable discovered tools as `INVALID:`:

```powershell
& .\scripts\preflight.ps1
& .\scripts\preflight.ps1 -Components Android
```

The repository PowerShell tooling resolves the Android SDK from the first nonempty source in this order:

1. `ANDROID_HOME`
2. `ANDROID_SDK_ROOT`
3. `sdk.dir` in ignored `android/local.properties`

If `JAVA_HOME` is nonempty, preflight uses only `JAVA_HOME\bin\javac.exe`; an absent, invalid, or older compiler there fails validation even when a newer `javac` is on `PATH`. If `JAVA_HOME` is empty, preflight resolves `javac` from `PATH`. In either case the selected JDK must be version 17 or later.

The Windows development and build scripts invoke the pinned Wails CLI as `go run github.com/wailsapp/wails/v2/cmd/wails@v2.14.0`. The first invocation needs network access when that module is not already in the Go module cache; no global Wails installation is required.

## Validation matrix

Run checks from the repository root unless the command says otherwise.

| Scope | Command | What it establishes |
| --- | --- | --- |
| Prerequisites | `& .\scripts\preflight.ps1` | Required tool discovery and minimum versions; it does not install tools or inspect signing values |
| Go relay/shared/Windows logic | `go test ./...`; `go vet ./...`; `go build ./...` | Go tests, static analysis, and compilation |
| Windows frontend | In `windows-client/frontend`: `npm run check`; `npm run build` | TypeScript checking and production frontend bundling, not the Wails runtime |
| Compose | `docker compose -f deploy/docker-compose.yml config --quiet` | Compose configuration parsing, not a built or running relay |
| Android | In `android`: `.\gradlew.bat testDebugUnitTest`; `.\gradlew.bat lintDebug`; `.\gradlew.bat assembleDebug` | JVM tests, lint, and a debug APK, not a signed install or physical radio/lifecycle test |
| PowerShell operation guards | `powershell -NoProfile -File scripts\test-operations-scripts.ps1` | Script-specific simulated and host-state assertions; it is separate from the full application gate |
| Full local application gate | `powershell -NoProfile -File scripts\test-all.ps1` | Go test/vet/build, frontend check/build, Compose parsing, and Android test/lint/debug assembly after prerequisite validation |

The full local gate does not build a relay image, package the Wails executable, produce or publish a signed Android release, run Android instrumentation, exercise the Wails runtime, or prove a physical deployment. Complete and record the [required physical-device checklist](docs/deployment.md#required-physical-device-checklist-still-required-not-executed-by-automated-verification) for relay ingress, signed installation, QR/permission flows, Windows runtime, cellular routing/fail-closed behavior, lifecycle, and streams. See [current status](docs/status.md) for the exact automated and manual boundary; there is currently no CI or automatic publishing.

## Generated files, dependencies, and secrets

Do not commit ignored local state or generated output. This includes relay `data/`; certificates and private-key containers; Android `.gradle/`, `local.properties`, `keystore.properties`, and `**/build/`; frontend `node_modules/`, `wailsjs/`, `*.tsbuildinfo`, and `package.json.md5`; Wails asset bundles and `windows-client/build/`; and root build, release, coverage, installer, executable, and APK/AAB output. Keep `windows-client/frontend/package-lock.json` tracked and update it with `package.json` when frontend dependencies intentionally change.

Use a dedicated Android release keystore stored and backed up outside this repository. Only the placeholder [signing template](android/keystore.properties.example) is tracked; its local `android/keystore.properties` copy must remain ignored and untracked. Never print, commit, paste into documentation, or attach private keys, keystores, signing passwords, invitation capabilities, live QR images, relay state, or generated SOCKS credentials. Use the guarded [Android release script](scripts/release-android.ps1), which validates that signing properties remain ignored without displaying their values.
