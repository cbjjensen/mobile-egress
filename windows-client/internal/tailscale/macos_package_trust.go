package tailscale

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	macPackageTeamID          = "W5364U7YZB"
	maximumPackageTrustOutput = 4 << 20
	maximumAppleRootManifest  = 32 << 10
	appleRootManifestSHA256   = "67007dcfd51c9a09a9fb1e3c5a477f726db19c88ed67d587ef6ab28561e9a0b2"

	packageTrustPKGUtilPath = "/usr/sbin/pkgutil"
	packageTrustSPCTLPath   = "/usr/sbin/spctl"
)

var (
	errMacPackageTrust            = errors.New("Tailscale macOS PKG verification failed")
	errMacPackageTrustUnavailable = errors.New("Tailscale macOS package trust unavailable")
)

type packageSignerIdentity struct {
	TeamID      string
	LeafSHA256  [32]byte
	ChainSHA256 [][32]byte
}

type pkgutilAssessment struct {
	Trusted bool
}

type evaluatedPackageChain struct {
	ChainSHA256      [][32]byte
	RevocationProven bool
}

type packageChainTrustEvaluator interface {
	Evaluate(context.Context, [][]byte) (evaluatedPackageChain, error)
}

type packageTerminalRootVerifier func(*x509.Certificate, [32]byte) error

type appleRootSet struct {
	DER          [][]byte
	Fingerprints map[[32]byte]struct{}
}

type packageTrustCommandInvocation struct {
	Path        string
	Arguments   []string
	Environment []string
	OutputLimit int64
}

type packageTrustCommandRunner interface {
	Run(context.Context, packageTrustCommandInvocation) ([]byte, error)
}

func validatePackageSigner(evidence xarSignerEvidence) (packageSignerIdentity, error) {
	return validatePackageSignerAt(evidence, time.Now())
}

func validatePackageSignerAt(evidence xarSignerEvidence, now time.Time) (packageSignerIdentity, error) {
	return validatePackageSignerAtWithTerminalVerifier(evidence, now, verifyPinnedAppleRootSelfSignature)
}

func validatePackageSignerAtWithTerminalVerifier(
	evidence xarSignerEvidence,
	now time.Time,
	verifyTerminal packageTerminalRootVerifier,
) (packageSignerIdentity, error) {
	if now.IsZero() || verifyTerminal == nil || evidence.Kind != xarSignatureCMS || len(evidence.SignedChecksum) != sha1.Size ||
		len(evidence.Signature) == 0 || len(evidence.Signature) > maximumMacXARSignature ||
		len(evidence.LegacySignature) == 0 || len(evidence.LegacySignature) > maximumMacXARSignature || len(evidence.ChainDER) != 3 {
		return packageSignerIdentity{}, errMacPackageTrust
	}
	certificates := make([]*x509.Certificate, len(evidence.ChainDER))
	fingerprints := make([][32]byte, len(evidence.ChainDER))
	seen := map[[32]byte]struct{}{}
	for index, der := range evidence.ChainDER {
		if len(der) == 0 || len(der) > maximumMacXARCertificate {
			return packageSignerIdentity{}, errMacPackageTrust
		}
		certificate, err := x509.ParseCertificate(der)
		if err != nil || !bytes.Equal(certificate.Raw, der) || now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
			return packageSignerIdentity{}, errMacPackageTrust
		}
		fingerprint := sha256.Sum256(der)
		if _, duplicate := seen[fingerprint]; duplicate {
			return packageSignerIdentity{}, errMacPackageTrust
		}
		seen[fingerprint] = struct{}{}
		certificates[index] = certificate
		fingerprints[index] = fingerprint
	}
	leaf := certificates[0]
	intermediate := certificates[1]
	root := certificates[2]
	if len(leaf.Subject.OrganizationalUnit) != 1 || leaf.Subject.OrganizationalUnit[0] != macPackageTeamID ||
		countCertificateOID(leaf, packageInstallerOID()) != 1 ||
		countCertificateOID(intermediate, packageIntermediateOID()) != 1 ||
		leaf.IsCA || !intermediate.IsCA || !root.IsCA {
		return packageSignerIdentity{}, errMacPackageTrust
	}
	if leaf.CheckSignatureFrom(intermediate) != nil || intermediate.CheckSignatureFrom(root) != nil || verifyTerminal(root, fingerprints[2]) != nil {
		return packageSignerIdentity{}, errMacPackageTrust
	}
	return packageSignerIdentity{
		TeamID:      macPackageTeamID,
		LeafSHA256:  fingerprints[0],
		ChainSHA256: append([][32]byte(nil), fingerprints...),
	}, nil
}

