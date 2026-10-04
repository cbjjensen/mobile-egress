//go:build windows

package clientapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Shadow all NetSecurity commands with an in-process model. The production
// script executes unchanged, but can neither query nor mutate the host firewall.
const firewallPowerShellModel = `
$global:mutations = 0
$global:ruleExists = $env:TEST_EXISTS -eq 'true'
$global:rule = [pscustomobject]@{Name='MobileEgressDirectClient';Description='Mobile Egress direct Client managed rule v2';Group='MobileEgressDirectClient';Direction='Inbound';Action='Allow';Enabled='True';Profile='Any';EdgeTraversalPolicy='Block'}
$global:application = [pscustomobject]@{Program=$env:MOBILE_EGRESS_DIRECT_PROGRAM;Package='Any'}
$global:service = [pscustomobject]@{Service='MobileEgressClient'}
$global:port = [pscustomobject]@{Protocol='TCP';LocalPort=$env:TEST_PORT;RemotePort='Any'}
$global:address = [pscustomobject]@{LocalAddress='Any';RemoteAddress='Any'}
$global:interface = [pscustomobject]@{InterfaceAlias='Any'}
$global:interfaceType = [pscustomobject]@{InterfaceType='Any'}
$global:security = [pscustomobject]@{Authentication='NotRequired';Encryption='NotRequired';LocalUser='Any';RemoteUser='Any';RemoteMachine='Any';OverrideBlockRules='False'}
if ($env:TEST_CONFLICT -eq 'true') {$global:application.Program='C:\unrelated.exe'}
function Get-NetFirewallRule {[CmdletBinding()]param($Name,$PolicyStore,$Direction,$Action,$Enabled) if($Name -eq $null){if($PolicyStore -cne 'ActiveStore' -or $Direction -cne 'Inbound' -or $Action -cne 'Block' -or $Enabled -cne 'True'){throw 'unsafe block query'};if($env:TEST_BLOCK_QUERY_FAILURE -eq 'true'){throw 'private block query failure'};if($env:TEST_BLOCK_NONE -eq 'true'){Write-Error 'No matching rules' -Category ObjectNotFound;return};if($global:block){$global:block};return};if($Name -cne 'MobileEgressDirectClient'){throw 'wrong rule'}; if($env:TEST_QUERY_FAILURE -eq 'true'){Write-Error 'private access failure' -Category PermissionDenied;return}; if($global:ruleExists){$global:rule}}
function Get-NetFirewallApplicationFilter {[CmdletBinding()]param([Parameter(ValueFromPipeline)]$InputObject) process {if($InputObject.Name -eq 'OwnerManagedBlock'){$global:blockApplication}else{$global:application}}}
function Get-NetFirewallServiceFilter {[CmdletBinding()]param([Parameter(ValueFromPipeline)]$InputObject) process {if($InputObject.Name -eq 'OwnerManagedBlock'){$global:blockService}else{$global:service}}}
function Get-NetFirewallPortFilter {[CmdletBinding()]param([Parameter(ValueFromPipeline)]$InputObject) process {if($InputObject.Name -eq 'OwnerManagedBlock'){$global:blockPort}else{$global:port}}}
function Get-NetFirewallAddressFilter {[CmdletBinding()]param([Parameter(ValueFromPipeline)]$InputObject) process {if($InputObject.Name -eq 'OwnerManagedBlock'){$global:blockAddress}else{$global:address}}}
function Get-NetFirewallInterfaceFilter {[CmdletBinding()]param([Parameter(ValueFromPipeline)]$InputObject) process {if($InputObject.Name -eq 'OwnerManagedBlock'){$global:blockInterface}else{$global:interface}}}
function Get-NetFirewallInterfaceTypeFilter {[CmdletBinding()]param([Parameter(ValueFromPipeline)]$InputObject) process {if($InputObject.Name -eq 'OwnerManagedBlock'){$global:blockInterfaceType}else{$global:interfaceType}}}
function Get-NetFirewallSecurityFilter {[CmdletBinding()]param([Parameter(ValueFromPipeline)]$InputObject) process {if($InputObject.Name -eq 'OwnerManagedBlock'){$global:blockSecurity}else{$global:security}}}
function Set-NetFirewallApplicationFilter {throw 'Changing application restrictions is forbidden'}
function Set-NetFirewallInterfaceFilter {throw 'Changing interface restrictions is forbidden'}
function Set-NetFirewallInterfaceTypeFilter {throw 'Changing interface type restrictions is forbidden'}
function Set-NetFirewallSecurityFilter {throw 'Changing security restrictions is forbidden'}
function New-Object {[CmdletBinding()]param($ComObject) if($ComObject -cne 'HNetCfg.FwPolicy2'){throw 'unexpected COM object'};if($env:TEST_COM_FAILURE -eq 'true'){throw 'private COM failure'};$active=2;if($env:TEST_ACTIVE -ne $null){$active=[int]$env:TEST_ACTIVE};[pscustomobject]@{CurrentProfileTypes=$active}}
function Get-NetFirewallProfile {[CmdletBinding()]param($Name,$PolicyStore) if($env:TEST_PROFILE_FAILURE -eq 'true'){throw 'private profile failure'};$profiles=@([pscustomobject]@{Name='Private';Enabled=$env:TEST_ENABLED;AllowInboundRules=$env:TEST_INBOUND;AllowLocalFirewallRules='True'});if($env:TEST_PROFILES){$profiles=ConvertFrom-Json $env:TEST_PROFILES};if($null -eq $Name){$profiles}else{$profiles|Where-Object {$_.Name -in $Name}}}
function Set-NetFirewallRule {[CmdletBinding()]param([Parameter(ValueFromPipeline)]$InputObject,$Direction,$Action,$Enabled,$Profile,$Program,$Service,$EdgeTraversalPolicy) process {if($InputObject.Name -cne 'MobileEgressDirectClient' -or $Program -cne $env:MOBILE_EGRESS_DIRECT_PROGRAM -or $Service -cne 'MobileEgressClient'){throw 'wrong target'}; $global:mutations++;$global:rule.Enabled=$Enabled;$global:rule.Action=$Action}}
function Set-NetFirewallPortFilter {[CmdletBinding()]param([Parameter(ValueFromPipeline)]$InputObject,$Protocol,$LocalPort,$RemotePort) process {$global:mutations++;if($env:TEST_DENIAL -ne 'true'){$global:port.LocalPort=[string]$LocalPort};$global:port.Protocol=$Protocol;$global:port.RemotePort=$RemotePort}}
function New-NetFirewallRule {[CmdletBinding()]param($Name,$DisplayName,$Description,$Group,$Direction,$Action,$Enabled,$Profile,$Protocol,$LocalPort,$Program,$Service,$EdgeTraversalPolicy) if($Name -cne 'MobileEgressDirectClient' -or $Program -cne $env:MOBILE_EGRESS_DIRECT_PROGRAM -or $Service -cne 'MobileEgressClient'){throw 'wrong target'};$global:mutations++;$global:ruleExists=$true;$global:port.LocalPort=[string]$LocalPort}
`

