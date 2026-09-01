package tailscale

import (
	"bytes"
	"compress/zlib"
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	macXARHeaderSize           = 28
	maximumMacXARCompressedTOC = 8 << 20
	maximumMacXARExpandedTOC   = 32 << 20
	maximumMacXARSignature     = 4 << 20
	maximumMacXARCMSPadding    = 1 << 20
	maximumMacXARCertificates  = 16
	maximumMacXARCertificate   = 64 << 10
)

var errMacXARTrust = errors.New("Tailscale macOS PKG signature verification failed")

type xarSignatureKind uint8

const (
	xarSignatureCMS xarSignatureKind = iota + 1
	xarSignatureRSA
)

type xarSignerEvidence struct {
	Kind            xarSignatureKind
	SignedChecksum  []byte
	Signature       []byte
	LegacySignature []byte
	ChainDER        [][]byte
}

type macXARXMLDocument struct {
	XMLName xml.Name       `xml:"xar"`
	TOCs    []macXARXMLTOC `xml:"toc"`
}

type macXARXMLTOC struct {
	Checksums          []macXARXMLChecksum  `xml:"checksum"`
	Signatures         []macXARXMLSignature `xml:"signature"`
	ExtendedSignatures []macXARXMLSignature `xml:"x-signature"`
}

type macXARXMLChecksum struct {
	Style   string   `xml:"style,attr"`
	Offsets []string `xml:"offset"`
	Sizes   []string `xml:"size"`
}

type macXARXMLSignature struct {
	Style    string          `xml:"style,attr"`
	Offsets  []string        `xml:"offset"`
	Sizes    []string        `xml:"size"`
	KeyInfos []macXARKeyInfo `xml:"KeyInfo"`
}

type macXARKeyInfo struct {
	X509Datas []macXARX509Data `xml:"X509Data"`
}

type macXARX509Data struct {
	Certificates []string `xml:"X509Certificate"`
}

type macXARRange struct {
	offset uint64
	size   uint64
}

func extractVerifiedXARSigner(reader io.ReaderAt, size int64) (xarSignerEvidence, error) {
	if reader == nil || size < macXARHeaderSize || size > maximumPKGBytes {
		return xarSignerEvidence{}, errMacXARTrust
	}
	header := make([]byte, macXARHeaderSize)
	if err := readMacXARAt(reader, header, 0); err != nil || string(header[:4]) != "xar!" ||
		binary.BigEndian.Uint16(header[4:6]) != macXARHeaderSize || binary.BigEndian.Uint16(header[6:8]) != 1 ||
		binary.BigEndian.Uint32(header[24:28]) != 1 {
		return xarSignerEvidence{}, errMacXARTrust
	}
	compressedSize := binary.BigEndian.Uint64(header[8:16])
	expandedSize := binary.BigEndian.Uint64(header[16:24])
	if compressedSize == 0 || compressedSize > maximumMacXARCompressedTOC || expandedSize == 0 || expandedSize > maximumMacXARExpandedTOC {
		return xarSignerEvidence{}, errMacXARTrust
	}
	heapStart, ok := checkedMacXARAdd(macXARHeaderSize, compressedSize)
	if !ok || heapStart > uint64(size) {
		return xarSignerEvidence{}, errMacXARTrust
	}
	compressed := make([]byte, int(compressedSize))
	if err := readMacXARAt(reader, compressed, macXARHeaderSize); err != nil {
		return xarSignerEvidence{}, errMacXARTrust
	}
	expanded, err := expandMacXARTOC(compressed, expandedSize)
	if err != nil {
		return xarSignerEvidence{}, errMacXARTrust
	}
	document, err := parseMacXARTOC(expanded)
	if err != nil {
		return xarSignerEvidence{}, errMacXARTrust
	}
	toc := document.TOCs[0]
	checksumRange, err := parseMacXARRange(toc.Checksums[0].Offsets, toc.Checksums[0].Sizes, sha1.Size)
	if err != nil || toc.Checksums[0].Style != "sha1" {
		return xarSignerEvidence{}, errMacXARTrust
	}
	legacyXML := toc.Signatures[0]
	cmsXML := toc.ExtendedSignatures[0]
	legacyRange, err := parseMacXARRange(legacyXML.Offsets, legacyXML.Sizes, 0)
	if err != nil || legacyXML.Style != "RSA" || legacyRange.size == 0 || legacyRange.size > maximumMacXARSignature {
		return xarSignerEvidence{}, errMacXARTrust
	}
	cmsRange, err := parseMacXARRange(cmsXML.Offsets, cmsXML.Sizes, 0)
	if err != nil || cmsXML.Style != "CMS" || cmsRange.size == 0 || cmsRange.size > maximumMacXARSignature ||
		macXARRangesOverlap(checksumRange, legacyRange) || macXARRangesOverlap(checksumRange, cmsRange) ||
		macXARRangesOverlap(legacyRange, cmsRange) {
		return xarSignerEvidence{}, errMacXARTrust
	}
	heapSize := uint64(size) - heapStart
	if !macXARRangeWithin(checksumRange, heapSize) || !macXARRangeWithin(legacyRange, heapSize) || !macXARRangeWithin(cmsRange, heapSize) {
		return xarSignerEvidence{}, errMacXARTrust
	}
	checksum := make([]byte, checksumRange.size)
	if err := readMacXARAt(reader, checksum, int64(heapStart+checksumRange.offset)); err != nil {
		return xarSignerEvidence{}, errMacXARTrust
	}
	wantChecksum := sha1.Sum(compressed)
	if !bytes.Equal(checksum, wantChecksum[:]) {
		return xarSignerEvidence{}, errMacXARTrust
	}
	legacySignature := make([]byte, legacyRange.size)
	if err := readMacXARAt(reader, legacySignature, int64(heapStart+legacyRange.offset)); err != nil {
		return xarSignerEvidence{}, errMacXARTrust
	}
	cmsSignature := make([]byte, cmsRange.size)
	if err := readMacXARAt(reader, cmsSignature, int64(heapStart+cmsRange.offset)); err != nil {
		return xarSignerEvidence{}, errMacXARTrust
	}
	legacyChain, legacyCertificates, err := parseMacXARCertificateChain(legacyXML.KeyInfos[0].X509Datas[0].Certificates)
	if err != nil {
		return xarSignerEvidence{}, errMacXARTrust
	}
	cmsChain, cmsCertificates, err := parseMacXARCertificateChain(cmsXML.KeyInfos[0].X509Datas[0].Certificates)
	if err != nil || !sameOrderedMacXARChain(legacyChain, cmsChain) ||
		verifyMacXARRSASignature(checksum, legacySignature, legacyCertificates[0]) != nil ||
		verifyMacXARCMSSignature(checksum, cmsSignature, cmsChain, cmsCertificates) != nil {
		return xarSignerEvidence{}, errMacXARTrust
	}
	return xarSignerEvidence{
		Kind:            xarSignatureCMS,
		SignedChecksum:  append([]byte(nil), checksum...),
		Signature:       append([]byte(nil), cmsSignature...),
		LegacySignature: append([]byte(nil), legacySignature...),
		ChainDER:        cloneMacXARDER(cmsChain),
	}, nil
}

