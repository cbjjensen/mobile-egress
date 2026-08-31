//go:build windows

package cloud

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestNodeTrustBootstrapScriptsParseInWindowsPowerShell(t *testing.T) {
	t.Parallel()

	release := testNodeRelease(t)
	for name, script := range map[string]string{"install": installScript(release), "update": updateScript(release)} {
		script := script
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			command := exec.Command("powershell", "-NoProfile", "-Command", `$tokens = $null
$parseErrors = $null
$script = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($env:MOBILE_EGRESS_TEST_SCRIPT))
$null = [Management.Automation.Language.Parser]::ParseInput($script, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) {
  $parseErrors | ForEach-Object { [Console]::Error.WriteLine($_.Message) }
  exit 1
}`)
			command.Env = append(os.Environ(), "MOBILE_EGRESS_TEST_SCRIPT="+base64.StdEncoding.EncodeToString([]byte(script)))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("generated %s script did not parse: %v\n%s", name, err, output)
			}
		})
	}
}

func TestNodeTrustBootstrapStopsOnEveryNativeCommandFailureInWindowsPowerShell51(t *testing.T) {
	release := testNodeRelease(t)
	tests := []struct {
		name          string
		script        string
		failure       string
		serviceExists bool
		clientCalled  bool
	}{
		{name: "install ACL", script: installScript(release), failure: "icacls"},
		{name: "update ACL", script: updateScript(release), failure: "icacls"},
		{name: "install service create", script: installScript(release), failure: "sc-create"},
		{name: "install service config", script: installScript(release), failure: "sc-config", serviceExists: true},
		{name: "update service create", script: updateScript(release), failure: "sc-create"},
		{name: "update service config", script: updateScript(release), failure: "sc-config", serviceExists: true},
		{name: "install bootstrap", script: installScript(release), failure: "client", clientCalled: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			result := runNodeTrustScriptInPowerShell51(t, release, test.script, nodeTrustPowerShellScenario{
				Failure: test.failure, ServiceExists: test.serviceExists, InitialStores: "exact",
			})
			if result.Error == "" {
				t.Fatalf("native %s failure returned success output %q", test.failure, result.Output)
			}
			if strings.Contains(result.Output, `"updated":true`) || strings.Contains(result.Output, `"csrPem"`) {
				t.Fatalf("native %s failure reached success output %q", test.failure, result.Output)
			}
			if result.ClientCalled != test.clientCalled {
				t.Fatalf("native %s failure Client-called = %t, want %t", test.failure, result.ClientCalled, test.clientCalled)
			}
		})
	}
}

func TestNodeTrustBootstrapAcceptsServer2019UnknownErrorOnlyBeforePinnedTrust(t *testing.T) {
	release := testNodeRelease(t)

	t.Run("fresh Server 2019 pre-trust status", func(t *testing.T) {
		result := runNodeTrustScriptInPowerShell51(t, release, updateScriptWithOptions(release, trustedReleaseScriptOptions{
			MutexName: testNodeTrustMutexName(t), MutexWaitMilliseconds: 250, StorePrimitiveFunctions: nodeTrustInMemoryStorePrimitives,
		}), nodeTrustPowerShellScenario{
			InitialStores: "absent", PreTrustStatus: "UnknownError", PostTrustStatus: "Valid",
		})
		if result.Error != "" || !strings.Contains(result.Output, `"updated":true`) {
			t.Fatalf("Server 2019 UnknownError pre-trust result = output %q/error %q", result.Output, result.Error)
		}
		if !slices.Equal(result.ImportedStores, []string{"Root", "TrustedPublisher"}) || result.RootCount != 1 || result.PublisherCount != 1 {
			t.Fatalf("Server 2019 trust result = imported %#v, Root %d, Publisher %d", result.ImportedStores, result.RootCount, result.PublisherCount)
		}
	})

	for _, status := range []string{"HashMismatch", "NotSigned"} {
		status := status
		t.Run("pre-trust "+status+" remains rejected", func(t *testing.T) {
			result := runNodeTrustScriptInPowerShell51(t, release, updateScriptWithOptions(release, trustedReleaseScriptOptions{
				MutexName: testNodeTrustMutexName(t), MutexWaitMilliseconds: 250, StorePrimitiveFunctions: nodeTrustInMemoryStorePrimitives,
			}), nodeTrustPowerShellScenario{
				InitialStores: "absent", PreTrustStatus: status, PostTrustStatus: "Valid",
			})
			if result.Error == "" || strings.Contains(result.Output, `"updated":true`) {
				t.Fatalf("pre-trust status %q was accepted: output %q/error %q", status, result.Output, result.Error)
			}
		})
	}

	for _, status := range []string{"UnknownError", "HashMismatch", "NotSigned"} {
		status := status
		t.Run("post-trust "+status+" remains rejected", func(t *testing.T) {
			result := runNodeTrustScriptInPowerShell51(t, release, updateScriptWithOptions(release, trustedReleaseScriptOptions{
				MutexName: testNodeTrustMutexName(t), MutexWaitMilliseconds: 250, StorePrimitiveFunctions: nodeTrustInMemoryStorePrimitives,
			}), nodeTrustPowerShellScenario{
				InitialStores: "exact", PreTrustStatus: "Valid", PostTrustStatus: status,
			})
			if result.Error == "" || strings.Contains(result.Output, `"updated":true`) {
				t.Fatalf("post-trust status %q was accepted: output %q/error %q", status, result.Output, result.Error)
			}
		})
	}
}