func countCertificateOID(certificate *x509.Certificate, expected [8]int) int {
	if certificate == nil {
		return 0
	}
	count := 0
	for _, extension := range certificate.Extensions {
		if objectIdentifierEquals(extension.Id, expected) {
			count++
		}
	}
	return count
}

func objectIdentifierEquals(actual []int, expected [8]int) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

func packageInstallerOID() [8]int {
	return [8]int{1, 2, 840, 113635, 100, 6, 1, 14}
}

func packageIntermediateOID() [8]int {
	return [8]int{1, 2, 840, 113635, 100, 6, 2, 6}
}

func newPackageTrustEnvironment() []string {
	return []string{
		"LC_ALL=C",
		"LANG=C",
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
	}
}

type appleRootManifest struct {
	Version int                       `json:"version"`
	Roots   []appleRootManifestRecord `json:"roots"`
}

type appleRootManifestRecord struct {
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	SourceURL string `json:"sourceURL"`
	Subject   string `json:"subject"`
	Serial    string `json:"serial"`
	NotBefore string `json:"notBefore"`
	NotAfter  string `json:"notAfter"`
}

func frozenAppleRootManifestValue() appleRootManifest {
	return appleRootManifest{
		Version: 1,
		Roots: []appleRootManifestRecord{
			{
				Filename: "AppleIncRootCertificate.cer", SHA256: "b0b1730ecbc7ff4505142c49f1295e6eda6bcaed7e2c68c5be91b5a11001f024",
				SourceURL: "https://www.apple.com/appleca/AppleIncRootCertificate.cer",
				Subject:   "CN=Apple Root CA,OU=Apple Certification Authority,O=Apple Inc.,C=US", Serial: "02",
				NotBefore: "2006-04-25T21:40:36Z", NotAfter: "2035-02-09T21:40:36Z",
			},
			{
				Filename: "AppleRootCA-G2.cer", SHA256: "c2b9b042dd57830e7d117dac55ac8ae19407d38e41d88f3215bc3a890444a050",
				SourceURL: "https://www.apple.com/certificateauthority/AppleRootCA-G2.cer",
				Subject:   "CN=Apple Root CA - G2,OU=Apple Certification Authority,O=Apple Inc.,C=US", Serial: "01e0e5b58367a3e0",
				NotBefore: "2014-04-30T18:10:09Z", NotAfter: "2039-04-30T18:10:09Z",
			},
			{
				Filename: "AppleRootCA-G3.cer", SHA256: "63343abfb89a6a03ebb57e9b3f5fa7be7c4f5c756f3017b3a8c488c3653e9179",
				SourceURL: "https://www.apple.com/certificateauthority/AppleRootCA-G3.cer",
				Subject:   "CN=Apple Root CA - G3,OU=Apple Certification Authority,O=Apple Inc.,C=US", Serial: "2dc5fc88d2c54b95",
				NotBefore: "2014-04-30T18:19:06Z", NotAfter: "2039-04-30T18:19:06Z",
			},
		},
	}
}

//go:embed apple_roots/manifest.json apple_roots/*.cer
var embeddedAppleRootFiles embed.FS

func loadEmbeddedAppleRoots() (appleRootSet, error) {
	return loadAppleRootsFromFS(embeddedAppleRootFiles)
}

