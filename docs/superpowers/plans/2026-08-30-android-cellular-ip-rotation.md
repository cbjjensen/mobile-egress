# Android Guided Cellular IP Rotation

## Goal

Let a paired, running Android Agent help a non-rooted, single-SIM phone request a fresh carrier address without pretending Android can change Airplane Mode itself. The Agent disconnects current proxy streams, measures public IPv4 and IPv6 over the selected cellular network, opens the system Airplane Mode settings, observes cellular loss and return, reconnects, and reports whether a comparable public address changed.

## User flow

1. The user chooses **Rotate cellular IP**. If streams are active, the app confirms that they will be disconnected.
2. The foreground service suppresses relay reconnects, closes the current session, and probes the current address through the cellular `Network` only.
3. The app opens Android's Airplane Mode settings. The user turns Airplane Mode on, waits until the foreground notification's ten-second countdown finishes, and turns it off.
4. The service observes cellular loss and return, probes again, reconnects the relay, and reports **Changed**, **Unchanged**, or **Could not verify**.
5. An unchanged result offers a retry with a recommended 30-second Airplane Mode interval.

The app never toggles Airplane Mode, mobile data, APNs, or SIM state. Carrier reassignment remains best effort and may return the same CGNAT address.

## Design

- A pure rotation controller owns attempt identity, phases, timeouts, countdown, comparison, and effects. Android callbacks and coroutines execute those effects in the foreground service.
- A public-IP probe queries ipify's IPv4 and IPv6 HTTPS endpoints concurrently with bounded timeouts. It uses the supplied cellular `Network` for sockets and DNS so Wi-Fi cannot become a fallback. IPv6 failure is optional; a result is comparable only when the same address family succeeds before and after.
- `AgentRuntimeStatus` carries transient rotation progress and results. Exact addresses remain in memory, are omitted from logs and copied diagnostics, and disappear when the service stops.
- A versioned attempt identifier lets Compose launch `Settings.ACTION_AIRPLANE_MODE_SETTINGS` once. General wireless settings are the fallback when that activity is unavailable.
- Rotation closes active streams and suppresses relay reconnects. Cancellation or a two-minute no-loss timeout reconnects on the current cellular network. A three-minute return timeout leaves the normal Agent running and waiting for cellular.

## Validation and release

- Unit tests cover reducer transitions, duplicate requests, comparison outcomes, probe parsing/failures, reconnect suppression, UI presentation, timeouts, cancellation, and redaction.
- Android unit tests, lint, and debug APK assembly must pass, followed by the repository Android component gate.
- Physical acceptance uses Wi-Fi enabled, an active proxied stream, Airplane Mode rotation, unchanged/retry behavior, and relay recovery.
- Release only the signed Android APK as prerelease `v1.0.9`, with `versionName 1.0.9` and `versionCode 8`. Windows artifacts are unchanged and are not rebuilt or republished.
