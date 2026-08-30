package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/sealedconfig"
)

type NodeRelease struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Publisher string `json:"publisher,omitempty"`
}

type ManagedNode struct {
	InstanceID             string `json:"instanceId"`
	ClientSerial           string `json:"clientSerial"`
	ConfigurationPublicKey string `json:"configurationPublicKey"`
	ServiceVersion         string `json:"serviceVersion"`
	Health                 string `json:"health"`
	SOCKSUsername          string `json:"socksUsername"`
	SOCKSPassword          string `json:"socksPassword"`
	SOCKSPort              uint16 `json:"socksPort"`
	RelayURL               string `json:"relayUrl"`
	CertificatePEM         string `json:"certificatePem"`
	CACertificatePEM       string `json:"caCertificatePem"`
}

type CommandRunner interface {
	RunPowerShell(context.Context, string, string) (string, error)
}

type CertificateIssuer interface {
	ProvisionClient(context.Context, string) (relayclient.ProvisionedIdentity, error)
}

type NodeStore interface {
	SaveNode(context.Context, ManagedNode) error
}

type Orchestrator struct {
	runner CommandRunner
	issuer CertificateIssuer
	store  NodeStore
}

func NewOrchestrator(runner CommandRunner, issuer CertificateIssuer, store NodeStore) *Orchestrator {
	return &Orchestrator{runner: runner, issuer: issuer, store: store}
}

func (orchestrator *Orchestrator) Install(ctx context.Context, instanceID string, release NodeRelease) (ManagedNode, error) {
	if orchestrator == nil || orchestrator.runner == nil || orchestrator.issuer == nil || orchestrator.store == nil {
		return ManagedNode{}, errors.New("node orchestration dependencies are required")
	}
	if !validInstanceID(instanceID) {
		return ManagedNode{}, errors.New("invalid EC2 instance ID")
	}
	if err := release.Validate(); err != nil {
		return ManagedNode{}, err
	}
	bootstrapOutput, err := orchestrator.runner.RunPowerShell(ctx, instanceID, installScript(release))
	if err != nil {
		return ManagedNode{}, errors.New("Client installation command failed; inspect redacted SSM status")
	}
	bootstrap, err := decodeBootstrapOutput([]byte(bootstrapOutput))
	if err != nil {
		return ManagedNode{}, errors.New("Client bootstrap returned invalid public output")
	}
	issued, err := orchestrator.issuer.ProvisionClient(ctx, bootstrap.CSRPEM)
	if err != nil {
		return ManagedNode{}, errors.New("relay rejected the Client certificate request")
	}
	if issued.Role != "client" || issued.Serial == "" || issued.RelayURL == "" || issued.CertificatePEM == "" || issued.CACertificatePEM == "" {
		return ManagedNode{}, errors.New("relay returned an incomplete Client identity")
	}
	username, err := randomCredential()
	if err != nil {
		return ManagedNode{}, err
	}
	password, err := randomCredential()
	if err != nil {
		return ManagedNode{}, err
	}
	configuration := nodeservice.Configuration{
		Version: 1, RelayURL: issued.RelayURL, Role: issued.Role, Serial: strings.ToUpper(issued.Serial),
		CertificatePEM: issued.CertificatePEM, CACertificatePEM: issued.CACertificatePEM,
		SOCKSUsername: username, SOCKSPassword: password, SOCKSPort: 1080,
	}
	plaintext, err := json.Marshal(configuration)
	if err != nil {
		return ManagedNode{}, err
	}
	envelope, err := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, plaintext)
	clear(plaintext)
	if err != nil {
		return ManagedNode{}, errors.New("seal Client configuration")
	}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return ManagedNode{}, err
	}
	defer clear(envelopeJSON)
	applyOutput, err := orchestrator.runner.RunPowerShell(ctx, instanceID, applyScript(envelopeJSON))
	if err != nil {
		return ManagedNode{}, errors.New("sealed Client configuration command failed; inspect redacted SSM status")
	}
	if !validApplyOutput([]byte(applyOutput)) {
		return ManagedNode{}, errors.New("Client configuration returned invalid redacted output")
	}
	node := ManagedNode{
		InstanceID: instanceID, ClientSerial: strings.ToUpper(issued.Serial),
		ConfigurationPublicKey: bootstrap.ConfigurationPublicKey, ServiceVersion: release.Version,
		Health: "installed", SOCKSUsername: username, SOCKSPassword: password, SOCKSPort: 1080,
		RelayURL: issued.RelayURL, CertificatePEM: issued.CertificatePEM, CACertificatePEM: issued.CACertificatePEM,
	}
	if err := orchestrator.store.SaveNode(ctx, node); err != nil {
		return ManagedNode{}, errors.New("save encrypted managed-node metadata")
	}
	return node, nil
}

