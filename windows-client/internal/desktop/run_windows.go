//go:build windows

// Package desktop hosts the thin Wails and tray shell around the testable core.
package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getlantern/systray"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	windowssys "golang.org/x/sys/windows"
	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/assets"
	"mobile-egress/windows-client/internal/awssdk"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/cloud"
	"mobile-egress/windows-client/internal/localbridge"
	"mobile-egress/windows-client/internal/prerequisites"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/tailscale"
)

type DesktopApp struct {
	core             *client.Core
	bridge           *localbridge.Manager
	tailscale        *tailscale.Controller
	tailscaleInstall tailscaleInstaller
	cloudRepository  *cloud.Repository
	ownerRepository  *client.Repository
	browserOpenURL   func(context.Context, string)

	mu              sync.RWMutex
	provisioning    sync.Mutex
	ctx             context.Context
	awsClient       *awssdk.Client
	identityLogin   *awssdk.IdentityCenterLogin
	identitySession *awssdk.IdentityCenterSession
	awsInventory    []cloud.Instance
	quitting        atomic.Bool
	shutdown        sync.Once
}

type tailscaleInstaller interface {
	Install(context.Context) (tailscale.Release, error)
}

// embeddedReleaseManifestBase64 is injected before the controller executable is
// Authenticode-signed. Node release trust is therefore rooted in the signed
// controller instead of mutable files beside it.
var embeddedReleaseManifestBase64 string

type AgentQrView struct {
	ImageDataURL string `json:"imageDataUrl"`
	ExpiresAt    string `json:"expiresAt"`
}

type EndpointMigrationView struct {
	ImageDataURL string   `json:"imageDataUrl"`
	ExpiresAt    string   `json:"expiresAt"`
	UpdatedNodes []string `json:"updatedNodes"`
	FailedNodes  []string `json:"failedNodes"`
}

type BridgeView struct {
	TailscaleInstalled bool   `json:"tailscaleInstalled"`
	TailscaleOnline    bool   `json:"tailscaleOnline"`
	FunnelReady        bool   `json:"funnelReady"`
	RelayReady         bool   `json:"relayReady"`
	FQDN               string `json:"fqdn,omitempty"`
	PublicURL          string `json:"publicUrl,omitempty"`
	OwnerReady         bool   `json:"ownerReady"`
	Ready              bool   `json:"ready"`
	NeedsRotation      bool   `json:"needsRotation"`
}

func Run() error {
	if err := prerequisites.CheckWebView2Installed(); err != nil {
		showFatal(err)
		return err
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("locate Windows configuration directory: %w", err)
	}
	store, err := securestore.NewDPAPIStore(filepath.Join(configDirectory, "MobileEgress", "secure"))
	if err != nil {
		return err
	}
	core, err := client.NewCore(context.Background(), store, client.DefaultGateway{})
	if err != nil {
		return err
	}
	cloudRepository := cloud.NewRepository(store)
	ownerRepository := client.NewRepository(store)
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate Mobile Egress executable: %w", err)
	}
	binDirectory := filepath.Dir(executable)
	tailscaleController := tailscale.NewController(`C:\Program Files\Tailscale\tailscale.exe`, tailscale.ExecRunner{})
	application := &DesktopApp{
		core: core, tailscale: tailscaleController, tailscaleInstall: tailscale.DefaultInstaller(),
		cloudRepository: cloudRepository, ownerRepository: ownerRepository, browserOpenURL: runtime.BrowserOpenURL,
	}
	tailscaleController.SetFunnelApprovalHandler(application.openFunnelApproval)
	application.bridge = localbridge.NewManager(tailscaleController, localbridge.UACHelper{
		AdminExecutable: filepath.Join(binDirectory, "mobile-egress-admin.exe"),
		RelayExecutable: filepath.Join(binDirectory, "mobile-egress-relay.exe"),
	}, coreOwnerSink{core: core})
	if credentials, loadErr := cloudRepository.AccessKeys(context.Background()); loadErr == nil {
		application.awsClient, _ = awssdk.NewAccessKey(context.Background(), awssdk.AccessKeyCredentials{
			AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken,
		})
	}
	err = wails.Run(&options.App{
		Title: "Mobile Egress", Width: 880, Height: 660, MinWidth: 720, MinHeight: 540,
		BackgroundColour: options.NewRGB(15, 20, 28),
		AssetServer:      &assetserver.Options{Assets: assets.Files()},
		OnStartup:        application.startup, OnShutdown: application.onShutdown,
		OnBeforeClose: application.beforeClose, Bind: []interface{}{application},
		Windows:                          &windows.Options{WebviewIsTransparent: false, WindowIsTranslucent: false},
		EnableDefaultContextMenu:         false,
		EnableFraudulentWebsiteDetection: false,
		SingleInstanceLock:               controllerSingleInstanceLock(application),
	})
	if err != nil {
		application.shutdownApp()
		showFatal(err)
	}
	return err
}

