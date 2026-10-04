# Windows Client local access repair

## Scope and evidence

During the approved owner pilot activation, the signed Windows Client service ran as LocalSystem but the owner's app could not open its protected named pipe. Read-only checks confirmed the saved installation owner, SYSTEM pipe ownership, expected DACL, and installed source identity. No owner, credential, pairing, or firewall reset is needed.

An isolated native Windows pipe reproduced `ERROR_ACCESS_DENIED` with owner rights `0x00120003`. Adding only `FILE_READ_ATTRIBUTES` (`0x80`) allowed the original client open and `GetNamedPipeInfo`; adding write attributes alone did not. `FILE_CREATE_PIPE_INSTANCE` remained excluded. The installed pipe was not changed during diagnosis.

## Implementation checklist

- [x] Preserve installed state and reproduce the failure on temporary local pipes.
- [x] Add a native Windows regression using the production access mask and an isolated first pipe instance; record failure before repair.
- [x] Grant the installation owner the required read-attributes right. Preserve the SYSTEM owner check and deny pipe-instance creation, broad write rights, and unrelated access.
- [x] Run the relevant Go, Windows/frontend, installer and signing checks; review the minimal fix.
- [ ] Commit the source on the existing hosted feature branch and build a new signed local validation installer using the established publisher identity.
- [ ] Repair the existing installation through the supported installer, preserving owner, protected state and service identity. Verify ordinary unelevated IPC before activation.
- [ ] Activate the Client through the owner's already-approved pilot account and prepare one time-limited pairing QR. Record phone installation/traffic separately if hardware is unavailable.

## Acceptance and rollback

The real pipe regression must fail before the permission correction and pass afterward. The installed unelevated app must receive service status while unprivileged fake servers remain rejected. No public listener or inbound firewall change is required for hosted activation.

Use the supported signed installer transaction and its rollback behavior; do not manually replace live executables or weaken the installed pipe ACL. Keep the preceding validation artifact intact. No public release or Mobile repository main merge is authorized by this repair. Android artifacts and the wire protocol are unchanged.

## Validation record

Initial isolated reproduction: original mask failed with Win32 error 5; read-attributes-only addition passed. Read-only elevated ownership check and unelevated DACL inspection confirmed the expected owner and descriptor.

The native Windows regression was added against the unchanged production mask (`0x00120003`). `go test ./windows-client/internal/clientapp -run 'TestPipeOwnerExchangesBytesWithoutCreatingPipeInstances|TestGUIRejectsAnUnprivilegedNamedPipeServer' -count=1 -v` failed at the real client open with `Access is denied`; the fake-server rejection test passed. Adding only `FILE_READ_ATTRIBUTES` to the shared owner/dial access mask (`0x00120083`) made both focused tests pass. The final regression exchanges literal status request/response bytes through the native pipe and verifies that an actual second-server-instance attempt through `NtCreateNamedPipeFile` returns `STATUS_ACCESS_DENIED`.

`go test ./windows-client/internal/clientapp -count=1` passed the complete Client app package in 29.206 seconds. `git diff --check` passed for both changed Go files; Git only noted the workspace's expected LF-to-CRLF conversion. The installed service and frozen operator diagnostics were unchanged. Root-owned full Windows gates, signing, installed validation and physical phone results remain pending.

Independent review found no unresolved findings after correcting the test to attempt actual server-instance creation. Its uncached native IPC and focused installer repair tests passed. The existing Windows signing identity also passed `setup-windows-signing.ps1 -ValidateOnly`; it was not replaced.

The root-owned `scripts/test-all.ps1 -Components Windows` completed successfully: all Go tests, vet/build, installer/payload and release contract checks, manifest/schema checks, all 40 frontend tests and syntax validation passed. Full output is retained in the ignored `G:/codex-build-cache/mobile-egress-hosted-20261004/windows-ipc-repair-gate.log`. Signed repair and installed acceptance are the next steps.