const firewallPowerShellBlockModel = `
$global:block=[pscustomobject]@{Name='OwnerManagedBlock';Profile='Any';Enabled='True';Direction='Inbound';Action='Block'}
$global:blockApplication=[pscustomobject]@{Program=$env:MOBILE_EGRESS_DIRECT_PROGRAM;Package='Any'}
$global:blockService=[pscustomobject]@{Service='Any'}
$global:blockPort=[pscustomobject]@{Protocol='TCP';LocalPort='9443';RemotePort='Any'}
$global:blockAddress=[pscustomobject]@{LocalAddress='Any';RemoteAddress='Any'}
$global:blockInterface=[pscustomobject]@{InterfaceAlias='Any'}
$global:blockInterfaceType=[pscustomobject]@{InterfaceType='Any'}
$global:blockSecurity=[pscustomobject]@{Authentication='NotRequired';Encryption='NotRequired';LocalUser='Any';RemoteUser='Any';RemoteMachine='Any'}
`

func TestWindowsFirewallScriptTargetsOwnedRuleAndReadsBack(t *testing.T) {
	for _, tc := range []struct {
		name, mode, exists, port, enabled, inbound, conflict, denial, want string
		mutations                                                          int
	}{
		{"check exact", "check", "true", "9443", "True", "True", "false", "false", "allowed", 0},
		{"check wrong port", "check", "true", "8443", "True", "True", "false", "false", "blocked", 0},
		{"check absent", "check", "false", "8443", "True", "True", "false", "false", "blocked", 0},
		{"retry existing", "retry", "true", "8443", "True", "True", "false", "false", "allowed", 2},
		{"retry absent", "retry", "false", "8443", "True", "True", "false", "false", "allowed", 1},
		{"retry denied", "retry", "true", "8443", "True", "True", "false", "true", "blocked", 2},
		{"disabled", "check", "false", "8443", "False", "True", "false", "false", "disabled", 0},
		{"retry disabled preserves rule", "retry", "false", "8443", "False", "True", "false", "false", "disabled", 1},
		{"block all", "retry", "true", "8443", "True", "False", "false", "false", "blocked", 0},
		{"conflict", "retry", "true", "8443", "True", "True", "true", "false", "blocked", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "firewall-test.ps1")
			script := firewallPowerShellModel + "\n& {\n" + firewallScript + "\n}\nWrite-Output ('mutations=' + $global:mutations)\n"
			if err := os.WriteFile(path, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
			cmd.Env = append(os.Environ(), "MOBILE_EGRESS_DIRECT_PROGRAM=C:\\Program Files\\Mobile Egress\\client.exe", "MOBILE_EGRESS_DIRECT_PORT=9443", "MOBILE_EGRESS_FIREWALL_MODE="+tc.mode, "TEST_EXISTS="+tc.exists, "TEST_PORT="+tc.port, "TEST_ENABLED="+tc.enabled, "TEST_INBOUND="+tc.inbound, "TEST_CONFLICT="+tc.conflict, "TEST_DENIAL="+tc.denial)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("script failed: %v %s", err, output)
			}
			lines := strings.Fields(string(output))
			wantMutations := []string{"mutations=0", "mutations=1", "mutations=2"}[tc.mutations]
			if len(lines) != 2 || lines[0] != tc.want || lines[1] != wantMutations {
				t.Fatalf("result: %q, expected %s %s", output, tc.want, wantMutations)
			}
		})
	}
}

