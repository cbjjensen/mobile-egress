# Mac Client secure storage and native acceptance

The root LaunchDaemon uses the file-based System Keychain implementation under logical service `com.zfnf.mobile-egress.client`. It does not depend on a logged-in user's data-protection/login Keychain. Apple documents this requirement for daemons outside a user session in [TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains).

Each item uses the signed daemon's designated requirement for access. The graphical app does not read these secrets; it uses a local Unix socket with peer-UID validation. The installer preserves the owner UID, daemon identifier, protected state directory and signing identity across upgrades.

The LaunchDaemon is `com.zfnf.mobile-egress.client`. Its state resides in `/Library/Application Support/MobileEgressClient`; local IPC is `/var/run/mobile-egress-client/control.sock`. Root-owned paths reject symbolic links or unsafe permissions. Boot/logout operation requires macOS to have booted and networking to be available; FileVault pre-boot is outside that guarantee.

## Source versus signed acceptance

Ordinary Go tests exercise the storage abstraction and failure handling without modifying System Keychain. The explicit native integration suite is guarded by `darwin && cgo && macsystemintegration`. Build its test binary from the exact release source, sign it with the established daemon identifier and Developer ID identity, and run it as root only in the designated acceptance environment.

`TestSignedRootSystemKeychainCRUD` checks native create/read/update/delete. `TestSystemKeychainSameSignedUpgrade` uses phases A and B of two separately built, same-signed binaries with the same absolute private state path. Phase A records only a random fixture item name; phase B verifies continuity, replacement and cleanup. Its cleanup phase removes the exact test item after interruption. Never operate on arbitrary System Keychain items or export daemon private material.

Record those results, signed PKG repair/upgrade, logout and reboot in [the physical acceptance record](templates/physical-acceptance-record.md). Unsigned tests or a successful PKG build are not evidence of signed keychain continuity.

The old controller data-protection Keychain harness is retired. Historical controller evidence is not direct Client service acceptance.
