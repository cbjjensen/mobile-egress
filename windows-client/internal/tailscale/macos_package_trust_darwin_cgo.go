//go:build darwin && cgo

package tailscale

/*
#cgo CFLAGS: -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework CoreFoundation -framework Security -mmacosx-version-min=13.0
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static uint32_t mePackageTrustRead32(const uint8_t *value) {
	return ((uint32_t)value[0] << 24) | ((uint32_t)value[1] << 16) |
	       ((uint32_t)value[2] << 8) | (uint32_t)value[3];
}

static void mePackageTrustWrite32(uint8_t *value, uint32_t number) {
	value[0] = (uint8_t)(number >> 24);
	value[1] = (uint8_t)(number >> 16);
	value[2] = (uint8_t)(number >> 8);
	value[3] = (uint8_t)number;
}

static int mePackageTrustAppendCertificates(
	CFMutableArrayRef destination,
	const uint8_t *packed,
	size_t packedLength
) {
	if (destination == NULL || packed == NULL || packedLength < 4) {
		return 0;
	}
	uint32_t count = mePackageTrustRead32(packed);
	if (count == 0 || count > 16) {
		return 0;
	}
	size_t cursor = 4;
	for (uint32_t index = 0; index < count; index++) {
		if (cursor > packedLength || packedLength - cursor < 4) {
			return 0;
		}
		uint32_t certificateLength = mePackageTrustRead32(packed + cursor);
		cursor += 4;
		if (certificateLength == 0 || certificateLength > (64U << 10) ||
		    cursor > packedLength || packedLength - cursor < certificateLength) {
			return 0;
		}
		CFDataRef data = CFDataCreate(kCFAllocatorDefault, packed + cursor, certificateLength);
		if (data == NULL) {
			return 0;
		}
		SecCertificateRef certificate = SecCertificateCreateWithData(kCFAllocatorDefault, data);
		CFRelease(data);
		if (certificate == NULL) {
			return 0;
		}
		CFArrayAppendValue(destination, certificate);
		CFRelease(certificate);
		cursor += certificateLength;
	}
	return cursor == packedLength;
}

static int mePackageTrustEvaluate(
	const uint8_t *packedChain,
	size_t packedChainLength,
	const uint8_t *packedRoots,
	size_t packedRootsLength,
	uint8_t **output,
	size_t *outputLength
) {
	if (output == NULL || outputLength == NULL) {
		return 0;
	}
	*output = NULL;
	*outputLength = 0;
	int success = 0;
	CFMutableArrayRef chain = NULL;
	CFMutableArrayRef roots = NULL;
	SecPolicyRef basicPolicy = NULL;
	SecPolicyRef revocationPolicy = NULL;
	CFArrayRef policies = NULL;
	SecTrustRef trust = NULL;
	CFDateRef verifyDate = NULL;
	CFArrayRef evaluatedChain = NULL;

	chain = CFArrayCreateMutable(kCFAllocatorDefault, 0, &kCFTypeArrayCallBacks);
	roots = CFArrayCreateMutable(kCFAllocatorDefault, 0, &kCFTypeArrayCallBacks);
	if (!mePackageTrustAppendCertificates(chain, packedChain, packedChainLength) ||
	    !mePackageTrustAppendCertificates(roots, packedRoots, packedRootsLength)) {
		goto cleanup;
	}
	basicPolicy = SecPolicyCreateBasicX509();
	revocationPolicy = SecPolicyCreateRevocation(
		kSecRevocationUseAnyAvailableMethod | kSecRevocationRequirePositiveResponse
	);
	if (basicPolicy == NULL || revocationPolicy == NULL) {
		goto cleanup;
	}
	const void *policyValues[2] = { basicPolicy, revocationPolicy };
	policies = CFArrayCreate(kCFAllocatorDefault, policyValues, 2, &kCFTypeArrayCallBacks);
	if (policies == NULL || SecTrustCreateWithCertificates(chain, policies, &trust) != errSecSuccess || trust == NULL) {
		goto cleanup;
	}
	if (SecTrustSetAnchorCertificates(trust, roots) != errSecSuccess ||
	    SecTrustSetAnchorCertificatesOnly(trust, true) != errSecSuccess ||
	    SecTrustSetNetworkFetchAllowed(trust, true) != errSecSuccess) {
		goto cleanup;
	}
	verifyDate = CFDateCreate(kCFAllocatorDefault, CFAbsoluteTimeGetCurrent());
	if (verifyDate == NULL || SecTrustSetVerifyDate(trust, verifyDate) != errSecSuccess) {
		goto cleanup;
	}
	if (!SecTrustEvaluateWithError(trust, NULL)) {
		goto cleanup;
	}
	evaluatedChain = SecTrustCopyCertificateChain(trust);
	if (evaluatedChain == NULL) {
		goto cleanup;
	}
	CFIndex count = CFArrayGetCount(evaluatedChain);
	if (count <= 0 || count > 16) {
		goto cleanup;
	}
	size_t total = 4;
	for (CFIndex index = 0; index < count; index++) {
		SecCertificateRef certificate = (SecCertificateRef)CFArrayGetValueAtIndex(evaluatedChain, index);
		CFDataRef data = certificate == NULL ? NULL : SecCertificateCopyData(certificate);
		if (data == NULL) {
			goto cleanup;
		}
		CFIndex length = CFDataGetLength(data);
		CFRelease(data);
		if (length <= 0 || length > (64 << 10) || total > SIZE_MAX - 4 - (size_t)length) {
			goto cleanup;
		}
		total += 4 + (size_t)length;
	}
	uint8_t *packed = (uint8_t *)malloc(total);
	if (packed == NULL) {
		goto cleanup;
	}
	mePackageTrustWrite32(packed, (uint32_t)count);
	size_t cursor = 4;
	for (CFIndex index = 0; index < count; index++) {
		SecCertificateRef certificate = (SecCertificateRef)CFArrayGetValueAtIndex(evaluatedChain, index);
		CFDataRef data = SecCertificateCopyData(certificate);
		if (data == NULL) {
			free(packed);
			goto cleanup;
		}
		CFIndex length = CFDataGetLength(data);
		mePackageTrustWrite32(packed + cursor, (uint32_t)length);
		cursor += 4;
		memcpy(packed + cursor, CFDataGetBytePtr(data), (size_t)length);
		cursor += (size_t)length;
		CFRelease(data);
	}
	*output = packed;
	*outputLength = total;
	success = 1;

cleanup:
	if (evaluatedChain != NULL) CFRelease(evaluatedChain);
	if (verifyDate != NULL) CFRelease(verifyDate);
	if (trust != NULL) CFRelease(trust);
	if (policies != NULL) CFRelease(policies);
	if (revocationPolicy != NULL) CFRelease(revocationPolicy);
	if (basicPolicy != NULL) CFRelease(basicPolicy);
	if (roots != NULL) CFRelease(roots);
	if (chain != NULL) CFRelease(chain);
	return success;
}
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"unsafe"
)

type securityFrameworkPackageChainTrustEvaluator struct {
	roots appleRootSet
}

func newDarwinPackageChainTrustEvaluator(roots appleRootSet) packageChainTrustEvaluator {
	return &securityFrameworkPackageChainTrustEvaluator{roots: cloneAppleRootSet(roots)}
}

func (evaluator *securityFrameworkPackageChainTrustEvaluator) Evaluate(ctx context.Context, chain [][]byte) (evaluatedPackageChain, error) {
	if evaluator == nil || ctx == nil || ctx.Err() != nil || !validAppleRootSet(evaluator.roots) || len(chain) == 0 || len(chain) > maximumMacXARCertificates {
		return evaluatedPackageChain{}, errMacPackageTrust
	}
	packedChain, ok := packPackageTrustCertificates(chain)
	if !ok {
		return evaluatedPackageChain{}, errMacPackageTrust
	}
	packedRoots, ok := packPackageTrustCertificates(evaluator.roots.DER)
	if !ok {
		return evaluatedPackageChain{}, errMacPackageTrust
	}
	chainInput := C.CBytes(packedChain)
	defer C.free(chainInput)
	rootInput := C.CBytes(packedRoots)
	defer C.free(rootInput)
	var output *C.uint8_t
	var outputLength C.size_t
	if C.mePackageTrustEvaluate(
		(*C.uint8_t)(chainInput), C.size_t(len(packedChain)),
		(*C.uint8_t)(rootInput), C.size_t(len(packedRoots)),
		&output, &outputLength,
	) != 1 || output == nil || outputLength == 0 || uint64(outputLength) > uint64(maximumMacXARCertificates*(maximumMacXARCertificate+4)+4) {
		if output != nil {
			C.free(unsafe.Pointer(output))
		}
		return evaluatedPackageChain{}, errMacPackageTrust
	}
	defer C.free(unsafe.Pointer(output))
	packedResult := C.GoBytes(unsafe.Pointer(output), C.int(outputLength))
	resultDER, ok := unpackPackageTrustCertificates(packedResult)
	if !ok || ctx.Err() != nil {
		return evaluatedPackageChain{}, errMacPackageTrust
	}
	result := evaluatedPackageChain{ChainSHA256: make([][32]byte, len(resultDER)), RevocationProven: true}
	for index := range resultDER {
		result.ChainSHA256[index] = sha256.Sum256(resultDER[index])
	}
	return result, nil
}

func packPackageTrustCertificates(certificates [][]byte) ([]byte, bool) {
	if len(certificates) == 0 || len(certificates) > maximumMacXARCertificates {
		return nil, false
	}
	total := 4
	for _, certificate := range certificates {
		if len(certificate) == 0 || len(certificate) > maximumMacXARCertificate || total > int(^uint32(0))-4-len(certificate) {
			return nil, false
		}
		total += 4 + len(certificate)
	}
	result := make([]byte, total)
	binary.BigEndian.PutUint32(result[:4], uint32(len(certificates)))
	cursor := 4
	for _, certificate := range certificates {
		binary.BigEndian.PutUint32(result[cursor:cursor+4], uint32(len(certificate)))
		cursor += 4
		copy(result[cursor:cursor+len(certificate)], certificate)
		cursor += len(certificate)
	}
	return result, true
}

func unpackPackageTrustCertificates(value []byte) ([][]byte, bool) {
	if len(value) < 4 {
		return nil, false
	}
	count := binary.BigEndian.Uint32(value[:4])
	if count == 0 || count > maximumMacXARCertificates {
		return nil, false
	}
	result := make([][]byte, 0, count)
	cursor := 4
	for index := uint32(0); index < count; index++ {
		if cursor > len(value) || len(value)-cursor < 4 {
			return nil, false
		}
		length := int(binary.BigEndian.Uint32(value[cursor : cursor+4]))
		cursor += 4
		if length <= 0 || length > maximumMacXARCertificate || cursor > len(value) || len(value)-cursor < length {
			return nil, false
		}
		result = append(result, append([]byte(nil), value[cursor:cursor+length]...))
		cursor += length
	}
	return result, cursor == len(value)
}

func cloneAppleRootSet(roots appleRootSet) appleRootSet {
	result := appleRootSet{DER: cloneMacXARDER(roots.DER), Fingerprints: make(map[[32]byte]struct{}, len(roots.Fingerprints))}
	for fingerprint := range roots.Fingerprints {
		result.Fingerprints[fingerprint] = struct{}{}
	}
	return result
}

var _ packageChainTrustEvaluator = (*securityFrameworkPackageChainTrustEvaluator)(nil)