func TestWindowsFirewallQueryFailureCannotCreateRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "firewall-test.ps1")
	script := firewallPowerShellModel + "\n& {\n" + firewallScript + "\n}\nWrite-Output ('mutations=' + $global:mutations)\n"
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
	cmd.Env = append(os.Environ(), "MOBILE_EGRESS_DIRECT_PROGRAM=C:\\Program Files\\Mobile Egress\\client.exe", "MOBILE_EGRESS_DIRECT_PORT=9443", "MOBILE_EGRESS_FIREWALL_MODE=retry", "TEST_ENABLED=True", "TEST_INBOUND=True", "TEST_QUERY_FAILURE=true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v %s", err, output)
	}
	if strings.Join(strings.Fields(string(output)), " ") != "unavailable mutations=0" {
		t.Fatalf("query failure mutated rule or leaked output: %s", output)
	}
}

func TestWindowsFirewallUsesOnlyActiveProfiles(t *testing.T) {
	const inactiveBlocked = `[{"Name":"Domain","Enabled":"True","AllowInboundRules":"False","AllowLocalFirewallRules":"False"},{"Name":"Private","Enabled":"True","AllowInboundRules":"True","AllowLocalFirewallRules":"True"}]`
	const bothAllowed = `[{"Name":"Private","Enabled":"True","AllowInboundRules":"True","AllowLocalFirewallRules":"True"},{"Name":"Public","Enabled":"True","AllowInboundRules":"True","AllowLocalFirewallRules":"True"}]`
	const disabledAndAllowed = `[{"Name":"Domain","Enabled":"False","AllowInboundRules":"False","AllowLocalFirewallRules":"False"},{"Name":"Private","Enabled":"True","AllowInboundRules":"True","AllowLocalFirewallRules":"True"}]`
	for _, tc := range []struct{ name, mode, active, profiles, comFailure, profileFailure, want string }{
		{"inactive domain restriction check", "check", "2", inactiveBlocked, "false", "false", "allowed mutations=0"},
		{"inactive domain restriction retry", "retry", "2", inactiveBlocked, "false", "false", "allowed mutations=2"},
		{"public bit selected", "check", "4", bothAllowed, "false", "false", "allowed mutations=0"},
		{"both active allowed", "check", "6", bothAllowed, "false", "false", "allowed mutations=0"},
		{"mixed active check", "check", "3", inactiveBlocked, "false", "false", "unknown mutations=0"},
		{"mixed active retry maintains exception", "retry", "3", inactiveBlocked, "false", "false", "unknown mutations=2"},
		{"mixed active disabled retry maintains exception", "retry", "3", disabledAndAllowed, "false", "false", "unknown mutations=2"},
		{"domain active blocked", "retry", "1", inactiveBlocked, "false", "false", "blocked mutations=0"},
		{"active profile absent", "retry", "4", inactiveBlocked, "false", "false", "unknown mutations=0"},
		{"no active profiles", "retry", "0", inactiveBlocked, "false", "false", "unknown mutations=0"},
		{"unsupported profile mask", "retry", "8", inactiveBlocked, "false", "false", "unknown mutations=0"},
		{"missing policy fields", "retry", "2", `[{"Name":"Private","Enabled":"True"}]`, "false", "false", "unknown mutations=0"},
		{"COM failure", "retry", "2", inactiveBlocked, "true", "false", "unavailable mutations=0"},
		{"profile query failure", "retry", "2", inactiveBlocked, "false", "true", "unavailable mutations=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "firewall-test.ps1")
			script := firewallPowerShellModel + "\n& {\n" + firewallScript + "\n}\nWrite-Output ('mutations=' + $global:mutations)\n"
			if err := os.WriteFile(path, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
			cmd.Env = append(os.Environ(), "MOBILE_EGRESS_DIRECT_PROGRAM=C:\\Program Files\\Mobile Egress\\client.exe", "MOBILE_EGRESS_DIRECT_PORT=9443", "MOBILE_EGRESS_FIREWALL_MODE="+tc.mode, "TEST_EXISTS=true", "TEST_PORT=9443", "TEST_ACTIVE="+tc.active, "TEST_PROFILES="+tc.profiles, "TEST_COM_FAILURE="+tc.comFailure, "TEST_PROFILE_FAILURE="+tc.profileFailure)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("script failed: %v %s", err, output)
			}
			if strings.Join(strings.Fields(string(output)), " ") != tc.want {
				t.Fatalf("result: %q, expected %s", output, tc.want)
			}
		})
	}
}

