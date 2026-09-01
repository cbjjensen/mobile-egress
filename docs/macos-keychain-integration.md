# Signed macOS Keychain integration

The controller uses Security.framework data-protection Keychain APIs under service `com.cbjjensen.mobile-egress.controller`; it never shells out to `security`. Logical store keys become lowercase SHA-256 account names. Items explicitly set `kSecAttrSynchronizable=false` and `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`, and replacement preserves the item identity. There is no plaintext or locally encrypted file fallback.

Apple requires the restricted Keychain access-group entitlement to be authorized by a Developer ID distribution profile embedded in an app-like bundle. A raw or unsigned `go test` process is therefore not a valid integration host and is intentionally rejected by the production store.

Run this only on an Apple Silicon Mac in the logged-in, unlocked operator session. Start in the repository root and supply a Developer ID distribution provisioning profile for bundle ID `com.cbjjensen.mobile-egress.controller` plus the exact locally available Developer ID Application identity label:

```bash
go run ./windows-client/cmd/mobile-egress-keychain-integration \
  -profile "/absolute/path/MobileEgressController.provisionprofile" \
  -identity "Developer ID Application: Operator Name (TEAMID1234)"
```

The harness fails closed unless all of the following agree:

- the profile is a Developer ID distribution profile and authorizes the controller's private Keychain group;
- the supplied identity resolves to exactly one currently valid code-signing certificate whose exact SHA-1 fingerprint and DER leaf are present in the profile's `DeveloperCertificates` array;
- that leaf is a Developer ID Application certificate with the exact identity common name and team ID, digital-signature key usage, code-signing extended key usage, and Developer ID Application purpose;
- `com.apple.application-identifier` is exactly `TEAMID.com.cbjjensen.mobile-egress.controller`;
- `com.apple.developer.team-identifier`, the identity's team, the bundle ID, and the signed metadata match;
- `codesign` is invoked by the resolved certificate fingerprint, each signed bundle's extracted leaf is the same profile-authorized certificate, and the signed executable has exactly the application ID, team ID, and one private `keychain-access-groups` value with no extra entitlements; and
- both generated app bundles pass strict `codesign` verification.

The harness builds and signs version A and version B app-like bundles with the same application identity. Version A creates a random test item. Version B reads that exact item, verifies its persistent reference, replaces its non-secret fixture value without changing item identity, verifies the new value, and deletes it. A signed cleanup phase runs after a version-B failure when state remains.

The operator's private key remains in the macOS Keychain and is never read or copied by the harness. The profile and identity label are not credentials. Temporary state contains only a random logical test key and an opaque persistent reference, is written with owner-only permissions, and is removed with the temporary bundles after the run.

The command above contains placeholder profile/identity examples only. The signed harness remains an external release gate until it is run on the authorized Mac with the approved production profile and identity; portable unit tests do not prove Keychain entitlement continuity.

The access-group and app-like-bundle requirements follow Apple's [TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains) and [`kSecAttrAccessGroup` documentation](https://developer.apple.com/documentation/security/ksecattraccessgroup).