func TestNodeTrustBootstrapReportsOnlyTheSanitizedFailureStage(t *testing.T) {
	release := testNodeRelease(t)
	result := runNodeTrustScriptInPowerShell51(t, release, installScript(release), nodeTrustPowerShellScenario{
		Failure: "icacls", InitialStores: "exact", PreTrustStatus: "Valid", PostTrustStatus: "Valid",
	})
	if got, want := result.Error, "Client release operation failed [MOBILE_EGRESS_STAGE=state-acl]"; got != want {
		t.Fatalf("sanitized node failure = %q, want %q", got, want)
	}
	if strings.Contains(result.Error, "icacls") || strings.Contains(result.Error, "exit") {
		t.Fatalf("sanitized node failure exposed native detail: %q", result.Error)
	}
}

func TestNodeTrustBootstrapValidatesEveryThumbprintMatchIndependentOfEnumerationOrder(t *testing.T) {
	release := testNodeRelease(t)
	tests := []struct {
		name          string
		initialStores string
		wantSuccess   bool
	}{
		{name: "exact only", initialStores: "exact", wantSuccess: true},
		{name: "collision only", initialStores: "collision"},
		{name: "mixed exact then collision", initialStores: "mixed-exact-first"},
		{name: "mixed collision then exact", initialStores: "mixed-collision-first"},
		{name: "ambiguous exact duplicates", initialStores: "exact-duplicate"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			result := runNodeTrustScriptInPowerShell51(t, release, updateScript(release), nodeTrustPowerShellScenario{
				InitialStores: test.initialStores,
			})
			if test.wantSuccess {
				if result.Error != "" || !strings.Contains(result.Output, `"updated":true`) {
					t.Fatalf("exact-only store result = output %q/error %q", result.Output, result.Error)
				}
				return
			}
			if result.Error == "" || strings.Contains(result.Output, `"updated":true`) {
				t.Fatalf("ambiguous/colliding store %q was accepted: output %q/error %q", test.initialStores, result.Output, result.Error)
			}
		})
	}
}

