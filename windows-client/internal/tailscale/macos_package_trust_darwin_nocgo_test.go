//go:build darwin && !cgo

package tailscale

import (
	"context"
	"errors"
	"testing"
)

func TestDarwinNoCGOPackageTrustFailsClosed(t *testing.T) {
	evaluator := newDarwinPackageChainTrustEvaluator(appleRootSet{})
	if evaluator == nil {
		t.Fatal("no-CGO constructor returned nil instead of a fail-closed evaluator")
	}
	result, err := evaluator.Evaluate(context.Background(), [][]byte{{0x30, 0x00}})
	if !errors.Is(err, errMacPackageTrustUnavailable) {
		t.Fatalf("error = %v, want fixed unavailable error", err)
	}
	if len(result.ChainSHA256) != 0 || result.RevocationProven {
		t.Fatalf("no-CGO evaluator returned trust evidence: %#v", result)
	}
}

func TestDarwinStagedVerifierSelectsUnavailableEvaluatorWithoutCGO(t *testing.T) {
	dependencies := darwinStagedMacPKGTrustDependencies()
	roots, err := dependencies.loadRoots()
	if err != nil {
		t.Fatalf("load embedded roots: %v", err)
	}
	evaluator := dependencies.newEvaluator(roots)
	if _, ok := evaluator.(unavailableDarwinPackageChainTrustEvaluator); !ok {
		t.Fatalf("no-CGO staged verifier selected %T, want unavailable evaluator", evaluator)
	}
	if _, err := evaluator.Evaluate(context.Background(), roots.DER[:1]); !errors.Is(err, errMacPackageTrustUnavailable) {
		t.Fatalf("selected no-CGO evaluator error = %v, want fixed unavailable error", err)
	}
}
