package tailscale

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const red5TrustedPKGUtilOutput = `Package "Tailscale-1.100.1-macos.pkg":
   Status: signed by a certificate trusted by Mac OS X
   Certificate Chain:
    1. Developer ID Installer: Fixture only (W5364U7YZB)
`

const red5CurrentTrustedPKGUtilOutput = `Package "Tailscale-1.100.1-macos.pkg":
   Status: signed by a developer certificate issued by Apple for distribution
   Notarization: trusted by the Apple notary service
   Signed with a trusted timestamp on: 2026-05-29 19:15:36 +0000
   Certificate Chain:
    1. Developer ID Installer: Fixture only (W5364U7YZB)
`

func red5Evidence(pki red5PKI) xarSignerEvidence {
	checksum := sha1.Sum([]byte("red5 compressed XAR TOC"))
	return xarSignerEvidence{
		Kind:            xarSignatureCMS,
		SignedChecksum:  append([]byte(nil), checksum[:]...),
		Signature:       []byte("fixture CMS record already verified by the XAR parser"),
		LegacySignature: []byte("fixture RSA record already verified by the XAR parser"),
		ChainDER:        red5CloneDER(pki.chainDER),
	}
}

func red5CloneDER(chain [][]byte) [][]byte {
	result := make([][]byte, len(chain))
	for index := range chain {
		result[index] = append([]byte(nil), chain[index]...)
	}
	return result
}

func TestValidatePackageSignerRequiresExactInstallerIdentityAndOrderedChain(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	pki := red5MakePKI(t, red5PKIOptions{now: now})
	identity, err := validatePackageSignerAt(red5Evidence(pki), now)
	if err != nil {
		t.Fatalf("validatePackageSignerAt: %v", err)
	}
	if identity.TeamID != "W5364U7YZB" {
		t.Fatalf("TeamID = %q", identity.TeamID)
	}
	wantLeaf := sha256.Sum256(pki.leaf.Raw)
	if identity.LeafSHA256 != wantLeaf {
		t.Fatalf("leaf fingerprint = %x, want %x", identity.LeafSHA256, wantLeaf)
	}
	if len(identity.ChainSHA256) != 3 {
		t.Fatalf("chain fingerprints = %d, want 3", len(identity.ChainSHA256))
	}
	for index, der := range pki.chainDER {
		want := sha256.Sum256(der)
		if identity.ChainSHA256[index] != want {
			t.Fatalf("chain[%d] fingerprint = %x, want %x", index, identity.ChainSHA256[index], want)
		}
	}
}