func TestNodeTrustBootstrapMutexBoundsWaitHandlesAbandonmentAndReleasesOwnership(t *testing.T) {
	release := testNodeRelease(t)

	t.Run("bounded wait rejects a concurrently owned transaction", func(t *testing.T) {
		mutexName := testNodeTrustMutexName(t)
		releaseMutex := holdNodeTrustMutex(t, mutexName)
		defer releaseMutex()
		script := updateScriptWithOptions(release, trustedReleaseScriptOptions{
			MutexName: mutexName, MutexWaitMilliseconds: 75, StorePrimitiveFunctions: nodeTrustInMemoryStorePrimitives,
		})
		started := time.Now()
		result := runNodeTrustScriptInPowerShell51(t, release, script, nodeTrustPowerShellScenario{
			InitialStores: "exact", MutexName: mutexName,
		})
		if result.Error == "" || strings.Contains(result.Output, `"updated":true`) {
			t.Fatalf("concurrent transaction was not rejected: output %q/error %q", result.Output, result.Error)
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Fatalf("mutex wait was not bounded: %v", elapsed)
		}
		if len(result.ImportedStores) != 0 {
			t.Fatalf("timed-out transaction imported trust: %#v", result.ImportedStores)
		}
		if result.DownloadCalled {
			t.Fatal("timed-out transaction began the serialized download/trust/install sequence")
		}
	})

	t.Run("abandoned mutex is acquired and released", func(t *testing.T) {
		mutexName := testNodeTrustMutexName(t)
		keepAlive := abandonNodeTrustMutex(t, mutexName)
		defer windows.CloseHandle(keepAlive)
		script := updateScriptWithOptions(release, trustedReleaseScriptOptions{
			MutexName: mutexName, MutexWaitMilliseconds: 250, StorePrimitiveFunctions: nodeTrustInMemoryStorePrimitives,
		})
		result := runNodeTrustScriptInPowerShell51(t, release, script, nodeTrustPowerShellScenario{
			InitialStores: "exact", MutexName: mutexName, ProbeMutex: true,
		})
		if result.Error != "" || !strings.Contains(result.Output, `"updated":true`) {
			t.Fatalf("abandoned mutex was not treated as acquired: output %q/error %q", result.Output, result.Error)
		}
		if !result.MutexProbeAcquired {
			t.Fatal("transaction did not release/dispose the abandoned mutex ownership")
		}
	})

	t.Run("failure releases owned mutex", func(t *testing.T) {
		mutexName := testNodeTrustMutexName(t)
		script := updateScriptWithOptions(release, trustedReleaseScriptOptions{
			MutexName: mutexName, MutexWaitMilliseconds: 250, StorePrimitiveFunctions: nodeTrustInMemoryStorePrimitives,
		})
		result := runNodeTrustScriptInPowerShell51(t, release, script, nodeTrustPowerShellScenario{
			Failure: "icacls", InitialStores: "exact", MutexName: mutexName, ProbeMutex: true,
		})
		if result.Error == "" || strings.Contains(result.Output, `"updated":true`) {
			t.Fatalf("failed transaction returned success: output %q/error %q", result.Output, result.Error)
		}
		if !result.MutexProbeAcquired {
			t.Fatal("failed transaction retained machine-global mutex ownership")
		}
	})
}

func TestNodeTrustBootstrapRollbackIsLimitedToStoresAbsentAtTransactionStart(t *testing.T) {
	release := testNodeRelease(t)
	tests := []struct {
		name          string
		initialStores string
		failure       string
		wantSuccess   bool
		wantRootCount int
		wantPublisher int
		wantImported  []string
		wantRemoved   []string
	}{
		{
			name: "partial second import removes both transaction additions", initialStores: "absent", failure: "partial-import-TrustedPublisher",
			wantRootCount: 0, wantPublisher: 0, wantImported: []string{"Root", "TrustedPublisher"}, wantRemoved: []string{"Root", "TrustedPublisher"},
		},
		{
			name: "pre-existing exact entries survive later failure", initialStores: "exact", failure: "icacls",
			wantRootCount: 1, wantPublisher: 1,
		},
		{
			name: "only initially absent store is rolled back", initialStores: "root-exact-publisher-absent", failure: "partial-import-TrustedPublisher",
			wantRootCount: 1, wantPublisher: 0, wantImported: []string{"TrustedPublisher"}, wantRemoved: []string{"TrustedPublisher"},
		},
		{
			name: "confirmed transaction additions remain on success", initialStores: "absent", wantSuccess: true,
			wantRootCount: 1, wantPublisher: 1, wantImported: []string{"Root", "TrustedPublisher"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			mutexName := testNodeTrustMutexName(t)
			script := updateScriptWithOptions(release, trustedReleaseScriptOptions{
				MutexName: mutexName, MutexWaitMilliseconds: 250, StorePrimitiveFunctions: nodeTrustInMemoryStorePrimitives,
			})
			result := runNodeTrustScriptInPowerShell51(t, release, script, nodeTrustPowerShellScenario{
				Failure: test.failure, InitialStores: test.initialStores, MutexName: mutexName, ProbeMutex: true,
			})
			if test.wantSuccess {
				if result.Error != "" || !strings.Contains(result.Output, `"updated":true`) {
					t.Fatalf("successful trust transaction = output %q/error %q", result.Output, result.Error)
				}
			} else if result.Error == "" || strings.Contains(result.Output, `"updated":true`) {
				t.Fatalf("failed trust transaction returned success: output %q/error %q", result.Output, result.Error)
			}
			if result.RootCount != test.wantRootCount || result.PublisherCount != test.wantPublisher {
				t.Fatalf("final trust counts = Root %d/Publisher %d, want %d/%d", result.RootCount, result.PublisherCount, test.wantRootCount, test.wantPublisher)
			}
			if !slices.Equal(result.ImportedStores, test.wantImported) || !slices.Equal(result.RemovedStores, test.wantRemoved) {
				t.Fatalf("trust changes = imported %#v/removed %#v, want %#v/%#v", result.ImportedStores, result.RemovedStores, test.wantImported, test.wantRemoved)
			}
			if !result.MutexProbeAcquired {
				t.Fatal("trust transaction did not release its mutex")
			}
		})
	}
}

