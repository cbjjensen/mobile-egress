package tailscale

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

const red5TrustedPKGUtilOutput = `Package "Tailscale-1.100.1-macos.pkg":
   Status: signed by a certificate trusted by Mac OS X
   Certificate Chain:
    1. Developer ID Installer: Fixture only (W5364U7YZB)
`

const red5TailscaleTrustedPKGUtilOutput = `Package "Tailscale-1.100.1-macos.pkg":
   Status: signed by a developer certificate issued by Apple for distribution
   Notarization: trusted by the Apple notary service
   Signed with a trusted timestamp on: 2026-05-29 19:15:36 +0000
   Certificate Chain:
    1. Developer ID Installer: Tailscale Inc. (W5364U7YZB)
`

const red5CurrentTrustedPKGUtilOutput = `Package "Tailscale-1.100.1-macos.pkg":
   Status: signed by a developer certificate issued by Apple for distribution
   Notarization: trusted by the Apple notary service
   Signed with a trusted timestamp on: 2026-05-29 19:15:36 +0000
   Certificate Chain:
    1. Developer ID Installer: Fixture only (W5364U7YZB)
`

func TestParsePKGSignatureOutputRequiresOneExactTrustedStatusShape(t *testing.T) {
	for name, fixture := range map[string]string{
		"legacy trusted phrase": red5TrustedPKGUtilOutput,
		"legacy phrase with current metadata": strings.Replace(
			red5TrustedPKGUtilOutput,
			"Certificate Chain:",
			"Notarization: trusted by the Apple notary service\n   Signed with a trusted timestamp on: 2026-05-29 19:15:36 +0000\n   Certificate Chain:",
			1,
		),
		"current distribution phrase without metadata": strings.Replace(
			strings.Replace(red5CurrentTrustedPKGUtilOutput, "   Notarization: trusted by the Apple notary service\n", "", 1),
			"   Signed with a trusted timestamp on: 2026-05-29 19:15:36 +0000\n", "", 1,
		),
		"current distribution phrase with metadata": red5CurrentTrustedPKGUtilOutput,
	} {
		t.Run(name, func(t *testing.T) {
			assessment, err := parsePKGSignatureOutput([]byte(fixture))
			if err != nil || !assessment.Trusted {
				t.Fatalf("trusted fixture = %#v, %v", assessment, err)
			}
		})
	}
	tests := []struct {
		name  string
		value []byte
	}{
		{name: "empty", value: nil},
		{name: "missing status", value: []byte("Package \"x.pkg\":\nCertificate Chain:\n 1. anything\n")},
		{name: "untrusted", value: []byte("Package \"x.pkg\":\n Status: signed by an untrusted certificate\n Certificate Chain:\n 1. anything\n")},
		{name: "ambiguous status", value: []byte(red5TrustedPKGUtilOutput + " Status: signed by an untrusted certificate\n")},
		{name: "duplicate trusted status", value: []byte(red5TrustedPKGUtilOutput + " Status: signed by a certificate trusted by Mac OS X\n")},
		{name: "mixed duplicate trusted statuses", value: []byte(red5TrustedPKGUtilOutput + " Status: signed by a developer certificate issued by Apple for distribution\n")},
		{name: "development certificate", value: []byte(strings.Replace(red5CurrentTrustedPKGUtilOutput, "Status: signed by a developer certificate issued by Apple for distribution", "Status: signed by a developer certificate issued by Apple (Development)", 1))},
		{name: "App Store certificate", value: []byte(strings.Replace(red5CurrentTrustedPKGUtilOutput, "signed by a developer certificate issued by Apple for distribution", "signed for the Mac App Store", 1))},
		{name: "untrusted notarization metadata", value: []byte(strings.Replace(red5CurrentTrustedPKGUtilOutput, "Notarization: trusted by the Apple notary service", "Notarization: rejected", 1))},
		{name: "duplicate notarization metadata", value: []byte(strings.Replace(red5CurrentTrustedPKGUtilOutput, "Signed with", "Notarization: trusted by the Apple notary service\nSigned with", 1))},
		{name: "malformed trusted timestamp", value: []byte(strings.Replace(red5CurrentTrustedPKGUtilOutput, "2026-05-29 19:15:36 +0000", "yesterday", 1))},
		{name: "duplicate trusted timestamp", value: []byte(strings.Replace(red5CurrentTrustedPKGUtilOutput, "Certificate Chain:", "Signed with a trusted timestamp on: 2026-05-29 19:15:36 +0000\nCertificate Chain:", 1))},
		{name: "unknown pre-chain metadata", value: []byte(strings.Replace(red5CurrentTrustedPKGUtilOutput, "Certificate Chain:", "Mystery: accepted\nCertificate Chain:", 1))},
		{name: "duplicate chain header", value: []byte(red5CurrentTrustedPKGUtilOutput + " Certificate Chain:\n 1. lookalike\n")},
		{name: "duplicate package header", value: []byte(red5CurrentTrustedPKGUtilOutput + " Package \"lookalike.pkg\":\n")},
		{name: "package header family tab after chain", value: []byte(red5CurrentTrustedPKGUtilOutput + " Package\t\"lookalike.pkg\":\n")},
		{name: "package header family no space after chain", value: []byte(red5CurrentTrustedPKGUtilOutput + " Package\"lookalike.pkg\":\n")},
		{name: "package header family wrong case after chain", value: []byte(red5CurrentTrustedPKGUtilOutput + " package \"lookalike.pkg\":\n")},
		{name: "status header family spaced colon after chain", value: []byte(red5CurrentTrustedPKGUtilOutput + " Status : signed by a certificate trusted by Mac OS X\n")},
		{name: "status header family wrong case after chain", value: []byte(red5CurrentTrustedPKGUtilOutput + " status: signed by a certificate trusted by Mac OS X\n")},
		{name: "chain header family spaced colon after chain", value: []byte(red5CurrentTrustedPKGUtilOutput + " Certificate Chain :\n 1. lookalike\n")},
		{name: "chain header family wrong case after chain", value: []byte(red5CurrentTrustedPKGUtilOutput + " certificate chain:\n 1. lookalike\n")},
		{name: "trusted substring", value: []byte("Package \"x.pkg\":\n Certificate: Status: signed by a certificate trusted by Mac OS X\n")},
		{name: "wrong case", value: []byte(strings.Replace(red5TrustedPKGUtilOutput, "Status:", "status:", 1))},
		{name: "NUL", value: append([]byte(red5TrustedPKGUtilOutput), 0)},
		{name: "above output cap", value: bytes.Repeat([]byte{'x'}, (4<<20)+1)},
		{name: "missing package header", value: []byte("Status: signed by a certificate trusted by Mac OS X\nCertificate Chain:\n1. x\n")},
		{name: "missing chain header", value: []byte("Package \"x.pkg\":\nStatus: signed by a certificate trusted by Mac OS X\n1. x\n")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parsePKGSignatureOutput(test.value); err == nil {
				t.Fatal("malformed pkgutil status accepted")
			}
		})
	}
}

