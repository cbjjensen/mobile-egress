# Signed macOS Keychain integration

The controller uses the macOS data-protection Keychain. Apple requires its restricted Keychain access-group entitlement to be authorized by a provisioning profile embedded in an app-like bundle. A raw or unsigned `go test` process is therefore not a valid integration host and is intentionally rejected by the production store.

Run this only on an Apple Silicon Mac in the logged-in, unlocked operator session. Start in the repository root and supply a Developer ID distribution provisioning profile for bundle ID `com.cbjjensen.mobile-egress.controller` plus the exact locally available Developer ID Application identity label:

```bash
go run ./windows-client/cmd/mobile-egress-keychain-integration \
  -profile "/absolute/path/MobileEgressController.provisionprofile" \
  -identity "Developer ID Application: Operator Name (TEAMID1234)"
```

The harness fails closed unless all of the following agree:

- the profile is a Developer ID distribution profile and authorizes the controller's private Keychain group;
- `com.apple.application-identifier` is exactly `TEAMID.com.cbjjensen.mobile-egress.controller`;
- `com.apple.developer.team-identifier`, the identity's team, the bundle ID, and the signed metadata match;
- the signed executable contains only the exact private group in `keychain-access-groups`; and
- both generated app bundles pass strict `codesign` verification.

The harness builds and signs version A and version B app-like bundles with the same application identity. Version A creates a random test item. Version B reads that exact item, verifies its persistent reference, replaces its non-secret fixture value without changing item identity, verifies the new value, and deletes it. A signed cleanup phase runs after a version-B failure when state remains.

The operator's private key remains in the macOS Keychain and is never read or copied by the harness. The profile and identity label are not credentials. Temporary state contains only a random logical test key and an opaque persistent reference, is written with owner-only permissions, and is removed with the temporary bundles after the run.

The access-group and app-like-bundle requirements follow Apple's [TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains) and [`kSecAttrAccessGroup` documentation](https://developer.apple.com/documentation/security/ksecattraccessgroup).