func readMacXARAt(reader io.ReaderAt, destination []byte, offset int64) error {
	if offset < 0 {
		return errMacXARTrust
	}
	read, err := reader.ReadAt(destination, offset)
	if read != len(destination) || err != nil {
		return errMacXARTrust
	}
	return nil
}

func checkedMacXARAdd(left int, right uint64) (uint64, bool) {
	if left < 0 || right > math.MaxUint64-uint64(left) {
		return 0, false
	}
	return uint64(left) + right, true
}

func expandMacXARTOC(compressed []byte, expected uint64) ([]byte, error) {
	if len(compressed) == 0 || expected == 0 || expected > maximumMacXARExpandedTOC {
		return nil, errMacXARTrust
	}
	input := bytes.NewReader(compressed)
	reader, err := zlib.NewReader(input)
	if err != nil {
		return nil, errMacXARTrust
	}
	limited := io.LimitReader(reader, int64(expected)+1)
	expanded, readErr := io.ReadAll(limited)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || uint64(len(expanded)) != expected || input.Len() != 0 {
		return nil, errMacXARTrust
	}
	return expanded, nil
}

func parseMacXARTOC(value []byte) (macXARXMLDocument, error) {
	if len(value) == 0 || len(value) > maximumMacXARExpandedTOC || bytes.Contains(value, []byte("<!")) ||
		!validMacXARAuthorityGrammar(value) {
		return macXARXMLDocument{}, errMacXARTrust
	}
	decoder := xml.NewDecoder(bytes.NewReader(value))
	decoder.Strict = true
	var document macXARXMLDocument
	if err := decoder.Decode(&document); err != nil || document.XMLName.Local != "xar" || document.XMLName.Space != "" ||
		len(document.TOCs) != 1 || len(document.TOCs[0].Checksums) != 1 || len(document.TOCs[0].Signatures) != 1 ||
		len(document.TOCs[0].ExtendedSignatures) != 1 ||
		!validMacXARSignatureXML(document.TOCs[0].Signatures[0]) ||
		!validMacXARSignatureXML(document.TOCs[0].ExtendedSignatures[0]) ||
		!validMacXARSignatureElementOrder(value) {
		return macXARXMLDocument{}, errMacXARTrust
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return macXARXMLDocument{}, errMacXARTrust
		}
		if characters, ok := token.(xml.CharData); !ok || strings.TrimSpace(string(characters)) != "" {
			return macXARXMLDocument{}, errMacXARTrust
		}
	}
	return document, nil
}

const macXARXMLDSIGNamespace = "http://www.w3.org/2000/09/xmldsig#"

type macXARXMLFrameKind uint8

const (
	macXARXMLRootFrame macXARXMLFrameKind = iota + 1
	macXARXMLTOCFrame
	macXARXMLChecksumFrame
	macXARXMLSignatureFrame
	macXARXMLKeyInfoFrame
	macXARXMLX509DataFrame
	macXARXMLCertificateFrame
	macXARXMLScalarFrame
	macXARXMLInventoryFrame
)

type macXARXMLFrame struct {
	kind     macXARXMLFrameKind
	children int
}

func validMacXARAuthorityGrammar(value []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(value))
	decoder.Strict = true
	stack := make([]macXARXMLFrame, 0, 8)
	rootSeen := false
	rootClosed := false
	xmlDeclarationSeen := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return false
		}
		switch typed := token.(type) {
		case xml.ProcInst:
			if rootSeen || xmlDeclarationSeen || typed.Target != "xml" {
				return false
			}
			xmlDeclarationSeen = true
		case xml.Directive, xml.Comment:
			return false
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(typed)) != "" {
					return false
				}
				continue
			}
			switch stack[len(stack)-1].kind {
			case macXARXMLScalarFrame, macXARXMLCertificateFrame, macXARXMLInventoryFrame:
			default:
				if strings.TrimSpace(string(typed)) != "" {
					return false
				}
			}
		case xml.StartElement:
			if rootClosed {
				return false
			}
			kind, ok := nextMacXARXMLFrame(typed, stack)
			if !ok {
				return false
			}
			if len(stack) == 0 {
				if rootSeen {
					return false
				}
				rootSeen = true
			} else {
				stack[len(stack)-1].children++
			}
			stack = append(stack, macXARXMLFrame{kind: kind})
		case xml.EndElement:
			if len(stack) == 0 || !validCompletedMacXARXMLFrame(stack[len(stack)-1]) {
				return false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				rootClosed = true
			}
		default:
			return false
		}
	}
	return rootSeen && rootClosed && len(stack) == 0
}

