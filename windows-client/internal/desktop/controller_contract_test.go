package desktop

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

func TestWindowsBridgeStatusIncludesThePlatformContract(t *testing.T) {
	t.Parallel()

	app, err := newDesktopApp(context.Background(), desktopControllerConfig{
		Platform: platformWindows,
		Store:    securestore.NewMemoryStore(),
		Gateway:  contractGateway{},
		RelayServiceState: func() relayServiceState {
			return relayServiceEnabled
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	status := app.GetBridgeStatus()
	if got, want := status.Platform, "windows"; got != want {
		t.Fatalf("BridgeStatus platform = %q, want %q", got, want)
	}
	if got, want := status.RelayServiceState, "not-required"; got != want {
		t.Fatalf("BridgeStatus relayServiceState = %q, want %q", got, want)
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var contract map[string]any
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if contract["platform"] != "windows" || contract["relayServiceState"] != "not-required" {
		t.Fatalf("BridgeStatus JSON = %s", raw)
	}
}

func TestMacOSBridgeStatusExposesEveryRelayServiceStateVerbatim(t *testing.T) {
	t.Parallel()

	wantStates := []relayServiceState{
		relayServiceNotRequired,
		relayServiceNotRegistered,
		relayServiceApprovalRequired,
		relayServiceEnabled,
		relayServiceVersionMismatch,
		relayServiceUnavailable,
	}
	if got := []string{
		string(wantStates[0]), string(wantStates[1]), string(wantStates[2]),
		string(wantStates[3]), string(wantStates[4]), string(wantStates[5]),
	}; !reflect.DeepEqual(got, []string{
		"not-required", "not-registered", "approval-required", "enabled", "version-mismatch", "unavailable",
	}) {
		t.Fatalf("relay service states = %#v", got)
	}

	current := relayServiceApprovalRequired
	app, err := newDesktopApp(context.Background(), desktopControllerConfig{
		Platform:          platformMacOS,
		Store:             securestore.NewMemoryStore(),
		Gateway:           contractGateway{},
		RelayServiceState: func() relayServiceState { return current },
	})
	if err != nil {
		t.Fatal(err)
	}
	if status := app.GetBridgeStatus(); status.Platform != "macos" || status.RelayServiceState != "approval-required" || status.Ready {
		t.Fatalf("approval-required BridgeStatus = %#v", status)
	}
	current = relayServiceEnabled
	if status := app.GetBridgeStatus(); status.RelayServiceState != "enabled" {
		t.Fatalf("enabled BridgeStatus = %#v", status)
	}
}

func TestWailsOptionsSelectTheRequestedDesktopPlatform(t *testing.T) {
	t.Parallel()

	app := &DesktopApp{}
	windowsOptions := newWailsOptions(app, platformWindows)
	if windowsOptions.Windows == nil || windowsOptions.Mac != nil {
		t.Fatalf("Windows Wails options = %#v", windowsOptions)
	}
	macOptions := newWailsOptions(app, platformMacOS)
	if macOptions.Mac == nil || macOptions.Windows != nil {
		t.Fatalf("macOS Wails options = %#v", macOptions)
	}
	if macOptions.Title != desktopDisplayName || len(macOptions.Bind) != 1 || macOptions.Bind[0] != app {
		t.Fatalf("macOS Wails options lost the controller contract: %#v", macOptions)
	}
}

func TestDesktopAppPreservesEveryExistingWailsBinding(t *testing.T) {
	t.Parallel()

	appType := reflect.TypeFor[*DesktopApp]()
	methods := make([]string, 0, appType.NumMethod())
	for index := range appType.NumMethod() {
		methods = append(methods, appType.Method(index).Name)
	}
	for _, binding := range []string{
		"AWSIdentityCenterRoles", "BeginAWSIdentityCenter", "BootstrapOwner", "CancelEC2NodeReservation",
		"CompleteAWSIdentityCenter", "ConnectTailscale", "EnsureInstanceSSM", "GetBridgeStatus", "GetStatus",
		"InstallEC2Node", "InstallTailscale", "InstanceSSMOnline", "InstanceSSMStatus", "IssueAgentQr",
		"ListEC2Instances", "ManagedNodes", "NodeProxyLine", "NodeSOCKSProxyURL", "OpenAWSIAMUserCreateConsole",
		"OpenAWSIdentityCenterConsole", "PendingEC2NodeReservations", "ProxyLine", "Quit", "RebootEC2Instance",
		"RepairEC2Node", "RepairLocalBridge", "ReplaceClient", "RetryClientSetup", "Revoke", "RotateLocalBridge",
		"SaveAWSAccessKeys", "SelectAWSIdentityCenterRole", "SetupLocalBridge", "StartProxy", "StopProxy", "UpdateEC2Node",
	} {
		if !slices.Contains(methods, binding) {
			t.Errorf("DesktopApp binding %s is missing; methods = %#v", binding, methods)
		}
	}
}

type contractGateway struct{}

func (contractGateway) Enroll(context.Context, pairing.Bundle) (relayclient.Identity, error) {
	return relayclient.Identity{}, nil
}

func (contractGateway) DialSession(context.Context, relayclient.Identity) (client.Tunnel, error) {
	return nil, nil
}

func (contractGateway) IssuePairing(context.Context, relayclient.Identity, string) (relayclient.PairingCode, error) {
	return relayclient.PairingCode{}, nil
}

func (contractGateway) Revoke(context.Context, relayclient.Identity, string) error { return nil }