func TestValidatePackageSignerRequiresDualXAREvidenceShape(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	pki := red5MakePKI(t, red5PKIOptions{now: now})
	tests := []struct {
		name   string
		mutate func(*xarSignerEvidence)
	}{
		{name: "legacy RSA-only kind", mutate: func(value *xarSignerEvidence) { value.Kind = xarSignatureRSA }},
		{name: "missing SHA-1 TOC digest", mutate: func(value *xarSignerEvidence) { value.SignedChecksum = nil }},
		{name: "SHA-256-sized TOC digest", mutate: func(value *xarSignerEvidence) { value.SignedChecksum = make([]byte, sha256.Size) }},
		{name: "missing extended CMS record", mutate: func(value *xarSignerEvidence) { value.Signature = nil }},
		{name: "missing ordinary RSA record", mutate: func(value *xarSignerEvidence) { value.LegacySignature = nil }},
		{name: "oversized extended CMS record", mutate: func(value *xarSignerEvidence) { value.Signature = make([]byte, maximumMacXARSignature+1) }},
		{name: "oversized ordinary RSA record", mutate: func(value *xarSignerEvidence) { value.LegacySignature = make([]byte, maximumMacXARSignature+1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := red5Evidence(pki)
			test.mutate(&evidence)
			if _, err := validatePackageSignerAt(evidence, now); err == nil {
				t.Fatal("non-dual XAR evidence shape accepted")
			}
		})
	}
}

func TestPackageSignerOIDAuthoritiesAreReturnedByValue(t *testing.T) {
	installer := packageInstallerOID()
	intermediate := packageIntermediateOID()
	installer[0] = 9
	intermediate[0] = 9
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	pki := red5MakePKI(t, red5PKIOptions{now: now})
	if _, err := validatePackageSignerAt(red5Evidence(pki), now); err != nil {
		t.Fatalf("caller mutation changed package certificate authorities: %v", err)
	}
}

func TestPackageSignerTerminalVerifierSeamSupportsPinnedLegacyRootWithoutWideningSHA1(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	pki := red5MakePKI(t, red5PKIOptions{now: now, rootSignatureAlgorithm: x509.SHA1WithRSA})
	called := 0
	identity, err := validatePackageSignerAtWithTerminalVerifier(red5Evidence(pki), now, func(certificate *x509.Certificate, fingerprint [32]byte) error {
		called++
		if certificate == nil || fingerprint != sha256.Sum256(pki.root.Raw) {
			return errors.New("wrong terminal")
		}
		return nil
	})
	if err != nil || called != 1 || identity.TeamID != "W5364U7YZB" {
		t.Fatalf("terminal verifier seam = %#v, called %d, err %v", identity, called, err)
	}
	if _, err := validatePackageSignerAt(red5Evidence(pki), now); err == nil {
		t.Fatal("unreviewed synthetic SHA-1 root passed the production pinned-root policy")
	}
}

func TestPinnedLegacyAppleRootSelfSignatureIsNarrowlyVerified(t *testing.T) {
	der, err := os.ReadFile(filepath.Join("apple_roots", "AppleIncRootCertificate.cer"))
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(der)
	if err := verifyPinnedAppleRootSelfSignature(certificate, fingerprint); err != nil {
		t.Fatalf("exact reviewed Apple legacy root rejected: %v", err)
	}
	lookalike := red5MakePKI(t, red5PKIOptions{rootSignatureAlgorithm: x509.SHA1WithRSA})
	if err := verifyPinnedAppleRootSelfSignature(lookalike.root, sha256.Sum256(lookalike.root.Raw)); err == nil {
		t.Fatal("unreviewed SHA-1 lookalike root accepted")
	}
	modernDER, err := os.ReadFile(filepath.Join("apple_roots", "AppleRootCA-G2.cer"))
	if err != nil {
		t.Fatal(err)
	}
	modern, err := x509.ParseCertificate(modernDER)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPinnedAppleRootSelfSignature(modern, sha256.Sum256([]byte("mismatched caller fingerprint"))); err == nil {
		t.Fatal("self-signature helper accepted a fingerprint that did not bind certificate.Raw")
	}
}

func TestValidatePackageSignerRejectsOUOIDTimeAndChainMutations(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	valid := red5MakePKI(t, red5PKIOptions{now: now})
	other := red5MakePKI(t, red5PKIOptions{now: now})
	applicationOID := asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 1, 13}
	appStoreOID := asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 1, 9}
	unknownOID := asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 1, 99}
	wrongIntermediateOID := asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 2, 7}

	tests := []struct {
		name     string
		evidence func(*testing.T) xarSignerEvidence
		at       time.Time
	}{
		{name: "missing OU", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{now: now, leafOU: []string{}}))
		}},
		{name: "duplicate OU", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{now: now, leafOU: []string{"W5364U7YZB", "W5364U7YZB"}}))
		}},
		{name: "wrong case OU", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{now: now, leafOU: []string{"w5364u7yzb"}}))
		}},
		{name: "wrong team", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{now: now, team: "WRONGTEAM00"}))
		}},
		{name: "missing installer OID", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{now: now, leafOIDs: []asn1.ObjectIdentifier{}}))
		}},
		{name: "duplicate installer OID", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			evidence := red5Evidence(valid)
			evidence.ChainDER[0] = red5DuplicateCertificateExtension(t, valid.leaf.Raw, red5OIDInstaller)
			return evidence
		}},
		{name: "Developer ID Application OID", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{now: now, leafOIDs: []asn1.ObjectIdentifier{applicationOID}}))
		}},
		{name: "App Store OID", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{now: now, leafOIDs: []asn1.ObjectIdentifier{appStoreOID}}))
		}},
		{name: "unknown leaf class OID", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{now: now, leafOIDs: []asn1.ObjectIdentifier{unknownOID}}))
		}},
		{name: "missing intermediate OID", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{now: now, intermediateOIDs: []asn1.ObjectIdentifier{}}))
		}},
		{name: "duplicate intermediate OID", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			evidence := red5Evidence(valid)
			evidence.ChainDER[1] = red5DuplicateCertificateExtension(t, valid.intermediate.Raw, red5OIDIntermediate)
			return evidence
		}},
		{name: "wrong intermediate OID", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{now: now, intermediateOIDs: []asn1.ObjectIdentifier{wrongIntermediateOID}}))
		}},
		{name: "reordered chain", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			evidence := red5Evidence(valid)
			evidence.ChainDER = [][]byte{valid.intermediate.Raw, valid.leaf.Raw, valid.root.Raw}
			return evidence
		}},
		{name: "wrong issuer", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			evidence := red5Evidence(valid)
			evidence.ChainDER[1] = append([]byte(nil), other.intermediate.Raw...)
			evidence.ChainDER[2] = append([]byte(nil), other.root.Raw...)
			return evidence
		}},
		{name: "invalid adjacent signature", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			evidence := red5Evidence(valid)
			evidence.ChainDER[0] = append([]byte(nil), evidence.ChainDER[0]...)
			evidence.ChainDER[0][len(evidence.ChainDER[0])-1] ^= 0x01
			return evidence
		}},
		{name: "expired signer", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{
				now: now, leafNotBefore: now.Add(-2 * time.Hour), leafNotAfter: now.Add(-time.Hour),
			}))
		}},
		{name: "not yet valid signer", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			return red5Evidence(red5MakePKI(t, red5PKIOptions{
				now: now, leafNotBefore: now.Add(time.Hour), leafNotAfter: now.Add(2 * time.Hour),
			}))
		}},
		{name: "extra certificate", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			evidence := red5Evidence(valid)
			evidence.ChainDER = append(evidence.ChainDER, other.leaf.Raw)
			return evidence
		}},
		{name: "actual wrong signer with correct-looking extra", at: now, evidence: func(t *testing.T) xarSignerEvidence {
			wrong := red5MakePKI(t, red5PKIOptions{now: now, team: "WRONGTEAM00"})
			evidence := red5Evidence(wrong)
			evidence.ChainDER = append(evidence.ChainDER, valid.leaf.Raw)
			return evidence
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validatePackageSignerAt(test.evidence(t), test.at); err == nil {
				t.Fatal("mutated package signer identity accepted")
			}
		})
	}
}

type red5FrozenRoot struct {
	filename  string
	sha256    string
	sourceURL string
	subject   string
	serial    string
	notBefore string
	notAfter  string
}