func nextMacXARXMLFrame(element xml.StartElement, stack []macXARXMLFrame) (macXARXMLFrameKind, bool) {
	if len(stack) == 0 {
		return macXARXMLRootFrame, element.Name.Space == "" && element.Name.Local == "xar" && len(element.Attr) == 0
	}
	parent := stack[len(stack)-1]
	switch parent.kind {
	case macXARXMLRootFrame:
		return macXARXMLTOCFrame, parent.children == 0 && element.Name.Space == "" && element.Name.Local == "toc" && len(element.Attr) == 0
	case macXARXMLTOCFrame:
		switch {
		case element.Name.Space == "" && element.Name.Local == "checksum" && validMacXARXMLStyleAttribute(element.Attr):
			return macXARXMLChecksumFrame, true
		case element.Name.Space == "" && (element.Name.Local == "signature" || element.Name.Local == "x-signature") &&
			validMacXARXMLStyleAttribute(element.Attr):
			return macXARXMLSignatureFrame, true
		case element.Name.Space == "" && element.Name.Local == "file" && validMacXARXMLInventoryAttributes(element.Attr):
			return macXARXMLInventoryFrame, true
		default:
			return 0, false
		}
	case macXARXMLChecksumFrame:
		if element.Name.Space != "" || len(element.Attr) != 0 ||
			parent.children == 0 && element.Name.Local != "offset" || parent.children == 1 && element.Name.Local != "size" || parent.children > 1 {
			return 0, false
		}
		return macXARXMLScalarFrame, true
	case macXARXMLSignatureFrame:
		switch parent.children {
		case 0:
			return macXARXMLScalarFrame, element.Name.Space == "" && element.Name.Local == "offset" && len(element.Attr) == 0
		case 1:
			return macXARXMLScalarFrame, element.Name.Space == "" && element.Name.Local == "size" && len(element.Attr) == 0
		case 2:
			return macXARXMLKeyInfoFrame, element.Name.Space == macXARXMLDSIGNamespace && element.Name.Local == "KeyInfo" &&
				validMacXARXMLNamespaceDeclaration(element.Attr)
		default:
			return 0, false
		}
	case macXARXMLKeyInfoFrame:
		return macXARXMLX509DataFrame, parent.children == 0 && element.Name.Space == macXARXMLDSIGNamespace &&
			element.Name.Local == "X509Data" && len(element.Attr) == 0
	case macXARXMLX509DataFrame:
		return macXARXMLCertificateFrame, element.Name.Space == macXARXMLDSIGNamespace &&
			element.Name.Local == "X509Certificate" && len(element.Attr) == 0
	case macXARXMLInventoryFrame:
		if element.Name.Space != "" || !validMacXARXMLInventoryAttributes(element.Attr) {
			return 0, false
		}
		return macXARXMLInventoryFrame, true
	default:
		return 0, false
	}
}

func validCompletedMacXARXMLFrame(frame macXARXMLFrame) bool {
	switch frame.kind {
	case macXARXMLRootFrame:
		return frame.children == 1
	case macXARXMLChecksumFrame:
		return frame.children == 2
	case macXARXMLSignatureFrame:
		return frame.children == 3
	case macXARXMLKeyInfoFrame:
		return frame.children == 1
	case macXARXMLX509DataFrame:
		return frame.children > 0
	case macXARXMLCertificateFrame, macXARXMLScalarFrame:
		return frame.children == 0
	default:
		return true
	}
}

func validMacXARXMLStyleAttribute(attributes []xml.Attr) bool {
	return len(attributes) == 1 && attributes[0].Name.Space == "" && attributes[0].Name.Local == "style"
}

func validMacXARXMLNamespaceDeclaration(attributes []xml.Attr) bool {
	if len(attributes) != 1 || attributes[0].Value != macXARXMLDSIGNamespace {
		return false
	}
	return attributes[0].Name.Space == "" && attributes[0].Name.Local == "xmlns" || attributes[0].Name.Space == "xmlns"
}

func validMacXARXMLInventoryAttributes(attributes []xml.Attr) bool {
	for _, attribute := range attributes {
		if attribute.Name.Space != "" || attribute.Name.Local == "xmlns" {
			return false
		}
	}
	return true
}

func validMacXARSignatureXML(signature macXARXMLSignature) bool {
	return len(signature.KeyInfos) == 1 && len(signature.KeyInfos[0].X509Datas) == 1
}

func validMacXARSignatureElementOrder(value []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(value))
	decoder.Strict = true
	depth := 0
	insideTOC := false
	signatures := make([]string, 0, 2)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return false
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if depth == 1 && typed.Name.Space == "" && typed.Name.Local == "toc" {
				insideTOC = true
			}
			if insideTOC && depth == 2 && typed.Name.Space == "" &&
				(typed.Name.Local == "signature" || typed.Name.Local == "x-signature") {
				signatures = append(signatures, typed.Name.Local)
			} else if insideTOC && depth == 2 && strings.Contains(strings.ToLower(typed.Name.Local), "signature") {
				return false
			}
			depth++
		case xml.EndElement:
			depth--
			if insideTOC && depth == 1 && typed.Name.Space == "" && typed.Name.Local == "toc" {
				insideTOC = false
			}
		}
	}
	return len(signatures) == 2 && signatures[0] == "signature" && signatures[1] == "x-signature"
}

func parseMacXARRange(offsets, sizes []string, exactSize uint64) (macXARRange, error) {
	if len(offsets) != 1 || len(sizes) != 1 || offsets[0] == "" || sizes[0] == "" ||
		offsets[0] != strings.TrimSpace(offsets[0]) || sizes[0] != strings.TrimSpace(sizes[0]) ||
		!canonicalMacXARDecimal(offsets[0]) || !canonicalMacXARDecimal(sizes[0]) {
		return macXARRange{}, errMacXARTrust
	}
	offset, err := strconv.ParseUint(offsets[0], 10, 64)
	if err != nil {
		return macXARRange{}, errMacXARTrust
	}
	size, err := strconv.ParseUint(sizes[0], 10, 64)
	if err != nil || exactSize != 0 && size != exactSize {
		return macXARRange{}, errMacXARTrust
	}
	return macXARRange{offset: offset, size: size}, nil
}

func canonicalMacXARDecimal(value string) bool {
	if len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}

func macXARRangeWithin(value macXARRange, limit uint64) bool {
	return value.offset <= limit && value.size <= limit-value.offset
}

func macXARRangesOverlap(left, right macXARRange) bool {
	leftEnd, leftOK := checkedMacXARRangeEnd(left)
	rightEnd, rightOK := checkedMacXARRangeEnd(right)
	return !leftOK || !rightOK || left.offset < rightEnd && right.offset < leftEnd
}