type nodeTrustPowerShellScenario struct {
	Failure         string
	ServiceExists   bool
	InitialStores   string
	MutexName       string
	ProbeMutex      bool
	PreTrustStatus  string
	PostTrustStatus string
}

type nodeTrustPowerShellResult struct {
	Output             string   `json:"output"`
	Error              string   `json:"error"`
	ClientCalled       bool     `json:"clientCalled"`
	ImportedStores     []string `json:"importedStores"`
	RemovedStores      []string `json:"removedStores"`
	RootCount          int      `json:"rootCount"`
	PublisherCount     int      `json:"publisherCount"`
	MutexProbeAcquired bool     `json:"mutexProbeAcquired"`
	DownloadCalled     bool     `json:"downloadCalled"`
}

func runNodeTrustScriptInPowerShell51(t *testing.T, release NodeRelease, script string, scenario nodeTrustPowerShellScenario) nodeTrustPowerShellResult {
	t.Helper()
	command := exec.Command("powershell", "-NoProfile", "-Command", nodeTrustPowerShellHarness)
	command.Env = append(os.Environ(),
		"MOBILE_EGRESS_TEST_SCRIPT="+base64.StdEncoding.EncodeToString([]byte(script)),
		"MOBILE_EGRESS_TEST_CERTIFICATE_BASE64="+release.SignerCertificateBase64,
		"MOBILE_EGRESS_TEST_CERTIFICATE_SHA256="+release.SignerCertificateSHA256,
		"MOBILE_EGRESS_TEST_ARTIFACT_SHA256="+release.SHA256,
		"MOBILE_EGRESS_TEST_FAILURE="+scenario.Failure,
		"MOBILE_EGRESS_TEST_SERVICE_EXISTS="+map[bool]string{true: "true", false: "false"}[scenario.ServiceExists],
		"MOBILE_EGRESS_TEST_INITIAL_STORES="+scenario.InitialStores,
		"MOBILE_EGRESS_TEST_MUTEX_NAME="+scenario.MutexName,
		"MOBILE_EGRESS_TEST_PROBE_MUTEX="+map[bool]string{true: "true", false: "false"}[scenario.ProbeMutex],
		"MOBILE_EGRESS_TEST_PRETRUST_STATUS="+scenario.PreTrustStatus,
		"MOBILE_EGRESS_TEST_POSTTRUST_STATUS="+scenario.PostTrustStatus,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell 5.1 trust harness failed: %v\n%s", err, output)
	}
	var result nodeTrustPowerShellResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode PowerShell 5.1 trust result: %v\n%s", err, output)
	}
	return result
}

func testNodeTrustMutexName(t *testing.T) string {
	t.Helper()
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	return `Local\MobileEgress-NodeTrust-Test-` + hex.EncodeToString(random)
}

