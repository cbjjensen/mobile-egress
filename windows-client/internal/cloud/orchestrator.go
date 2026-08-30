package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/sealedconfig"
)

type NodeRelease struct {
	Version                 string `json:"version"`
	URL                     string `json:"url"`
	SHA256                  string `json:"sha256"`
	SignerThumbprint        string `json:"signerThumbprint"`
	SignerCertificateSHA256 string `json:"signerCertificateSha256"`
	SignerCertificateBase64 string `json:"signerCertificateBase64"`
}

type ManagedNode struct {
	InstanceID              string `json:"instanceId"`
	ClientSerial            string `json:"clientSerial"`
	ConfigurationPublicKey  string `json:"configurationPublicKey"`
	ConfigurationGeneration uint64 `json:"configurationGeneration"`
	ServiceVersion          string `json:"serviceVersion"`
	Health                  string `json:"health"`
	SOCKSUsername           string `json:"socksUsername"`
	SOCKSPassword           string `json:"socksPassword"`
	SOCKSPort               uint16 `json:"socksPort"`
	RelayURL                string `json:"relayUrl"`
	CertificatePEM          string `json:"certificatePem"`
	CACertificatePEM        string `json:"caCertificatePem"`
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
		Version: 1, Generation: 1, RelayURL: issued.RelayURL, Role: issued.Role, Serial: strings.ToUpper(issued.Serial),
		CertificatePEM: issued.CertificatePEM, CACertificatePEM: issued.CACertificatePEM,
		SOCKSUsername: username, SOCKSPassword: password, SOCKSPort: 1080,
	}
	node := ManagedNode{
		InstanceID: instanceID, ClientSerial: strings.ToUpper(issued.Serial),
		ConfigurationPublicKey: bootstrap.ConfigurationPublicKey, ConfigurationGeneration: 1, ServiceVersion: release.Version,
		Health: "configuring", SOCKSUsername: username, SOCKSPassword: password, SOCKSPort: 1080,
		RelayURL: issued.RelayURL, CertificatePEM: issued.CertificatePEM, CACertificatePEM: issued.CACertificatePEM,
	}
	if err := orchestrator.store.SaveNode(ctx, node); err != nil {
		return ManagedNode{}, errors.New("save recoverable managed-node metadata before configuration")
	}
	if err := orchestrator.applyConfiguration(ctx, node, configuration); err != nil {
		return ManagedNode{}, err
	}
	node.Health = "installed"
	if err := orchestrator.store.SaveNode(ctx, node); err != nil {
		return ManagedNode{}, errors.New("save installed managed-node health")
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
		Version: 1, Generation: node.ConfigurationGeneration + 1, RelayURL: origin.String(), Role: "client", Serial: node.ClientSerial,
		CertificatePEM: node.CertificatePEM, CACertificatePEM: node.CACertificatePEM,
		SOCKSUsername: node.SOCKSUsername, SOCKSPassword: node.SOCKSPassword, SOCKSPort: node.SOCKSPort,
	}
	desired := node
	desired.RelayURL = origin.String()
	desired.ConfigurationGeneration++
	desired.Health = "configuring"
	if err := orchestrator.store.SaveNode(ctx, desired); err != nil {
		return ManagedNode{}, errors.New("save recoverable endpoint metadata before configuration")
	}
	if err := orchestrator.applyConfiguration(ctx, desired, configuration); err != nil {
		return ManagedNode{}, err
	}
	desired.Health = "installed"
	if err := orchestrator.store.SaveNode(ctx, desired); err != nil {
		return ManagedNode{}, errors.New("save rotated managed-node metadata")
	}
	return desired, nil
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
	if node.Health != "configuring" {
		node.Health = "installed"
	}
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
	return orchestrator.ReapplyConfiguration(ctx, updated)
}

// ReapplyConfiguration delivers the controller's current generation. The node
// accepts this only when it is either the missing initial configuration or an
// authenticated byte-for-byte match for the configuration already persisted.
func (orchestrator *Orchestrator) ReapplyConfiguration(ctx context.Context, node ManagedNode) (ManagedNode, error) {
	if orchestrator == nil || orchestrator.runner == nil || orchestrator.store == nil {
		return ManagedNode{}, errors.New("node orchestration dependencies are required")
	}
	if err := validateManagedNode(node); err != nil {
		return ManagedNode{}, err
	}
	configuration := nodeservice.Configuration{
		Version: 1, Generation: node.ConfigurationGeneration, RelayURL: node.RelayURL, Role: "client", Serial: node.ClientSerial,
		CertificatePEM: node.CertificatePEM, CACertificatePEM: node.CACertificatePEM,
		SOCKSUsername: node.SOCKSUsername, SOCKSPassword: node.SOCKSPassword, SOCKSPort: node.SOCKSPort,
	}
	if err := orchestrator.applyConfiguration(ctx, node, configuration); err != nil {
		return ManagedNode{}, err
	}
	node.Health = "installed"
	if err := orchestrator.store.SaveNode(ctx, node); err != nil {
		return ManagedNode{}, errors.New("save repaired managed-node metadata")
	}
	return node, nil
}

