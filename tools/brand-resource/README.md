# Windows Client icon resources

This build-only tool uses the same `winres` version as Wails v2.14.0. It embeds the generated ICO as icon resource **3**, which Wails reads for its native window and Windows reads for Explorer/shortcuts. It adds no manifest, privilege request, version metadata or application dependency.

Run `scripts/generate-brand-assets.ps1` from the repository root on Windows to regenerate the full-color ICO, Mac ICNS, shared desktop header and mobile resources. The script uses this tool to write identical `brand_windows_amd64.syso` files into the Client GUI and setup command directories. These tracked resources are linked by ordinary and release Go builds; there is no post-signing mutation. Existing build/signing identities and artifact names are preserved.

`scripts/test-brand-assets.ps1` checks generated files and deterministic check mode, color/alpha preservation, icon containers and resource architecture. `scripts/generate-brand-assets.ps1 -Check` detects stale or missing tracked outputs. The generator requires the existing Go toolchain as well as PowerShell/System.Drawing; the isolated tool module pins its build dependencies without changing the applications' Go module.