var red5FrozenRoots = []red5FrozenRoot{
	{
		filename: "AppleIncRootCertificate.cer", sha256: "b0b1730ecbc7ff4505142c49f1295e6eda6bcaed7e2c68c5be91b5a11001f024",
		sourceURL: "https://www.apple.com/appleca/AppleIncRootCertificate.cer",
		subject:   "CN=Apple Root CA,OU=Apple Certification Authority,O=Apple Inc.,C=US", serial: "02",
		notBefore: "2006-04-25T21:40:36Z", notAfter: "2035-02-09T21:40:36Z",
	},
	{
		filename: "AppleRootCA-G2.cer", sha256: "c2b9b042dd57830e7d117dac55ac8ae19407d38e41d88f3215bc3a890444a050",
		sourceURL: "https://www.apple.com/certificateauthority/AppleRootCA-G2.cer",
		subject:   "CN=Apple Root CA - G2,OU=Apple Certification Authority,O=Apple Inc.,C=US", serial: "01e0e5b58367a3e0",
		notBefore: "2014-04-30T18:10:09Z", notAfter: "2039-04-30T18:10:09Z",
	},
	{
		filename: "AppleRootCA-G3.cer", sha256: "63343abfb89a6a03ebb57e9b3f5fa7be7c4f5c756f3017b3a8c488c3653e9179",
		sourceURL: "https://www.apple.com/certificateauthority/AppleRootCA-G3.cer",
		subject:   "CN=Apple Root CA - G3,OU=Apple Certification Authority,O=Apple Inc.,C=US", serial: "2dc5fc88d2c54b95",
		notBefore: "2014-04-30T18:19:06Z", notAfter: "2039-04-30T18:19:06Z",
	},
}

