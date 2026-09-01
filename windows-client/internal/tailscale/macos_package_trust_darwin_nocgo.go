//go:build darwin && !cgo

package tailscale

import "context"

type unavailableDarwinPackageChainTrustEvaluator struct{}

func newDarwinPackageChainTrustEvaluator(appleRootSet) packageChainTrustEvaluator {
	return unavailableDarwinPackageChainTrustEvaluator{}
}

func (unavailableDarwinPackageChainTrustEvaluator) Evaluate(context.Context, [][]byte) (evaluatedPackageChain, error) {
	return evaluatedPackageChain{}, errMacPackageTrustUnavailable
}

var _ packageChainTrustEvaluator = unavailableDarwinPackageChainTrustEvaluator{}
