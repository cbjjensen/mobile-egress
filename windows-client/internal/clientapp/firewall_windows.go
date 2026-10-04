//go:build windows

package clientapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const hostFirewallScope = "port"

func inspectHostFirewall(ctx context.Context, port uint16, retry bool) FirewallStatus {
	program, err := os.Executable()
	if err != nil {
		return firewallResult("unavailable", hostFirewallScope, port)
	}
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	return windowsFirewallWithSID(ctx, port, retry, program, shell, runFirewallCommand, windowsFirewallServiceSID)
}

func windowsFirewallServiceSID() (uint32, error) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(manager)
	service, err := windows.OpenService(manager, windows.StringToUTF16Ptr("MobileEgressClient"), windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(service)
	var sidType, needed uint32
	err = windows.QueryServiceConfig2(service, windows.SERVICE_CONFIG_SERVICE_SID_INFO, (*byte)(unsafe.Pointer(&sidType)), uint32(unsafe.Sizeof(sidType)), &needed)
	return sidType, err
}

func windowsFirewallWithSID(ctx context.Context, port uint16, retry bool, program, shell string, run firewallRunner, sid func() (uint32, error)) FirewallStatus {
	if ctx.Err() != nil {
		return firewallResult("unavailable", hostFirewallScope, port)
	}
	sidType, err := sid()
	if err != nil || (sidType != windows.SERVICE_SID_TYPE_UNRESTRICTED && sidType != windows.SERVICE_SID_TYPE_RESTRICTED) {
		state := "unknown"
		if err == nil && sidType == windows.SERVICE_SID_TYPE_NONE {
			state = "blocked"
		}
		status := firewallResult(state, hostFirewallScope, port)
		status.Message = "Repair the Mobile Egress Client installation to restore its service identity, then retry the firewall check."
		return status
	}
	return windowsFirewall(ctx, port, retry, program, shell, run)
}

func windowsFirewall(ctx context.Context, port uint16, retry bool, program, shell string, run firewallRunner) FirewallStatus {
	mode := "check"
	if retry {
		mode = "retry"
	}
	output, err := run(ctx, shell, []string{"-NoProfile", "-NonInteractive", "-Command", firewallScript}, []string{"MOBILE_EGRESS_DIRECT_PROGRAM=" + program, "MOBILE_EGRESS_DIRECT_PORT=" + strconv.Itoa(int(port)), "MOBILE_EGRESS_FIREWALL_MODE=" + mode})
	if err != nil {
		return firewallResult("unavailable", hostFirewallScope, port)
	}
	state := strings.TrimSpace(string(output))
	switch state {
	case "allowed", "disabled", "blocked", "unavailable", "unknown":
	default:
		state = "unknown"
	}
	return firewallResult(state, hostFirewallScope, port)
}