func checkedMacXARRangeEnd(value macXARRange) (uint64, bool) {
	if value.size > math.MaxUint64-value.offset {
		return 0, false
	}
	return value.offset + value.size, true
}

func parseMacXARCertificateChain(encoded []string) ([][]byte, []*x509.Certificate, error) {
	return parseMacXARCertificateChainWithTerminalVerifier(encoded, verifyPinnedAppleRootSelfSignature)
}

func parseMacXARCertificateChainWithTerminalVerifier(
	encoded []string,
	verifyTerminal packageTerminalRootVerifier,
) ([][]byte, []*x509.Certificate, error) {
	if verifyTerminal == nil || len(encoded) != 3 || len(encoded) > maximumMacXARCertificates {
		return nil, nil, errMacXARTrust
	}
	chain := make([][]byte, 0, len(encoded))
	certificates := make([]*x509.Certificate, 0, len(encoded))
	seen := map[[32]byte]struct{}{}
	for _, raw := range encoded {
		cleaned, ok := cleanMacXARBase64(raw)
		if !ok || len(cleaned) > base64.StdEncoding.EncodedLen(maximumMacXARCertificate) {
			return nil, nil, errMacXARTrust
		}
		der, err := base64.StdEncoding.Strict().DecodeString(cleaned)
		if err != nil || len(der) == 0 || len(der) > maximumMacXARCertificate {
			return nil, nil, errMacXARTrust
		}
		fingerprint := sha256.Sum256(der)
		if _, duplicate := seen[fingerprint]; duplicate {
			return nil, nil, errMacXARTrust
		}
		seen[fingerprint] = struct{}{}
		certificate, err := x509.ParseCertificate(der)
		if err != nil || !bytes.Equal(certificate.Raw, der) {
			return nil, nil, errMacXARTrust
		}
		chain = append(chain, append([]byte(nil), der...))
		certificates = append(certificates, certificate)
	}
	for index := 0; index+1 < len(certificates); index++ {
		if certificates[index].CheckSignatureFrom(certificates[index+1]) != nil {
			return nil, nil, errMacXARTrust
		}
	}
	terminalIndex := len(certificates) - 1
	if verifyTerminal(certificates[terminalIndex], sha256.Sum256(chain[terminalIndex])) != nil {
		return nil, nil, errMacXARTrust
	}
	return chain, certificates, nil
}

func cleanMacXARBase64(value string) (string, bool) {
	var result strings.Builder
	result.Grow(len(value))
	for _, character := range value {
		switch character {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			if character > 0x7f {
				return "", false
			}
			result.WriteRune(character)
		}
	}
	return result.String(), result.Len() != 0
}

func verifyMacXARRSASignature(checksum, signature []byte, signer *x509.Certificate) error {
	publicKey, ok := signer.PublicKey.(*rsa.PublicKey)
	if !ok || len(signature) != publicKey.Size() || len(checksum) != sha1.Size {
		return errMacXARTrust
	}
	if rsa.VerifyPKCS1v15(publicKey, crypto.SHA1, checksum, signature) != nil {
		return errMacXARTrust
	}
	return nil
}

func packageCMSDataOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
}

func packageCMSSignedDataOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
}

func packageSHA256OID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
}

func packageSHA1OID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
}

func packageSHA1RSAOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
}

func packageSHA256RSAOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
}

func packageRSAEncryptionOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
}

func packageCMSContentTypeOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
}

func packageCMSMessageDigestOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
}

func packageCMSTimestampTokenOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14}
}

func packageCMSTSTInfoOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
}

func packageCMSSigningCertificateOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 12}
}

func packageCMSSigningCertificateV2OID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 47}
}

func packageExtendedKeyUsageOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{2, 5, 29, 37}
}

func packageTimeStampingEKUOID() asn1.ObjectIdentifier {
	return asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 8}
}