func (orchestrator *Orchestrator) applyConfiguration(ctx context.Context, node ManagedNode, configuration nodeservice.Configuration) error {
	plaintext, err := json.Marshal(configuration)
	if err != nil {
		return err
	}
	envelope, err := sealedconfig.Seal(node.ConfigurationPublicKey, plaintext)
	clear(plaintext)
	if err != nil {
		return errors.New("seal Client configuration")
	}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	defer clear(envelopeJSON)
	output, err := orchestrator.runner.RunPowerShell(ctx, node.InstanceID, applyScript(envelopeJSON))
	if err != nil {
		return errors.New("sealed Client configuration command failed; inspect redacted SSM status")
	}
	if !validApplyOutput([]byte(output)) {
		return errors.New("Client configuration returned invalid redacted output")
	}
	return nil
}

func (release NodeRelease) Validate() error {
	parsed, err := url.Parse(release.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Client release must use an HTTPS github.com URL")
	}
	if strings.TrimSpace(release.Version) == "" || len(release.Version) > 64 || len(release.SHA256) != 64 || len(release.SignerThumbprint) != 40 {
		return errors.New("Client release metadata is invalid")
	}
	if _, err := hex.DecodeString(release.SHA256); err != nil {
		return errors.New("Client release SHA-256 is invalid")
	}
	if _, err := hex.DecodeString(release.SignerThumbprint); err != nil {
		return errors.New("Client release signer thumbprint is invalid")
	}
	if strings.ContainsAny(release.Version+release.URL, "'\"\r\n") {
		return errors.New("Client release metadata is invalid")
	}
	if len(release.SignerCertificateSHA256) != 64 || release.SignerCertificateSHA256 != strings.ToLower(release.SignerCertificateSHA256) {
		return errors.New("Client release signer certificate SHA-256 is invalid")
	}
	if _, err := hex.DecodeString(release.SignerCertificateSHA256); err != nil {
		return errors.New("Client release signer certificate SHA-256 is invalid")
	}
	const maxSignerCertificateDERBytes = 16 << 10
	if len(release.SignerCertificateBase64) == 0 || len(release.SignerCertificateBase64) > base64.StdEncoding.EncodedLen(maxSignerCertificateDERBytes) {
		return errors.New("Client release signer certificate is invalid")
	}
	certificateDER, err := base64.StdEncoding.DecodeString(release.SignerCertificateBase64)
	if err != nil || len(certificateDER) == 0 || len(certificateDER) > maxSignerCertificateDERBytes || base64.StdEncoding.EncodeToString(certificateDER) != release.SignerCertificateBase64 {
		return errors.New("Client release signer certificate is invalid")
	}
	certificate, err := x509.ParseCertificate(certificateDER)
	if err != nil || len(certificate.UnhandledCriticalExtensions) != 0 {
		return errors.New("Client release signer certificate is invalid")
	}
	certificateSHA256 := sha256.Sum256(certificateDER)
	if hex.EncodeToString(certificateSHA256[:]) != release.SignerCertificateSHA256 {
		return errors.New("Client release signer certificate SHA-256 does not match")
	}
	certificateSHA1 := sha1.Sum(certificateDER)
	if !strings.EqualFold(hex.EncodeToString(certificateSHA1[:]), release.SignerThumbprint) {
		return errors.New("Client release signer thumbprint does not match")
	}
	if err := certificate.CheckSignature(certificate.SignatureAlgorithm, certificate.RawTBSCertificate, certificate.Signature); err != nil {
		return errors.New("Client release signer certificate is not self-signed")
	}
	if !certificate.BasicConstraintsValid || certificate.IsCA {
		return errors.New("Client release signer certificate must enforce CA=false")
	}
	codeSigning := false
	for _, usage := range certificate.ExtKeyUsage {
		if usage == x509.ExtKeyUsageCodeSigning {
			codeSigning = true
			break
		}
	}
	if !codeSigning {
		return errors.New("Client release signer certificate does not permit code signing")
	}
	now := time.Now()
	if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
		return errors.New("Client release signer certificate is not currently valid")
	}
	return nil
}

