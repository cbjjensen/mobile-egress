# Mac installation choices

Inevitable Mobile Relay supports Apple Silicon Macs running macOS 13 or later.
The user-mode DMG is an additional installation option from Desktop 2.0.6.
See [the publisher procedure](macos-user-dmg.md) for release verification.

| Format | Installation | Runtime |
|---|---|---|
| DMG | Copy the app into your personal Applications folder (`~/Applications`); administrator credentials are not needed by the app | Keep the app open while using proxies. Minimizing preserves connections; closing or quitting stops them. Sign-out or sleep interrupts availability. |
| PKG | Install the system service with administrator credentials | The installed service continues after the app closes. |

To install the DMG, open it in Finder, open your Home folder with Shift-Command-H,
create an Applications folder there if needed, and copy Inevitable Mobile Relay
into it. Eject the disk image and open the copied app. Do not replace a system
installation or copy into the system Applications folder when your account lacks
permission. Managed Mac policy may independently restrict running downloaded apps.

The DMG uses the signed-in user's login Keychain. If that Keychain is locked or
unavailable, unlock it in Keychain Access and reopen the app. The app does not
request an administrator password, unlock/create a Keychain, or save credentials
in plaintext. Existing PKG credentials are not migrated: activate and pair the
DMG as a new Client. Subsequent same-signed DMG upgrades retain its saved state.

Only one user-mode instance runs per account. A registered PKG service or a proxy
port in use is reported without stopping another process or changing addresses.
Use the installed Client when its background service is registered. Hosted mode
needs only outbound connectivity. Advanced direct mode may need administrator
help with existing firewall policy; the user-mode app does not change it.

## Native storage validation

`securestore.NewUserKeychainStore` is separate from the installed daemon and
historical controller stores. Every operation explicitly opens the OS account's
`~/Library/Keychains/login.keychain-db`, checks ownership, and uses a fixed user
namespace. The native backend validates the established Developer ID app identity
and creates items with an ACL bound to the signed app's designated requirement.
There is no System Keychain search or disk fallback.

Ordinary securestore tests cover namespace separation and failures without
touching native credentials. The `macuserintegration` build tag adds explicitly
enabled native fixture tests. Build two test executables from committed source,
sign both with the established Developer ID and
`com.zfnf.mobile-egress.client.app`, and run them without elevation:

- `MOBILE_EGRESS_USER_KEYCHAIN_TEST=1` enables `TestSignedUserKeychainCRUD`.
- `MOBILE_EGRESS_USER_KEYCHAIN_PHASE=A` and then `B`, with the same absolute
  `MOBILE_EGRESS_USER_KEYCHAIN_STATE` path, exercise same-signed upgrade access.
  The file holds only a random fixture name. `cleanup` removes that exact fixture
  after an interrupted test.
- A separate test executable signed with a different identifier must pass
  `TestUserKeychainWrongIdentityRejected` when
  `MOBILE_EGRESS_USER_KEYCHAIN_EXPECT_REJECT=1`.

On 2026-10-07, source `79c410e9a902720cb8ef565db708e8aa565ae26b` passed
ordinary native storage tests and all three explicit checks on the configured
Mac using effective user ID 502, with no root execution. Private build logs are
retained under `G:/codex-build-cache/inevitable-mobile-relay-dmg/`. The file-based
Keychain APIs emit expected Apple deprecation warnings; this isolated backend
preserves the current signing arrangement without adding restricted entitlements
or provisioning changes.

## Release acceptance

Public release requires the automated release gates and signed/notarized DMG
verification. The owner explicitly authorized publication without waiting for
standard-user/physical-phone confirmation on 2026-10-07. Standard-user install,
activation, phone pairing, cellular HTTP/CONNECT/SOCKS, quit/minimize/reopen
behavior and upgrade acceptance remain unverified. The configured Mac's current
accounts all have administrator membership. Ordinary-user-process checks there
do not establish standard-account acceptance. Do not label this pilot stable.