type macCMSAlgorithm struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type macCMSEncapsulatedContent struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"optional"`
}

type macCMSSigner struct {
	Version            int
	SID                asn1.RawValue
	DigestAlgorithm    macCMSAlgorithm
	SignedAttributes   asn1.RawValue `asn1:"optional,tag:0"`
	SignatureAlgorithm macCMSAlgorithm
	Signature          []byte
	UnsignedAttributes asn1.RawValue `asn1:"optional,tag:1"`
}

type macCMSSignedData struct {
	Version          int
	DigestAlgorithms []macCMSAlgorithm `asn1:"set"`
	Content          macCMSEncapsulatedContent
	Certificates     asn1.RawValue  `asn1:"optional,tag:0"`
	CRLs             asn1.RawValue  `asn1:"optional,tag:1"`
	SignerInfos      []macCMSSigner `asn1:"set"`
}

type macCMSContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue
}

type macCMSIssuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type macCMSAttribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue
}

func verifyMacXARCMSSignature(checksum, cmsDER []byte, orderedDER [][]byte, ordered []*x509.Certificate) error {
	var contentInfo macCMSContentInfo
	rest, err := asn1.Unmarshal(cmsDER, &contentInfo)
	canonicalContentInfo, marshalErr := asn1.Marshal(contentInfo)
	objectLength := len(cmsDER) - len(rest)
	if err != nil || objectLength < 0 || marshalErr != nil || !bytes.Equal(canonicalContentInfo, cmsDER[:objectLength]) ||
		!validMacXARCMSPadding(rest) || !contentInfo.ContentType.Equal(packageCMSSignedDataOID()) ||
		contentInfo.Content.Class != 2 || contentInfo.Content.Tag != 0 || !contentInfo.Content.IsCompound {
		return errMacXARTrust
	}
	var signedData macCMSSignedData
	rest, err = asn1.Unmarshal(contentInfo.Content.Bytes, &signedData)
	if err != nil || len(rest) != 0 || len(signedData.DigestAlgorithms) != 1 ||
		!validMacCMSAlgorithm(signedData.DigestAlgorithms[0], packageSHA1OID()) || len(signedData.SignerInfos) != 1 ||
		!signedData.Content.ContentType.Equal(packageCMSDataOID()) || len(signedData.Content.Content.FullBytes) != 0 ||
		len(signedData.CRLs.FullBytes) != 0 {
		return errMacXARTrust
	}
	embeddedDER, err := parseMacCMSCertificates(signedData.Certificates)
	if err != nil || !sameMacXARCertificateSet(embeddedDER, orderedDER) {
		return errMacXARTrust
	}
	signerInfo := signedData.SignerInfos[0]
	if !validMacCMSAlgorithm(signerInfo.DigestAlgorithm, packageSHA1OID()) ||
		!validMacCMSSignatureAlgorithm(signerInfo.SignatureAlgorithm) ||
		len(signerInfo.SignedAttributes.FullBytes) == 0 ||
		validateMacCMSUnsignedAttributes(signerInfo.UnsignedAttributes, signerInfo.Signature) != nil {
		return errMacXARTrust
	}
	if !validMacCMSVersions(signedData.Version, signerInfo.Version, signerInfo.SID) {
		return errMacXARTrust
	}
	signerIndex, err := selectMacCMSSigner(signerInfo.SID, ordered)
	if err != nil || signerIndex != 0 {
		return errMacXARTrust
	}
	publicKey, ok := ordered[signerIndex].PublicKey.(*rsa.PublicKey)
	if !ok || len(signerInfo.Signature) != publicKey.Size() {
		return errMacXARTrust
	}
	if signerInfo.SignedAttributes.Class != 2 || signerInfo.SignedAttributes.Tag != 0 || !signerInfo.SignedAttributes.IsCompound ||
		validateMacCMSSignedAttributes(signerInfo.SignedAttributes.Bytes, checksum, crypto.SHA1) != nil {
		return errMacXARTrust
	}
	signedInput := wrapMacCMSDERSet(signerInfo.SignedAttributes.Bytes)
	digest := sha1.Sum(signedInput)
	if rsa.VerifyPKCS1v15(publicKey, crypto.SHA1, digest[:], signerInfo.Signature) != nil {
		return errMacXARTrust
	}
	return nil
}

func validMacCMSAlgorithm(value macCMSAlgorithm, expected asn1.ObjectIdentifier) bool {
	if !value.Algorithm.Equal(expected) {
		return false
	}
	if len(value.Parameters.FullBytes) == 0 {
		return true
	}
	return value.Parameters.Class == 0 && value.Parameters.Tag == asn1.TagNull && len(value.Parameters.Bytes) == 0 &&
		bytes.Equal(value.Parameters.FullBytes, []byte{0x05, 0x00})
}

func validMacCMSSignatureAlgorithm(value macCMSAlgorithm) bool {
	return value.Algorithm.Equal(packageSHA1RSAOID()) && bytes.Equal(value.Parameters.FullBytes, []byte{0x05, 0x00})
}

func validMacXARCMSPadding(value []byte) bool {
	if len(value) > maximumMacXARCMSPadding {
		return false
	}
	for _, octet := range value {
		if octet != 0 {
			return false
		}
	}
	return true
}

func parseMacCMSCertificates(value asn1.RawValue) ([][]byte, error) {
	if value.Class != 2 || value.Tag != 0 || !value.IsCompound || len(value.Bytes) == 0 {
		return nil, errMacXARTrust
	}
	remaining := value.Bytes
	certificates := make([][]byte, 0, 3)
	seen := map[[32]byte]struct{}{}
	var previous []byte
	for len(remaining) != 0 {
		var raw asn1.RawValue
		next, err := asn1.Unmarshal(remaining, &raw)
		if err != nil || len(raw.FullBytes) == 0 || raw.Class != 0 || raw.Tag != asn1.TagSequence || len(raw.FullBytes) > maximumMacXARCertificate ||
			previous != nil && bytes.Compare(previous, raw.FullBytes) >= 0 {
			return nil, errMacXARTrust
		}
		fingerprint := sha256.Sum256(raw.FullBytes)
		if _, duplicate := seen[fingerprint]; duplicate {
			return nil, errMacXARTrust
		}
		seen[fingerprint] = struct{}{}
		certificates = append(certificates, append([]byte(nil), raw.FullBytes...))
		previous = append(previous[:0], raw.FullBytes...)
		if len(certificates) > maximumMacXARCertificates || len(next) >= len(remaining) {
			return nil, errMacXARTrust
		}
		remaining = next
	}
	return certificates, nil
}

func validMacCMSVersions(signedDataVersion, signerVersion int, sid asn1.RawValue) bool {
	return signedDataVersion == 1 && signerVersion == 1 && sid.Class == 0 && sid.Tag == asn1.TagSequence && sid.IsCompound
}

func sameMacXARCertificateSet(left, right [][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	leftFingerprints := make([][32]byte, len(left))
	rightFingerprints := make([][32]byte, len(right))
	for index := range left {
		leftFingerprints[index] = sha256.Sum256(left[index])
		rightFingerprints[index] = sha256.Sum256(right[index])
	}
	sort.Slice(leftFingerprints, func(i, j int) bool { return bytes.Compare(leftFingerprints[i][:], leftFingerprints[j][:]) < 0 })
	sort.Slice(rightFingerprints, func(i, j int) bool { return bytes.Compare(rightFingerprints[i][:], rightFingerprints[j][:]) < 0 })
	for index := range leftFingerprints {
		if leftFingerprints[index] != rightFingerprints[index] {
			return false
		}
	}
	return true
}

func selectMacCMSSigner(sid asn1.RawValue, certificates []*x509.Certificate) (int, error) {
	matches := make([]int, 0, 1)
	switch {
	case sid.Class == 0 && sid.Tag == asn1.TagSequence:
		var issuerAndSerial macCMSIssuerAndSerial
		rest, err := asn1.Unmarshal(sid.FullBytes, &issuerAndSerial)
		canonical, marshalErr := asn1.Marshal(issuerAndSerial)
		if err != nil || len(rest) != 0 || issuerAndSerial.Serial == nil || issuerAndSerial.Serial.Sign() <= 0 || marshalErr != nil ||
			!bytes.Equal(canonical, sid.FullBytes) {
			return 0, errMacXARTrust
		}
		for index, certificate := range certificates {
			if certificate.SerialNumber.Cmp(issuerAndSerial.Serial) == 0 && bytes.Equal(certificate.RawIssuer, issuerAndSerial.Issuer.FullBytes) {
				matches = append(matches, index)
			}
		}
	case sid.Class == 2 && sid.Tag == 0 && !sid.IsCompound && len(sid.Bytes) != 0:
		for index, certificate := range certificates {
			if bytes.Equal(certificate.SubjectKeyId, sid.Bytes) {
				matches = append(matches, index)
			}
		}
	default:
		return 0, errMacXARTrust
	}
	if len(matches) != 1 {
		return 0, errMacXARTrust
	}
	return matches[0], nil
}

func validateMacCMSSignedAttributes(value, checksum []byte, digestAlgorithm crypto.Hash) error {
	if digestAlgorithm != crypto.SHA1 {
		return errMacXARTrust
	}
	remaining := value
	seen := map[string]struct{}{}
	contentTypeSeen := false
	messageDigestSeen := false
	previous := []byte(nil)
	for len(remaining) != 0 {
		var raw asn1.RawValue
		next, err := asn1.Unmarshal(remaining, &raw)
		if err != nil || len(raw.FullBytes) == 0 || raw.Class != 0 || raw.Tag != asn1.TagSequence ||
			previous != nil && bytes.Compare(previous, raw.FullBytes) >= 0 {
			return errMacXARTrust
		}
		var attribute macCMSAttribute
		rest, err := asn1.Unmarshal(raw.FullBytes, &attribute)
		if err != nil || len(rest) != 0 || attribute.Values.Class != 0 || attribute.Values.Tag != asn1.TagSet || !attribute.Values.IsCompound {
			return errMacXARTrust
		}
		key := attribute.Type.String()
		if _, duplicate := seen[key]; duplicate {
			return errMacXARTrust
		}
		seen[key] = struct{}{}
		switch {
		case attribute.Type.Equal(packageCMSContentTypeOID()):
			var contentType asn1.ObjectIdentifier
			rest, err = asn1.Unmarshal(attribute.Values.Bytes, &contentType)
			if err != nil || len(rest) != 0 || !contentType.Equal(packageCMSDataOID()) {
				return errMacXARTrust
			}
			contentTypeSeen = true
		case attribute.Type.Equal(packageCMSMessageDigestOID()):
			var digest []byte
			rest, err = asn1.Unmarshal(attribute.Values.Bytes, &digest)
			want := sha1.Sum(checksum)
			if err != nil || len(rest) != 0 || !bytes.Equal(digest, want[:]) {
				return errMacXARTrust
			}
			messageDigestSeen = true
		default:
			return errMacXARTrust
		}
		previous = append(previous[:0], raw.FullBytes...)
		if len(next) >= len(remaining) {
			return errMacXARTrust
		}
		remaining = next
	}
	if !contentTypeSeen || !messageDigestSeen {
		return errMacXARTrust
	}
	return nil
}

func validateMacCMSUnsignedAttributes(value asn1.RawValue, outerSignature []byte) error {
	if len(value.FullBytes) == 0 {
		return nil
	}
	if len(outerSignature) == 0 || value.Class != 2 || value.Tag != 1 || !value.IsCompound || len(value.Bytes) == 0 {
		return errMacXARTrust
	}
	var rawAttribute asn1.RawValue
	rest, err := asn1.Unmarshal(value.Bytes, &rawAttribute)
	if err != nil || len(rest) != 0 || rawAttribute.Class != 0 || rawAttribute.Tag != asn1.TagSequence {
		return errMacXARTrust
	}
	var attribute macCMSAttribute
	rest, err = asn1.Unmarshal(rawAttribute.FullBytes, &attribute)
	canonicalAttribute, marshalErr := asn1.Marshal(attribute)
	if err != nil || len(rest) != 0 || !attribute.Type.Equal(packageCMSTimestampTokenOID()) ||
		marshalErr != nil || !bytes.Equal(canonicalAttribute, rawAttribute.FullBytes) ||
		attribute.Values.Class != 0 || attribute.Values.Tag != asn1.TagSet || !attribute.Values.IsCompound {
		return errMacXARTrust
	}
	var rawTimestampToken asn1.RawValue
	rest, err = asn1.Unmarshal(attribute.Values.Bytes, &rawTimestampToken)
	if err != nil || len(rest) != 0 || rawTimestampToken.Class != 0 || rawTimestampToken.Tag != asn1.TagSequence {
		return errMacXARTrust
	}
	return validateMacTimestampToken(rawTimestampToken.FullBytes, outerSignature)
}

func validateMacTimestampToken(tokenDER, outerSignature []byte) error {
	var timestampToken macCMSContentInfo
	rest, err := asn1.Unmarshal(tokenDER, &timestampToken)
	canonicalTimestampToken, marshalErr := asn1.Marshal(timestampToken)
	if err != nil || len(rest) != 0 || !timestampToken.ContentType.Equal(packageCMSSignedDataOID()) ||
		marshalErr != nil || !bytes.Equal(canonicalTimestampToken, tokenDER) ||
		timestampToken.Content.Class != 2 || timestampToken.Content.Tag != 0 || !timestampToken.Content.IsCompound {
		return errMacXARTrust
	}
	var signedData macCMSSignedData
	rest, err = asn1.Unmarshal(timestampToken.Content.Bytes, &signedData)
	if err != nil || len(rest) != 0 || signedData.Version != 3 || len(signedData.DigestAlgorithms) != 1 ||
		!validMacCMSAlgorithm(signedData.DigestAlgorithms[0], packageSHA256OID()) ||
		!signedData.Content.ContentType.Equal(packageCMSTSTInfoOID()) ||
		signedData.Content.Content.Class != 2 || signedData.Content.Content.Tag != 0 || !signedData.Content.Content.IsCompound ||
		len(signedData.CRLs.FullBytes) != 0 || len(signedData.SignerInfos) != 1 {
		return errMacXARTrust
	}
	var tstInfoDER []byte
	rest, err = asn1.Unmarshal(signedData.Content.Content.Bytes, &tstInfoDER)
	if err != nil || len(rest) != 0 || validateMacTimestampInfo(tstInfoDER, outerSignature) != nil {
		return errMacXARTrust
	}
	certificateDER, err := parseMacCMSCertificates(signedData.Certificates)
	if err != nil || len(certificateDER) == 0 {
		return errMacXARTrust
	}
	certificates := make([]*x509.Certificate, len(certificateDER))
	for index, der := range certificateDER {
		certificate, parseErr := x509.ParseCertificate(der)
		if parseErr != nil || !bytes.Equal(certificate.Raw, der) {
			return errMacXARTrust
		}
		certificates[index] = certificate
	}
	signer := signedData.SignerInfos[0]
	if !validMacCMSAlgorithm(signer.DigestAlgorithm, packageSHA256OID()) ||
		!validMacTimestampSignatureAlgorithm(signer.SignatureAlgorithm) || len(signer.UnsignedAttributes.FullBytes) != 0 ||
		len(signer.SignedAttributes.FullBytes) == 0 || signer.SignedAttributes.Class != 2 || signer.SignedAttributes.Tag != 0 ||
		!signer.SignedAttributes.IsCompound || !validMacTimestampSignerVersion(signer.Version, signer.SID) {
		return errMacXARTrust
	}
	signerIndex, err := selectMacCMSSigner(signer.SID, certificates)
	if err != nil || !validMacTimestampSignerCertificate(certificates[signerIndex]) ||
		validateMacTimestampSignedAttributes(signer.SignedAttributes.Bytes, tstInfoDER, certificateDER[signerIndex]) != nil {
		return errMacXARTrust
	}
	publicKey, ok := certificates[signerIndex].PublicKey.(*rsa.PublicKey)
	if !ok || len(signer.Signature) != publicKey.Size() {
		return errMacXARTrust
	}
	digest := sha256.Sum256(wrapMacCMSDERSet(signer.SignedAttributes.Bytes))
	if rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signer.Signature) != nil {
		return errMacXARTrust
	}
	return nil
}

func validMacTimestampSignerCertificate(certificate *x509.Certificate) bool {
	if certificate == nil || certificate.IsCA || certificate.KeyUsage&x509.KeyUsageDigitalSignature == 0 ||
		len(certificate.ExtKeyUsage) != 1 || certificate.ExtKeyUsage[0] != x509.ExtKeyUsageTimeStamping ||
		len(certificate.UnknownExtKeyUsage) != 0 {
		return false
	}
	found := false
	for _, extension := range certificate.Extensions {
		if !extension.Id.Equal(packageExtendedKeyUsageOID()) {
			continue
		}
		if found || !extension.Critical {
			return false
		}
		found = true
		var usages []asn1.ObjectIdentifier
		rest, err := asn1.Unmarshal(extension.Value, &usages)
		canonical, marshalErr := asn1.Marshal(usages)
		if err != nil || len(rest) != 0 || marshalErr != nil || !bytes.Equal(canonical, extension.Value) ||
			len(usages) != 1 || !usages[0].Equal(packageTimeStampingEKUOID()) {
			return false
		}
	}
	return found
}

func validMacTimestampSignerVersion(version int, sid asn1.RawValue) bool {
	return version == 1 && sid.Class == 0 && sid.Tag == asn1.TagSequence ||
		version == 3 && sid.Class == 2 && sid.Tag == 0 && !sid.IsCompound
}

func validMacTimestampSignatureAlgorithm(value macCMSAlgorithm) bool {
	if validMacCMSAlgorithm(value, packageSHA256RSAOID()) {
		return true
	}
	return value.Algorithm.Equal(packageRSAEncryptionOID()) && bytes.Equal(value.Parameters.FullBytes, []byte{0x05, 0x00})
}

func validateMacTimestampInfo(value, outerSignature []byte) error {
	var sequence asn1.RawValue
	rest, err := asn1.Unmarshal(value, &sequence)
	if err != nil || len(rest) != 0 || sequence.Class != 0 || sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
		return errMacXARTrust
	}
	fields, err := parseMacASN1Children(sequence.Bytes, 10)
	if err != nil || len(fields) != 5 {
		return errMacXARTrust
	}
	var version int
	var policy asn1.ObjectIdentifier
	var imprint struct {
		HashAlgorithm macCMSAlgorithm
		HashedMessage []byte
	}
	var generatedAt time.Time
	serial, serialOK := parsePositiveMacASN1Integer(fields[3])
	if !unmarshalCanonicalMacASN1(fields[0], &version) || version != 1 ||
		!unmarshalCanonicalMacASN1(fields[1], &policy) || len(policy) == 0 ||
		!unmarshalCanonicalMacASN1(fields[2], &imprint) || !validMacCMSAlgorithm(imprint.HashAlgorithm, packageSHA256OID()) ||
		!serialOK || serial.Sign() <= 0 ||
		fields[4].Class != 0 || fields[4].Tag != asn1.TagGeneralizedTime {
		return errMacXARTrust
	}
	rest, err = asn1.UnmarshalWithParams(fields[4].FullBytes, &generatedAt, "generalized")
	canonicalTime, marshalErr := asn1.MarshalWithParams(generatedAt, "generalized")
	wantImprint := sha256.Sum256(outerSignature)
	if err != nil || len(rest) != 0 || marshalErr != nil || !bytes.Equal(canonicalTime, fields[4].FullBytes) || generatedAt.IsZero() ||
		!bytes.Equal(imprint.HashedMessage, wantImprint[:]) {
		return errMacXARTrust
	}
	return nil
}

func parsePositiveMacASN1Integer(value asn1.RawValue) (*big.Int, bool) {
	if value.Class != 0 || value.Tag != asn1.TagInteger || len(value.Bytes) == 0 || value.Bytes[0]&0x80 != 0 ||
		len(value.Bytes) > 1 && value.Bytes[0] == 0 && value.Bytes[1]&0x80 == 0 {
		return nil, false
	}
	return new(big.Int).SetBytes(value.Bytes), true
}

func validateMacTimestampSignedAttributes(value, tstInfoDER, signerDER []byte) error {
	remaining := value
	previous := []byte(nil)
	contentTypeSeen := false
	messageDigestSeen := false
	essSeen := false
	for len(remaining) != 0 {
		var raw asn1.RawValue
		next, err := asn1.Unmarshal(remaining, &raw)
		if err != nil || len(raw.FullBytes) == 0 || raw.Class != 0 || raw.Tag != asn1.TagSequence ||
			previous != nil && bytes.Compare(previous, raw.FullBytes) >= 0 || len(next) >= len(remaining) {
			return errMacXARTrust
		}
		var attribute macCMSAttribute
		rest, err := asn1.Unmarshal(raw.FullBytes, &attribute)
		canonical, marshalErr := asn1.Marshal(attribute)
		if err != nil || len(rest) != 0 || marshalErr != nil || !bytes.Equal(canonical, raw.FullBytes) ||
			attribute.Values.Class != 0 || attribute.Values.Tag != asn1.TagSet || !attribute.Values.IsCompound {
			return errMacXARTrust
		}
		switch {
		case attribute.Type.Equal(packageCMSContentTypeOID()):
			var contentType asn1.ObjectIdentifier
			rest, err = asn1.Unmarshal(attribute.Values.Bytes, &contentType)
			if contentTypeSeen || err != nil || len(rest) != 0 || !contentType.Equal(packageCMSTSTInfoOID()) {
				return errMacXARTrust
			}
			contentTypeSeen = true
		case attribute.Type.Equal(packageCMSMessageDigestOID()):
			var digest []byte
			rest, err = asn1.Unmarshal(attribute.Values.Bytes, &digest)
			want := sha256.Sum256(tstInfoDER)
			if messageDigestSeen || err != nil || len(rest) != 0 || !bytes.Equal(digest, want[:]) {
				return errMacXARTrust
			}
			messageDigestSeen = true
		case attribute.Type.Equal(packageCMSSigningCertificateOID()):
			if essSeen || validateMacTimestampESS(attribute.Values, signerDER, false) != nil {
				return errMacXARTrust
			}
			essSeen = true
		case attribute.Type.Equal(packageCMSSigningCertificateV2OID()):
			if essSeen || validateMacTimestampESS(attribute.Values, signerDER, true) != nil {
				return errMacXARTrust
			}
			essSeen = true
		default:
			return errMacXARTrust
		}
		previous = append(previous[:0], raw.FullBytes...)
		remaining = next
	}
	if !contentTypeSeen || !messageDigestSeen || !essSeen {
		return errMacXARTrust
	}
	return nil
}

func validateMacTimestampESS(values asn1.RawValue, signerDER []byte, version2 bool) error {
	children, err := parseMacASN1Children(values.Bytes, 1)
	if err != nil || len(children) != 1 || children[0].Class != 0 || children[0].Tag != asn1.TagSequence {
		return errMacXARTrust
	}
	signingCertificateFields, err := parseMacASN1Children(children[0].Bytes, 1)
	if err != nil || len(signingCertificateFields) != 1 || signingCertificateFields[0].Class != 0 ||
		signingCertificateFields[0].Tag != asn1.TagSequence {
		return errMacXARTrust
	}
	certIDs, err := parseMacASN1Children(signingCertificateFields[0].Bytes, 1)
	if err != nil || len(certIDs) != 1 || certIDs[0].Class != 0 || certIDs[0].Tag != asn1.TagSequence {
		return errMacXARTrust
	}
	fields, err := parseMacASN1Children(certIDs[0].Bytes, 3)
	if err != nil || len(fields) == 0 {
		return errMacXARTrust
	}
	hashIndex := 0
	if version2 && fields[0].Class == 0 && fields[0].Tag == asn1.TagSequence {
		var algorithm macCMSAlgorithm
		if !unmarshalCanonicalMacASN1(fields[0], &algorithm) || !validMacCMSAlgorithm(algorithm, packageSHA256OID()) {
			return errMacXARTrust
		}
		hashIndex = 1
	}
	if len(fields) != hashIndex+1 || fields[hashIndex].Class != 0 || fields[hashIndex].Tag != asn1.TagOctetString {
		return errMacXARTrust
	}
	var certificateHash []byte
	if !unmarshalCanonicalMacASN1(fields[hashIndex], &certificateHash) {
		return errMacXARTrust
	}
	if version2 {
		want := sha256.Sum256(signerDER)
		if !bytes.Equal(certificateHash, want[:]) {
			return errMacXARTrust
		}
	} else {
		want := sha1.Sum(signerDER)
		if !bytes.Equal(certificateHash, want[:]) {
			return errMacXARTrust
		}
	}
	return nil
}

func parseMacASN1Children(value []byte, maximum int) ([]asn1.RawValue, error) {
	if maximum <= 0 {
		return nil, errMacXARTrust
	}
	remaining := value
	children := make([]asn1.RawValue, 0, maximum)
	for len(remaining) != 0 {
		var child asn1.RawValue
		next, err := asn1.Unmarshal(remaining, &child)
		if err != nil || len(child.FullBytes) == 0 || len(next) >= len(remaining) || len(children) == maximum {
			return nil, errMacXARTrust
		}
		children = append(children, child)
		remaining = next
	}
	return children, nil
}

func unmarshalCanonicalMacASN1(value asn1.RawValue, destination any) bool {
	rest, err := asn1.Unmarshal(value.FullBytes, destination)
	return err == nil && len(rest) == 0
}

func sameOrderedMacXARChain(left, right [][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !bytes.Equal(left[index], right[index]) {
			return false
		}
	}
	return true
}

func wrapMacCMSDERSet(body []byte) []byte {
	length := len(body)
	var prefix []byte
	switch {
	case length < 128:
		prefix = []byte{0x31, byte(length)}
	case length <= math.MaxUint8:
		prefix = []byte{0x31, 0x81, byte(length)}
	case length <= math.MaxUint16:
		prefix = []byte{0x31, 0x82, byte(length >> 8), byte(length)}
	case uint64(length) <= math.MaxUint32:
		prefix = []byte{0x31, 0x84, byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
	default:
		return nil
	}
	return append(prefix, body...)
}

func cloneMacXARDER(value [][]byte) [][]byte {
	result := make([][]byte, len(value))
	for index := range value {
		result[index] = append([]byte(nil), value[index]...)
	}
	return result
}