func holdNodeTrustMutex(t *testing.T, name string) func() {
	t.Helper()
	ready := make(chan error, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		namePointer, err := windows.UTF16PtrFromString(name)
		if err != nil {
			ready <- err
			return
		}
		handle, err := windows.CreateMutex(nil, false, namePointer)
		if err != nil {
			ready <- err
			return
		}
		defer windows.CloseHandle(handle)
		status, err := windows.WaitForSingleObject(handle, windows.INFINITE)
		if err != nil {
			ready <- err
			return
		}
		if status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED {
			ready <- fmt.Errorf("unexpected mutex wait status %d", status)
			return
		}
		ready <- nil
		<-release
		_ = windows.ReleaseMutex(handle)
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	return func() {
		close(release)
		<-done
	}
}

func abandonNodeTrustMutex(t *testing.T, name string) windows.Handle {
	t.Helper()
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateMutex(nil, false, namePointer)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("powershell", "-NoProfile", "-Command", `$mutex = [Threading.Mutex]::new($false, $env:MOBILE_EGRESS_TEST_MUTEX_NAME)
$null = $mutex.WaitOne()
exit 0`)
	command.Env = append(os.Environ(), "MOBILE_EGRESS_TEST_MUTEX_NAME="+name)
	if output, err := command.CombinedOutput(); err != nil {
		windows.CloseHandle(handle)
		t.Fatalf("abandon test mutex: %v\n%s", err, output)
	}
	return handle
}

const nodeTrustInMemoryStorePrimitives = `function Get-TrustStoreThumbprintMatches {
  param([Parameter(Mandatory)][string]$StoreName)
  return @($script:stores[$StoreName])
}
function Import-ExactTrustCertificate {
  param([Parameter(Mandatory)][string]$StoreName)
  $null = $script:stores[$StoreName].Add($script:certificate)
  $null = $script:importedStores.Add($StoreName)
  if ($env:MOBILE_EGRESS_TEST_FAILURE -eq "partial-import-$StoreName") { throw 'simulated partial trust import failure' }
}
function Remove-ExactTrustCertificate {
  param([Parameter(Mandatory)][string]$StoreName)
  $store = $script:stores[$StoreName]
  for ($index = $store.Count - 1; $index -ge 0; $index--) {
    $candidate = $store[$index]
    if ($candidate.Thumbprint.ToUpperInvariant() -eq $certificateThumbprint -and [Convert]::ToBase64String($candidate.RawData) -ceq $certificateBase64) {
      $store.RemoveAt($index)
      $null = $script:removedStores.Add($StoreName)
    }
  }
}`

const nodeTrustPowerShellHarness = `$ErrorActionPreference = 'Stop'
$script:certificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new([Convert]::FromBase64String($env:MOBILE_EGRESS_TEST_CERTIFICATE_BASE64))
$script:clientCalled = $false
$script:downloadCalled = $false
$script:removedPaths = [Collections.Generic.List[string]]::new()
$script:importedStores = [Collections.Generic.List[string]]::new()
$script:removedStores = [Collections.Generic.List[string]]::new()
$script:signatureChecks = 0
$script:stores = @{
  Root = [Collections.Generic.List[object]]::new()
  TrustedPublisher = [Collections.Generic.List[object]]::new()
}
if ($env:MOBILE_EGRESS_TEST_INITIAL_STORES -eq 'exact') {
  $null = $script:stores.Root.Add($script:certificate)
  $null = $script:stores.TrustedPublisher.Add($script:certificate)
} elseif ($env:MOBILE_EGRESS_TEST_INITIAL_STORES -eq 'root-exact-publisher-absent') {
  $null = $script:stores.Root.Add($script:certificate)
}

function Invoke-WebRequest {
  [CmdletBinding()]
  param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
  $script:downloadCalled = $true
  [IO.File]::WriteAllBytes($OutFile, [byte[]](1, 2, 3))
}
function Get-FileHash {
  [CmdletBinding()]
  param([string]$Algorithm, [string]$LiteralPath)
  if ($LiteralPath.EndsWith('.cer')) { return [pscustomobject]@{ Hash = $env:MOBILE_EGRESS_TEST_CERTIFICATE_SHA256 } }
  return [pscustomobject]@{ Hash = $env:MOBILE_EGRESS_TEST_ARTIFACT_SHA256 }
}
function Get-AuthenticodeSignature {
  [CmdletBinding()]
  param([string]$LiteralPath)
  $script:signatureChecks++
  $status = if ($script:signatureChecks -eq 1) { $env:MOBILE_EGRESS_TEST_PRETRUST_STATUS } else { $env:MOBILE_EGRESS_TEST_POSTTRUST_STATUS }
  if ([string]::IsNullOrWhiteSpace($status)) { $status = 'Valid' }
  return [pscustomobject]@{ Status = $status; SignerCertificate = $script:certificate; TimeStamperCertificate = $script:certificate }
}
function Get-ChildItem {
  [CmdletBinding()]
  param([string]$LiteralPath)
  $collision = [pscustomobject]@{ Thumbprint = $script:certificate.Thumbprint; RawData = [byte[]](4, 5, 6) }
  switch ($env:MOBILE_EGRESS_TEST_INITIAL_STORES) {
    'exact' { return ,$script:certificate }
    'collision' { return ,$collision }
    'mixed-exact-first' { return @($script:certificate, $collision) }
    'mixed-collision-first' { return @($collision, $script:certificate) }
    'exact-duplicate' { return @($script:certificate, $script:certificate) }
    default { return @() }
  }
}
function Import-Certificate { throw 'test did not expect a trust import' }
function New-Item {
  [CmdletBinding()]
  param([string]$ItemType, [switch]$Force, [string]$Path)
  return $null
}
function Join-Path {
  [CmdletBinding()]
  param([string]$Path, [string]$ChildPath)
  if ($Path -eq 'C:\Program Files\MobileEgress' -and $ChildPath -eq 'mobile-egress-client.exe') { return 'mobile-egress-client.exe' }
  return Microsoft.PowerShell.Management\Join-Path -Path $Path -ChildPath $ChildPath
}
function Move-Item {
  [CmdletBinding()]
  param([switch]$Force, [string]$LiteralPath, [string]$Destination)
}
function Get-Service {
  [CmdletBinding()]
  param([string]$Name)
  if ($env:MOBILE_EGRESS_TEST_SERVICE_EXISTS -eq 'true') { return [pscustomobject]@{ Status = 'Stopped' } }
  return $null
}
function Stop-Service { [CmdletBinding()] param([string]$Name, [switch]$Force) }
function Start-Service { [CmdletBinding()] param([string]$Name) }
function Remove-Item {
  [CmdletBinding()]
  param([switch]$Force, [string]$LiteralPath)
  $null = $script:removedPaths.Add($LiteralPath)
}

Set-Item -Path Function:\global:icacls.exe -Value {
  $exitCode = if ($env:MOBILE_EGRESS_TEST_FAILURE -eq 'icacls') { 9 } else { 0 }
  & "$env:SystemRoot\System32\cmd.exe" /c "exit $exitCode"
}
Set-Item -Path Function:\global:sc.exe -Value {
  $operation = [string]$args[0]
  $exitCode = if ($env:MOBILE_EGRESS_TEST_FAILURE -eq "sc-$operation") { 9 } else { 0 }
  & "$env:SystemRoot\System32\cmd.exe" /c "exit $exitCode"
}
Set-Item -Path Function:\global:mobile-egress-client.exe -Value {
  $script:clientCalled = $true
  if ($env:MOBILE_EGRESS_TEST_FAILURE -eq 'client') {
    & "$env:SystemRoot\System32\cmd.exe" /c 'exit 9'
    return
  }
  Write-Output '{"csrPem":"-----BEGIN CERTIFICATE REQUEST-----\nPUBLIC\n-----END CERTIFICATE REQUEST-----\n","configurationPublicKey":"uwCX-1JULdTd8a14hBKNL8CyZdmKf6w_X0tnQkEMaV0"}'
  & "$env:SystemRoot\System32\cmd.exe" /c 'exit 0'
}

$scriptBlock = [scriptblock]::Create([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($env:MOBILE_EGRESS_TEST_SCRIPT)))
$scriptOutput = @()
$scriptError = ''
try {
  $scriptOutput = @(& $scriptBlock)
} catch {
  $scriptError = $_.Exception.Message
}
foreach ($path in @($script:removedPaths)) {
  if ([IO.File]::Exists($path)) { [IO.File]::Delete($path) }
}
$script:certificate.Dispose()
$mutexProbeAcquired = $false
if ($env:MOBILE_EGRESS_TEST_PROBE_MUTEX -eq 'true') {
  $null = & "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -Command '$mutex = [Threading.Mutex]::new($false, $env:MOBILE_EGRESS_TEST_MUTEX_NAME); $acquired = $false; try { try { $acquired = $mutex.WaitOne(500) } catch [Threading.AbandonedMutexException] { $acquired = $true }; if ($acquired) { $mutex.ReleaseMutex() } } finally { $mutex.Dispose() }; if ($acquired) { exit 0 } else { exit 2 }'
  $mutexProbeAcquired = $LASTEXITCODE -eq 0
}
[pscustomobject]@{
  output = (@($scriptOutput) -join [Environment]::NewLine)
  error = $scriptError
  clientCalled = $script:clientCalled
  importedStores = @($script:importedStores)
  removedStores = @($script:removedStores)
  rootCount = $script:stores.Root.Count
  publisherCount = $script:stores.TrustedPublisher.Count
  mutexProbeAcquired = $mutexProbeAcquired
  downloadCalled = $script:downloadCalled
} | ConvertTo-Json -Compress
`