func loadAppleRootsFromFS(files fs.FS) (appleRootSet, error) {
	if files == nil {
		return appleRootSet{}, errMacPackageTrust
	}
	frozenManifest := frozenAppleRootManifestValue()
	entries, err := fs.ReadDir(files, "apple_roots")
	if err != nil || len(entries) != len(frozenManifest.Roots)+1 {
		return appleRootSet{}, errMacPackageTrust
	}
	expectedFiles := map[string]struct{}{"manifest.json": {}}
	for _, expected := range frozenManifest.Roots {
		expectedFiles[expected.Filename] = struct{}{}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return appleRootSet{}, errMacPackageTrust
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return appleRootSet{}, errMacPackageTrust
		}
		if _, ok := expectedFiles[entry.Name()]; !ok {
			return appleRootSet{}, errMacPackageTrust
		}
	}
	manifestBytes, err := readBoundedAppleRootFile(files, "apple_roots/manifest.json", maximumAppleRootManifest)
	if err != nil {
		return appleRootSet{}, errMacPackageTrust
	}
	manifestFingerprint := sha256.Sum256(manifestBytes)
	if hex.EncodeToString(manifestFingerprint[:]) != appleRootManifestSHA256 {
		return appleRootSet{}, errMacPackageTrust
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var manifest appleRootManifest
	if err := decoder.Decode(&manifest); err != nil {
		return appleRootSet{}, errMacPackageTrust
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || !sameAppleRootManifest(manifest, frozenManifest) {
		return appleRootSet{}, errMacPackageTrust
	}

	result := appleRootSet{DER: make([][]byte, 0, len(manifest.Roots)), Fingerprints: make(map[[32]byte]struct{}, len(manifest.Roots))}
	for _, record := range manifest.Roots {
		der, err := readBoundedAppleRootFile(files, "apple_roots/"+record.Filename, maximumMacXARCertificate)
		if err != nil {
			return appleRootSet{}, errMacPackageTrust
		}
		fingerprint := sha256.Sum256(der)
		if hex.EncodeToString(fingerprint[:]) != record.SHA256 {
			return appleRootSet{}, errMacPackageTrust
		}
		if _, duplicate := result.Fingerprints[fingerprint]; duplicate {
			return appleRootSet{}, errMacPackageTrust
		}
		certificate, err := x509.ParseCertificate(der)
		if err != nil || !bytes.Equal(certificate.Raw, der) || !certificate.IsCA || verifyPinnedAppleRootSelfSignature(certificate, fingerprint) != nil ||
			certificate.Subject.String() != record.Subject || canonicalCertificateSerial(certificate.SerialNumber) != record.Serial ||
			certificate.NotBefore.UTC().Format(time.RFC3339) != record.NotBefore || certificate.NotAfter.UTC().Format(time.RFC3339) != record.NotAfter {
			return appleRootSet{}, errMacPackageTrust
		}
		result.DER = append(result.DER, append([]byte(nil), der...))
		result.Fingerprints[fingerprint] = struct{}{}
	}
	return result, nil
}

func verifyPinnedAppleRootSelfSignature(certificate *x509.Certificate, fingerprint [32]byte) error {
	if certificate == nil || sha256.Sum256(certificate.Raw) != fingerprint {
		return errMacPackageTrust
	}
	if certificate.SignatureAlgorithm != x509.SHA1WithRSA {
		if certificate.CheckSignatureFrom(certificate) != nil {
			return errMacPackageTrust
		}
		return nil
	}
	// Apple's 2006 root is a byte-for-byte pinned SHA-1 self-signed legacy
	// anchor. Go intentionally rejects SHA-1 in generic chain validation, so the
	// narrow manual check is permitted only for that reviewed DER fingerprint.
	if hex.EncodeToString(fingerprint[:]) != "b0b1730ecbc7ff4505142c49f1295e6eda6bcaed7e2c68c5be91b5a11001f024" {
		return errMacPackageTrust
	}
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok || len(certificate.Signature) != publicKey.Size() {
		return errMacPackageTrust
	}
	digest := sha1.Sum(certificate.RawTBSCertificate)
	if rsa.VerifyPKCS1v15(publicKey, crypto.SHA1, digest[:], certificate.Signature) != nil {
		return errMacPackageTrust
	}
	return nil
}

func readBoundedAppleRootFile(files fs.FS, name string, maximum int64) ([]byte, error) {
	info, err := fs.Stat(files, name)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, errMacPackageTrust
	}
	file, err := files.Open(name)
	if err != nil {
		return nil, errMacPackageTrust
	}
	value, err := io.ReadAll(io.LimitReader(file, maximum+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || len(value) == 0 || int64(len(value)) != info.Size() || int64(len(value)) > maximum {
		return nil, errMacPackageTrust
	}
	return value, nil
}

func sameAppleRootManifest(left, right appleRootManifest) bool {
	if left.Version != right.Version || len(left.Roots) != len(right.Roots) {
		return false
	}
	for index := range left.Roots {
		if left.Roots[index] != right.Roots[index] {
			return false
		}
	}
	return true
}

func canonicalCertificateSerial(serial *big.Int) string {
	if serial == nil || serial.Sign() < 0 {
		return ""
	}
	value := strings.ToLower(serial.Text(16))
	if len(value)%2 != 0 {
		value = "0" + value
	}
	return value
}

func validateEvaluatedPackageChain(identity packageSignerIdentity, roots appleRootSet, result evaluatedPackageChain) error {
	if identity.TeamID != macPackageTeamID || len(identity.ChainSHA256) < 2 || len(result.ChainSHA256) != len(identity.ChainSHA256) ||
		!result.RevocationProven || !validAppleRootSet(roots) {
		return errMacPackageTrust
	}
	for index := range identity.ChainSHA256 {
		if result.ChainSHA256[index] != identity.ChainSHA256[index] {
			return errMacPackageTrust
		}
	}
	if _, allowlisted := roots.Fingerprints[result.ChainSHA256[len(result.ChainSHA256)-1]]; !allowlisted {
		return errMacPackageTrust
	}
	return nil
}

func validAppleRootSet(roots appleRootSet) bool {
	if len(roots.DER) == 0 || len(roots.DER) != len(roots.Fingerprints) {
		return false
	}
	seen := make(map[[32]byte]struct{}, len(roots.DER))
	for _, der := range roots.DER {
		if len(der) == 0 {
			return false
		}
		fingerprint := sha256.Sum256(der)
		if _, ok := roots.Fingerprints[fingerprint]; !ok {
			return false
		}
		if _, duplicate := seen[fingerprint]; duplicate {
			return false
		}
		seen[fingerprint] = struct{}{}
	}
	return true
}

func parsePKGSignatureOutput(value []byte) (pkgutilAssessment, error) {
	if len(value) == 0 || len(value) > maximumPackageTrustOutput || !utf8.Valid(value) || bytes.IndexByte(value, 0) >= 0 {
		return pkgutilAssessment{}, errMacPackageTrust
	}
	text := string(value)
	if strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\r") {
		return pkgutilAssessment{}, errMacPackageTrust
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], `Package "`) || !strings.HasSuffix(lines[0], `":`) {
		return pkgutilAssessment{}, errMacPackageTrust
	}
	packageName := strings.TrimSuffix(strings.TrimPrefix(lines[0], `Package "`), `":`)
	if packageName == "" || strings.ContainsAny(packageName, "\"\r\n\x00") {
		return pkgutilAssessment{}, errMacPackageTrust
	}
	legacyTrustedStatus := "Status: signed by a certificate trusted by Mac OS X"
	currentTrustedStatus := "Status: signed by a developer certificate issued by Apple for distribution"
	status := strings.TrimSpace(lines[1])
	if status != legacyTrustedStatus && status != currentTrustedStatus {
		return pkgutilAssessment{}, errMacPackageTrust
	}
	chainIndex := -1
	notarizationSeen := false
	timestampSeen := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if index != 0 && hasPKGUtilHeaderFamily(trimmed, "Package") {
			return pkgutilAssessment{}, errMacPackageTrust
		}
		if hasPKGUtilHeaderFamily(trimmed, "Status") {
			if index != 1 || trimmed != status {
				return pkgutilAssessment{}, errMacPackageTrust
			}
			continue
		}
		if trimmed == "Certificate Chain:" {
			if index < 2 || chainIndex != -1 {
				return pkgutilAssessment{}, errMacPackageTrust
			}
			chainIndex = index
			continue
		}
		if hasPKGUtilHeaderFamily(trimmed, "Certificate Chain") {
			return pkgutilAssessment{}, errMacPackageTrust
		}
		if chainIndex == -1 && index >= 2 {
			switch {
			case trimmed == "Notarization: trusted by the Apple notary service":
				if notarizationSeen || timestampSeen {
					return pkgutilAssessment{}, errMacPackageTrust
				}
				notarizationSeen = true
			case strings.HasPrefix(trimmed, "Signed with a trusted timestamp on: "):
				if timestampSeen || !validPKGUtilTrustedTimestamp(strings.TrimPrefix(trimmed, "Signed with a trusted timestamp on: ")) {
					return pkgutilAssessment{}, errMacPackageTrust
				}
				timestampSeen = true
			default:
				return pkgutilAssessment{}, errMacPackageTrust
			}
		} else if strings.HasPrefix(trimmed, "Notarization:") || strings.HasPrefix(trimmed, "Signed with a trusted timestamp on:") {
			return pkgutilAssessment{}, errMacPackageTrust
		}
	}
	if chainIndex == -1 || chainIndex+1 >= len(lines) || strings.TrimSpace(lines[chainIndex+1]) == "" {
		return pkgutilAssessment{}, errMacPackageTrust
	}
	return pkgutilAssessment{Trusted: true}, nil
}

