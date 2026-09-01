package tailscale

import (
	"bytes"
	"compress/zlib"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"testing"
	"time"
)

var (
	red5OIDCMSData       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	red5OIDCMSSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	red5OIDSHA1          = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	red5OIDSHA256        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	red5OIDSHA1RSA       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	red5OIDSHA256RSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	red5OIDRSAEncryption = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	red5OIDContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	red5OIDMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	red5OIDTimestamp     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14}
	red5OIDTSTInfo       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
	red5OIDSigningCertV2 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 47}
	red5OIDExtendedUsage = asn1.ObjectIdentifier{2, 5, 29, 37}
	red5OIDTimeStamping  = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 8}
	red5OIDCodeSigning   = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 3}
	red5OIDInstaller     = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 1, 14}
	red5OIDIntermediate  = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 2, 6}
)

type red5PKIOptions struct {
	team                   string
	leafOIDs               []asn1.ObjectIdentifier
	intermediateOIDs       []asn1.ObjectIdentifier
	now                    time.Time
	leafNotBefore          time.Time
	leafNotAfter           time.Time
	leafOU                 []string
	leafCommonName         string
	leafExtraExtensions    []pkix.Extension
	leafIssuerOverride     pkix.Name
	rootSignatureAlgorithm x509.SignatureAlgorithm
}

type red5PKI struct {
	leaf         *x509.Certificate
	intermediate *x509.Certificate
	root         *x509.Certificate
	leafKey      *rsa.PrivateKey
	chainDER     [][]byte
}

func red5MakePKI(t *testing.T, options red5PKIOptions) red5PKI {
	t.Helper()
	now := options.now
	if now.IsZero() {
		now = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	}
	team := options.team
	if team == "" {
		team = "W5364U7YZB"
	}
	leafOU := options.leafOU
	if leafOU == nil {
		leafOU = []string{team}
	}
	leafOIDs := options.leafOIDs
	if leafOIDs == nil {
		leafOIDs = []asn1.ObjectIdentifier{red5OIDInstaller}
	}
	intermediateOIDs := options.intermediateOIDs
	if intermediateOIDs == nil {
		intermediateOIDs = []asn1.ObjectIdentifier{red5OIDIntermediate}
	}
	leafNotBefore := options.leafNotBefore
	if leafNotBefore.IsZero() {
		leafNotBefore = now.Add(-time.Hour)
	}
	leafNotAfter := options.leafNotAfter
	if leafNotAfter.IsZero() {
		leafNotAfter = now.Add(time.Hour)
	}

	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	intermediateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "RED5 Apple fixture root"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		SubjectKeyId:          []byte{0x01, 0x02, 0x03, 0x04},
		SignatureAlgorithm:    options.rootSignatureAlgorithm,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}

	intermediateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(101),
		Subject:               pkix.Name{CommonName: "RED5 Developer ID fixture intermediate"},
		NotBefore:             now.Add(-12 * time.Hour),
		NotAfter:              now.Add(12 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		SubjectKeyId:          []byte{0x05, 0x06, 0x07, 0x08},
		AuthorityKeyId:        root.SubjectKeyId,
	}
	for _, oid := range intermediateOIDs {
		intermediateTemplate.ExtraExtensions = append(intermediateTemplate.ExtraExtensions, pkix.Extension{Id: oid, Value: []byte{0x05, 0x00}})
	}
	intermediateDER, err := x509.CreateCertificate(rand.Reader, intermediateTemplate, root, &intermediateKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	intermediate, err := x509.ParseCertificate(intermediateDER)
	if err != nil {
		t.Fatal(err)
	}

	issuer := intermediate.Subject
	if len(options.leafIssuerOverride.Names) != 0 || options.leafIssuerOverride.CommonName != "" {
		issuer = options.leafIssuerOverride
	}
	leafCommonName := options.leafCommonName
	if leafCommonName == "" {
		leafCommonName = "RED5 package signer"
	}
	leafTemplate := &x509.Certificate{
		SerialNumber:   big.NewInt(102),
		Subject:        pkix.Name{CommonName: leafCommonName, OrganizationalUnit: leafOU},
		Issuer:         issuer,
		NotBefore:      leafNotBefore,
		NotAfter:       leafNotAfter,
		KeyUsage:       x509.KeyUsageDigitalSignature,
		SubjectKeyId:   []byte{0x09, 0x0a, 0x0b, 0x0c},
		AuthorityKeyId: intermediate.SubjectKeyId,
	}
	for _, oid := range leafOIDs {
		leafTemplate.ExtraExtensions = append(leafTemplate.ExtraExtensions, pkix.Extension{Id: oid, Value: []byte{0x05, 0x00}})
	}
	leafTemplate.ExtraExtensions = append(leafTemplate.ExtraExtensions, options.leafExtraExtensions...)
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, intermediate, &leafKey.PublicKey, intermediateKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	return red5PKI{
		leaf:         leaf,
		intermediate: intermediate,
		root:         root,
		leafKey:      leafKey,
		chainDER:     [][]byte{leafDER, intermediateDER, rootDER},
	}
}

type red5TimestampPKIOptions struct {
	missingEKU     bool
	noncriticalEKU bool
	additionalEKU  bool
}

func red5MakeTimestampPKI(t *testing.T, options red5TimestampPKIOptions) red5PKI {
	t.Helper()
	extraExtensions := make([]pkix.Extension, 0, 1)
	if !options.missingEKU {
		usages := []asn1.ObjectIdentifier{red5OIDTimeStamping}
		if options.additionalEKU {
			usages = append(usages, red5OIDCodeSigning)
		}
		value, err := asn1.Marshal(usages)
		if err != nil {
			t.Fatal(err)
		}
		extraExtensions = append(extraExtensions, pkix.Extension{
			Id:       red5OIDExtendedUsage,
			Critical: !options.noncriticalEKU,
			Value:    value,
		})
	}
	return red5MakePKI(t, red5PKIOptions{
		leafOIDs:            []asn1.ObjectIdentifier{},
		intermediateOIDs:    []asn1.ObjectIdentifier{},
		leafOU:              []string{},
		leafCommonName:      "RED5 RFC3161 TSA signer",
		leafExtraExtensions: extraExtensions,
	})
}

type red5CMSAlgorithm struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type red5CMSIssuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type red5CMSAttribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue
}

type red5CMSOptions struct {
	signerCount                         int
	useSKI                              bool
	wrongSID                            bool
	signedAttributes                    bool
	signerVersion                       int
	signedDataVersion                   int
	digestOID                           asn1.ObjectIdentifier
	signatureOID                        asn1.ObjectIdentifier
	signedDataDigestNullParameters      bool
	signerDigestNullParameters          bool
	signatureNullParameters             bool
	signedDataDigestInvalidParameters   bool
	signerDigestInvalidParameters       bool
	signatureInvalidParameters          bool
	certificates                        [][]byte
	signatureSuffix                     []byte
	wrongMessageDigest                  bool
	omitContentType                     bool
	duplicateDigest                     bool
	embeddedContent                     bool
	wrongEmbeddedContent                bool
	reverseCertificateSet               bool
	unsignedTimestamp                   bool
	unknownUnsigned                     bool
	duplicateTimestamp                  bool
	malformedTimestamp                  bool
	wrongTimestampImprint               bool
	missingTimestampESS                 bool
	duplicateTSASigner                  bool
	wrongTimestampESS                   bool
	wrongTimestampDigest                bool
	wrongTSASignature                   bool
	timestampSignatureOID               asn1.ObjectIdentifier
	timestampSignatureNullParameters    bool
	timestampSignatureInvalidParameters bool
	timestampESSIssuerSerial            bool
	timestampPKI                        *red5PKI
	timestampOptionalFields             [][]byte
}