func TestWindowsFirewallSanitizesNativeErrorsAndUsesEnvironmentData(t *testing.T) {
	for _, tc := range []struct {
		output, want string
		err          error
	}{{"allowed", "allowed", nil}, {"blocked", "blocked", nil}, {"sensitive native output", "unknown", nil}, {"sensitive output", "unavailable", errors.New("private failure")}} {
		status := windowsFirewall(context.Background(), 9443, true, `C:\Program Files\Client\client.exe`, `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, func(ctx context.Context, command string, args, env []string) ([]byte, error) {
			if !strings.Contains(strings.Join(env, "\n"), "MOBILE_EGRESS_DIRECT_PORT=9443") || !strings.Contains(strings.Join(env, "\n"), "MOBILE_EGRESS_FIREWALL_MODE=retry") {
				t.Fatal("missing operation data")
			}
			if strings.Contains(strings.Join(args, " "), `C:\Program Files\Client\client.exe`) {
				t.Fatal("program path interpolated into script")
			}
			return []byte(tc.output), tc.err
		})
		if status.State != tc.want || strings.Contains(status.Message, "sensitive") || strings.Contains(status.Message, "private") {
			t.Fatalf("status: %#v", status)
		}
	}
}

func TestWindowsFirewallRequiresServiceSIDForScopedRule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sid    uint32
		err    error
		want   string
		repair bool
	}{
		{"unrestricted", 1, nil, "allowed", false},
		{"restricted", 3, nil, "allowed", false},
		{"none", 0, nil, "blocked", true},
		{"unknown", 2, nil, "unknown", true},
		{"query failure", 0, errors.New("private service query failure"), "unknown", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs := 0
			status := windowsFirewallWithSID(context.Background(), 9443, true, `C:\Program Files\Client\client.exe`, `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, func(context.Context, string, []string, []string) ([]byte, error) {
				runs++
				return []byte("allowed"), nil
			}, func() (uint32, error) { return tc.sid, tc.err })
			if status.State != tc.want || strings.Contains(status.Message, "private") {
				t.Fatalf("status: %#v", status)
			}
			if tc.repair && (!strings.Contains(status.Message, "Repair") || runs != 0) {
				t.Fatalf("unsafe service scope: %#v, commands %d", status, runs)
			}
			if !tc.repair && runs != 1 {
				t.Fatal("valid service SID did not reach firewall")
			}
		})
	}
}

