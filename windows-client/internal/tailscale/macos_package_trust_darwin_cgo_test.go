//go:build darwin && cgo

package tailscale

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestDarwinSecurityFrameworkEvaluatorRejectsMalformedFixtureChain(t *testing.T) {
	evaluator := newDarwinPackageChainTrustEvaluator(appleRootSet{})
	if evaluator == nil {
		t.Fatal("Security.framework constructor returned nil")
	}
	result, err := evaluator.Evaluate(context.Background(), [][]byte{{0x30, 0x00}})
	if !errors.Is(err, errMacPackageTrust) {
		t.Fatalf("error = %v, want fixed package trust error", err)
	}
	if len(result.ChainSHA256) != 0 || result.RevocationProven {
		t.Fatalf("malformed chain returned trust evidence: %#v", result)
	}
}

func TestPackageTrustCertificatePackingRejectsTrailingAndTruncatedData(t *testing.T) {
	input := [][]byte{{0x30, 0x01, 0x00}, {0x30, 0x01, 0x01}}
	packed, ok := packPackageTrustCertificates(input)
	if !ok {
		t.Fatal("valid bounded fixture did not pack")
	}
	unpacked, ok := unpackPackageTrustCertificates(packed)
	if !ok || len(unpacked) != len(input) || !bytes.Equal(unpacked[0], input[0]) || !bytes.Equal(unpacked[1], input[1]) {
		t.Fatalf("round trip = %x, %t", unpacked, ok)
	}
	for _, malformed := range [][]byte{packed[:len(packed)-1], append(append([]byte(nil), packed...), 0)} {
		if _, ok := unpackPackageTrustCertificates(malformed); ok {
			t.Fatal("ambiguous packed certificate input accepted")
		}
	}
}

func TestDarwinStagedVerifierSelectsSecurityFrameworkEvaluatorWithCGO(t *testing.T) {
	dependencies := darwinStagedMacPKGTrustDependencies()
	roots, err := dependencies.loadRoots()
	if err != nil {
		t.Fatalf("load embedded roots: %v", err)
	}
	evaluator := dependencies.newEvaluator(roots)
	selected, ok := evaluator.(*securityFrameworkPackageChainTrustEvaluator)
	if !ok || selected == nil || !validAppleRootSet(selected.roots) {
		t.Fatalf("CGO staged verifier selected %#v, want initialized Security.framework evaluator", evaluator)
	}
}