func (orchestrator *Orchestrator) UpdateEndpoint(ctx context.Context, node ManagedNode, relayURL string) (ManagedNode, error) {
	if orchestrator == nil || orchestrator.runner == nil || orchestrator.store == nil {
		return ManagedNode{}, errors.New("node orchestration dependencies are required")
	}
	if err := validateManagedNode(node); err != nil {
		return ManagedNode{}, err
	}
	origin, err := pairing.RelayOrigin(relayURL)
	if err != nil {
		return ManagedNode{}, errors.New("new relay endpoint is invalid")
	}
	configuration := nodeservice.Configuration{
		Version: 1, RelayURL: origin.String(), Role: "client", Serial: node.ClientSerial,
		CertificatePEM: node.CertificatePEM, CACertificatePEM: node.CACertificatePEM,
		SOCKSUsername: node.SOCKSUsername, SOCKSPassword: node.SOCKSPassword, SOCKSPort: node.SOCKSPort,
	}
	plaintext, err := json.Marshal(configuration)
	if err != nil {
		return ManagedNode{}, err
	}
	envelope, err := sealedconfig.Seal(node.ConfigurationPublicKey, plaintext)
	clear(plaintext)
	if err != nil {
		return ManagedNode{}, errors.New("seal Client endpoint update")
	}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return ManagedNode{}, err
	}
	defer clear(envelopeJSON)
	output, err := orchestrator.runner.RunPowerShell(ctx, node.InstanceID, applyScript(envelopeJSON))
	if err != nil {
		return ManagedNode{}, errors.New("sealed Client endpoint update failed; inspect redacted SSM status")
	}
	if !validApplyOutput([]byte(output)) {
		return ManagedNode{}, errors.New("Client endpoint update returned invalid redacted output")
	}
	node.RelayURL = origin.String()
	node.Health = "installed"
	if err := orchestrator.store.SaveNode(ctx, node); err != nil {
		return ManagedNode{}, errors.New("save rotated managed-node metadata")
	}
	return node, nil
}

func (orchestrator *Orchestrator) Update(ctx context.Context, node ManagedNode, release NodeRelease) (ManagedNode, error) {
	if orchestrator == nil || orchestrator.runner == nil || orchestrator.store == nil {
		return ManagedNode{}, errors.New("node orchestration dependencies are required")
	}
	if err := validateManagedNode(node); err != nil {
		return ManagedNode{}, err
	}
	if err := release.Validate(); err != nil {
		return ManagedNode{}, err
	}
	output, err := orchestrator.runner.RunPowerShell(ctx, node.InstanceID, updateScript(release))
	if err != nil {
		return ManagedNode{}, errors.New("signed Client update failed; inspect redacted SSM status")
	}
	if !validUpdateOutput([]byte(output)) {
		return ManagedNode{}, errors.New("Client update returned invalid redacted output")
	}
	node.ServiceVersion = release.Version
	node.Health = "installed"
	if err := orchestrator.store.SaveNode(ctx, node); err != nil {
		return ManagedNode{}, errors.New("save updated managed-node metadata")
	}
	return node, nil
}

func (orchestrator *Orchestrator) Repair(ctx context.Context, node ManagedNode, release NodeRelease) (ManagedNode, error) {
	updated, err := orchestrator.Update(ctx, node, release)
	if err != nil {
		return ManagedNode{}, err
	}
	return orchestrator.UpdateEndpoint(ctx, updated, updated.RelayURL)
}

func (release NodeRelease) Validate() error {
	parsed, err := url.Parse(release.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Client release must use an HTTPS github.com URL")
	}
	if strings.TrimSpace(release.Version) == "" || len(release.Version) > 64 || len(release.SHA256) != 64 {
		return errors.New("Client release metadata is invalid")
	}
	if _, err := hex.DecodeString(release.SHA256); err != nil {
		return errors.New("Client release SHA-256 is invalid")
	}
	if strings.ContainsAny(release.Version+release.Publisher+release.URL, "'\"\r\n") {
		return errors.New("Client release metadata is invalid")
	}
	return nil
}