func runFirewallCommand(ctx context.Context, name string, args, env []string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = append(os.Environ(), env...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.WaitDelay = 100 * time.Millisecond
	var output firewallOutput
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	return output.data, err
}

// A fixed script receives values as environment data, never executable text.
// It only manages the exact product-owned rule for this service executable.
const firewallScript = `$ErrorActionPreference = 'Stop'
$name = 'MobileEgressDirectClient'
$description = 'Mobile Egress direct Client managed rule v2'
$program = $env:MOBILE_EGRESS_DIRECT_PROGRAM
$port = [int]$env:MOBILE_EGRESS_DIRECT_PORT
$mode = $env:MOBILE_EGRESS_FIREWALL_MODE
if (($mode -cne 'check' -and $mode -cne 'retry') -or $port -lt 1 -or $port -gt 65535 -or -not [IO.Path]::IsPathRooted($program)) { Write-Output 'unavailable'; return }
function Get-GlobalPolicy {
  $unknown = [pscustomobject]@{State='unknown';CanConfigure=$false;Names=@()}
  $current = (New-Object -ComObject HNetCfg.FwPolicy2 -ErrorAction Stop).CurrentProfileTypes
  if ($null -eq $current -or [int]$current -lt 1 -or ([int]$current -band 7) -ne [int]$current) { return $unknown }
  $names = @()
  if ([int]$current -band 1) { $names += 'Domain' }
  if ([int]$current -band 2) { $names += 'Private' }
  if ([int]$current -band 4) { $names += 'Public' }
  $profiles = @(Get-NetFirewallProfile -Name $names -PolicyStore ActiveStore -ErrorAction Stop)
  if ($profiles.Count -ne $names.Count) { return $unknown }
  $states = @()
  foreach ($name in $names) {
    $matching = @($profiles | Where-Object { [string]$_.Name -ceq $name })
    if ($matching.Count -ne 1) { return $unknown }
    $profile = $matching[0]
    if ([string]$profile.Enabled -eq 'False') { $states += 'disabled'; continue }
    if ([string]$profile.Enabled -ne 'True' -or [string]$profile.AllowInboundRules -notin @('True','False','NotConfigured') -or [string]$profile.AllowLocalFirewallRules -notin @('True','False','NotConfigured')) { return $unknown }
    if ([string]$profile.AllowInboundRules -eq 'False' -or [string]$profile.AllowLocalFirewallRules -eq 'False') { $states += 'blocked' } else { $states += 'allowed' }
  }
  $states = @($states | Select-Object -Unique)
  if ($states.Count -ne 1) { return [pscustomobject]@{State='unknown';CanConfigure=$true;Names=$names} }
  if ($states[0] -eq 'allowed') { return [pscustomobject]@{State='';CanConfigure=$true;Names=$names} }
  return [pscustomobject]@{State=$states[0];CanConfigure=($states[0] -eq 'disabled');Names=$names}
}
function Test-OwnedRule($rule) {
  if (@($rule).Count -ne 1 -or $rule.Description -cne $description -or $rule.Group -cne 'MobileEgressDirectClient') { return $false }
  $application = @($rule | Get-NetFirewallApplicationFilter -ErrorAction Stop)
  $service = @($rule | Get-NetFirewallServiceFilter -ErrorAction Stop)
  return ($application.Count -eq 1 -and $application[0].Program -ieq $program -and $service.Count -eq 1 -and $service[0].Service -ceq 'MobileEgressClient')
}
function Get-OwnedRule($store) {
  $queryErrors = @()
  $rule = Get-NetFirewallRule -Name $name -PolicyStore $store -ErrorAction SilentlyContinue -ErrorVariable queryErrors
  if (@($queryErrors | Where-Object { $_.CategoryInfo.Category -ne 'ObjectNotFound' }).Count -gt 0) { throw 'Firewall query unavailable' }
  return $rule
}
function Get-PortMatch($values) {
  $unknown = $false
  foreach ($value in @($values)) {
    foreach ($part in ([string]$value -split ',')) {
      $part = $part.Trim()
      if ($part -eq 'Any') { return 1 }
      if ($part -match '^([0-9]{1,5})(?:-([0-9]{1,5}))?$') {
        $first = [int]$Matches[1]; $last = $first
        if ($Matches[2]) { $last = [int]$Matches[2] }
        if ($first -lt 1 -or $last -gt 65535 -or $last -lt $first) { $unknown = $true; continue }
        if ($port -ge $first -and $port -le $last) { return 1 }
      } else { $unknown = $true }
    }
  }
  if ($unknown -or @($values).Count -eq 0) { return -1 }
  return 0
}
function Get-BlockingPolicy($activeNames) {
  $queryErrors = @()
  $blocks = @(Get-NetFirewallRule -PolicyStore ActiveStore -Direction Inbound -Action Block -Enabled True -ErrorAction SilentlyContinue -ErrorVariable queryErrors)
  if (@($queryErrors | Where-Object { $_.CategoryInfo.Category -ne 'ObjectNotFound' }).Count -gt 0) { throw 'Firewall query unavailable' }
  $uncertain = $false
  $blockedProfiles = @()
  foreach ($block in $blocks) {
    $profileNames = @(([string]$block.Profile -split ',') | ForEach-Object { $_.Trim() })
    if (@($profileNames | Where-Object { $_ -notin @('Any','Domain','Private','Public') }).Count -gt 0) { $uncertain = $true; continue }
    $matchingProfiles = @($activeNames | Where-Object { 'Any' -in $profileNames -or $_ -in $profileNames })
    if ($matchingProfiles.Count -eq 0) { continue }
    $applications = @($block | Get-NetFirewallApplicationFilter -ErrorAction Stop)
    $services = @($block | Get-NetFirewallServiceFilter -ErrorAction Stop)
    $ports = @($block | Get-NetFirewallPortFilter -ErrorAction Stop)
    if ($applications.Count -ne 1 -or $services.Count -ne 1 -or $ports.Count -ne 1) { $uncertain = $true; continue }
    $blockProgram = [Environment]::ExpandEnvironmentVariables([string]$applications[0].Program)
    if ($blockProgram -ne 'Any' -and $blockProgram -ine $program) {
      if (-not [IO.Path]::IsPathRooted($blockProgram) -or $blockProgram -match '[*?%]') { $uncertain = $true }
      continue
    }
    $blockService = [string]$services[0].Service
    if ($blockService -ne 'Any' -and $blockService -ine 'MobileEgressClient') {
      if ($blockService -eq '' -or $blockService -match '[*?]') { $uncertain = $true }
      continue
    }
    $protocol = [string]$ports[0].Protocol
    if ($protocol -notin @('TCP','6','Any','256')) {
      if ($protocol -notin @('UDP','ICMPv4','ICMPv6') -and $protocol -notmatch '^([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])$') { $uncertain = $true }
      continue
    }
    $portMatch = Get-PortMatch $ports[0].LocalPort
    if ($portMatch -eq 0) { continue }
    if ($portMatch -lt 0) { $uncertain = $true; continue }
    $addresses = @($block | Get-NetFirewallAddressFilter -ErrorAction Stop)
    $interfaces = @($block | Get-NetFirewallInterfaceFilter -ErrorAction Stop)
    $interfaceTypes = @($block | Get-NetFirewallInterfaceTypeFilter -ErrorAction Stop)
    $security = @($block | Get-NetFirewallSecurityFilter -ErrorAction Stop)
    if ($addresses.Count -ne 1 -or $interfaces.Count -ne 1 -or $interfaceTypes.Count -ne 1 -or $security.Count -ne 1 -or [string]$applications[0].Package -ne 'Any' -or [string]$ports[0].RemotePort -ne 'Any' -or [string]$addresses[0].LocalAddress -ne 'Any' -or [string]$addresses[0].RemoteAddress -ne 'Any' -or [string]$interfaces[0].InterfaceAlias -ne 'Any' -or [string]$interfaceTypes[0].InterfaceType -ne 'Any' -or [string]$security[0].Authentication -ne 'NotRequired' -or [string]$security[0].Encryption -ne 'NotRequired' -or [string]$security[0].LocalUser -ne 'Any' -or [string]$security[0].RemoteUser -ne 'Any' -or [string]$security[0].RemoteMachine -ne 'Any') { $uncertain = $true; continue }
    $blockedProfiles += $matchingProfiles
  }
  $blockedProfiles = @($blockedProfiles | Select-Object -Unique)
  if ($blockedProfiles.Count -eq @($activeNames).Count) { return 'blocked' }
  if ($uncertain -or $blockedProfiles.Count -gt 0) { return 'unknown' }
  return ''
}
try {
  $policy = Get-GlobalPolicy
  if ($policy.State -ne '' -and -not ($policy.CanConfigure -and $mode -ceq 'retry')) { Write-Output $policy.State; return }
  if ($mode -ceq 'retry') {
    $rule = Get-OwnedRule 'PersistentStore'
    if ($null -ne $rule) {
      if (-not (Test-OwnedRule $rule)) { Write-Output 'blocked'; return }
      $rule | Set-NetFirewallRule -Direction Inbound -Action Allow -Enabled True -Profile Any -Program $program -Service MobileEgressClient -EdgeTraversalPolicy Block -ErrorAction Stop | Out-Null
      $rule | Get-NetFirewallPortFilter | Set-NetFirewallPortFilter -Protocol TCP -LocalPort $port -RemotePort Any -ErrorAction Stop | Out-Null
    } else {
      New-NetFirewallRule -Name $name -DisplayName 'Mobile Egress direct phone connection' -Description $description -Group 'MobileEgressDirectClient' -Direction Inbound -Action Allow -Enabled True -Profile Any -Protocol TCP -LocalPort $port -Program $program -Service MobileEgressClient -EdgeTraversalPolicy Block -ErrorAction Stop | Out-Null
    }
  }
  $policy = Get-GlobalPolicy
  if ($policy.State -ne '') { Write-Output $policy.State; return }
  $rule = Get-OwnedRule 'ActiveStore'
  if ($null -eq $rule -or -not (Test-OwnedRule $rule)) { Write-Output 'blocked'; return }
  $ports = @($rule | Get-NetFirewallPortFilter -ErrorAction Stop)
  $addresses = @($rule | Get-NetFirewallAddressFilter -ErrorAction Stop)
  if ($rule.Direction -ne 'Inbound' -or $rule.Action -ne 'Allow' -or $rule.Enabled -ne 'True' -or $rule.Profile -ne 'Any' -or $rule.EdgeTraversalPolicy -ne 'Block' -or $ports.Count -ne 1 -or ($ports[0].Protocol -ne 'TCP' -and $ports[0].Protocol -ne '6') -or [string]$ports[0].LocalPort -cne [string]$port -or [string]$ports[0].RemotePort -cne 'Any' -or $addresses.Count -ne 1 -or [string]$addresses[0].LocalAddress -cne 'Any' -or [string]$addresses[0].RemoteAddress -cne 'Any') { Write-Output 'blocked'; return }
  $applications = @($rule | Get-NetFirewallApplicationFilter -ErrorAction Stop)
  $interfaces = @($rule | Get-NetFirewallInterfaceFilter -ErrorAction Stop)
  $interfaceTypes = @($rule | Get-NetFirewallInterfaceTypeFilter -ErrorAction Stop)
  $security = @($rule | Get-NetFirewallSecurityFilter -ErrorAction Stop)
  # Preserve additional restrictions; their applicability to a phone connection is unknown.
  if ($applications.Count -ne 1 -or $interfaces.Count -ne 1 -or $interfaceTypes.Count -ne 1 -or $security.Count -ne 1 -or [string]$applications[0].Package -ne 'Any' -or [string]$interfaces[0].InterfaceAlias -ne 'Any' -or [string]$interfaceTypes[0].InterfaceType -ne 'Any' -or [string]$security[0].Authentication -ne 'NotRequired' -or [string]$security[0].Encryption -ne 'NotRequired' -or [string]$security[0].LocalUser -ne 'Any' -or [string]$security[0].RemoteUser -ne 'Any' -or [string]$security[0].RemoteMachine -ne 'Any' -or [string]$security[0].OverrideBlockRules -ne 'False') { Write-Output 'unknown'; return }
  $blocking = Get-BlockingPolicy $policy.Names
  if ($blocking -ne '') { Write-Output $blocking; return }
  Write-Output 'allowed'
} catch { Write-Output 'unavailable' }`

func configureHostFirewall(ctx context.Context, port uint16) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status := inspectHostFirewall(ctx, port, true)
	if status.State == "allowed" || status.State == "disabled" {
		return nil
	}
	return errors.New(status.Message)
}