func updateScript(release NodeRelease) string {
	return trustedReleaseScript(release, `$service = Get-Service -Name 'MobileEgressClient' -ErrorAction SilentlyContinue
if ($null -ne $service -and $service.Status -ne 'Stopped') { Stop-Service -Name 'MobileEgressClient' -Force }
$executable = Join-Path $installDir 'mobile-egress-client.exe'
Move-Item -Force -LiteralPath $download -Destination $executable
if ($null -eq $service) {
  $null = & sc.exe create MobileEgressClient binPath= ('"' + $executable + '" serve --state-dir "' + $stateDir + '"') start= auto obj= LocalSystem
} else {
  $null = & sc.exe config MobileEgressClient binPath= ('"' + $executable + '" serve --state-dir "' + $stateDir + '"') start= auto obj= LocalSystem
}
Start-Service -Name 'MobileEgressClient'
Write-Output '{"updated":true}'`)
}

func installScript(release NodeRelease) string {
	return trustedReleaseScript(release, `$executable = Join-Path $installDir 'mobile-egress-client.exe'
Move-Item -Force -LiteralPath $download -Destination $executable
$existing = Get-Service -Name 'MobileEgressClient' -ErrorAction SilentlyContinue
if ($null -eq $existing) {
  $null = & sc.exe create MobileEgressClient binPath= ('"' + $executable + '" serve --state-dir "' + $stateDir + '"') start= auto obj= LocalSystem
} else {
  $null = & sc.exe config MobileEgressClient binPath= ('"' + $executable + '" serve --state-dir "' + $stateDir + '"') start= auto obj= LocalSystem
}
$bootstrapOutput = (& $executable bootstrap --state-dir $stateDir 2>$null | Out-String).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Client bootstrap failed' }
if ([Text.Encoding]::UTF8.GetByteCount($bootstrapOutput) -gt 524288) { throw 'Client bootstrap output exceeded its public bound' }
Write-Output $bootstrapOutput`)
}

