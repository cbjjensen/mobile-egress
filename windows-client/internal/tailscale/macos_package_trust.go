package tailscale

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	macPackageSignerLine      = "1. Developer ID Installer: Tailscale Inc. (W5364U7YZB)"
	maximumPackageTrustOutput = 4 << 20
	packageTrustPKGUtilPath   = "/usr/sbin/pkgutil"
	packageTrustSPCTLPath     = "/usr/sbin/spctl"
)

var errMacPackageTrust = errors.New("Tailscale macOS PKG verification failed")

type pkgutilAssessment struct {
	Trusted bool
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

func newPackageTrustEnvironment() []string {
	return []string{
		"LC_ALL=C",
		"LANG=C",
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
	}
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

// verifyMacPackageSystemTrust delegates package-chain and notarization
// validation to the macOS trust services, then requires the fixed Tailscale
// Developer ID identity reported by pkgutil before opening Installer.
func verifyMacPackageSystemTrust(
	ctx context.Context,
	guard stagedPathGuard,
	runner packageTrustCommandRunner,
) error {
	if ctx == nil || guard == nil || runner == nil || ctx.Err() != nil {
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
	if err != nil || !assessment.Trusted || !hasTailscalePackageSigner(pkgutilOutput) {
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

func hasTailscalePackageSigner(value []byte) bool {
	lines := strings.Split(strings.ReplaceAll(string(value), "\r\n", "\n"), "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != "Certificate Chain:" {
			continue
		}
		return index+1 < len(lines) && strings.TrimSpace(lines[index+1]) == macPackageSignerLine
	}
	return false
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