func red5MarshalCMS(t *testing.T, checksum []byte, pki red5PKI, options red5CMSOptions) []byte {
	t.Helper()
	signerCount := options.signerCount
	if signerCount == 0 {
		signerCount = 1
	} else if signerCount < 0 {
		signerCount = 0
	}
	digestOID := options.digestOID
	if digestOID == nil {
		digestOID = red5OIDSHA1
	}
	signatureOID := options.signatureOID
	if signatureOID == nil {
		signatureOID = red5OIDSHA1RSA
	}
	signedDataDigestAlgorithm := red5CMSAlgorithm{Algorithm: digestOID}
	if options.signedDataDigestNullParameters {
		signedDataDigestAlgorithm.Parameters = asn1.RawValue{Class: 0, Tag: asn1.TagNull}
	} else if options.signedDataDigestInvalidParameters {
		signedDataDigestAlgorithm.Parameters = asn1.RawValue{FullBytes: red5MustASN1(t, 1)}
	}
	signerDigestAlgorithm := red5CMSAlgorithm{Algorithm: digestOID}
	if options.signerDigestNullParameters {
		signerDigestAlgorithm.Parameters = asn1.RawValue{Class: 0, Tag: asn1.TagNull}
	} else if options.signerDigestInvalidParameters {
		signerDigestAlgorithm.Parameters = asn1.RawValue{FullBytes: red5MustASN1(t, 1)}
	}
	signatureAlgorithmValue := red5CMSAlgorithm{Algorithm: signatureOID}
	if options.signatureNullParameters {
		signatureAlgorithmValue.Parameters = asn1.RawValue{Class: 0, Tag: asn1.TagNull}
	} else if options.signatureInvalidParameters {
		signatureAlgorithmValue.Parameters = asn1.RawValue{FullBytes: red5MustASN1(t, 1)}
	}
	certificates := options.certificates
	if certificates == nil {
		certificates = pki.chainDER
	}

	signers := make([][]byte, 0, signerCount)
	for index := 0; index < signerCount; index++ {
		var sid asn1.RawValue
		if options.useSKI {
			ski := append([]byte(nil), pki.leaf.SubjectKeyId...)
			if options.wrongSID {
				ski = []byte{0xff, 0xee, 0xdd}
			}
			sid = asn1.RawValue{Class: 2, Tag: 0, Bytes: ski}
		} else {
			serial := new(big.Int).Set(pki.leaf.SerialNumber)
			if options.wrongSID {
				serial.Add(serial, big.NewInt(1))
			}
			sidDER, err := asn1.Marshal(red5CMSIssuerAndSerial{
				Issuer: asn1.RawValue{FullBytes: append([]byte(nil), pki.leaf.RawIssuer...)},
				Serial: serial,
			})
			if err != nil {
				t.Fatal(err)
			}
			sid = asn1.RawValue{FullBytes: sidDER}
		}

		signedInput := checksum
		var signedAttributes asn1.RawValue
		if options.signedAttributes {
			digest := red5CMSDigest(digestOID, checksum)
			if options.wrongMessageDigest {
				digest[0] ^= 0xff
			}
			contentTypeValue := red5DERWrap(0x31, red5MustASN1(t, red5OIDCMSData))
			digestValue := red5DERWrap(0x31, red5MustASN1(t, digest[:]))
			var err error
			attributes := make([][]byte, 0, 3)
			if !options.omitContentType {
				contentTypeAttribute, err := asn1.Marshal(red5CMSAttribute{Type: red5OIDContentType, Values: asn1.RawValue{FullBytes: contentTypeValue}})
				if err != nil {
					t.Fatal(err)
				}
				attributes = append(attributes, contentTypeAttribute)
			}
			digestAttribute, err := asn1.Marshal(red5CMSAttribute{Type: red5OIDMessageDigest, Values: asn1.RawValue{FullBytes: digestValue}})
			if err != nil {
				t.Fatal(err)
			}
			attributes = append(attributes, digestAttribute)
			if options.duplicateDigest {
				attributes = append(attributes, append([]byte(nil), digestAttribute...))
			}
			sort.Slice(attributes, func(i, j int) bool { return bytes.Compare(attributes[i], attributes[j]) < 0 })
			attributeBytes := bytes.Join(attributes, nil)
			signedAttributes = asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: attributeBytes}
			signedInput = red5DERWrap(0x31, attributeBytes)
		}
		hashID, digest := red5CMSHash(digestOID, signedInput)
		signature, err := rsa.SignPKCS1v15(rand.Reader, pki.leafKey, hashID, digest)
		if err != nil {
			t.Fatal(err)
		}
		signature = append(signature, options.signatureSuffix...)
		version := 1
		if options.useSKI {
			version = 3
		}
		if options.signerVersion != 0 {
			version = options.signerVersion
		}
		signerParts := [][]byte{
			red5MustASN1(t, version),
			red5MustASN1(t, sid),
			red5MustASN1(t, signerDigestAlgorithm),
		}
		if len(signedAttributes.FullBytes) != 0 || len(signedAttributes.Bytes) != 0 {
			signerParts = append(signerParts, red5MustASN1(t, signedAttributes))
		}
		signerParts = append(signerParts,
			red5MustASN1(t, signatureAlgorithmValue),
			red5MustASN1(t, signature),
		)
		if options.unsignedTimestamp || options.unknownUnsigned || options.duplicateTimestamp || options.malformedTimestamp ||
			options.wrongTimestampImprint || options.missingTimestampESS || options.duplicateTSASigner ||
			options.wrongTimestampESS || options.wrongTimestampDigest || options.wrongTSASignature {
			attributeOID := red5OIDTimestamp
			if options.unknownUnsigned {
				attributeOID = asn1.ObjectIdentifier{1, 2, 3, 4, 5}
			}
			timestampContent := red5MarshalTimestampToken(t, signature, options)
			if options.malformedTimestamp {
				timestampContent = red5DERWrap(0x30, bytes.Join([][]byte{
					red5MustASN1(t, red5OIDCMSSignedData),
					red5DERWrap(0xa0, red5DERWrap(0x30, nil)),
				}, nil))
			}
			values := red5DERWrap(0x31, timestampContent)
			attribute := red5MustASN1(t, red5CMSAttribute{
				Type: attributeOID, Values: asn1.RawValue{FullBytes: values},
			})
			attributes := attribute
			if options.duplicateTimestamp {
				attributes = append(attributes, attribute...)
			}
			signerParts = append(signerParts, red5DERWrap(0xa1, attributes))
		}
		signers = append(signers, red5DERWrap(0x30, bytes.Join(signerParts, nil)))
	}

	certificateDER := make([][]byte, len(certificates))
	for index := range certificates {
		certificateDER[index] = append([]byte(nil), certificates[index]...)
	}
	sort.Slice(certificateDER, func(i, j int) bool { return bytes.Compare(certificateDER[i], certificateDER[j]) < 0 })
	if options.reverseCertificateSet {
		for left, right := 0, len(certificateDER)-1; left < right; left, right = left+1, right-1 {
			certificateDER[left], certificateDER[right] = certificateDER[right], certificateDER[left]
		}
	}
	sort.Slice(signers, func(i, j int) bool { return bytes.Compare(signers[i], signers[j]) < 0 })
	digestAlgorithm := red5MustASN1(t, signedDataDigestAlgorithm)
	encapsulatedContentParts := [][]byte{red5MustASN1(t, red5OIDCMSData)}
	if options.embeddedContent || options.wrongEmbeddedContent {
		embeddedChecksum := append([]byte(nil), checksum...)
		if options.wrongEmbeddedContent {
			embeddedChecksum[0] ^= 0xff
		}
		encapsulatedContentParts = append(encapsulatedContentParts, red5DERWrap(0xa0, red5MustASN1(t, embeddedChecksum)))
	}
	encapsulatedContent := red5DERWrap(0x30, bytes.Join(encapsulatedContentParts, nil))
	signedDataVersion := 1
	if options.useSKI {
		signedDataVersion = 3
	}
	if options.signedDataVersion != 0 {
		signedDataVersion = options.signedDataVersion
	}
	signedDataDER := red5DERWrap(0x30, bytes.Join([][]byte{
		red5MustASN1(t, signedDataVersion),
		red5DERWrap(0x31, digestAlgorithm),
		encapsulatedContent,
		red5DERWrap(0xa0, bytes.Join(certificateDER, nil)),
		red5DERWrap(0x31, bytes.Join(signers, nil)),
	}, nil))
	return red5DERWrap(0x30, bytes.Join([][]byte{
		red5MustASN1(t, red5OIDCMSSignedData),
		red5DERWrap(0xa0, signedDataDER),
	}, nil))
}