func hasPKGUtilHeaderFamily(line, family string) bool {
	if len(line) < len(family) || !strings.EqualFold(line[:len(family)], family) {
		return false
	}
	if len(line) == len(family) {
		return true
	}
	switch line[len(family)] {
	case ' ', '\t', ':', '"':
		return true
	default:
		return false
	}
}

func validPKGUtilTrustedTimestamp(value string) bool {
	const layout = "2006-01-02 15:04:05 -0700"
	parsed, err := time.Parse(layout, value)
	return err == nil && parsed.Format(layout) == value
}

func verifyStagedMacPKG(
	ctx context.Context,
	stage *stagedMacPKG,
	evaluator packageChainTrustEvaluator,
	roots appleRootSet,
	runner packageTrustCommandRunner,
	now time.Time,
) error {
	if stage == nil || stage.file == nil || stage.size <= 0 {
		return errMacPackageTrust
	}
	return verifyMacPackageTrust(ctx, stage.file, stage.size, stage, evaluator, roots, runner, now)
}

type stagedMacPKGTrustDependencies struct {
	loadRoots    func() (appleRootSet, error)
	newEvaluator func(appleRootSet) packageChainTrustEvaluator
	runner       packageTrustCommandRunner
	now          func() time.Time
	verify       func(context.Context, *stagedMacPKG, packageChainTrustEvaluator, appleRootSet, packageTrustCommandRunner, time.Time) error
}