func (app *DesktopApp) openFunnelApproval(approvalURL string) {
	if app == nil || app.browserOpenURL == nil {
		return
	}
	if runtimeContext := app.runtimeContext(); runtimeContext != nil {
		app.browserOpenURL(runtimeContext, approvalURL)
	}
}

func controllerSingleInstanceLock(app *DesktopApp) *options.SingleInstanceLock {
	return &options.SingleInstanceLock{
		UniqueId: "com.cbjjensen.mobile-egress.controller",
		OnSecondInstanceLaunch: func(options.SecondInstanceData) {
			if app == nil {
				return
			}
			if ctx := app.runtimeContext(); ctx != nil {
				runtime.WindowUnminimise(ctx)
				runtime.WindowShow(ctx)
			}
		},
	}
}

func (app *DesktopApp) startup(ctx context.Context) {
	app.mu.Lock()
	app.ctx = ctx
	app.mu.Unlock()
	go systray.Run(app.trayReady, func() {})
}

func (app *DesktopApp) onShutdown(context.Context) { app.shutdownApp() }

func (app *DesktopApp) beforeClose(ctx context.Context) bool {
	if app.quitting.Load() {
		return false
	}
	runtime.WindowHide(ctx)
	return true
}

func (app *DesktopApp) GetStatus() client.Status { return app.core.Status() }

func (app *DesktopApp) GetBridgeStatus() BridgeView {
	view := BridgeView{OwnerReady: app.core.Status().OwnerReady}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner, _, ownerErr := app.ownerRepository.LoadOwnerIdentity(ctx)
	if ownerErr == nil {
		if health, healthErr := relayclient.Health(ctx, owner); healthErr == nil {
			view.RelayReady = health.Readiness
		}
	}
	if app.tailscale != nil {
		view.TailscaleInstalled = app.tailscale.Installed()
		if status, err := app.tailscale.Status(ctx); err == nil {
			view.TailscaleOnline = status.Online
			view.FunnelReady = status.FunnelReady
			view.FQDN = status.FQDN
			view.PublicURL = status.PublicURL
			if ownerErr == nil && owner.RelayURL != status.PublicURL {
				view.NeedsRotation = true
			}
		}
	}
	view.Ready = view.TailscaleOnline && view.FunnelReady && view.RelayReady && view.OwnerReady && !view.NeedsRotation
	return view
}