func TestWindowsFirewallExplicitBlocksOverrideOwnedAllow(t *testing.T) {
	for _, tc := range []struct{ name, setup, mode, want string }{
		{"exact program", "", "check", "blocked mutations=0"},
		{"retry preserves blocker", "", "retry", "blocked mutations=2"},
		{"all programs and listener range", "$global:blockApplication.Program='Any';$global:blockPort.LocalPort='9000-9500'", "check", "blocked mutations=0"},
		{"same service", "$global:blockService.Service='MobileEgressClient'", "check", "blocked mutations=0"},
		{"unrelated program", "$global:blockApplication.Program='C:\\Unrelated\\other.exe'", "check", "allowed mutations=0"},
		{"unrelated service", "$global:blockService.Service='UnrelatedService'", "check", "allowed mutations=0"},
		{"unrelated port", "$global:blockPort.LocalPort='8443'", "check", "allowed mutations=0"},
		{"unrelated protocol", "$global:blockPort.Protocol='UDP'", "check", "allowed mutations=0"},
		{"inactive profile", "$global:block.Profile='Domain'", "check", "allowed mutations=0"},
		{"partially blocked active profiles", "$global:block.Profile='Private';$env:TEST_ACTIVE='6';$env:TEST_PROFILES='[{\"Name\":\"Private\",\"Enabled\":\"True\",\"AllowInboundRules\":\"True\",\"AllowLocalFirewallRules\":\"True\"},{\"Name\":\"Public\",\"Enabled\":\"True\",\"AllowInboundRules\":\"True\",\"AllowLocalFirewallRules\":\"True\"}]'", "check", "unknown mutations=0"},
		{"no block matches", "$env:TEST_BLOCK_NONE='true'", "check", "allowed mutations=0"},
		{"restricted address", "$global:blockAddress.RemoteAddress='192.0.2.0/24'", "check", "unknown mutations=0"},
		{"restricted remote port", "$global:blockPort.RemotePort='54321'", "check", "unknown mutations=0"},
		{"restricted interface", "$global:blockInterface.InterfaceAlias='Ethernet'", "check", "unknown mutations=0"},
		{"restricted package", "$global:blockApplication.Package='S-1-15-2-1'", "check", "unknown mutations=0"},
		{"restricted authentication", "$global:blockSecurity.Authentication='Required'", "check", "unknown mutations=0"},
		{"unsupported port", "$global:blockPort.LocalPort='RPC'", "check", "unknown mutations=0"},
		{"unsupported protocol", "$global:blockPort.Protocol='new-protocol'", "check", "unknown mutations=0"},
		{"block query failure", "$env:TEST_BLOCK_QUERY_FAILURE='true'", "check", "unavailable mutations=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "firewall-test.ps1")
			script := firewallPowerShellModel + firewallPowerShellBlockModel + "\n" + tc.setup + "\n& {\n" + firewallScript + "\n}\nWrite-Output ('mutations=' + $global:mutations)\n"
			if err := os.WriteFile(path, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
			cmd.Env = append(os.Environ(), "MOBILE_EGRESS_DIRECT_PROGRAM=C:\\Program Files\\Mobile Egress\\client.exe", "MOBILE_EGRESS_DIRECT_PORT=9443", "MOBILE_EGRESS_FIREWALL_MODE="+tc.mode, "TEST_EXISTS=true", "TEST_PORT=9443", "TEST_ENABLED=True", "TEST_INBOUND=True")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("script failed: %v %s", err, output)
			}
			if strings.Join(strings.Fields(string(output)), " ") != tc.want {
				t.Fatalf("result: %q, expected %s", output, tc.want)
			}
		})
	}
}