func TestVerifyMacPackageSystemTrustAcceptsTrustedTailscaleSigner(t *testing.T) {
	runner := &red5PackageTrustRunner{
		outputs: map[string][]byte{
			packageTrustPKGUtilPath: []byte(red5TailscaleTrustedPKGUtilOutput),
			packageTrustSPCTLPath:   nil,
		},
		errors: map[string]error{},
	}
	guard := &red5PackageTrustGuard{path: "/private/stage/Tailscale-1.100.1-macos.pkg", current: "admitted"}

	if err := verifyMacPackageSystemTrust(context.Background(), guard, runner); err != nil {
		t.Fatalf("verifyMacPackageSystemTrust() = %v", err)
	}
	if len(runner.invocations) != 2 {
		t.Fatalf("commands = %d, want pkgutil and spctl", len(runner.invocations))
	}
}

func TestVerifyMacPackageSystemTrustRejectsOtherTrustedDeveloper(t *testing.T) {
	runner := &red5PackageTrustRunner{
		outputs: map[string][]byte{
			packageTrustPKGUtilPath: []byte(red5TrustedPKGUtilOutput),
			packageTrustSPCTLPath:   nil,
		},
		errors: map[string]error{},
	}
	guard := &red5PackageTrustGuard{path: "/private/stage/Tailscale-1.100.1-macos.pkg", current: "admitted"}

	if err := verifyMacPackageSystemTrust(context.Background(), guard, runner); !errors.Is(err, errMacPackageTrust) {
		t.Fatalf("verifyMacPackageSystemTrust() = %v, want fixed trust error", err)
	}
	if len(runner.invocations) != 1 {
		t.Fatalf("commands = %d, want only pkgutil", len(runner.invocations))
	}
}

type red5PackageTrustGuard struct {
	path          string
	current       string
	events        *red5PackageTrustEventLog
	revalidations int
	failAt        int
}

func (guard *red5PackageTrustGuard) Path() string { return guard.path }

func (guard *red5PackageTrustGuard) Revalidate(context.Context) error {
	guard.revalidations++
	guard.events.add("revalidate")
	if guard.failAt == guard.revalidations || guard.current != "admitted" {
		return errors.New("raw guard path /private/fixture changed")
	}
	return nil
}

type red5PackageTrustRunner struct {
	events      *red5PackageTrustEventLog
	invocations []packageTrustCommandInvocation
	outputs     map[string][]byte
	errors      map[string]error
	hook        func(packageTrustCommandInvocation)
}

func (runner *red5PackageTrustRunner) Run(_ context.Context, invocation packageTrustCommandInvocation) ([]byte, error) {
	runner.invocations = append(runner.invocations, packageTrustCommandInvocation{
		Path:        invocation.Path,
		Arguments:   append([]string(nil), invocation.Arguments...),
		Environment: append([]string(nil), invocation.Environment...),
		OutputLimit: invocation.OutputLimit,
	})
	runner.events.add(filepath.Base(invocation.Path))
	if runner.hook != nil {
		runner.hook(invocation)
	}
	if err := runner.errors[invocation.Path]; err != nil {
		return nil, err
	}
	return append([]byte(nil), runner.outputs[invocation.Path]...), nil
}

type red5PackageTrustEventLog struct{ values []string }

func (events *red5PackageTrustEventLog) add(value string) {
	if events != nil {
		events.values = append(events.values, value)
	}
}

func TestRunPackageTrustPathPhaseRejectsZeroArgumentsWithoutPanicking(t *testing.T) {
	guard := &red5PackageTrustGuard{path: "/private/stage/Tailscale.pkg", current: "admitted"}
	runner := &red5PackageTrustRunner{outputs: map[string][]byte{}, errors: map[string]error{}}
	if _, err := runPackageTrustPathPhase(context.Background(), guard, runner, packageTrustCommandInvocation{
		Path: packageTrustPKGUtilPath, Environment: newPackageTrustEnvironment(), OutputLimit: maximumPackageTrustOutput,
	}); !errors.Is(err, errMacPackageTrust) {
		t.Fatalf("error = %v, want fixed package trust error", err)
	}
	if len(runner.invocations) != 0 {
		t.Fatal("malformed invocation reached a native command runner")
	}
}

var _ packageTrustCommandRunner = (*red5PackageTrustRunner)(nil)
var _ stagedPathGuard = (*red5PackageTrustGuard)(nil)
