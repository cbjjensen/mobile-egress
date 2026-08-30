# Deployment, release, and acceptance

Normal operators use signed release artifacts and the Windows app. The Docker Compose files remain a developer/legacy relay harness and are not part of the supported local-Funnel friend setup.

## Release artifacts

A Windows release contains these siblings in one signed ZIP:

- `mobile-egress-windows.exe` — controller UI;
- `mobile-egress-admin.exe` — narrow UAC helper;
- `mobile-egress-relay.exe` — LocalSystem loopback relay;
- `mobile-egress-client.exe` — signed headless EC2 release; and
- `release-manifest.json` — exact Client version, GitHub HTTPS URL, SHA-256, and expected publisher.

The same signed `mobile-egress-client.exe` must be attached to the matching GitHub release URL in the manifest. The controller refuses a missing or invalid manifest; EC2 install/update refuses a digest or Authenticode mismatch.

Build from a reviewed tag with a code-signing certificate whose subject contains `Mobile Egress`:

```powershell
& .\scripts\build-windows.ps1 -ReleaseVersion 1.2.3 -CodeSigningThumbprint <40-hex-thumbprint>
```

The script builds all four binaries, signs and verifies each with `signtool`, writes the Client manifest, and creates `windows-client\build\release\mobile-egress-windows-1.2.3.zip`. Publish the ZIP and the standalone Client executable on GitHub Releases. Do not publish signing keys, AWS credentials, relay state, SOCKS credentials, or pairing material.

Build the Android release with the externally backed-up keystore described by `android\keystore.properties.example`:

```powershell
& .\scripts\release-android.ps1 -ValidateOnly
& .\scripts\release-android.ps1
```

Record source tag/commit, filenames, hashes, signer identities, Android versionCode/versionName, build time, and acceptance result.

## Operator setup

The operator needs Windows 10/11 with WebView2, an Android 10+ phone with cellular data, a Tailscale account that permits Funnel, and access to existing Windows Server 2019 EC2 instances in `us-east-1`.

1. Extract the signed Windows ZIP and run `mobile-egress-windows.exe`.
2. In **Bridge**, install/connect Tailscale and set up the local relay. Approve the explicit browser and UAC prompts.
3. In **Phone**, generate and scan the Agent enrollment QR, then start the Android foreground Agent.
4. In **AWS Login**, use IAM Identity Center or the access-key fallback.
5. In **EC2 Nodes**, refresh inventory. For an instance with no profile, **Prepare SSM** creates and attaches a dedicated profile. For an existing non-SSM role, the app shows its name and requires explicit confirmation before attaching only `AmazonSSMManagedInstanceCore`; it never replaces the profile.
6. Wait until the instance is SSM online, then install the Client. Copy its SOCKS credentials only into the intended workload.

Only SSM reachability and outbound HTTPS are required. Do not add inbound port 8443/1080 rules, a public IP, an Elastic IP, or a system-wide proxy.

## Minimum AWS permissions

The selected identity needs read access for EC2 images/instances and SSM inventory; SSM SendCommand/GetCommandInvocation for selected instances; and, when preparing IAM, narrowly scoped instance-profile/role operations. It also needs `iam:PassRole` and EC2 profile association only for a previously profile-less selected instance. Use account policy and resource constraints appropriate to the operator; the application itself filters to `us-east-1`, supported instances, and the SSM managed policy.

## Physical acceptance record

Automated tests do not replace this run. Use one local PC, one Android phone, and at least two SSM-managed Windows Server 2019 EC2 nodes.

- [ ] Verify the Windows ZIP, each executable signature, Client release SHA-256, and Android APK signature.
- [ ] Complete app-only Tailscale/relay setup without Docker, router changes, or relay EC2 infrastructure.
- [ ] Pair Android, start the Agent, and confirm cellular available / relay connected with Wi-Fi still enabled.
- [ ] Install two EC2 Clients. Confirm each listens only on `127.0.0.1:1080` and uses different credentials/Client serials.
- [ ] Configure one workload per node and confirm both report the phone carrier egress while unconfigured traffic retains its original route.
- [ ] Exercise four streams on one Client and confirm its fifth is rejected; exercise aggregate load up to 32 across Clients and confirm the 33rd is rejected without starvation.
- [ ] Disable cellular while Wi-Fi remains available. Existing streams must close and new requests must fail without Wi-Fi fallback.
- [ ] Reboot the PC, phone, and both EC2 nodes separately. Confirm Windows services recover and applications reconnect after dependencies return.
- [ ] Publish/install a newer signed Client with **Update**; use **Repair** to reapply service and sealed configuration.
- [ ] Perform a controlled Funnel-name change. Connect AWS, choose endpoint rotation, verify sealed node updates, scan the Android migration QR, and confirm all existing serials/keys and SOCKS credentials are retained.
- [ ] Review logs/SSM history for secret redaction and confirm no inbound EC2 networking changed.

Record only aggregate outcomes and finite error classes. Do not capture QR payloads, capabilities, credentials, keys, certificates, destinations, or traffic.

## Rollback and state recovery

Normal code rollback preserves `C:\ProgramData\MobileEgress\Relay` and the relay CA. Install a previously accepted signed controller bundle and relay binary only after confirming protocol/schema compatibility. Never restore stale SQLite state as a code rollback because it can reverse revocation or capability consumption.

Back up the entire relay state directory while the relay service is stopped, preserving ACLs. A backup contains the CA private key and is as sensitive as live state. If the CA/state or sole Owner identity is lost or compromised, stop the service and perform a reviewed full trust reset with re-enrollment; endpoint rotation does not replace the CA and is not compromise recovery.