func red5MarshalTimestampToken(t *testing.T, outerSignature []byte, options red5CMSOptions) []byte {
	t.Helper()
	if options.timestampPKI == nil {
		t.Fatal("timestamp fixture requires a distinct TSA identity")
	}
	tsa := *options.timestampPKI
	imprint := sha256.Sum256(outerSignature)
	if options.wrongTimestampImprint {
		imprint[0] ^= 0xff
	}
	messageImprint := red5MustASN1(t, struct {
		HashAlgorithm red5CMSAlgorithm
		HashedMessage []byte
	}{
		HashAlgorithm: red5CMSAlgorithm{Algorithm: red5OIDSHA256},
		HashedMessage: imprint[:],
	})
	generatedAt, err := asn1.MarshalWithParams(time.Date(2026, 9, 1, 12, 34, 56, 0, time.UTC), "generalized")
	if err != nil {
		t.Fatal(err)
	}
	tstInfoFields := [][]byte{
		red5MustASN1(t, 1),
		red5MustASN1(t, asn1.ObjectIdentifier{1, 2, 3, 4, 1}),
		messageImprint,
		red5MustASN1(t, big.NewInt(987654321)),
		generatedAt,
	}
	tstInfoFields = append(tstInfoFields, options.timestampOptionalFields...)
	tstInfo := red5DERWrap(0x30, bytes.Join(tstInfoFields, nil))
	encapsulatedContent := red5DERWrap(0x30, bytes.Join([][]byte{
		red5MustASN1(t, red5OIDTSTInfo),
		red5DERWrap(0xa0, red5MustASN1(t, tstInfo)),
	}, nil))

	tstDigest := sha256.Sum256(tstInfo)
	if options.wrongTimestampDigest {
		tstDigest[0] ^= 0xff
	}
	contentTypeValue := red5DERWrap(0x31, red5MustASN1(t, red5OIDTSTInfo))
	digestValue := red5DERWrap(0x31, red5MustASN1(t, tstDigest[:]))
	contentTypeAttribute := red5MustASN1(t, red5CMSAttribute{Type: red5OIDContentType, Values: asn1.RawValue{FullBytes: contentTypeValue}})
	digestAttribute := red5MustASN1(t, red5CMSAttribute{Type: red5OIDMessageDigest, Values: asn1.RawValue{FullBytes: digestValue}})
	attributes := [][]byte{contentTypeAttribute, digestAttribute}
	if !options.missingTimestampESS {
		certificateHash := sha256.Sum256(tsa.leaf.Raw)
		if options.wrongTimestampESS {
			certificateHash[0] ^= 0xff
		}
		essCertIDFields := [][]byte{red5MustASN1(t, certificateHash[:])}
		if options.timestampESSIssuerSerial {
			generalNames := red5DERWrap(0x30, red5DERWrap(0xa4, append([]byte(nil), tsa.leaf.RawIssuer...)))
			issuerSerial := red5DERWrap(0x30, append(generalNames, red5MustASN1(t, tsa.leaf.SerialNumber)...))
			essCertIDFields = append(essCertIDFields, issuerSerial)
		}
		essCertID := red5DERWrap(0x30, bytes.Join(essCertIDFields, nil))
		signingCertificateV2 := red5DERWrap(0x30, red5DERWrap(0x30, essCertID))
		essValues := red5DERWrap(0x31, signingCertificateV2)
		attributes = append(attributes, red5MustASN1(t, red5CMSAttribute{
			Type: red5OIDSigningCertV2, Values: asn1.RawValue{FullBytes: essValues},
		}))
	}
	sort.Slice(attributes, func(i, j int) bool { return bytes.Compare(attributes[i], attributes[j]) < 0 })
	attributeBytes := bytes.Join(attributes, nil)
	attributeSet := red5DERWrap(0x31, attributeBytes)
	attributeDigest := sha256.Sum256(attributeSet)
	tsaSignature, err := rsa.SignPKCS1v15(rand.Reader, tsa.leafKey, crypto.SHA256, attributeDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	if options.wrongTSASignature {
		tsaSignature[0] ^= 0xff
	}
	sidDER := red5MustASN1(t, red5CMSIssuerAndSerial{
		Issuer: asn1.RawValue{FullBytes: append([]byte(nil), tsa.leaf.RawIssuer...)},
		Serial: new(big.Int).Set(tsa.leaf.SerialNumber),
	})
	timestampSignatureOID := options.timestampSignatureOID
	if timestampSignatureOID == nil {
		timestampSignatureOID = red5OIDSHA256RSA
	}
	timestampSignatureAlgorithm := red5CMSAlgorithm{Algorithm: timestampSignatureOID}
	if options.timestampSignatureNullParameters {
		timestampSignatureAlgorithm.Parameters = asn1.RawValue{Class: 0, Tag: asn1.TagNull}
	} else if options.timestampSignatureInvalidParameters {
		timestampSignatureAlgorithm.Parameters = asn1.RawValue{FullBytes: red5MustASN1(t, 1)}
	}
	signerInfo := red5DERWrap(0x30, bytes.Join([][]byte{
		red5MustASN1(t, 1),
		sidDER,
		red5MustASN1(t, red5CMSAlgorithm{Algorithm: red5OIDSHA256}),
		red5DERWrap(0xa0, attributeBytes),
		red5MustASN1(t, timestampSignatureAlgorithm),
		red5MustASN1(t, tsaSignature),
	}, nil))
	signerInfos := signerInfo
	if options.duplicateTSASigner {
		signerInfos = append(signerInfos, signerInfo...)
	}
	certificates := red5CloneDER(tsa.chainDER)
	sort.Slice(certificates, func(i, j int) bool { return bytes.Compare(certificates[i], certificates[j]) < 0 })
	signedData := red5DERWrap(0x30, bytes.Join([][]byte{
		red5MustASN1(t, 3),
		red5DERWrap(0x31, red5MustASN1(t, red5CMSAlgorithm{Algorithm: red5OIDSHA256})),
		encapsulatedContent,
		red5DERWrap(0xa0, bytes.Join(certificates, nil)),
		red5DERWrap(0x31, signerInfos),
	}, nil))
	return red5DERWrap(0x30, bytes.Join([][]byte{
		red5MustASN1(t, red5OIDCMSSignedData),
		red5DERWrap(0xa0, signedData),
	}, nil))
}

func red5CMSHash(oid asn1.ObjectIdentifier, value []byte) (crypto.Hash, []byte) {
	if oid.Equal(red5OIDSHA256) {
		digest := sha256.Sum256(value)
		return crypto.SHA256, digest[:]
	}
	digest := sha1.Sum(value)
	return crypto.SHA1, digest[:]
}

func red5CMSDigest(oid asn1.ObjectIdentifier, value []byte) []byte {
	_, digest := red5CMSHash(oid, value)
	return append([]byte(nil), digest...)
}

func red5CMSNeedsTimestamp(options red5CMSOptions) bool {
	return options.unsignedTimestamp || options.unknownUnsigned || options.duplicateTimestamp || options.malformedTimestamp ||
		options.wrongTimestampImprint || options.missingTimestampESS || options.duplicateTSASigner ||
		options.wrongTimestampESS || options.wrongTimestampDigest || options.wrongTSASignature ||
		options.timestampSignatureOID != nil || options.timestampSignatureNullParameters ||
		options.timestampSignatureInvalidParameters || options.timestampESSIssuerSerial
}

func red5MustASN1(t *testing.T, value any) []byte {
	t.Helper()
	result, err := asn1.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func red5DERWrap(tag byte, body []byte) []byte {
	result := []byte{tag}
	switch {
	case len(body) < 128:
		result = append(result, byte(len(body)))
	case len(body) < 256:
		result = append(result, 0x81, byte(len(body)))
	default:
		result = append(result, 0x82, byte(len(body)>>8), byte(len(body)))
	}
	return append(result, body...)
}

func red5DuplicateCertificateExtension(t *testing.T, certificateDER []byte, target asn1.ObjectIdentifier) []byte {
	t.Helper()
	var certificate struct {
		TBS                asn1.RawValue
		SignatureAlgorithm pkix.AlgorithmIdentifier
		Signature          asn1.BitString
	}
	rest, err := asn1.Unmarshal(certificateDER, &certificate)
	if err != nil || len(rest) != 0 {
		t.Fatalf("parse fixture certificate: %v", err)
	}
	remaining := certificate.TBS.Bytes
	fields := make([]asn1.RawValue, 0, 10)
	for len(remaining) != 0 {
		var field asn1.RawValue
		next, err := asn1.Unmarshal(remaining, &field)
		if err != nil || len(next) >= len(remaining) {
			t.Fatalf("parse fixture TBS field: %v", err)
		}
		fields = append(fields, field)
		remaining = next
	}
	found := false
	for index, field := range fields {
		if field.Class != 2 || field.Tag != 3 || !field.IsCompound {
			continue
		}
		var extensions asn1.RawValue
		rest, err := asn1.Unmarshal(field.Bytes, &extensions)
		if err != nil || len(rest) != 0 || extensions.Class != 0 || extensions.Tag != asn1.TagSequence {
			t.Fatalf("parse fixture extensions: %v", err)
		}
		extensionBytes := extensions.Bytes
		var duplicate []byte
		for len(extensionBytes) != 0 {
			var raw asn1.RawValue
			next, err := asn1.Unmarshal(extensionBytes, &raw)
			if err != nil || len(next) >= len(extensionBytes) {
				t.Fatalf("parse fixture extension: %v", err)
			}
			var extension pkix.Extension
			if rest, err := asn1.Unmarshal(raw.FullBytes, &extension); err != nil || len(rest) != 0 {
				t.Fatalf("decode fixture extension: %v", err)
			}
			if extension.Id.Equal(target) {
				duplicate = append([]byte(nil), raw.FullBytes...)
			}
			extensionBytes = next
		}
		if duplicate == nil {
			t.Fatalf("target fixture extension %s missing", target)
		}
		newExtensions := red5DERWrap(0x30, append(append([]byte(nil), extensions.Bytes...), duplicate...))
		fields[index] = asn1.RawValue{FullBytes: red5DERWrap(0xa3, newExtensions)}
		found = true
	}
	if !found {
		t.Fatal("fixture certificate has no extensions field")
	}
	fieldDER := make([][]byte, len(fields))
	for index := range fields {
		fieldDER[index] = fields[index].FullBytes
	}
	tbsDER := red5DERWrap(0x30, bytes.Join(fieldDER, nil))
	return red5DERWrap(0x30, bytes.Join([][]byte{
		tbsDER,
		red5MustASN1(t, certificate.SignatureAlgorithm),
		red5MustASN1(t, certificate.Signature),
	}, nil))
}

type red5XARSpec struct {
	style                      string
	signatureElement           string
	signatureAttributes        string
	chainDER                   [][]byte
	legacyStyle                string
	legacyElement              string
	legacySignatureAttributes  string
	legacyChainDER             [][]byte
	checksumStyle              string
	checksumAttributes         string
	checksumOffset             string
	checksumSize               string
	legacySignatureOffset      string
	legacySignatureSize        string
	signatureOffset            string
	signatureSize              string
	extraTOC                   string
	extraChecksumFields        string
	extraLegacySignatureFields string
	extraLegacyKeyInfoFields   string
	extraLegacyX509DataFields  string
	extraSignatureFields       string
	extraKeyInfoFields         string
	extraX509DataFields        string
	extraCertificateFields     string
	keyInfoNamespaceAttribute  string
	keyInfoAttributes          string
	x509DataAttributes         string
	certificateAttributes      string
	rootAttributes             string
	tocAttributes              string
	extraRoot                  string
	extraAfterRoot             string
	cms                        red5CMSOptions
	cmsPadding                 []byte
	rsaSigner                  *rsa.PrivateKey
	signatureSuffix            []byte
	headerChecksumAlg          uint32
	omitLegacy                 bool
	omitCMS                    bool
	extendedBeforeLegacy       bool
	heapGapAfterChecksum       int
	heapGapBetweenSignatures   int
	reverseSignatureHeapOrder  bool
}

type red5XARFixture struct {
	archive         []byte
	compressedStart int
	compressedSize  int
	heapStart       int
	signatureStart  int
	signatureSize   int
	cmsStart        int
	cmsDERSize      int
	cmsSize         int
}

func red5BuildXAR(t *testing.T, pki red5PKI, configure func(*red5XARSpec)) red5XARFixture {
	t.Helper()
	spec := red5XARSpec{
		style:                     "CMS",
		signatureElement:          "x-signature",
		chainDER:                  red5CloneDER(pki.chainDER),
		legacyStyle:               "RSA",
		legacyElement:             "signature",
		legacyChainDER:            red5CloneDER(pki.chainDER),
		checksumStyle:             "sha1",
		checksumOffset:            "0",
		checksumSize:              "20",
		rsaSigner:                 pki.leafKey,
		headerChecksumAlg:         1,
		keyInfoNamespaceAttribute: ` xmlns="http://www.w3.org/2000/09/xmldsig#"`,
		cms: red5CMSOptions{
			signedAttributes:        true,
			signatureNullParameters: true,
		},
	}
	if configure != nil {
		configure(&spec)
	}
	if red5CMSNeedsTimestamp(spec.cms) && spec.cms.timestampPKI == nil {
		tsa := red5MakeTimestampPKI(t, red5TimestampPKIOptions{})
		spec.cms.timestampPKI = &tsa
	}
	placeholderChecksum := make([]byte, sha1.Size)
	placeholderLegacy := red5XARRSASignature(t, placeholderChecksum, spec)
	placeholderCMSDER := red5MarshalCMS(t, placeholderChecksum, pki, spec.cms)
	placeholderCMS := append(append([]byte(nil), placeholderCMSDER...), spec.cmsPadding...)
	legacyHeapOffset := sha1.Size + spec.heapGapAfterChecksum
	cmsHeapOffset := legacyHeapOffset + len(placeholderLegacy) + spec.heapGapBetweenSignatures
	if spec.reverseSignatureHeapOrder {
		cmsHeapOffset = sha1.Size + spec.heapGapAfterChecksum
		legacyHeapOffset = cmsHeapOffset + len(placeholderCMS) + spec.heapGapBetweenSignatures
	}
	if spec.legacySignatureOffset == "" {
		spec.legacySignatureOffset = fmt.Sprintf("%d", legacyHeapOffset)
	}
	if spec.legacySignatureSize == "" {
		spec.legacySignatureSize = fmt.Sprintf("%d", len(placeholderLegacy))
	}
	if spec.signatureOffset == "" {
		spec.signatureOffset = fmt.Sprintf("%d", cmsHeapOffset)
	}
	if spec.signatureSize == "" {
		spec.signatureSize = fmt.Sprintf("%d", len(placeholderCMS))
	}
	xmlBytes := red5XARXML(t, spec)
	compressed := red5Compress(t, xmlBytes)
	checksum := sha1.Sum(compressed)
	legacySignature := red5XARRSASignature(t, checksum[:], spec)
	cmsDER := red5MarshalCMS(t, checksum[:], pki, spec.cms)
	cmsSignature := append(append([]byte(nil), cmsDER...), spec.cmsPadding...)
	if len(legacySignature) != len(placeholderLegacy) || len(cmsSignature) != len(placeholderCMS) {
		t.Fatalf("signature fixture size changed: RSA %d/%d, CMS %d/%d", len(placeholderLegacy), len(legacySignature), len(placeholderCMS), len(cmsSignature))
	}
	header := make([]byte, 28)
	copy(header, "xar!")
	binary.BigEndian.PutUint16(header[4:6], 28)
	binary.BigEndian.PutUint16(header[6:8], 1)
	binary.BigEndian.PutUint64(header[8:16], uint64(len(compressed)))
	binary.BigEndian.PutUint64(header[16:24], uint64(len(xmlBytes)))
	binary.BigEndian.PutUint32(header[24:28], spec.headerChecksumAlg)
	heapSize := legacyHeapOffset + len(legacySignature)
	if cmsEnd := cmsHeapOffset + len(cmsSignature); cmsEnd > heapSize {
		heapSize = cmsEnd
	}
	heap := make([]byte, heapSize)
	copy(heap, checksum[:])
	copy(heap[legacyHeapOffset:], legacySignature)
	copy(heap[cmsHeapOffset:], cmsSignature)
	archive := append(append(header, compressed...), heap...)
	archive = append(archive, []byte("fixture-package-payload")...)
	return red5XARFixture{
		archive:         archive,
		compressedStart: len(header),
		compressedSize:  len(compressed),
		heapStart:       len(header) + len(compressed),
		signatureStart:  len(header) + len(compressed) + legacyHeapOffset,
		signatureSize:   len(legacySignature),
		cmsStart:        len(header) + len(compressed) + cmsHeapOffset,
		cmsDERSize:      len(cmsDER),
		cmsSize:         len(cmsSignature),
	}
}

func red5XARRSASignature(t *testing.T, checksum []byte, spec red5XARSpec) []byte {
	t.Helper()
	if len(checksum) != sha1.Size {
		t.Fatalf("legacy XAR signer received %d checksum bytes, want %d", len(checksum), sha1.Size)
	}
	signature, err := rsa.SignPKCS1v15(rand.Reader, spec.rsaSigner, crypto.SHA1, checksum)
	if err != nil {
		t.Fatal(err)
	}
	return append(signature, spec.signatureSuffix...)
}

func red5XARXML(t *testing.T, spec red5XARSpec) []byte {
	t.Helper()
	var cmsCertificates strings.Builder
	for _, certificate := range spec.chainDER {
		cmsCertificates.WriteString("<X509Certificate" + spec.certificateAttributes + ">")
		cmsCertificates.WriteString(base64.StdEncoding.EncodeToString(certificate))
		cmsCertificates.WriteString("</X509Certificate>")
	}
	var legacyCertificates strings.Builder
	for _, certificate := range spec.legacyChainDER {
		legacyCertificates.WriteString("<X509Certificate" + spec.certificateAttributes + ">")
		legacyCertificates.WriteString(base64.StdEncoding.EncodeToString(certificate))
		legacyCertificates.WriteString("</X509Certificate>")
	}
	legacyRecord := ""
	if !spec.omitLegacy {
		legacyRecord = fmt.Sprintf(`<%s style="%s"%s><offset>%s</offset><size>%s</size>%s%s<KeyInfo%s%s>%s<X509Data%s>%s%s</X509Data></KeyInfo></%s>`,
			spec.legacyElement, spec.legacyStyle, spec.legacySignatureAttributes, spec.legacySignatureOffset, spec.legacySignatureSize,
			spec.extraLegacySignatureFields, spec.extraLegacyKeyInfoFields, spec.keyInfoNamespaceAttribute, spec.keyInfoAttributes,
			spec.extraLegacyX509DataFields, spec.x509DataAttributes, spec.extraCertificateFields,
			legacyCertificates.String(), spec.legacyElement)
	}
	cmsRecord := ""
	if !spec.omitCMS {
		cmsRecord = fmt.Sprintf(`<%s style="%s"%s><offset>%s</offset><size>%s</size>%s%s<KeyInfo%s%s>%s<X509Data%s>%s%s</X509Data></KeyInfo></%s>`,
			spec.signatureElement, spec.style, spec.signatureAttributes, spec.signatureOffset, spec.signatureSize,
			spec.extraSignatureFields, spec.extraKeyInfoFields, spec.keyInfoNamespaceAttribute, spec.keyInfoAttributes,
			spec.extraX509DataFields, spec.x509DataAttributes, spec.extraCertificateFields,
			cmsCertificates.String(), spec.signatureElement)
	}
	signatureRecords := legacyRecord + cmsRecord
	if spec.extendedBeforeLegacy {
		signatureRecords = cmsRecord + legacyRecord
	}
	value := fmt.Sprintf(
		`<xar%s><toc%s><checksum style="%s"%s><offset>%s</offset><size>%s</size>%s</checksum>%s%s</toc>%s</xar>%s`,
		spec.rootAttributes,
		spec.tocAttributes,
		spec.checksumStyle,
		spec.checksumAttributes,
		spec.checksumOffset,
		spec.checksumSize,
		spec.extraChecksumFields,
		signatureRecords,
		spec.extraTOC,
		spec.extraRoot,
		spec.extraAfterRoot,
	)
	return []byte(value)
}

func red5Compress(t *testing.T, value []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zlib.NewWriter(&output)
	if _, err := writer.Write(value); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestExtractVerifiedXARSignerBindsRealRSAAndCMSSigner(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	tests := []struct {
		name      string
		configure func(*red5XARSpec)
	}{
		{name: "outer SHA-1 digest parameters absent and absent"},
		{name: "outer SHA-1 digest parameters absent and NULL", configure: func(spec *red5XARSpec) {
			spec.cms.signerDigestNullParameters = true
		}},
		{name: "outer SHA-1 digest parameters NULL and absent", configure: func(spec *red5XARSpec) {
			spec.cms.signedDataDigestNullParameters = true
		}},
		{name: "outer SHA-1 digest parameters NULL and NULL", configure: func(spec *red5XARSpec) {
			spec.cms.signedDataDigestNullParameters = true
			spec.cms.signerDigestNullParameters = true
		}},
		{name: "productbuild timestamped CMS", configure: func(spec *red5XARSpec) {
			spec.cms.signedAttributes = true
			spec.cms.unsignedTimestamp = true
			spec.cmsPadding = make([]byte, 4096)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := red5BuildXAR(t, pki, test.configure)
			evidence, err := extractVerifiedXARSigner(bytes.NewReader(fixture.archive), int64(len(fixture.archive)))
			if err != nil {
				t.Fatalf("extractVerifiedXARSigner: %v", err)
			}
			if evidence.Kind != xarSignatureCMS {
				t.Fatalf("kind = %v, want CMS", evidence.Kind)
			}
			compressed := fixture.archive[fixture.compressedStart : fixture.compressedStart+fixture.compressedSize]
			wantChecksum := sha1.Sum(compressed)
			if !bytes.Equal(evidence.SignedChecksum, wantChecksum[:]) {
				t.Fatalf("signed checksum = %x, want %x", evidence.SignedChecksum, wantChecksum)
			}
			if len(evidence.ChainDER) != 3 {
				t.Fatalf("chain length = %d, want 3", len(evidence.ChainDER))
			}
			for index := range pki.chainDER {
				if !bytes.Equal(evidence.ChainDER[index], pki.chainDER[index]) {
					t.Fatalf("chain[%d] did not preserve signer-first DER order", index)
				}
			}
		})
	}
}

func TestRFC3161FixtureBindsTSTInfoImprintESSAndOneTSASignature(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	tsa := red5MakeTimestampPKI(t, red5TimestampPKIOptions{})
	if bytes.Equal(tsa.leaf.Raw, pki.leaf.Raw) || bytes.Equal(tsa.leaf.RawSubjectPublicKeyInfo, pki.leaf.RawSubjectPublicKeyInfo) {
		t.Fatal("TSA identity unexpectedly reuses the Developer ID package signer")
	}
	outerSignature := bytes.Repeat([]byte{0x5a}, pki.leaf.PublicKey.(*rsa.PublicKey).Size())
	tokenDER := red5MarshalTimestampToken(t, outerSignature, red5CMSOptions{timestampPKI: &tsa})
	var token macCMSContentInfo
	rest, err := asn1.Unmarshal(tokenDER, &token)
	if err != nil || len(rest) != 0 {
		t.Fatalf("independent token parse: %v", err)
	}
	var signedData macCMSSignedData
	rest, err = asn1.Unmarshal(token.Content.Bytes, &signedData)
	if err != nil || len(rest) != 0 {
		t.Fatalf("independent SignedData parse: %v", err)
	}
	var tstInfoDER []byte
	rest, err = asn1.Unmarshal(signedData.Content.Content.Bytes, &tstInfoDER)
	if err != nil || len(rest) != 0 {
		t.Fatalf("independent TSTInfo extraction: %v", err)
	}
	var tstSequence asn1.RawValue
	if rest, err = asn1.Unmarshal(tstInfoDER, &tstSequence); err != nil || len(rest) != 0 {
		t.Fatalf("independent TSTInfo sequence: %v", err)
	}
	tstFields, err := parseMacASN1Children(tstSequence.Bytes, 10)
	if err != nil || len(tstFields) != 5 {
		t.Fatalf("independent TSTInfo fields = %d, %v", len(tstFields), err)
	}
	var tstVersion int
	var tstPolicy asn1.ObjectIdentifier
	var tstImprint struct {
		HashAlgorithm macCMSAlgorithm
		HashedMessage []byte
	}
	if !unmarshalCanonicalMacASN1(tstFields[0], &tstVersion) || tstVersion != 1 {
		t.Fatal("independent TSTInfo version is not canonical v1")
	}
	if !unmarshalCanonicalMacASN1(tstFields[1], &tstPolicy) || len(tstPolicy) == 0 {
		t.Fatal("independent TSTInfo policy is not canonical")
	}
	if !unmarshalCanonicalMacASN1(tstFields[2], &tstImprint) || !validMacCMSAlgorithm(tstImprint.HashAlgorithm, packageSHA256OID()) {
		t.Fatal("independent TSTInfo message imprint algorithm is not canonical SHA-256")
	}
	tstSerial, serialOK := parsePositiveMacASN1Integer(tstFields[3])
	if !serialOK || tstSerial.Sign() <= 0 {
		t.Fatal("independent TSTInfo serial is not canonical and positive")
	}
	if err := validateMacTimestampInfo(tstInfoDER, outerSignature); err != nil {
		t.Fatalf("TSTInfo profile: %v", err)
	}
	certificateDER, err := parseMacCMSCertificates(signedData.Certificates)
	if err != nil {
		t.Fatalf("TSA certificates: %v", err)
	}
	certificates := make([]*x509.Certificate, len(certificateDER))
	for index, der := range certificateDER {
		certificates[index], err = x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
	}
	signer := signedData.SignerInfos[0]
	signerIndex, err := selectMacCMSSigner(signer.SID, certificates)
	if err != nil {
		t.Fatalf("TSA signer selection: %v", err)
	}
	if !bytes.Equal(certificateDER[signerIndex], tsa.leaf.Raw) || bytes.Equal(certificateDER[signerIndex], pki.leaf.Raw) {
		t.Fatal("timestamp signer does not select the distinct TSA leaf")
	}
	if err := validateMacTimestampSignedAttributes(signer.SignedAttributes.Bytes, tstInfoDER, certificateDER[signerIndex]); err != nil {
		t.Fatalf("TSA signed attributes: %v", err)
	}
	if err := validateMacTimestampToken(tokenDER, outerSignature); err != nil {
		t.Fatalf("complete timestamp token: %v", err)
	}
}

func TestRFC3161TimestampSignatureAlgorithmParameterProfile(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	tsa := red5MakeTimestampPKI(t, red5TimestampPKIOptions{})
	outerSignature := bytes.Repeat([]byte{0x5a}, pki.leaf.PublicKey.(*rsa.PublicKey).Size())
	for _, test := range []struct {
		name    string
		options red5CMSOptions
	}{
		{name: "sha256WithRSAEncryption absent parameters"},
		{name: "sha256WithRSAEncryption NULL parameters", options: red5CMSOptions{timestampSignatureNullParameters: true}},
		{name: "rsaEncryption NULL parameters", options: red5CMSOptions{
			timestampSignatureOID: red5OIDRSAEncryption, timestampSignatureNullParameters: true,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.options.timestampPKI = &tsa
			tokenDER := red5MarshalTimestampToken(t, outerSignature, test.options)
			if err := validateMacTimestampToken(tokenDER, outerSignature); err != nil {
				t.Fatalf("standards-required timestamp signature profile rejected: %v", err)
			}
		})
	}
}

func TestExtractVerifiedXARSignerRequiresExactProductbuildDualSignatureShape(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	other := red5MakePKI(t, red5PKIOptions{})
	tests := []struct {
		name      string
		configure func(*red5XARSpec)
		mutate    func(red5XARFixture) []byte
	}{
		{name: "missing ordinary RSA", configure: func(spec *red5XARSpec) { spec.omitLegacy = true }},
		{name: "missing extended CMS", configure: func(spec *red5XARSpec) { spec.omitCMS = true }},
		{name: "ordinary element is extended", configure: func(spec *red5XARSpec) { spec.legacyElement = "x-signature" }},
		{name: "CMS element is ordinary", configure: func(spec *red5XARSpec) { spec.signatureElement = "signature" }},
		{name: "ordinary style is CMS", configure: func(spec *red5XARSpec) { spec.legacyStyle = "CMS" }},
		{name: "extended style is RSA", configure: func(spec *red5XARSpec) { spec.style = "RSA" }},
		{name: "extended precedes ordinary", configure: func(spec *red5XARSpec) { spec.extendedBeforeLegacy = true }},
		{name: "duplicate ordinary", configure: func(spec *red5XARSpec) {
			spec.extraTOC = `<signature style="RSA"><offset>20</offset><size>256</size><KeyInfo><X509Data/></KeyInfo></signature>`
		}},
		{name: "duplicate extended", configure: func(spec *red5XARSpec) {
			spec.extraTOC = `<x-signature style="CMS"><offset>276</offset><size>1</size><KeyInfo><X509Data/></KeyInfo></x-signature>`
		}},
		{name: "unknown signature record", configure: func(spec *red5XARSpec) {
			spec.extraTOC = `<alternate-signature style="CMS"><offset>999</offset><size>1</size></alternate-signature>`
		}},
		{name: "RSA and CMS chain mismatch", configure: func(spec *red5XARSpec) {
			spec.chainDER = red5CloneDER(other.chainDER)
		}},
		{name: "RSA and CMS heap ranges swapped", configure: func(spec *red5XARSpec) {
			spec.legacySignatureOffset = "276"
			spec.signatureOffset = "20"
		}},
		{name: "mutated extended CMS", mutate: func(fixture red5XARFixture) []byte {
			archive := append([]byte(nil), fixture.archive...)
			archive[fixture.cmsStart+fixture.cmsDERSize-1] ^= 1
			return archive
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := red5BuildXAR(t, pki, test.configure)
			archive := fixture.archive
			if test.mutate != nil {
				archive = test.mutate(fixture)
			}
			if _, err := extractVerifiedXARSigner(bytes.NewReader(archive), int64(len(archive))); err == nil {
				t.Fatal("non-productbuild dual-signature shape accepted")
			}
		})
	}
}

func TestLegacyXARRSAVerifiesTheEncodedTOCDigestWithoutHashingItAgain(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	checksum := sha1.Sum([]byte("compressed XAR TOC"))
	direct, err := rsa.SignPKCS1v15(rand.Reader, pki.leafKey, crypto.SHA1, checksum[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyMacXARRSASignature(checksum[:], direct, pki.leaf); err != nil {
		t.Fatalf("direct SHA-1 DigestInfo signature rejected: %v", err)
	}
	doubleDigest := sha1.Sum(checksum[:])
	doubleHashed, err := rsa.SignPKCS1v15(rand.Reader, pki.leafKey, crypto.SHA1, doubleDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyMacXARRSASignature(checksum[:], doubleHashed, pki.leaf); err == nil {
		t.Fatal("legacy RSA signature that hashes the encoded TOC digest twice was accepted")
	}
}

func TestExtractVerifiedXARSignerAllowsOnlyBoundedZeroCMSReservationTail(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	valid := red5BuildXAR(t, pki, func(spec *red5XARSpec) { spec.cmsPadding = make([]byte, 8192) })
	if _, err := extractVerifiedXARSigner(bytes.NewReader(valid.archive), int64(len(valid.archive))); err != nil {
		t.Fatalf("reviewed all-zero CMS reservation tail rejected: %v", err)
	}
	nonzero := red5BuildXAR(t, pki, func(spec *red5XARSpec) { spec.cmsPadding = []byte{0, 0, 1, 0} })
	if _, err := extractVerifiedXARSigner(bytes.NewReader(nonzero.archive), int64(len(nonzero.archive))); err == nil {
		t.Fatal("non-zero CMS reservation tail accepted")
	}
	oversized := red5BuildXAR(t, pki, func(spec *red5XARSpec) { spec.cmsPadding = make([]byte, (1<<20)+1) })
	if _, err := extractVerifiedXARSigner(bytes.NewReader(oversized.archive), int64(len(oversized.archive))); err == nil {
		t.Fatal("oversized CMS reservation tail accepted")
	}
}

func TestExtractVerifiedXARSignerCharacterizesDisjointHeapLayoutsPendingAuthenticProfileFreeze(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	for _, test := range []struct {
		name      string
		configure func(*red5XARSpec)
	}{
		{name: "authenticated gaps", configure: func(spec *red5XARSpec) {
			spec.heapGapAfterChecksum = 7
			spec.heapGapBetweenSignatures = 11
		}},
		{name: "authenticated reversed physical signature order", configure: func(spec *red5XARSpec) {
			spec.reverseSignatureHeapOrder = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := red5BuildXAR(t, pki, test.configure)
			if _, err := extractVerifiedXARSigner(bytes.NewReader(fixture.archive), int64(len(fixture.archive))); err != nil {
				t.Fatalf("safe disjoint heap security grammar rejected before authentic serialization freeze: %v", err)
			}
		})
	}
}

func TestXARChainTerminalVerifierSeamHandlesOnlyReviewedLegacyRootPolicy(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{rootSignatureAlgorithm: x509.SHA1WithRSA})
	encoded := make([]string, len(pki.chainDER))
	for index := range pki.chainDER {
		encoded[index] = base64.StdEncoding.EncodeToString(pki.chainDER[index])
	}
	called := 0
	chain, _, err := parseMacXARCertificateChainWithTerminalVerifier(encoded, func(certificate *x509.Certificate, fingerprint [32]byte) error {
		called++
		if certificate == nil || fingerprint != sha256.Sum256(pki.root.Raw) {
			return errors.New("wrong terminal")
		}
		return nil
	})
	if err != nil || called != 1 || len(chain) != 3 {
		t.Fatalf("terminal verifier seam = chain %d, called %d, err %v", len(chain), called, err)
	}
	if _, _, err := parseMacXARCertificateChain(encoded); err == nil {
		t.Fatal("unreviewed synthetic SHA-1 root passed the production pinned-root policy")
	}
}

func TestExtractVerifiedXARSignerReturnsFreshCopies(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	fixture := red5BuildXAR(t, pki, nil)
	first, err := extractVerifiedXARSigner(bytes.NewReader(fixture.archive), int64(len(fixture.archive)))
	if err != nil {
		t.Fatal(err)
	}
	first.SignedChecksum[0] ^= 0xff
	first.Signature[0] ^= 0xff
	first.LegacySignature[0] ^= 0xff
	first.ChainDER[0][0] ^= 0xff
	second, err := extractVerifiedXARSigner(bytes.NewReader(fixture.archive), int64(len(fixture.archive)))
	if err != nil {
		t.Fatal(err)
	}
	if second.SignedChecksum[0] == first.SignedChecksum[0] || second.Signature[0] == first.Signature[0] ||
		second.LegacySignature[0] == first.LegacySignature[0] || second.ChainDER[0][0] == first.ChainDER[0][0] {
		t.Fatal("returned evidence aliases parser input or a previous result")
	}
}

type red5FullErrorReader struct{ value []byte }

func (reader red5FullErrorReader) ReadAt(destination []byte, offset int64) (int, error) {
	if offset < 0 || offset >= int64(len(reader.value)) {
		return 0, io.EOF
	}
	read := copy(destination, reader.value[offset:])
	return read, io.EOF
}

func TestExtractVerifiedXARSignerRejectsReaderErrorEvenWithFullCount(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	fixture := red5BuildXAR(t, pki, nil)
	if _, err := extractVerifiedXARSigner(red5FullErrorReader{value: fixture.archive}, int64(len(fixture.archive))); err == nil {
		t.Fatal("ambiguous ReaderAt result with a non-nil error was accepted")
	}
}

func TestCMSObjectIdentifierAuthoritiesAreReturnedByValue(t *testing.T) {
	oids := []asn1.ObjectIdentifier{
		packageCMSDataOID(), packageCMSSignedDataOID(), packageSHA1OID(), packageSHA1RSAOID(), packageSHA256OID(), packageSHA256RSAOID(),
		packageRSAEncryptionOID(), packageCMSContentTypeOID(), packageCMSMessageDigestOID(),
		packageCMSTimestampTokenOID(), packageCMSTSTInfoOID(), packageCMSSigningCertificateOID(), packageCMSSigningCertificateV2OID(),
		packageExtendedKeyUsageOID(), packageTimeStampingEKUOID(),
	}
	for _, oid := range oids {
		oid[0] = 9
	}
	pki := red5MakePKI(t, red5PKIOptions{})
	fixture := red5BuildXAR(t, pki, nil)
	if _, err := extractVerifiedXARSigner(bytes.NewReader(fixture.archive), int64(len(fixture.archive))); err != nil {
		t.Fatalf("caller mutation changed CMS trust authorities: %v", err)
	}
}

func TestExtractVerifiedXARSignerRejectsHeaderTOCAndRangeAmbiguity(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	valid := red5BuildXAR(t, pki, nil)
	mutate := func(change func([]byte)) []byte {
		result := append([]byte(nil), valid.archive...)
		change(result)
		return result
	}
	compressedTrailing := make([]byte, len(valid.archive)+1)
	copy(compressedTrailing, valid.archive[:valid.heapStart])
	compressedTrailing[valid.heapStart] = 0
	copy(compressedTrailing[valid.heapStart+1:], valid.archive[valid.heapStart:])
	binary.BigEndian.PutUint64(compressedTrailing[8:16], uint64(valid.compressedSize+1))
	tests := []struct {
		name    string
		archive []byte
		size    int64
	}{
		{name: "archive exceeds 250 MiB", archive: valid.archive, size: (250 << 20) + 1},
		{name: "short header", archive: valid.archive[:27], size: 27},
		{name: "wrong magic", archive: mutate(func(value []byte) { value[0] = 'z' })},
		{name: "wrong header size", archive: mutate(func(value []byte) { binary.BigEndian.PutUint16(value[4:6], 29) })},
		{name: "wrong version", archive: mutate(func(value []byte) { binary.BigEndian.PutUint16(value[6:8], 2) })},
		{name: "unsupported header checksum", archive: mutate(func(value []byte) { binary.BigEndian.PutUint32(value[24:28], 3) })},
		{name: "compressed TOC cap", archive: mutate(func(value []byte) { binary.BigEndian.PutUint64(value[8:16], (8<<20)+1) })},
		{name: "expanded TOC cap", archive: mutate(func(value []byte) { binary.BigEndian.PutUint64(value[16:24], (32<<20)+1) })},
		{name: "expanded TOC length smaller than stream", archive: mutate(func(value []byte) { binary.BigEndian.PutUint64(value[16:24], binary.BigEndian.Uint64(value[16:24])-1) })},
		{name: "expanded TOC length larger than stream", archive: mutate(func(value []byte) { binary.BigEndian.PutUint64(value[16:24], binary.BigEndian.Uint64(value[16:24])+1) })},
		{name: "truncated compressed TOC", archive: mutate(func(value []byte) { binary.BigEndian.PutUint64(value[8:16], uint64(len(value))) })},
		{name: "compressed TOC trailing ambiguity", archive: compressedTrailing},
		{name: "malformed zlib", archive: mutate(func(value []byte) { value[valid.compressedStart] ^= 0xff })},
		{name: "truncated heap", archive: valid.archive[:valid.signatureStart+valid.signatureSize-1]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			size := test.size
			if size == 0 {
				size = int64(len(test.archive))
			}
			if _, err := extractVerifiedXARSigner(bytes.NewReader(test.archive), size); err == nil {
				t.Fatal("malformed XAR accepted")
			}
		})
	}
}

func TestExtractVerifiedXARSignerRejectsMalformedXMLAlgorithmsAndCaps(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	lookalike := red5MakePKI(t, red5PKIOptions{})
	tests := []struct {
		name      string
		configure func(*red5XARSpec)
	}{
		{name: "XML directive", configure: func(spec *red5XARSpec) { spec.extraTOC = `<!DOCTYPE x [<!ENTITY y "z">]>` }},
		{name: "XML entity", configure: func(spec *red5XARSpec) { spec.extraTOC = `<file>&y;</file>` }},
		{name: "in-root processing instruction", configure: func(spec *red5XARSpec) { spec.extraTOC = `<?profile widen?>` }},
		{name: "XML trailing element", configure: func(spec *red5XARSpec) { spec.extraAfterRoot = `<xar/>` }},
		{name: "duplicate top-level TOC", configure: func(spec *red5XARSpec) { spec.extraRoot = `<toc/>` }},
		{name: "unknown root attribute", configure: func(spec *red5XARSpec) { spec.rootAttributes = ` authority="extra"` }},
		{name: "unknown TOC attribute", configure: func(spec *red5XARSpec) { spec.tocAttributes = ` authority="extra"` }},
		{name: "unknown checksum attribute", configure: func(spec *red5XARSpec) { spec.checksumAttributes = ` authority="extra"` }},
		{name: "unknown signature attribute", configure: func(spec *red5XARSpec) { spec.signatureAttributes = ` authority="extra"` }},
		{name: "unknown KeyInfo attribute", configure: func(spec *red5XARSpec) { spec.keyInfoAttributes = ` authority="extra"` }},
		{name: "unknown X509Data attribute", configure: func(spec *red5XARSpec) { spec.x509DataAttributes = ` authority="extra"` }},
		{name: "unknown X509Certificate attribute", configure: func(spec *red5XARSpec) { spec.certificateAttributes = ` authority="extra"` }},
		{name: "unknown direct TOC field", configure: func(spec *red5XARSpec) { spec.extraTOC = `<authority/>` }},
		{name: "unknown checksum field", configure: func(spec *red5XARSpec) { spec.extraChecksumFields = `<authority/>` }},
		{name: "unknown signature field", configure: func(spec *red5XARSpec) { spec.extraSignatureFields = `<authority/>` }},
		{name: "unknown KeyInfo field", configure: func(spec *red5XARSpec) { spec.extraX509DataFields = `<authority/>` }},
		{name: "unknown X509Data field", configure: func(spec *red5XARSpec) { spec.extraCertificateFields = `<authority/>` }},
		{name: "absent XMLDSIG namespace", configure: func(spec *red5XARSpec) { spec.keyInfoNamespaceAttribute = "" }},
		{name: "wrong XMLDSIG namespace", configure: func(spec *red5XARSpec) { spec.keyInfoNamespaceAttribute = ` xmlns="urn:lookalike"` }},
		{name: "nested namespace shadows X509Data", configure: func(spec *red5XARSpec) { spec.x509DataAttributes = ` xmlns="urn:lookalike"` }},
		{name: "namespace in signed file inventory", configure: func(spec *red5XARSpec) {
			spec.extraTOC = `<file id="1"><name>payload.pkg</name><metadata xmlns="urn:lookalike"><value>1</value></metadata></file>`
		}},
		{name: "unsupported checksum style", configure: func(spec *red5XARSpec) { spec.checksumStyle = "sha256" }},
		{name: "unsupported signature style", configure: func(spec *red5XARSpec) { spec.style = "DSA" }},
		{name: "duplicate signature record", configure: func(spec *red5XARSpec) {
			spec.extraTOC = `<signature style="RSA"><offset>20</offset><size>256</size><KeyInfo><X509Data></X509Data></KeyInfo></signature>`
		}},
		{name: "duplicate extended signature record", configure: func(spec *red5XARSpec) {
			spec.extraTOC = `<x-signature style="CMS"><offset>276</offset><size>256</size><KeyInfo><X509Data></X509Data></KeyInfo></x-signature>`
		}},
		{name: "duplicate checksum record", configure: func(spec *red5XARSpec) {
			spec.extraTOC = `<checksum style="sha1"><offset>0</offset><size>20</size></checksum>`
		}},
		{name: "duplicate checksum offset", configure: func(spec *red5XARSpec) { spec.extraChecksumFields = `<offset>0</offset>` }},
		{name: "duplicate signature size", configure: func(spec *red5XARSpec) { spec.extraSignatureFields = `<size>256</size>` }},
		{name: "duplicate KeyInfo authority", configure: func(spec *red5XARSpec) { spec.extraKeyInfoFields = `<KeyInfo><X509Data></X509Data></KeyInfo>` }},
		{name: "duplicate X509Data authority", configure: func(spec *red5XARSpec) { spec.extraX509DataFields = `<X509Data></X509Data>` }},
		{name: "checksum overflow", configure: func(spec *red5XARSpec) { spec.checksumOffset = "18446744073709551615" }},
		{name: "signature overflow", configure: func(spec *red5XARSpec) { spec.signatureOffset = "18446744073709551615" }},
		{name: "noncanonical checksum offset", configure: func(spec *red5XARSpec) { spec.checksumOffset = "00" }},
		{name: "noncanonical checksum size", configure: func(spec *red5XARSpec) { spec.checksumSize = "020" }},
		{name: "noncanonical signature offset", configure: func(spec *red5XARSpec) { spec.signatureOffset = "0276" }},
		{name: "checksum wrong size", configure: func(spec *red5XARSpec) { spec.checksumSize = "32" }},
		{name: "signature checksum overlap", configure: func(spec *red5XARSpec) { spec.signatureOffset = "0" }},
		{name: "signature blob cap", configure: func(spec *red5XARSpec) { spec.signatureSize = fmt.Sprintf("%d", (4<<20)+1) }},
		{name: "wrong header checksum mapping", configure: func(spec *red5XARSpec) { spec.headerChecksumAlg = 3 }},
		{name: "duplicate certificate", configure: func(spec *red5XARSpec) {
			spec.chainDER = append(append([][]byte(nil), pki.chainDER...), pki.chainDER[2])
		}},
		{name: "missing terminal certificate", configure: func(spec *red5XARSpec) { spec.chainDER = spec.chainDER[:2] }},
		{name: "unreferenced extra certificate", configure: func(spec *red5XARSpec) {
			spec.chainDER = append(append([][]byte(nil), pki.chainDER...), lookalike.leaf.Raw)
		}},
		{name: "reordered chain", configure: func(spec *red5XARSpec) { spec.chainDER = [][]byte{pki.intermediate.Raw, pki.leaf.Raw, pki.root.Raw} }},
		{name: "more than sixteen certificates", configure: func(spec *red5XARSpec) {
			for len(spec.chainDER) < 17 {
				spec.chainDER = append(spec.chainDER, lookalike.leaf.Raw)
			}
		}},
		{name: "per-certificate cap", configure: func(spec *red5XARSpec) {
			spec.chainDER = [][]byte{bytes.Repeat([]byte{1}, (64<<10)+1), pki.intermediate.Raw, pki.root.Raw}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := red5BuildXAR(t, pki, test.configure)
			if _, err := extractVerifiedXARSigner(bytes.NewReader(fixture.archive), int64(len(fixture.archive))); err == nil {
				t.Fatal("ambiguous or unsupported XAR accepted")
			}
		})
	}
}

func TestExtractVerifiedXARSignerAllowsSignedFileInventoryWithoutGrantingAnotherTOCAuthority(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	fixture := red5BuildXAR(t, pki, func(spec *red5XARSpec) {
		spec.extraTOC = `<file id="1"><name>payload.pkg</name><type>file</type><data><length>7</length><offset>999</offset><size>7</size></data></file>`
	})
	if _, err := extractVerifiedXARSigner(bytes.NewReader(fixture.archive), int64(len(fixture.archive))); err != nil {
		t.Fatalf("legitimate signed file inventory rejected: %v", err)
	}
}

func TestExtractVerifiedXARSignerRejectsInvalidChecksumSignatureAndChain(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	other := red5MakePKI(t, red5PKIOptions{})
	tests := []struct {
		name      string
		configure func(*red5XARSpec)
		mutate    func(red5XARFixture) []byte
	}{
		{name: "wrong RSA signer", configure: func(spec *red5XARSpec) { spec.rsaSigner = other.leafKey }},
		{name: "broken issuer signature", configure: func(spec *red5XARSpec) {
			spec.chainDER = [][]byte{pki.leaf.Raw, other.intermediate.Raw, other.root.Raw}
		}},
		{name: "RSA trailing ambiguity", configure: func(spec *red5XARSpec) { spec.signatureSuffix = []byte{0} }},
		{name: "invalid TOC checksum", mutate: func(fixture red5XARFixture) []byte {
			value := append([]byte(nil), fixture.archive...)
			value[fixture.heapStart] ^= 0xff
			return value
		}},
		{name: "invalid signature", mutate: func(fixture red5XARFixture) []byte {
			value := append([]byte(nil), fixture.archive...)
			value[fixture.signatureStart] ^= 0xff
			return value
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := red5BuildXAR(t, pki, test.configure)
			archive := fixture.archive
			if test.mutate != nil {
				archive = test.mutate(fixture)
			}
			if _, err := extractVerifiedXARSigner(bytes.NewReader(archive), int64(len(archive))); err == nil {
				t.Fatal("invalid XAR signature admitted")
			}
		})
	}
}

func TestExtractVerifiedXARSignerRejectsCMSAmbiguityAndSignerConfusion(t *testing.T) {
	pki := red5MakePKI(t, red5PKIOptions{})
	lookalike := red5MakePKI(t, red5PKIOptions{})
	wrongSigner := red5MakePKI(t, red5PKIOptions{team: "WRONGTEAM00"})
	tests := []struct {
		name      string
		configure func(*red5XARSpec)
	}{
		{name: "zero signer infos", configure: func(spec *red5XARSpec) { spec.cms.signerCount = -1 }},
		{name: "two signer infos", configure: func(spec *red5XARSpec) { spec.cms.signerCount = 2 }},
		{name: "wrong issuer serial", configure: func(spec *red5XARSpec) { spec.cms.wrongSID = true }},
		{name: "subject key identifier profile is not admitted without an authentic artifact", configure: func(spec *red5XARSpec) {
			spec.cms.useSKI = true
		}},
		{name: "wrong subject key identifier", configure: func(spec *red5XARSpec) { spec.cms.useSKI = true; spec.cms.wrongSID = true }},
		{name: "issuer signer version mismatch", configure: func(spec *red5XARSpec) { spec.cms.signerVersion = 3 }},
		{name: "issuer signed-data version mismatch", configure: func(spec *red5XARSpec) { spec.cms.signedDataVersion = 3 }},
		{name: "SKI signer version mismatch", configure: func(spec *red5XARSpec) { spec.cms.useSKI = true; spec.cms.signerVersion = 1 }},
		{name: "SKI signed-data version mismatch", configure: func(spec *red5XARSpec) { spec.cms.useSKI = true; spec.cms.signedDataVersion = 1 }},
		{name: "unsupported digest algorithm", configure: func(spec *red5XARSpec) {
			spec.cms.digestOID = red5OIDSHA256
		}},
		{name: "unsupported signature algorithm", configure: func(spec *red5XARSpec) {
			spec.cms.signatureOID = red5OIDSHA256RSA
		}},
		{name: "outer SignedData SHA-1 digest algorithm invalid parameters", configure: func(spec *red5XARSpec) {
			spec.cms.signedDataDigestInvalidParameters = true
		}},
		{name: "outer SignerInfo SHA-1 digest algorithm invalid parameters", configure: func(spec *red5XARSpec) {
			spec.cms.signerDigestInvalidParameters = true
		}},
		{name: "outer sha1WithRSA signature algorithm absent parameters", configure: func(spec *red5XARSpec) {
			spec.cms.signatureNullParameters = false
		}},
		{name: "outer sha1WithRSA signature algorithm invalid parameters", configure: func(spec *red5XARSpec) {
			spec.cms.signatureNullParameters = false
			spec.cms.signatureInvalidParameters = true
		}},
		{name: "generic RSA encryption signature algorithm is not admitted without an authentic artifact", configure: func(spec *red5XARSpec) {
			spec.cms.signatureOID = red5OIDRSAEncryption
		}},
		{name: "signed attributes are mandatory", configure: func(spec *red5XARSpec) {
			spec.cms.signedAttributes = false
		}},
		{name: "signed attribute message digest mismatch", configure: func(spec *red5XARSpec) {
			spec.cms.signedAttributes = true
			spec.cms.wrongMessageDigest = true
		}},
		{name: "signed attribute missing content type", configure: func(spec *red5XARSpec) {
			spec.cms.signedAttributes = true
			spec.cms.omitContentType = true
		}},
		{name: "signed attribute duplicate digest", configure: func(spec *red5XARSpec) {
			spec.cms.signedAttributes = true
			spec.cms.duplicateDigest = true
		}},
		{name: "embedded content is not admitted without an authentic artifact", configure: func(spec *red5XARSpec) {
			spec.cms.embeddedContent = true
		}},
		{name: "encapsulated content mismatch", configure: func(spec *red5XARSpec) { spec.cms.wrongEmbeddedContent = true }},
		{name: "noncanonical CMS certificate set", configure: func(spec *red5XARSpec) { spec.cms.reverseCertificateSet = true }},
		{name: "CMS signature trailing ambiguity", configure: func(spec *red5XARSpec) { spec.cms.signatureSuffix = []byte{0} }},
		{name: "unknown unsigned attribute", configure: func(spec *red5XARSpec) { spec.cms.unknownUnsigned = true }},
		{name: "duplicate timestamp token", configure: func(spec *red5XARSpec) { spec.cms.duplicateTimestamp = true }},
		{name: "malformed timestamp token", configure: func(spec *red5XARSpec) { spec.cms.malformedTimestamp = true }},
		{name: "timestamp imprint does not bind outer signature", configure: func(spec *red5XARSpec) { spec.cms.wrongTimestampImprint = true }},
		{name: "timestamp missing ESS signing-certificate binding", configure: func(spec *red5XARSpec) { spec.cms.missingTimestampESS = true }},
		{name: "timestamp has two TSA signers", configure: func(spec *red5XARSpec) { spec.cms.duplicateTSASigner = true }},
		{name: "timestamp ESS certificate hash mismatch", configure: func(spec *red5XARSpec) { spec.cms.wrongTimestampESS = true }},
		{name: "timestamp content digest mismatch", configure: func(spec *red5XARSpec) { spec.cms.wrongTimestampDigest = true }},
		{name: "timestamp TSA signature invalid", configure: func(spec *red5XARSpec) { spec.cms.wrongTSASignature = true }},
		{name: "timestamp rsaEncryption signature algorithm absent parameters", configure: func(spec *red5XARSpec) {
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampSignatureOID = red5OIDRSAEncryption
		}},
		{name: "timestamp rsaEncryption signature algorithm non-NULL parameters", configure: func(spec *red5XARSpec) {
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampSignatureOID = red5OIDRSAEncryption
			spec.cms.timestampSignatureInvalidParameters = true
		}},
		{name: "timestamp ESS issuerSerial is deferred until authentic profile evidence", configure: func(spec *red5XARSpec) {
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampESSIssuerSerial = true
		}},
		{name: "timestamp TSA certificate missing extended key usage", configure: func(spec *red5XARSpec) {
			tsa := red5MakeTimestampPKI(t, red5TimestampPKIOptions{missingEKU: true})
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampPKI = &tsa
		}},
		{name: "timestamp TSA certificate has noncritical extended key usage", configure: func(spec *red5XARSpec) {
			tsa := red5MakeTimestampPKI(t, red5TimestampPKIOptions{noncriticalEKU: true})
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampPKI = &tsa
		}},
		{name: "timestamp TSA certificate has an additional extended key usage", configure: func(spec *red5XARSpec) {
			tsa := red5MakeTimestampPKI(t, red5TimestampPKIOptions{additionalEKU: true})
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampPKI = &tsa
		}},
		{name: "timestamp optional Accuracy is deferred until an authentic artifact", configure: func(spec *red5XARSpec) {
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampOptionalFields = [][]byte{red5DERWrap(0x30, red5MustASN1(t, 1))}
		}},
		{name: "timestamp optional ordering is deferred until an authentic artifact", configure: func(spec *red5XARSpec) {
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampOptionalFields = [][]byte{red5MustASN1(t, true)}
		}},
		{name: "timestamp optional nonce is deferred until an authentic artifact", configure: func(spec *red5XARSpec) {
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampOptionalFields = [][]byte{red5MustASN1(t, big.NewInt(7))}
		}},
		{name: "timestamp optional TSA name is deferred until an authentic artifact", configure: func(spec *red5XARSpec) {
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampOptionalFields = [][]byte{red5DERWrap(0xa0, red5DERWrap(0xa4, red5DERWrap(0x30, nil)))}
		}},
		{name: "timestamp optional extensions are deferred until an authentic artifact", configure: func(spec *red5XARSpec) {
			spec.cms.unsignedTimestamp = true
			spec.cms.timestampOptionalFields = [][]byte{red5DERWrap(0xa1, red5DERWrap(0x30, nil))}
		}},
		{name: "CMS has unreferenced extra certificate", configure: func(spec *red5XARSpec) {
			spec.cms.certificates = append(append([][]byte(nil), pki.chainDER...), lookalike.leaf.Raw)
		}},
		{name: "actual wrong signer plus correct-looking extra", configure: func(spec *red5XARSpec) {
			spec.chainDER = append(append([][]byte(nil), wrongSigner.chainDER...), pki.leaf.Raw)
			spec.cms.certificates = spec.chainDER
			spec.cms.useSKI = true
			// The CMS is generated with pki's key, but the ordered chain starts with the
			// wrong signer. A parser that merely searches for a correct-looking cert
			// would admit it; actual-signer/first-chain binding must reject it.
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := red5BuildXAR(t, pki, test.configure)
			if _, err := extractVerifiedXARSigner(bytes.NewReader(fixture.archive), int64(len(fixture.archive))); err == nil {
				t.Fatal("ambiguous CMS signer or certificate set accepted")
			}
		})
	}
}