func (app *DesktopApp) RotateLocalBridge() (EndpointMigrationView, error) {
	if app.bridge == nil {
		return EndpointMigrationView{}, errors.New("Local bridge rotation is unavailable in this build.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	owner, _, err := app.ownerRepository.LoadOwnerIdentity(ctx)
	if err != nil {
		return EndpointMigrationView{}, errors.New("Set up the local Owner before rotating the Funnel endpoint.")
	}
	nodes, err := app.cloudRepository.Nodes(ctx)
	if err != nil {
		return EndpointMigrationView{}, errors.New("Unable to load encrypted managed-node metadata.")
	}
	awsClient := app.currentAWSClient()
	if len(nodes) > 0 && awsClient == nil {
		return EndpointMigrationView{}, errors.New("Connect AWS before rotation so every managed EC2 node can receive the new endpoint.")
	}
	_, rotatedOwner, err := app.bridge.Rotate(ctx, owner)
	if err != nil {
		return EndpointMigrationView{}, errors.New("Unable to rotate the local relay endpoint. Approve browser and UAC prompts, then try again.")
	}
	migration, err := relayclient.IssueEndpointMigration(ctx, rotatedOwner)
	if err != nil {
		return EndpointMigrationView{}, errors.New("The relay rotated, but an Android migration QR could not be issued.")
	}
	updated := make([]string, 0, len(nodes))
	failed := make([]string, 0)
	if awsClient != nil {
		orchestrator := cloud.NewOrchestrator(awsClient, nil, app.cloudRepository)
		for _, node := range nodes {
			if _, updateErr := orchestrator.UpdateEndpoint(ctx, node, rotatedOwner.RelayURL); updateErr != nil {
				failed = append(failed, node.InstanceID)
			} else {
				updated = append(updated, node.InstanceID)
			}
		}
	}
	encodedMigration, err := json.Marshal(migration)
	if err != nil {
		return EndpointMigrationView{}, errors.New("Unable to encode the Android migration QR.")
	}
	encoded := base64.RawURLEncoding.EncodeToString(encodedMigration)
	clear(encodedMigration)
	png, err := encodeQrPNG(encoded)
	if err != nil {
		return EndpointMigrationView{}, errors.New("Unable to create the Android migration QR.")
	}
	return EndpointMigrationView{
		ImageDataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		ExpiresAt:    migration.ExpiresAt.UTC().Format(time.RFC3339), UpdatedNodes: updated, FailedNodes: failed,
	}, nil
}

func (app *DesktopApp) InstallTailscale() error {
	if app.tailscale != nil && app.tailscale.Installed() {
		return errors.New("Tailscale is already installed. Use Connect Tailscale instead.")
	}
	if app.tailscaleInstall == nil {
		return errors.New("Unable to install Tailscale in this build.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if _, err := app.tailscaleInstall.Install(ctx); err != nil {
		return fmt.Errorf("Unable to install the verified Tailscale package: %w", err)
	}
	return nil
}

func (app *DesktopApp) ConnectTailscale() (BridgeView, error) {
	if app.tailscale == nil || !app.tailscale.Installed() {
		return BridgeView{}, errors.New("Install Tailscale before connecting it.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := app.tailscale.Connect(ctx); err != nil {
		return BridgeView{}, errors.New("Unable to connect Tailscale. Complete browser sign-in and verify internet access, then try again.")
	}
	return app.GetBridgeStatus(), nil
}

func (app *DesktopApp) SetupLocalBridge() (BridgeView, error) {
	if app.bridge == nil {
		return BridgeView{}, errors.New("Local bridge setup is unavailable in this build.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := app.bridge.Setup(ctx); err != nil {
		return BridgeView{}, fmt.Errorf("Unable to finish the local relay and Funnel setup: %w", err)
	}
	return app.GetBridgeStatus(), nil
}

func (app *DesktopApp) RepairLocalBridge() (BridgeView, error) {
	if app.bridge == nil || !app.core.Status().OwnerReady {
		return BridgeView{}, errors.New("Set up the local bridge before repairing it.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := app.bridge.Repair(ctx); err != nil {
		return BridgeView{}, errors.New("Unable to repair the signed local relay service. Approve UAC and try again.")
	}
	return app.GetBridgeStatus(), nil
}

func (app *DesktopApp) SaveAWSAccessKeys(accessKeyID, secretAccessKey, sessionToken string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	awsClient, err := awssdk.NewAccessKey(ctx, awssdk.AccessKeyCredentials{
		AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey, SessionToken: sessionToken,
	})
	if err != nil {
		return errors.New("AWS access-key credentials are incomplete.")
	}
	if err := app.cloudRepository.SaveAccessKeys(ctx, cloud.StoredAccessKeys{
		AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey, SessionToken: sessionToken,
	}); err != nil {
		return errors.New("Unable to protect AWS credentials with Windows DPAPI.")
	}
	app.mu.Lock()
	app.awsClient = awsClient
	app.identityLogin = nil
	app.identitySession = nil
	app.awsInventory = nil
	app.mu.Unlock()
	return nil
}

func (app *DesktopApp) BeginAWSIdentityCenter(startURL, ssoRegion string) (awssdk.DeviceAuthorization, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	login, err := awssdk.BeginIdentityCenterLogin(ctx, startURL, ssoRegion)
	if err != nil {
		return awssdk.DeviceAuthorization{}, errors.New("Unable to start IAM Identity Center browser login. Verify the start URL and region.")
	}
	authorization := login.Authorization()
	app.mu.Lock()
	app.identityLogin = login
	app.identitySession = nil
	app.mu.Unlock()
	if runtimeContext := app.runtimeContext(); runtimeContext != nil {
		runtime.BrowserOpenURL(runtimeContext, authorization.VerificationURL)
	}
	return authorization, nil
}

func (app *DesktopApp) CompleteAWSIdentityCenter() ([]awssdk.IdentityCenterAccount, error) {
	app.mu.RLock()
	login := app.identityLogin
	app.mu.RUnlock()
	if login == nil {
		return nil, errors.New("Start IAM Identity Center login first.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	session, err := login.Complete(ctx)
	if err != nil {
		return nil, errors.New("IAM Identity Center browser login did not complete.")
	}
	accounts, err := session.Accounts(ctx)
	if err != nil {
		return nil, errors.New("Unable to list IAM Identity Center accounts.")
	}
	app.mu.Lock()
	app.identitySession = session
	app.mu.Unlock()
	return accounts, nil
}

func (app *DesktopApp) AWSIdentityCenterRoles(accountID string) ([]string, error) {
	app.mu.RLock()
	session := app.identitySession
	app.mu.RUnlock()
	if session == nil {
		return nil, errors.New("Complete IAM Identity Center login first.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	roles, err := session.Roles(ctx, accountID)
	if err != nil {
		return nil, errors.New("Unable to list IAM Identity Center roles.")
	}
	return roles, nil
}

func (app *DesktopApp) SelectAWSIdentityCenterRole(accountID, roleName string) error {
	app.mu.RLock()
	session := app.identitySession
	app.mu.RUnlock()
	if session == nil {
		return errors.New("Complete IAM Identity Center login first.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	awsClient, err := session.Client(ctx, accountID, roleName)
	if err != nil {
		return errors.New("Unable to use that IAM Identity Center account and role.")
	}
	app.mu.Lock()
	app.awsClient = awsClient
	app.awsInventory = nil
	app.mu.Unlock()
	return nil
}

func (app *DesktopApp) ListEC2Instances() ([]cloud.Instance, error) {
	awsClient := app.currentAWSClient()
	if awsClient == nil {
		return nil, errors.New("Connect AWS with IAM Identity Center or access keys first.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	instances, err := awsClient.Instances(ctx)
	if err != nil {
		return nil, errors.New("Unable to list supported Windows Server 2019 instances in us-east-1.")
	}
	app.mu.Lock()
	app.awsInventory = append([]cloud.Instance(nil), instances...)
	app.mu.Unlock()
	return instances, nil
}

func (app *DesktopApp) EnsureInstanceSSM(instanceID string, confirmExistingRoleChange bool) (cloud.SSMProfileResult, error) {
	awsClient := app.currentAWSClient()
	if awsClient == nil {
		return cloud.SSMProfileResult{}, errors.New("Connect AWS first.")
	}
	instance, ok := app.inventoryInstance(instanceID)
	if !ok {
		return cloud.SSMProfileResult{}, errors.New("Refresh EC2 inventory and select a supported instance.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := cloud.NewManager(awsClient).EnsureSSM(ctx, instance, confirmExistingRoleChange)
	if errors.Is(err, cloud.ErrConfirmationRequired) {
		return result, errors.New("Explicit confirmation is required before adding AmazonSSMManagedInstanceCore to the existing role.")
	}
	if err != nil {
		return result, errors.New("Unable to prepare that instance for Systems Manager without replacing its profile.")
	}
	return result, nil
}

func (app *DesktopApp) InstallEC2Node(instanceID string) (cloud.ManagedNodeView, error) {
	app.provisioning.Lock()
	defer app.provisioning.Unlock()

	awsClient := app.currentAWSClient()
	if awsClient == nil {
		return cloud.ManagedNodeView{}, errors.New("Connect AWS first.")
	}
	instance, ok := app.inventoryInstance(instanceID)
	if !ok || !instance.SSMOnline {
		return cloud.ManagedNodeView{}, errors.New("The selected instance is not currently online in Systems Manager.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := app.cloudRepository.ReserveNode(ctx, instanceID); err != nil {
		return cloud.ManagedNodeView{}, errors.New("At most ten unique EC2 Client nodes can be managed. Use Update or Repair for an existing node.")
	}
	defer func() {
		releaseContext, releaseCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer releaseCancel()
		_ = app.cloudRepository.ReleaseNodeReservation(releaseContext, instanceID)
	}()
	release, err := loadNodeRelease()
	if err != nil {
		return cloud.ManagedNodeView{}, errors.New("This desktop build is missing a valid signed Client release manifest.")
	}
	orchestrator := cloud.NewOrchestrator(awsClient, ownerCertificateIssuer{repository: app.ownerRepository}, app.cloudRepository)
	if _, err := orchestrator.Install(ctx, instanceID, release); err != nil {
		return cloud.ManagedNodeView{}, errors.New("Unable to install the Client node through Systems Manager. No EC2 networking was changed.")
	}
	views, err := app.cloudRepository.NodeViews(ctx)
	if err != nil {
		return cloud.ManagedNodeView{}, errors.New("Unable to load encrypted node metadata.")
	}
	for _, view := range views {
		if view.InstanceID == instanceID {
			return view, nil
		}
	}
	return cloud.ManagedNodeView{}, errors.New("Installed node metadata is unavailable.")
}

func (app *DesktopApp) UpdateEC2Node(instanceID string) (cloud.ManagedNodeView, error) {
	return app.updateOrRepairNode(instanceID, false)
}

func (app *DesktopApp) RepairEC2Node(instanceID string) (cloud.ManagedNodeView, error) {
	return app.updateOrRepairNode(instanceID, true)
}

func (app *DesktopApp) updateOrRepairNode(instanceID string, repair bool) (cloud.ManagedNodeView, error) {
	awsClient := app.currentAWSClient()
	if awsClient == nil {
		return cloud.ManagedNodeView{}, errors.New("Connect AWS before updating a managed node.")
	}
	nodes, err := app.cloudRepository.Nodes(context.Background())
	if err != nil {
		return cloud.ManagedNodeView{}, errors.New("Unable to load encrypted managed-node metadata.")
	}
	var selected *cloud.ManagedNode
	for index := range nodes {
		if nodes[index].InstanceID == instanceID {
			selected = &nodes[index]
			break
		}
	}
	if selected == nil {
		return cloud.ManagedNodeView{}, errors.New("That EC2 instance is not a managed node.")
	}
	release, err := loadNodeRelease()
	if err != nil {
		return cloud.ManagedNodeView{}, errors.New("This desktop build is missing a valid signed Client release manifest.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	orchestrator := cloud.NewOrchestrator(awsClient, nil, app.cloudRepository)
	if repair {
		_, err = orchestrator.Repair(ctx, *selected, release)
	} else {
		_, err = orchestrator.Update(ctx, *selected, release)
	}
	if err != nil {
		return cloud.ManagedNodeView{}, errors.New("Unable to update the signed Client service through Systems Manager.")
	}
	views, err := app.cloudRepository.NodeViews(ctx)
	if err != nil {
		return cloud.ManagedNodeView{}, errors.New("Unable to load updated node metadata.")
	}
	for _, view := range views {
		if view.InstanceID == instanceID {
			return view, nil
		}
	}
	return cloud.ManagedNodeView{}, errors.New("Updated node metadata is unavailable.")
}

func (app *DesktopApp) ManagedNodes() ([]cloud.ManagedNodeView, error) {
	return app.cloudRepository.NodeViews(context.Background())
}

func (app *DesktopApp) PendingEC2NodeReservations() ([]string, error) {
	reservations, err := app.cloudRepository.NodeReservations(context.Background())
	if err != nil {
		return nil, errors.New("Unable to load encrypted pending-node reservations.")
	}
	return reservations, nil
}

func (app *DesktopApp) CancelEC2NodeReservation(instanceID string, confirmed bool) error {
	if !confirmed {
		return errors.New("Explicit confirmation is required to cancel a pending node reservation.")
	}
	app.provisioning.Lock()
	defer app.provisioning.Unlock()
	reservations, err := app.cloudRepository.NodeReservations(context.Background())
	if err != nil {
		return errors.New("Unable to load encrypted pending-node reservations.")
	}
	found := false
	for _, reservedInstanceID := range reservations {
		if reservedInstanceID == instanceID {
			found = true
			break
		}
	}
	if !found {
		return errors.New("That pending node reservation no longer exists.")
	}
	if err := app.cloudRepository.ReleaseNodeReservation(context.Background(), instanceID); err != nil {
		return errors.New("Unable to cancel the encrypted pending-node reservation.")
	}
	return nil
}

func (app *DesktopApp) NodeProxyLine(instanceID string) (string, error) {
	return app.cloudRepository.ProxyLine(context.Background(), instanceID)
}

func (app *DesktopApp) BootstrapOwner(encodedBundle string) error {
	bundle, err := pairing.Decode(encodedBundle)
	if err != nil {
		return errors.New("Unable to complete secure setup. Verify the owner invitation and try again.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := app.core.BootstrapOwner(ctx, bundle); err != nil {
		return errors.New("Unable to complete secure setup. Verify the owner invitation and try again.")
	}
	return nil
}

func (app *DesktopApp) RetryClientSetup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := app.core.RetryClientSetup(ctx); err != nil {
		return errors.New("Unable to finish Windows client setup. Please try again.")
	}
	return nil
}

func (app *DesktopApp) ReplaceClient() error {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := app.core.ReplaceClient(ctx); err != nil {
		return errors.New("Unable to replace the local Windows Client. Please try again.")
	}
	return nil
}

func (app *DesktopApp) StartProxy(port uint16) error { return app.core.StartProxy(port) }

func (app *DesktopApp) StopProxy() error { return app.core.StopProxy() }

func (app *DesktopApp) ProxyLine() (string, error) { return app.core.ProxyLine() }

func (app *DesktopApp) IssueAgentQr() (AgentQrView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := app.core.IssuePairing(ctx, "agent")
	if err != nil {
		return AgentQrView{}, errors.New("Unable to create a phone pairing code. Please try again.")
	}
	encoded, err := pairing.Encode(result)
	if err != nil {
		return AgentQrView{}, errors.New("Unable to create a phone pairing code. Please try again.")
	}
	png, err := encodeQrPNG(encoded)
	if err != nil {
		return AgentQrView{}, errors.New("Unable to create a phone pairing code. Please try again.")
	}
	return AgentQrView{
		ImageDataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		ExpiresAt:    result.ExpiresAt.UTC().Format(time.RFC3339),
	}, nil
}

func encodeQrPNG(encoded string) ([]byte, error) {
	// A negative size asks go-qrcode for an exact number of pixels per module.
	// Whole-pixel modules keep dense pairing payloads scannable.
	return qrcode.Encode(encoded, qrcode.Medium, -4)
}

func (app *DesktopApp) Revoke(serial string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := app.core.Revoke(ctx, serial); err != nil {
		return errors.New("Unable to revoke that certificate. Verify the serial and try again.")
	}
	return nil
}

func (app *DesktopApp) Quit() {
	app.quitting.Store(true)
	app.shutdownApp()
	if ctx := app.runtimeContext(); ctx != nil {
		runtime.Quit(ctx)
	}
}

func (app *DesktopApp) trayReady() {
	systray.SetIcon(trayIcon())
	systray.SetTooltip("Mobile Egress")
	statusItem := systray.AddMenuItem("Bridge status unavailable", "Local relay and Funnel status")
	statusItem.Disable()
	showItem := systray.AddMenuItem("Show Mobile Egress", "Open the controller window")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit controller", "Close the controller; Windows services keep running")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-showItem.ClickedCh:
			if ctx := app.runtimeContext(); ctx != nil {
				runtime.WindowShow(ctx)
				runtime.WindowUnminimise(ctx)
			}
		case <-quitItem.ClickedCh:
			app.Quit()
			return
		case <-ticker.C:
			status := app.GetBridgeStatus()
			switch {
			case status.NeedsRotation:
				statusItem.SetTitle("Funnel endpoint changed · rotation required")
			case status.Ready:
				statusItem.SetTitle("Local relay and Funnel ready")
			case status.TailscaleOnline:
				statusItem.SetTitle("Tailscale online · relay setup required")
			default:
				statusItem.SetTitle("Bridge setup required")
			}
		}
	}
}

func (app *DesktopApp) runtimeContext() context.Context {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.ctx
}

func (app *DesktopApp) currentAWSClient() *awssdk.Client {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.awsClient
}

func (app *DesktopApp) inventoryInstance(instanceID string) (cloud.Instance, bool) {
	app.mu.RLock()
	defer app.mu.RUnlock()
	for _, instance := range app.awsInventory {
		if instance.ID == instanceID {
			return instance, true
		}
	}
	return cloud.Instance{}, false
}

type coreOwnerSink struct{ core *client.Core }

func (sink coreOwnerSink) SaveOwnerIdentity(ctx context.Context, identity relayclient.Identity) error {
	if sink.core == nil {
		return errors.New("Owner controller is unavailable")
	}
	return sink.core.AdoptOwnerIdentity(ctx, identity)
}

func (sink coreOwnerSink) UpdateOwnerIdentity(ctx context.Context, identity relayclient.Identity) error {
	if sink.core == nil {
		return errors.New("Owner controller is unavailable")
	}
	return sink.core.UpdateOwnerEndpoint(ctx, identity)
}

type ownerCertificateIssuer struct{ repository *client.Repository }

func (issuer ownerCertificateIssuer) ProvisionClient(ctx context.Context, csrPEM string) (relayclient.ProvisionedIdentity, error) {
	if issuer.repository == nil {
		return relayclient.ProvisionedIdentity{}, errors.New("Owner identity is unavailable")
	}
	owner, _, err := issuer.repository.LoadOwnerIdentity(ctx)
	if err != nil {
		return relayclient.ProvisionedIdentity{}, errors.New("Owner identity is unavailable")
	}
	return relayclient.ProvisionClient(ctx, owner, csrPEM)
}

func loadNodeRelease() (cloud.NodeRelease, error) {
	if embeddedReleaseManifestBase64 == "" || len(embeddedReleaseManifestBase64) > 128<<10 {
		return cloud.NodeRelease{}, errors.New("release manifest is unavailable")
	}
	raw, err := base64.StdEncoding.DecodeString(embeddedReleaseManifestBase64)
	if err != nil {
		return cloud.NodeRelease{}, errors.New("release manifest is unavailable")
	}
	defer clear(raw)
	return decodeNodeReleaseManifest(raw)
}

func decodeNodeReleaseManifest(raw []byte) (cloud.NodeRelease, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return cloud.NodeRelease{}, errors.New("release manifest is unavailable")
	}
	var manifest struct {
		Version int               `json:"version"`
		Client  cloud.NodeRelease `json:"client"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || manifest.Version != 2 || manifest.Client.Validate() != nil {
		return cloud.NodeRelease{}, errors.New("release manifest is invalid")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return cloud.NodeRelease{}, errors.New("release manifest is invalid")
	}
	return manifest.Client, nil
}

func (app *DesktopApp) shutdownApp() {
	app.shutdown.Do(func() {
		_ = app.core.Close()
		systray.Quit()
	})
}

func trayIcon() []byte {
	const (
		width      = 16
		height     = 16
		pixelBytes = width * height * 4
		maskBytes  = height * 4
		imageBytes = 40 + pixelBytes + maskBytes
	)
	icon := make([]byte, 6+16+imageBytes)
	binary.LittleEndian.PutUint16(icon[2:4], 1)
	binary.LittleEndian.PutUint16(icon[4:6], 1)
	icon[6], icon[7] = width, height
	binary.LittleEndian.PutUint16(icon[10:12], 1)
	binary.LittleEndian.PutUint16(icon[12:14], 32)
	binary.LittleEndian.PutUint32(icon[14:18], imageBytes)
	binary.LittleEndian.PutUint32(icon[18:22], 22)
	dib := icon[22:]
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], width)
	binary.LittleEndian.PutUint32(dib[8:12], height*2)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	binary.LittleEndian.PutUint32(dib[20:24], pixelBytes)
	for index := 0; index < width*height; index++ {
		offset := 40 + index*4
		dib[offset], dib[offset+1], dib[offset+2], dib[offset+3] = 215, 139, 61, 255
	}
	return icon
}

func showFatal(err error) {
	if err == nil {
		return
	}
	message, messageErr := windowssys.UTF16PtrFromString(err.Error())
	caption, captionErr := windowssys.UTF16PtrFromString("Mobile Egress")
	if messageErr == nil && captionErr == nil {
		_, _ = windowssys.MessageBox(0, message, caption, windowssys.MB_OK|windowssys.MB_ICONERROR)
	}
	_, _ = fmt.Fprintln(os.Stderr, err)
}
