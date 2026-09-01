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
- the supplied identity resolves to exactly one currently valid code-signing certificate whose exact SHA-1 fingerprint and DER leaf are present in the profile's `DeveloperCertificates` array;
- that leaf is a Developer ID Application certificate with the exact identity common name and team ID, digital-signature key usage, code-signing extended key usage, and Developer ID Application purpose;
- `com.apple.application-identifier` is exactly `TEAMID.com.cbjjensen.mobile-egress.controller`;
- `com.apple.developer.team-identifier`, the identity's team, the bundle ID, and the signed metadata match;
- `codesign` is invoked by the resolved certificate fingerprint, each signed bundle's extracted leaf is the same profile-authorized certificate, and the signed executable has exactly the application ID, team ID, and one private `keychain-access-groups` value with no extra entitlements; and
- both generated app bundles pass strict `codesign` verification.

The harness builds and signs version A and version B app-like bundles with the same application identity. Version A creates a random test item. Version B reads that exact item, verifies its persistent reference, replaces its non-secret fixture value without changing item identity, verifies the new value, and deletes it. A signed cleanup phase runs after a version-B failure when state remains.

The operator's private key remains in the macOS Keychain and is never read or copied by the harness. The profile and identity label are not credentials. Temporary state contains only a random logical test key and an opaque persistent reference, is written with owner-only permissions, and is removed with the temporary bundles after the run.

## Signed capacity acceptance host

The developer-only capacity runner can use the production controller Owner on macOS only while the complete `run` path is executing inside a temporary app signed for that same private Keychain group. Build the ignored non-release launcher before handling the one-time token, then invoke it directly with capacity mode:

```bash
mkdir -p .local/capacity-harness
if go build -tags capacityharness -trimpath -o .local/capacity-harness/mobile-egress-keychain-integration ./windows-client/cmd/mobile-egress-keychain-integration; then
  ./.local/capacity-harness/mobile-egress-keychain-integration \
    -profile "/absolute/path/MobileEgressController.provisionprofile" \
    -identity "Developer ID Application: Operator Name (TEAMID1234)" \
    -capacity-run
else
  printf '%s\n' 'Signed capacity launcher build failed before secret entry.' >&2
fi
```

The build must finish before secret entry. After launch, wait until the signed child emits exactly `{"phase":"input","attempted":0,"open":0,"verified":0,"closed":0,"failure":"none"}`; only then enter the strict run-secret JSON document on stdin and send EOF. If profile validation, signing, or child startup fails first, enter nothing. Do not put the token or target in an argument, environment variable, shell history, temporary file, or log. The launcher disables terminal echo before profile validation or signing and fails closed if a detected terminal cannot be protected. It never parses the document: after starting the signed child behind a private readiness gate, it flushes input queued before the handoff, transfers terminal ownership, and launches the child's fixed capacity `run` mode. A pre-handoff failure flushes unread terminal input before echo is restored. The signed child loads the Owner through the production `KeychainStore` and repository; it never returns Owner certificate or private-key material to the launcher.

The host accepts only the capacity runner's bounded JSON event schema on stdout and stderr. Unknown fields, invalid values, partial lines, lines over 512 bytes, or more than 1,024 lines per stream fail closed without forwarding the rejected content. On interruption it signals the child first, allows the runner's bounded cleanup and identity revocation to finish, and force-terminates only after the cleanup grace expires. The temporary bundle is removed after the run.

Both the signed-host code and capacity command remain behind the `capacityharness` build tag and are absent from normal controller and release dependency graphs. This command is acceptance tooling, not macOS package/version metadata and not a release artifact. Delete `.local/capacity-harness/mobile-egress-keychain-integration` after the run; never distribute or attach it to a release.

Follow the complete [authenticated 256-stream acceptance runbook](capacity-acceptance.md) for the one-time target, strict stdin fields, dedicated-relay preconditions, required result, and cleanup policy.

The access-group and app-like-bundle requirements follow Apple's [TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains) and [`kSecAttrAccessGroup` documentation](https://developer.apple.com/documentation/security/ksecattraccessgroup).