func red5ReadRootMapFS(t *testing.T) fstest.MapFS {
	t.Helper()
	result := fstest.MapFS{}
	manifest, err := os.ReadFile(filepath.Join("apple_roots", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	result["apple_roots/manifest.json"] = &fstest.MapFile{Data: append([]byte(nil), manifest...), Mode: 0o600}
	for _, root := range red5FrozenRoots {
		der, err := os.ReadFile(filepath.Join("apple_roots", root.filename))
		if err != nil {
			t.Fatal(err)
		}
		result["apple_roots/"+root.filename] = &fstest.MapFile{Data: append([]byte(nil), der...), Mode: 0o600}
	}
	return result
}

func red5CopyMapFS(input fstest.MapFS) fstest.MapFS {
	result := fstest.MapFS{}
	for name, file := range input {
		copyFile := *file
		copyFile.Data = append([]byte(nil), file.Data...)
		result[name] = &copyFile
	}
	return result
}

func TestLoadEmbeddedAppleRootsMatchesFrozenManifest(t *testing.T) {
	roots, err := loadEmbeddedAppleRoots()
	if err != nil {
		t.Fatalf("loadEmbeddedAppleRoots: %v", err)
	}
	if len(roots.DER) != 3 || len(roots.Fingerprints) != 3 {
		t.Fatalf("roots = %d DER / %d fingerprints, want 3/3", len(roots.DER), len(roots.Fingerprints))
	}
	for index, frozen := range red5FrozenRoots {
		want, err := hex.DecodeString(frozen.sha256)
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(roots.DER[index])
		if !bytes.Equal(got[:], want) {
			t.Fatalf("root[%d] fingerprint = %x, want %s", index, got, frozen.sha256)
		}
		if _, ok := roots.Fingerprints[got]; !ok {
			t.Fatalf("root[%d] absent from allowlist", index)
		}
	}
	// Each call owns fresh bytes; callers cannot mutate the embedded allowlist.
	roots.DER[0][0] ^= 0xff
	again, err := loadEmbeddedAppleRoots()
	if err != nil {
		t.Fatal(err)
	}
	if roots.DER[0][0] == again.DER[0][0] {
		t.Fatal("embedded root bytes alias a previous result")
	}
}

func TestFrozenAppleRootManifestAuthorityIsReturnedByValue(t *testing.T) {
	manifest := frozenAppleRootManifestValue()
	manifest.Version = 99
	manifest.Roots[0].SHA256 = strings.Repeat("0", 64)
	manifest.Roots = append(manifest.Roots, appleRootManifestRecord{Filename: "Lookalike.cer"})
	if _, err := loadEmbeddedAppleRoots(); err != nil {
		t.Fatalf("caller mutation changed frozen Apple root authority: %v", err)
	}
	again := frozenAppleRootManifestValue()
	if again.Version != 1 || len(again.Roots) != 3 || again.Roots[0].SHA256 != red5FrozenRoots[0].sha256 {
		t.Fatalf("frozen manifest did not return a fresh literal: %#v", again)
	}
}

func TestLoadAppleRootsRejectsEveryManifestAndFileMutation(t *testing.T) {
	base := red5ReadRootMapFS(t)
	manifestPath := "apple_roots/manifest.json"
	mutateManifest := func(t *testing.T, fsys fstest.MapFS, mutate func(map[string]any)) {
		t.Helper()
		var document map[string]any
		if err := json.Unmarshal(fsys[manifestPath].Data, &document); err != nil {
			t.Fatal(err)
		}
		mutate(document)
		value, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		fsys[manifestPath].Data = value
	}
	tests := []struct {
		name   string
		mutate func(*testing.T, fstest.MapFS)
	}{
		{name: "replacement DER", mutate: func(t *testing.T, fsys fstest.MapFS) { fsys["apple_roots/AppleRootCA-G3.cer"].Data[0] ^= 0xff }},
		{name: "duplicate manifest entry", mutate: func(t *testing.T, fsys fstest.MapFS) {
			mutateManifest(t, fsys, func(document map[string]any) {
				roots := document["roots"].([]any)
				document["roots"] = append(roots, roots[0])
			})
		}},
		{name: "undeclared root file", mutate: func(t *testing.T, fsys fstest.MapFS) {
			fsys["apple_roots/Lookalike.cer"] = &fstest.MapFile{Data: []byte("lookalike")}
		}},
		{name: "missing root file", mutate: func(t *testing.T, fsys fstest.MapFS) { delete(fsys, "apple_roots/AppleRootCA-G2.cer") }},
		{name: "source mismatch", mutate: func(t *testing.T, fsys fstest.MapFS) {
			mutateManifest(t, fsys, func(document map[string]any) {
				document["roots"].([]any)[0].(map[string]any)["sourceURL"] = "https://example.invalid/root.cer"
			})
		}},
		{name: "subject mismatch", mutate: func(t *testing.T, fsys fstest.MapFS) {
			mutateManifest(t, fsys, func(document map[string]any) {
				document["roots"].([]any)[0].(map[string]any)["subject"] = "CN=Lookalike"
			})
		}},
		{name: "serial mismatch", mutate: func(t *testing.T, fsys fstest.MapFS) {
			mutateManifest(t, fsys, func(document map[string]any) { document["roots"].([]any)[0].(map[string]any)["serial"] = "03" })
		}},
		{name: "validity mismatch", mutate: func(t *testing.T, fsys fstest.MapFS) {
			mutateManifest(t, fsys, func(document map[string]any) {
				document["roots"].([]any)[0].(map[string]any)["notAfter"] = "2035-02-09T21:40:37Z"
			})
		}},
		{name: "hash mismatch", mutate: func(t *testing.T, fsys fstest.MapFS) {
			mutateManifest(t, fsys, func(document map[string]any) {
				document["roots"].([]any)[0].(map[string]any)["sha256"] = strings.Repeat("0", 64)
			})
		}},
		{name: "changed version", mutate: func(t *testing.T, fsys fstest.MapFS) {
			mutateManifest(t, fsys, func(document map[string]any) { document["version"] = float64(2) })
		}},
		{name: "unknown manifest field", mutate: func(t *testing.T, fsys fstest.MapFS) {
			mutateManifest(t, fsys, func(document map[string]any) { document["unexpected"] = true })
		}},
		{name: "manifest trailing JSON", mutate: func(t *testing.T, fsys fstest.MapFS) {
			fsys[manifestPath].Data = append(fsys[manifestPath].Data, []byte("{}")...)
		}},
		{name: "duplicate top-level key", mutate: func(t *testing.T, fsys fstest.MapFS) {
			fsys[manifestPath].Data = bytes.Replace(fsys[manifestPath].Data, []byte(`"version": 1`), []byte(`"version": 1, "version": 1`), 1)
		}},
		{name: "duplicate root field", mutate: func(t *testing.T, fsys fstest.MapFS) {
			fsys[manifestPath].Data = bytes.Replace(fsys[manifestPath].Data, []byte(`"filename": "AppleIncRootCertificate.cer"`), []byte(`"filename": "AppleIncRootCertificate.cer", "filename": "AppleIncRootCertificate.cer"`), 1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fsys := red5CopyMapFS(base)
			test.mutate(t, fsys)
			if _, err := loadAppleRootsFromFS(fsys); err == nil {
				t.Fatal("mutated Apple root allowlist accepted")
			}
		})
	}
}

func red5SyntheticRootSet(pki red5PKI) appleRootSet {
	fingerprint := sha256.Sum256(pki.root.Raw)
	return appleRootSet{
		DER:          [][]byte{append([]byte(nil), pki.root.Raw...)},
		Fingerprints: map[[32]byte]struct{}{fingerprint: {}},
	}
}

func TestValidateEvaluatedPackageChainRequiresExactPrefixAnchorAndRevocation(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	pki := red5MakePKI(t, red5PKIOptions{now: now})
	identity, err := validatePackageSignerAt(red5Evidence(pki), now)
	if err != nil {
		t.Fatal(err)
	}
	roots := red5SyntheticRootSet(pki)
	valid := evaluatedPackageChain{ChainSHA256: append([][32]byte(nil), identity.ChainSHA256...), RevocationProven: true}
	if err := validateEvaluatedPackageChain(identity, roots, valid); err != nil {
		t.Fatalf("valid chain: %v", err)
	}
	lookalike := sha256.Sum256([]byte("locally trusted lookalike"))
	tests := []struct {
		name   string
		mutate func(*evaluatedPackageChain)
	}{
		{name: "locally trusted lookalike root", mutate: func(result *evaluatedPackageChain) { result.ChainSHA256[len(result.ChainSHA256)-1] = lookalike }},
		{name: "substituted leaf", mutate: func(result *evaluatedPackageChain) { result.ChainSHA256[0] = lookalike }},
		{name: "reordered chain", mutate: func(result *evaluatedPackageChain) {
			result.ChainSHA256[0], result.ChainSHA256[1] = result.ChainSHA256[1], result.ChainSHA256[0]
		}},
		{name: "inserted certificate", mutate: func(result *evaluatedPackageChain) {
			result.ChainSHA256 = append(result.ChainSHA256[:1], append([][32]byte{lookalike}, result.ChainSHA256[1:]...)...)
		}},
		{name: "missing root", mutate: func(result *evaluatedPackageChain) {
			result.ChainSHA256 = result.ChainSHA256[:len(result.ChainSHA256)-1]
		}},
		{name: "extra terminal", mutate: func(result *evaluatedPackageChain) { result.ChainSHA256 = append(result.ChainSHA256, lookalike) }},
		{name: "no positive revocation proof", mutate: func(result *evaluatedPackageChain) { result.RevocationProven = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := evaluatedPackageChain{ChainSHA256: append([][32]byte(nil), valid.ChainSHA256...), RevocationProven: valid.RevocationProven}
			test.mutate(&result)
			if err := validateEvaluatedPackageChain(identity, roots, result); err == nil {
				t.Fatal("mutated native trust result accepted")
			}
		})
	}
}

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

type red5PackageTrustEvaluator struct {
	events *red5PackageTrustEventLog
	result evaluatedPackageChain
	err    error
	chain  [][]byte
}

func (evaluator *red5PackageTrustEvaluator) Evaluate(_ context.Context, chain [][]byte) (evaluatedPackageChain, error) {
	evaluator.events.add("evaluate")
	evaluator.chain = red5CloneDER(chain)
	return evaluator.result, evaluator.err
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

func red5ValidTrustParts(t *testing.T) (red5XARFixture, red5PKI, packageSignerIdentity, appleRootSet, *red5PackageTrustEvaluator, *red5PackageTrustRunner, *red5PackageTrustGuard) {
	t.Helper()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	pki := red5MakePKI(t, red5PKIOptions{now: now})
	fixture := red5BuildXAR(t, pki, func(spec *red5XARSpec) {
		spec.style = "CMS"
		spec.cms.signedAttributes = true
	})
	identity, err := validatePackageSignerAt(red5Evidence(pki), now)
	if err != nil {
		t.Fatal(err)
	}
	sharedEvents := &red5PackageTrustEventLog{}
	evaluator := &red5PackageTrustEvaluator{
		events: sharedEvents,
		result: evaluatedPackageChain{ChainSHA256: append([][32]byte(nil), identity.ChainSHA256...), RevocationProven: true},
	}
	runner := &red5PackageTrustRunner{
		events:  sharedEvents,
		outputs: map[string][]byte{"/usr/sbin/pkgutil": []byte(red5TrustedPKGUtilOutput), "/usr/sbin/spctl": nil},
		errors:  map[string]error{},
	}
	guard := &red5PackageTrustGuard{path: "/private/stage/Tailscale-1.100.1-macos.pkg", current: "admitted", events: sharedEvents}
	return fixture, pki, identity, red5SyntheticRootSet(pki), evaluator, runner, guard
}

func TestVerifyMacPackageTrustComposesDescriptorSignerNativeTrustAndToolsInOrder(t *testing.T) {
	fixture, pki, _, roots, evaluator, runner, guard := red5ValidTrustParts(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	// Share one observable event slice without asserting on the doubles themselves:
	// the ordered trace is the product boundary that prevents a later phase from
	// running before internal signer trust and complete path validation.
	events := &red5PackageTrustEventLog{}
	evaluator.events = events
	runner.events = events
	guard.events = events
	if err := verifyMacPackageTrust(
		context.Background(), bytes.NewReader(fixture.archive), int64(len(fixture.archive)), guard,
		evaluator, roots, runner, now,
	); err != nil {
		t.Fatalf("verifyMacPackageTrust: %v", err)
	}
	if len(evaluator.chain) != len(pki.chainDER) {
		t.Fatalf("native evaluator chain length = %d, want %d", len(evaluator.chain), len(pki.chainDER))
	}
	for index := range pki.chainDER {
		if !bytes.Equal(evaluator.chain[index], pki.chainDER[index]) {
			t.Fatalf("native evaluator chain[%d] differs from XAR signer chain", index)
		}
	}
	if guard.revalidations != 5 {
		t.Fatalf("revalidations = %d, want 5 (initial plus pre/post for two pathname tools)", guard.revalidations)
	}
	wantEvents := []string{"revalidate", "evaluate", "revalidate", "pkgutil", "revalidate", "revalidate", "spctl", "revalidate"}
	if fmt.Sprint(events.values) != fmt.Sprint(wantEvents) {
		t.Fatalf("trust phase order = %v, want %v", events.values, wantEvents)
	}
	if len(runner.invocations) != 2 {
		t.Fatalf("tool invocations = %d, want 2", len(runner.invocations))
	}
	want := []packageTrustCommandInvocation{
		{
			Path: "/usr/sbin/pkgutil", Arguments: []string{"--check-signature", guard.path},
			Environment: []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, OutputLimit: 4 << 20,
		},
		{
			Path: "/usr/sbin/spctl", Arguments: []string{"--assess", "--type", "install", guard.path},
			Environment: []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, OutputLimit: 4 << 20,
		},
	}
	for index := range want {
		got := runner.invocations[index]
		if got.Path != want[index].Path || fmt.Sprint(got.Arguments) != fmt.Sprint(want[index].Arguments) ||
			fmt.Sprint(got.Environment) != fmt.Sprint(want[index].Environment) || got.OutputLimit != want[index].OutputLimit {
			t.Fatalf("invocation[%d] = %#v, want %#v", index, got, want[index])
		}
	}
}

func TestVerifyMacPackageTrustStopsAtEveryFailureAndRedactsDiagnostics(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*red5XARFixture, *red5PackageTrustEvaluator, *red5PackageTrustRunner, *red5PackageTrustGuard, *appleRootSet)
		wantCommands int
	}{
		{name: "initial guard", wantCommands: 0, mutate: func(_ *red5XARFixture, _ *red5PackageTrustEvaluator, _ *red5PackageTrustRunner, guard *red5PackageTrustGuard, _ *appleRootSet) {
			guard.failAt = 1
		}},
		{name: "XAR signer", wantCommands: 0, mutate: func(fixture *red5XARFixture, _ *red5PackageTrustEvaluator, _ *red5PackageTrustRunner, _ *red5PackageTrustGuard, _ *appleRootSet) {
			fixture.archive[fixture.signatureStart] ^= 0xff
		}},
		{name: "native error including offline revocation", wantCommands: 0, mutate: func(_ *red5XARFixture, evaluator *red5PackageTrustEvaluator, _ *red5PackageTrustRunner, _ *red5PackageTrustGuard, _ *appleRootSet) {
			evaluator.err = errors.New("offline no positive response for /private/fixture")
		}},
		{name: "indeterminate native chain", wantCommands: 0, mutate: func(_ *red5XARFixture, evaluator *red5PackageTrustEvaluator, _ *red5PackageTrustRunner, _ *red5PackageTrustGuard, _ *appleRootSet) {
			evaluator.result.RevocationProven = false
		}},
		{name: "locally trusted lookalike root", wantCommands: 0, mutate: func(_ *red5XARFixture, evaluator *red5PackageTrustEvaluator, _ *red5PackageTrustRunner, _ *red5PackageTrustGuard, _ *appleRootSet) {
			evaluator.result.ChainSHA256[len(evaluator.result.ChainSHA256)-1] = sha256.Sum256([]byte("lookalike"))
		}},
		{name: "pre pkgutil guard", wantCommands: 0, mutate: func(_ *red5XARFixture, _ *red5PackageTrustEvaluator, _ *red5PackageTrustRunner, guard *red5PackageTrustGuard, _ *appleRootSet) {
			guard.failAt = 2
		}},
		{name: "pkgutil exit", wantCommands: 1, mutate: func(_ *red5XARFixture, _ *red5PackageTrustEvaluator, runner *red5PackageTrustRunner, _ *red5PackageTrustGuard, _ *appleRootSet) {
			runner.errors["/usr/sbin/pkgutil"] = errors.New("hostile output /private/fixture")
		}},
		{name: "post pkgutil guard", wantCommands: 1, mutate: func(_ *red5XARFixture, _ *red5PackageTrustEvaluator, _ *red5PackageTrustRunner, guard *red5PackageTrustGuard, _ *appleRootSet) {
			guard.failAt = 3
		}},
		{name: "pkgutil shape", wantCommands: 1, mutate: func(_ *red5XARFixture, _ *red5PackageTrustEvaluator, runner *red5PackageTrustRunner, _ *red5PackageTrustGuard, _ *appleRootSet) {
			runner.outputs["/usr/sbin/pkgutil"] = []byte("Status: trusted lookalike")
		}},
		{name: "pkgutil aggregate overflow", wantCommands: 1, mutate: func(_ *red5XARFixture, _ *red5PackageTrustEvaluator, runner *red5PackageTrustRunner, _ *red5PackageTrustGuard, _ *appleRootSet) {
			runner.outputs["/usr/sbin/pkgutil"] = bytes.Repeat([]byte{'x'}, (4<<20)+1)
		}},
		{name: "pre spctl guard", wantCommands: 1, mutate: func(_ *red5XARFixture, _ *red5PackageTrustEvaluator, _ *red5PackageTrustRunner, guard *red5PackageTrustGuard, _ *appleRootSet) {
			guard.failAt = 4
		}},
		{name: "spctl exit", wantCommands: 2, mutate: func(_ *red5XARFixture, _ *red5PackageTrustEvaluator, runner *red5PackageTrustRunner, _ *red5PackageTrustGuard, _ *appleRootSet) {
			runner.errors["/usr/sbin/spctl"] = errors.New("rejected /private/fixture because revoked")
		}},
		{name: "spctl aggregate overflow", wantCommands: 2, mutate: func(_ *red5XARFixture, _ *red5PackageTrustEvaluator, runner *red5PackageTrustRunner, _ *red5PackageTrustGuard, _ *appleRootSet) {
			runner.outputs["/usr/sbin/spctl"] = bytes.Repeat([]byte{'x'}, (4<<20)+1)
		}},
		{name: "post spctl guard", wantCommands: 2, mutate: func(_ *red5XARFixture, _ *red5PackageTrustEvaluator, _ *red5PackageTrustRunner, guard *red5PackageTrustGuard, _ *appleRootSet) {
			guard.failAt = 5
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, _, _, roots, evaluator, runner, guard := red5ValidTrustParts(t)
			test.mutate(&fixture, evaluator, runner, guard, &roots)
			err := verifyMacPackageTrust(
				context.Background(), bytes.NewReader(fixture.archive), int64(len(fixture.archive)), guard,
				evaluator, roots, runner, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
			)
			if !errors.Is(err, errMacPackageTrust) {
				t.Fatalf("error = %v, want fixed package trust error", err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "offline") || strings.Contains(err.Error(), "revoked") || strings.Contains(err.Error(), "hostile") {
				t.Fatalf("raw diagnostic leaked: %q", err)
			}
			if len(runner.invocations) != test.wantCommands {
				t.Fatalf("commands = %d, want %d", len(runner.invocations), test.wantCommands)
			}
		})
	}
}

func TestVerifyMacPackageTrustRejectsPersistentSwapButCharacterizesIntraCommandABA(t *testing.T) {
	t.Run("boundary-persistent replacement stops before spctl", func(t *testing.T) {
		fixture, _, _, roots, evaluator, runner, guard := red5ValidTrustParts(t)
		runner.hook = func(invocation packageTrustCommandInvocation) {
			if invocation.Path == "/usr/sbin/pkgutil" {
				guard.current = "replacement"
			}
		}
		err := verifyMacPackageTrust(
			context.Background(), bytes.NewReader(fixture.archive), int64(len(fixture.archive)), guard,
			evaluator, roots, runner, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		)
		if !errors.Is(err, errMacPackageTrust) {
			t.Fatalf("error = %v", err)
		}
		if len(runner.invocations) != 1 {
			t.Fatalf("commands = %d, want only pkgutil", len(runner.invocations))
		}
	})

	t.Run("intra-command ABA remains an explicit residual", func(t *testing.T) {
		fixture, _, _, roots, evaluator, runner, guard := red5ValidTrustParts(t)
		consumerObservedReplacement := false
		runner.hook = func(invocation packageTrustCommandInvocation) {
			if invocation.Path == "/usr/sbin/pkgutil" {
				guard.current = "replacement"
				consumerObservedReplacement = guard.current == "replacement"
				guard.current = "admitted"
			}
		}
		if err := verifyMacPackageTrust(
			context.Background(), bytes.NewReader(fixture.archive), int64(len(fixture.archive)), guard,
			evaluator, roots, runner, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		); err != nil {
			t.Fatalf("boundary guards should not be misrepresented as binding an intra-command consumer: %v", err)
		}
		if !consumerObservedReplacement {
			t.Fatal("ABA fixture did not demonstrate the documented pathname-consumer residual")
		}
	})
}

func TestVerifyMacPackageTrustEnvironmentCannotBeMutatedAcrossPhases(t *testing.T) {
	fixture, _, _, roots, evaluator, runner, guard := red5ValidTrustParts(t)
	runner.hook = func(invocation packageTrustCommandInvocation) {
		if invocation.Path == packageTrustPKGUtilPath {
			invocation.Environment[0] = "DYLD_LIBRARY_PATH=/tmp/lookalike"
			invocation.Environment = append(invocation.Environment, "HOME=/Users/attacker")
		}
	}
	if err := verifyMacPackageTrust(
		context.Background(), bytes.NewReader(fixture.archive), int64(len(fixture.archive)), guard,
		evaluator, roots, runner, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	if len(runner.invocations) != 2 || fmt.Sprint(runner.invocations[1].Environment) != fmt.Sprint([]string{
		"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin",
	}) {
		t.Fatalf("later trust environment was mutable: %#v", runner.invocations)
	}
	first := newPackageTrustEnvironment()
	first[0] = "hostile"
	if got := newPackageTrustEnvironment()[0]; got != "LC_ALL=C" {
		t.Fatalf("canonical trust environment mutated to %q", got)
	}
}

func TestVerifyStagedMacPKGReadsRetainedDescriptorWithoutReopeningPath(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	pki := red5MakePKI(t, red5PKIOptions{now: now})
	fixture := red5BuildXAR(t, pki, nil)
	digest := sha256.Sum256(fixture.archive)
	release := MacRelease{
		Version: "1.100.1", PKGURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg",
		ChecksumURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg.sha256",
	}
	operations := newModelStageOperations(t.TempDir())
	client := packageBodyClient(release.PKGURL, int64(len(fixture.archive)), bytes.NewReader(fixture.archive))
	stage, err := stageMacPKGWithOperations(
		context.Background(), client, release, hex.EncodeToString(digest[:]), operations,
		macStageDirectoryPrefix+"abcdefabcdefabcdefabcdefabcdefab",
	)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := validatePackageSignerAt(red5Evidence(pki), now)
	if err != nil {
		t.Fatal(err)
	}
	evaluator := &red5PackageTrustEvaluator{result: evaluatedPackageChain{ChainSHA256: identity.ChainSHA256, RevocationProven: true}}
	runner := &red5PackageTrustRunner{
		outputs: map[string][]byte{"/usr/sbin/pkgutil": []byte(red5TrustedPKGUtilOutput), "/usr/sbin/spctl": nil},
		errors:  map[string]error{},
	}
	// The model has no open-by-path API. Success therefore proves this wrapper
	// handed the already retained ReaderAt to the parser and used the path only
	// for the two explicitly residual external consumers.
	if err := verifyStagedMacPKG(context.Background(), stage, evaluator, red5SyntheticRootSet(pki), runner, now); err != nil {
		t.Fatalf("verifyStagedMacPKG: %v", err)
	}
	if len(runner.invocations) != 2 {
		t.Fatalf("external pathname consumers = %d, want pkgutil and spctl", len(runner.invocations))
	}
}

func TestStagedMacPKGTrustDependencyCompositionUsesOneRootSetAndFreshTime(t *testing.T) {
	rootDER := []byte{0x30, 0x01, 0x00}
	rootFingerprint := sha256.Sum256(rootDER)
	roots := appleRootSet{
		DER:          [][]byte{rootDER},
		Fingerprints: map[[32]byte]struct{}{rootFingerprint: {}},
	}
	stage := &stagedMacPKG{}
	evaluator := &red5PackageTrustEvaluator{}
	runner := &red5PackageTrustRunner{outputs: map[string][]byte{}, errors: map[string]error{}}
	wantTime := time.Date(2026, 9, 1, 12, 34, 56, 0, time.UTC)
	events := make([]string, 0, 4)
	loaded := 0

	dependencies := stagedMacPKGTrustDependencies{
		loadRoots: func() (appleRootSet, error) {
			loaded++
			events = append(events, "roots")
			return roots, nil
		},
		newEvaluator: func(value appleRootSet) packageChainTrustEvaluator {
			events = append(events, "evaluator")
			if len(value.DER) != 1 || &value.DER[0][0] != &roots.DER[0][0] {
				t.Fatal("evaluator was not constructed from the loaded root set")
			}
			return evaluator
		},
		runner: runner,
		now: func() time.Time {
			events = append(events, "time")
			return wantTime
		},
		verify: func(
			gotContext context.Context,
			gotStage *stagedMacPKG,
			gotEvaluator packageChainTrustEvaluator,
			gotRoots appleRootSet,
			gotRunner packageTrustCommandRunner,
			gotTime time.Time,
		) error {
			events = append(events, "verify")
			if gotContext == nil || gotStage != stage || gotEvaluator != evaluator || gotRunner != runner || !gotTime.Equal(wantTime) {
				t.Fatalf("composed verifier received mismatched dependencies")
			}
			if len(gotRoots.DER) != 1 || &gotRoots.DER[0][0] != &roots.DER[0][0] {
				t.Fatal("verification did not receive the same loaded root set as the evaluator factory")
			}
			return nil
		},
	}
	if err := verifyStagedMacPKGWithDependencies(context.Background(), stage, dependencies); err != nil {
		t.Fatalf("verifyStagedMacPKGWithDependencies: %v", err)
	}
	if loaded != 1 {
		t.Fatalf("embedded roots loaded %d times, want once", loaded)
	}
	if got, want := strings.Join(events, ","), "roots,evaluator,time,verify"; got != want {
		t.Fatalf("composition order = %q, want %q", got, want)
	}
}

func TestPackageTrustInterfacesRejectNilAndCancelledInputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fixture, _, _, roots, evaluator, runner, guard := red5ValidTrustParts(t)
	tests := []struct {
		name      string
		ctx       context.Context
		reader    io.ReaderAt
		size      int64
		guard     stagedPathGuard
		evaluator packageChainTrustEvaluator
		roots     appleRootSet
		runner    packageTrustCommandRunner
	}{
		{name: "cancelled", ctx: ctx, reader: bytes.NewReader(fixture.archive), size: int64(len(fixture.archive)), guard: guard, evaluator: evaluator, roots: roots, runner: runner},
		{name: "nil reader", ctx: context.Background(), size: int64(len(fixture.archive)), guard: guard, evaluator: evaluator, roots: roots, runner: runner},
		{name: "zero size", ctx: context.Background(), reader: bytes.NewReader(fixture.archive), guard: guard, evaluator: evaluator, roots: roots, runner: runner},
		{name: "nil guard", ctx: context.Background(), reader: bytes.NewReader(fixture.archive), size: int64(len(fixture.archive)), evaluator: evaluator, roots: roots, runner: runner},
		{name: "nil evaluator", ctx: context.Background(), reader: bytes.NewReader(fixture.archive), size: int64(len(fixture.archive)), guard: guard, roots: roots, runner: runner},
		{name: "empty roots", ctx: context.Background(), reader: bytes.NewReader(fixture.archive), size: int64(len(fixture.archive)), guard: guard, evaluator: evaluator, runner: runner},
		{name: "nil runner", ctx: context.Background(), reader: bytes.NewReader(fixture.archive), size: int64(len(fixture.archive)), guard: guard, evaluator: evaluator, roots: roots},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := verifyMacPackageTrust(test.ctx, test.reader, test.size, test.guard, test.evaluator, test.roots, test.runner, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)); !errors.Is(err, errMacPackageTrust) {
				t.Fatalf("error = %v, want fixed package trust error", err)
			}
		})
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

func TestRootManifestFSRejectsNonRegularEntries(t *testing.T) {
	base := red5ReadRootMapFS(t)
	for _, mode := range []fs.FileMode{fs.ModeSymlink, fs.ModeDir} {
		fsys := red5CopyMapFS(base)
		fsys["apple_roots/AppleRootCA-G3.cer"].Mode = mode
		if _, err := loadAppleRootsFromFS(fsys); err == nil {
			t.Fatalf("mode %v accepted", mode)
		}
	}
}

type red5CloseErrorFS struct {
	fs.FS
	target string
}

func (files red5CloseErrorFS) Open(name string) (fs.File, error) {
	file, err := files.FS.Open(name)
	if err != nil || name != files.target {
		return file, err
	}
	return red5CloseErrorFile{File: file}, nil
}

type red5CloseErrorFile struct{ fs.File }

func (file red5CloseErrorFile) Close() error {
	_ = file.File.Close()
	return errors.New("fixture close uncertainty")
}

func TestLoadAppleRootsRejectsDescriptorCloseUncertainty(t *testing.T) {
	base := red5ReadRootMapFS(t)
	for _, target := range []string{"apple_roots/manifest.json", "apple_roots/AppleRootCA-G2.cer"} {
		if _, err := loadAppleRootsFromFS(red5CloseErrorFS{FS: base, target: target}); err == nil {
			t.Fatalf("close failure for %s accepted", target)
		}
	}
}

// Compile-time checks keep the fake seams honest without adding exported APIs.
var _ packageChainTrustEvaluator = (*red5PackageTrustEvaluator)(nil)
var _ packageTrustCommandRunner = (*red5PackageTrustRunner)(nil)
var _ stagedPathGuard = (*red5PackageTrustGuard)(nil)