func verifyStagedMacPKGWithDependencies(
	ctx context.Context,
	stage *stagedMacPKG,
	dependencies stagedMacPKGTrustDependencies,
) error {
	if ctx == nil || stage == nil || dependencies.loadRoots == nil || dependencies.newEvaluator == nil ||
		dependencies.runner == nil || dependencies.now == nil || dependencies.verify == nil || ctx.Err() != nil {
		return errMacPackageTrust
	}
	roots, err := dependencies.loadRoots()
	if err != nil || !validAppleRootSet(roots) {
		return errMacPackageTrust
	}
	evaluator := dependencies.newEvaluator(roots)
	if evaluator == nil {
		return errMacPackageTrust
	}
	now := dependencies.now()
	if now.IsZero() || dependencies.verify(ctx, stage, evaluator, roots, dependencies.runner, now) != nil {
		return errMacPackageTrust
	}
	return nil
}

func verifyMacPackageTrust(
	ctx context.Context,
	reader io.ReaderAt,
	size int64,
	guard stagedPathGuard,
	evaluator packageChainTrustEvaluator,
	roots appleRootSet,
	runner packageTrustCommandRunner,
	now time.Time,
) error {
	if ctx == nil || reader == nil || size <= 0 || guard == nil || evaluator == nil || runner == nil || now.IsZero() || !validAppleRootSet(roots) || ctx.Err() != nil {
		return errMacPackageTrust
	}
	if guard.Revalidate(ctx) != nil || guard.Path() == "" {
		return errMacPackageTrust
	}
	evidence, err := extractVerifiedXARSigner(reader, size)
	if err != nil {
		return errMacPackageTrust
	}
	identity, err := validatePackageSignerAt(evidence, now)
	if err != nil {
		return errMacPackageTrust
	}
	evaluated, err := evaluator.Evaluate(ctx, cloneMacXARDER(evidence.ChainDER))
	if err != nil || validateEvaluatedPackageChain(identity, roots, evaluated) != nil {
		return errMacPackageTrust
	}
	pkgutilOutput, err := runPackageTrustPathPhase(ctx, guard, runner, packageTrustCommandInvocation{
		Path:        packageTrustPKGUtilPath,
		Arguments:   []string{"--check-signature", guard.Path()},
		Environment: newPackageTrustEnvironment(),
		OutputLimit: maximumPackageTrustOutput,
	})
	if err != nil {
		return errMacPackageTrust
	}
	assessment, err := parsePKGSignatureOutput(pkgutilOutput)
	if err != nil || !assessment.Trusted {
		return errMacPackageTrust
	}
	if _, err := runPackageTrustPathPhase(ctx, guard, runner, packageTrustCommandInvocation{
		Path:        packageTrustSPCTLPath,
		Arguments:   []string{"--assess", "--type", "install", guard.Path()},
		Environment: newPackageTrustEnvironment(),
		OutputLimit: maximumPackageTrustOutput,
	}); err != nil {
		return errMacPackageTrust
	}
	return nil
}

func runPackageTrustPathPhase(
	ctx context.Context,
	guard stagedPathGuard,
	runner packageTrustCommandRunner,
	invocation packageTrustCommandInvocation,
) ([]byte, error) {
	if ctx == nil || guard == nil || runner == nil || invocation.Path == "" || invocation.OutputLimit != maximumPackageTrustOutput ||
		len(invocation.Arguments) == 0 || guard.Revalidate(ctx) != nil || guard.Path() == "" ||
		invocation.Arguments[len(invocation.Arguments)-1] != guard.Path() {
		return nil, errMacPackageTrust
	}
	output, runErr := runner.Run(ctx, invocation)
	postErr := guard.Revalidate(ctx)
	if runErr != nil || postErr != nil || len(output) > int(invocation.OutputLimit) {
		return nil, errMacPackageTrust
	}
	return output, nil
}