func trustedReleaseScript(release NodeRelease, operation string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$installDir = 'C:\Program Files\MobileEgress'
$stateDir = 'C:\ProgramData\MobileEgress\Client'
$attemptID = [Guid]::NewGuid().ToString('N')
$download = Join-Path $env:TEMP ("mobile-egress-client-$attemptID.exe")
$certificatePath = Join-Path $env:TEMP ("mobile-egress-publisher-$attemptID.cer")
$certificateBase64 = '%s'
$certificateSha256 = '%s'
$certificateThumbprint = '%s'
$addedStores = [Collections.Generic.List[string]]::new()

function Get-ExactTrustCertificate {
  param([Parameter(Mandatory)][string]$StoreName)
  $storePath = "Cert:\LocalMachine\$StoreName"
  $thumbprintMatches = @(Get-ChildItem -LiteralPath $storePath | Where-Object { $_.Thumbprint.ToUpperInvariant() -eq $certificateThumbprint })
  foreach ($candidate in $thumbprintMatches) {
    if ([Convert]::ToBase64String($candidate.RawData) -ceq $certificateBase64) { return $candidate }
  }
  if ($thumbprintMatches.Count -ne 0) { throw 'publisher trust store contains a non-exact thumbprint collision' }
  return $null
}

function Ensure-ExactTrust {
  param([Parameter(Mandatory)][string]$StoreName)
  if ($null -ne (Get-ExactTrustCertificate -StoreName $StoreName)) { return }
  $null = $addedStores.Add($StoreName)
  $null = Import-Certificate -FilePath $certificatePath -CertStoreLocation "Cert:\LocalMachine\$StoreName"
  if ($null -eq (Get-ExactTrustCertificate -StoreName $StoreName)) { throw 'exact publisher trust import failed' }
}

function Remove-AttemptTrust {
  param([Parameter(Mandatory)][string]$StoreName)
  $store = [Security.Cryptography.X509Certificates.X509Store]::new($StoreName, [Security.Cryptography.X509Certificates.StoreLocation]::LocalMachine)
  try {
    $store.Open([Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite)
    foreach ($candidate in @($store.Certificates)) {
      if ($candidate.Thumbprint.ToUpperInvariant() -eq $certificateThumbprint -and [Convert]::ToBase64String($candidate.RawData) -ceq $certificateBase64) {
        $store.Remove($candidate)
      }
    }
  } finally {
    $store.Close()
    $store.Dispose()
  }
}

try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  Invoke-WebRequest -UseBasicParsing -Uri '%s' -OutFile $download
  $digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $download).Hash.ToLowerInvariant()
  if ($digest -ne '%s') { throw 'release digest verification failed' }

  [IO.File]::WriteAllBytes($certificatePath, [Convert]::FromBase64String($certificateBase64))
  $certificateDigest = (Get-FileHash -Algorithm SHA256 -LiteralPath $certificatePath).Hash.ToLowerInvariant()
  if ($certificateDigest -ne $certificateSha256) { throw 'publisher certificate digest verification failed' }
  $embeddedCertificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new($certificatePath)
  try {
    if ($embeddedCertificate.Thumbprint.ToUpperInvariant() -ne $certificateThumbprint -or [Convert]::ToBase64String($embeddedCertificate.RawData) -cne $certificateBase64) {
      throw 'publisher certificate identity verification failed'
    }
  } finally {
    $embeddedCertificate.Dispose()
  }

  $untrustedSignature = Get-AuthenticodeSignature -LiteralPath $download
  if ($untrustedSignature.Status.ToString() -notin @('NotTrusted', 'Valid')) { throw 'release signature is not intact before trust' }
  if ($null -eq $untrustedSignature.SignerCertificate -or [Convert]::ToBase64String($untrustedSignature.SignerCertificate.RawData) -cne $certificateBase64) {
    throw 'release signer certificate does not match before trust'
  }
  if ($untrustedSignature.SignerCertificate.Thumbprint.ToUpperInvariant() -ne $certificateThumbprint) { throw 'release signer thumbprint does not match before trust' }

  Ensure-ExactTrust -StoreName 'Root'
  Ensure-ExactTrust -StoreName 'TrustedPublisher'

  $trustedSignature = Get-AuthenticodeSignature -LiteralPath $download
  if ($trustedSignature.Status.ToString() -ne 'Valid' -or $null -eq $trustedSignature.SignerCertificate) { throw 'release signature is not valid after trust' }
  if ($trustedSignature.SignerCertificate.Thumbprint.ToUpperInvariant() -ne $certificateThumbprint -or [Convert]::ToBase64String($trustedSignature.SignerCertificate.RawData) -cne $certificateBase64) {
    throw 'release signer certificate does not match after trust'
  }

  $null = New-Item -ItemType Directory -Force -Path $installDir
  $null = New-Item -ItemType Directory -Force -Path $stateDir
  $null = & icacls.exe $stateDir /inheritance:r /grant:r 'SYSTEM:(OI)(CI)F' 'BUILTIN\Administrators:(OI)(CI)F'
%s
} catch {
  $rollbackFailed = $false
  foreach ($storeName in @($addedStores)) {
    try { Remove-AttemptTrust -StoreName $storeName } catch { $rollbackFailed = $true }
  }
  if ($rollbackFailed) { throw 'Client release failed and exact publisher trust rollback failed' }
  throw 'Client release operation failed'
} finally {
  Remove-Item -Force -LiteralPath $certificatePath -ErrorAction SilentlyContinue
  Remove-Item -Force -LiteralPath $download -ErrorAction SilentlyContinue
}`, release.SignerCertificateBase64, release.SignerCertificateSHA256, strings.ToUpper(release.SignerThumbprint), release.URL, strings.ToLower(release.SHA256), operation)
}

func applyScript(envelopeJSON []byte) string {
	encoded := base64.StdEncoding.EncodeToString(envelopeJSON)
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$executable = 'C:\Program Files\MobileEgress\mobile-egress-client.exe'
$stateDir = 'C:\ProgramData\MobileEgress\Client'
$envelopePath = Join-Path $env:TEMP 'mobile-egress-client.sealed.json'
try {
  [IO.File]::WriteAllBytes($envelopePath, [Convert]::FromBase64String('%s'))
  $null = & $executable apply-config --state-dir $stateDir --envelope-file $envelopePath
  if ($LASTEXITCODE -ne 0) { throw 'sealed configuration was rejected' }
  $service = Get-Service -Name 'MobileEgressClient' -ErrorAction Stop
  if ($service.Status -ne 'Stopped') {
    Stop-Service -Name 'MobileEgressClient' -Force -ErrorAction Stop
    $service.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
  }
  Start-Service -Name 'MobileEgressClient' -ErrorAction Stop
  (Get-Service -Name 'MobileEgressClient' -ErrorAction Stop).WaitForStatus('Running', [TimeSpan]::FromSeconds(30))
  Write-Output '{"configured":true}'
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