func updateScript(release NodeRelease) string {
	publisher := release.Publisher
	if publisher == "" {
		publisher = "Mobile Egress"
	}
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$installDir = 'C:\Program Files\MobileEgress'
$stateDir = 'C:\ProgramData\MobileEgress\Client'
$download = Join-Path $env:TEMP 'mobile-egress-client.update.exe'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -UseBasicParsing -Uri '%s' -OutFile $download
$digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $download).Hash.ToLowerInvariant()
if ($digest -ne '%s') { throw 'release digest verification failed' }
$signature = Get-AuthenticodeSignature -LiteralPath $download
if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notlike '*%s*') { throw 'release signature verification failed' }
$null = New-Item -ItemType Directory -Force -Path $installDir
$null = New-Item -ItemType Directory -Force -Path $stateDir
$null = & icacls.exe $stateDir /inheritance:r /grant:r 'SYSTEM:(OI)(CI)F' 'BUILTIN\Administrators:(OI)(CI)F'
$service = Get-Service -Name 'MobileEgressClient' -ErrorAction SilentlyContinue
if ($null -ne $service -and $service.Status -ne 'Stopped') { Stop-Service -Name 'MobileEgressClient' -Force }
$executable = Join-Path $installDir 'mobile-egress-client.exe'
Move-Item -Force -LiteralPath $download -Destination $executable
if ($null -eq $service) {
  $null = & sc.exe create MobileEgressClient binPath= ('"' + $executable + '" serve --state-dir "' + $stateDir + '"') start= auto obj= LocalSystem
} else {
  $null = & sc.exe config MobileEgressClient binPath= ('"' + $executable + '" serve --state-dir "' + $stateDir + '"') start= auto obj= LocalSystem
}
Start-Service -Name 'MobileEgressClient'
Write-Output '{"updated":true}'`, release.URL, strings.ToLower(release.SHA256), publisher)
}

func installScript(release NodeRelease) string {
	publisher := release.Publisher
	if publisher == "" {
		publisher = "Mobile Egress"
	}
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$installDir = 'C:\Program Files\MobileEgress'
$stateDir = 'C:\ProgramData\MobileEgress\Client'
$download = Join-Path $env:TEMP 'mobile-egress-client.download.exe'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -UseBasicParsing -Uri '%s' -OutFile $download
$digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $download).Hash.ToLowerInvariant()
if ($digest -ne '%s') { throw 'release digest verification failed' }
$signature = Get-AuthenticodeSignature -LiteralPath $download
if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notlike '*%s*') { throw 'release signature verification failed' }
$null = New-Item -ItemType Directory -Force -Path $installDir
$null = New-Item -ItemType Directory -Force -Path $stateDir
$null = & icacls.exe $stateDir /inheritance:r /grant:r 'SYSTEM:(OI)(CI)F' 'BUILTIN\Administrators:(OI)(CI)F'
$executable = Join-Path $installDir 'mobile-egress-client.exe'
Move-Item -Force -LiteralPath $download -Destination $executable
$existing = Get-Service -Name 'MobileEgressClient' -ErrorAction SilentlyContinue
if ($null -eq $existing) {
  $null = & sc.exe create MobileEgressClient binPath= ('"' + $executable + '" serve --state-dir "' + $stateDir + '"') start= auto obj= LocalSystem
} else {
  $null = & sc.exe config MobileEgressClient binPath= ('"' + $executable + '" serve --state-dir "' + $stateDir + '"') start= auto obj= LocalSystem
}
& $executable bootstrap --state-dir $stateDir`, release.URL, strings.ToLower(release.SHA256), publisher)
}

func applyScript(envelopeJSON []byte) string {
	encoded := base64.StdEncoding.EncodeToString(envelopeJSON)
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$executable = 'C:\Program Files\MobileEgress\mobile-egress-client.exe'
$stateDir = 'C:\ProgramData\MobileEgress\Client'
$envelopePath = Join-Path $env:TEMP 'mobile-egress-client.sealed.json'
try {
  [IO.File]::WriteAllBytes($envelopePath, [Convert]::FromBase64String('%s'))
  & $executable apply-config --state-dir $stateDir --envelope-file $envelopePath
  $null = & sc.exe start MobileEgressClient
} finally {
  Remove-Item -Force -LiteralPath $envelopePath -ErrorAction SilentlyContinue
}`, encoded)
}

func decodeBootstrapOutput(raw []byte) (nodeservice.BootstrapResponse, error) {
	if len(raw) == 0 || len(raw) > 512<<10 {
		return nodeservice.BootstrapResponse{}, errors.New("bootstrap output missing or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var response nodeservice.BootstrapResponse
	if err := decoder.Decode(&response); err != nil || response.CSRPEM == "" || response.ConfigurationPublicKey == "" {
		return nodeservice.BootstrapResponse{}, errors.New("invalid bootstrap output")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nodeservice.BootstrapResponse{}, errors.New("invalid bootstrap output")
	}
	return response, nil
}

func validApplyOutput(raw []byte) bool {
	if len(raw) == 0 || len(raw) > 4096 {
		return false
	}
	var response struct {
		Configured bool `json:"configured"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&response) == nil && response.Configured && decoder.Decode(&struct{}{}) == io.EOF
}

func validUpdateOutput(raw []byte) bool {
	if len(raw) == 0 || len(raw) > 4096 {
		return false
	}
	var response struct {
		Updated bool `json:"updated"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&response) == nil && response.Updated && decoder.Decode(&struct{}{}) == io.EOF
}

func randomCredential() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate SOCKS credential: %w", err)
	}
	defer clear(raw)
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validInstanceID(value string) bool {
	if !strings.HasPrefix(value, "i-") || len(value) < 10 || len(value) > 32 {
		return false
	}
	for _, character := range value[2:] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}