func TestWindowsFirewallOwnedRuleRestrictionsRemainUnknownAndPreserved(t *testing.T) {
	for _, tc := range []struct{ name, setup, want string }{
		{"unrestricted", "", "allowed"},
		{"interface alias", "$global:interface.InterfaceAlias='Unused Ethernet'", "unknown"},
		{"interface type", "$global:interfaceType.InterfaceType='Wireless'", "unknown"},
		{"authentication", "$global:security.Authentication='Required'", "unknown"},
		{"encryption", "$global:security.Encryption='Required'", "unknown"},
		{"package", "$global:application.Package='S-1-15-2-1'", "unknown"},
		{"local user", "$global:security.LocalUser='D:(A;;CC;;;S-1-5-18)'", "unknown"},
		{"remote user", "$global:security.RemoteUser='D:(A;;CC;;;S-1-5-18)'", "unknown"},
		{"remote machine", "$global:security.RemoteMachine='D:(A;;CC;;;S-1-5-18)'", "unknown"},
		{"block override", "$global:security.OverrideBlockRules='True'", "unknown"},
		{"missing interface filter", "$global:interface=$null", "unknown"},
		{"missing interface type filter", "$global:interfaceType=$null", "unknown"},
		{"missing security filter", "$global:security=$null", "unknown"},
		{"missing package field", "$global:application.PSObject.Properties.Remove('Package')", "unknown"},
		{"security query failure", "function Get-NetFirewallSecurityFilter {[CmdletBinding()]param([Parameter(ValueFromPipeline)]$InputObject) process {throw 'private security query failure'}}", "unavailable"},
	} {
		for _, mode := range []string{"check", "retry"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "firewall-test.ps1")
				// Observe the constraints before and after the real production script,
				// so returning unknown by clearing restrictions cannot satisfy the test.
				capture := "@($global:application,$global:interface,$global:interfaceType,$global:security) | ConvertTo-Json -Depth 5 -Compress"
				script := firewallPowerShellModel + "\n" + tc.setup + "\n$beforeRestrictions = " + capture + "\n& {\n" + firewallScript + "\n}\n$afterRestrictions = " + capture + "\nif ($beforeRestrictions -cne $afterRestrictions) { throw 'Rule restrictions were changed' }\nWrite-Output ('mutations=' + $global:mutations)\n"
				if err := os.WriteFile(path, []byte(script), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
				cmd.Env = append(os.Environ(), "MOBILE_EGRESS_DIRECT_PROGRAM=C:\\Program Files\\Mobile Egress\\client.exe", "MOBILE_EGRESS_DIRECT_PORT=9443", "MOBILE_EGRESS_FIREWALL_MODE="+mode, "TEST_EXISTS=true", "TEST_PORT=9443", "TEST_ENABLED=True", "TEST_INBOUND=True")
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("script failed: %v %s", err, output)
				}
				mutations := "0"
				if mode == "retry" {
					mutations = "2"
				}
				want := tc.want + " mutations=" + mutations
				if strings.Join(strings.Fields(string(output)), " ") != want {
					t.Fatalf("result: %q, expected %s", output, want)
				}
			})
		}
	}
}
