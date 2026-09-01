package tailscale

import (
	"context"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

const (
	maximumMacChecksumBytes       = 66
	maximumRedirectHops           = 5
	maximumPKGBytes         int64 = 250 << 20
)

var (
	errMacDownload = errors.New("Tailscale macOS download failed")
	errMacPKGSize  = errors.New("Tailscale macOS PKG is missing or too large")
)

type syncWriter interface {
	io.Writer
	Sync() error
}

func parseMacSHA256(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > maximumMacChecksumBytes {
		return "", errors.New("invalid Tailscale macOS checksum response")
	}
	digest := raw
	switch len(raw) {
	case 64:
	case 65:
		if raw[64] != '\n' {
			return "", errors.New("invalid Tailscale macOS checksum response")
		}
		digest = raw[:64]
	case 66:
		if raw[64] != '\r' || raw[65] != '\n' {
			return "", errors.New("invalid Tailscale macOS checksum response")
		}
		digest = raw[:64]
	default:
		return "", errors.New("invalid Tailscale macOS checksum response")
	}
	for _, character := range digest {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return "", errors.New("invalid Tailscale macOS checksum response")
		}
	}
	return strings.ToLower(string(digest)), nil
}

func validateMacReleaseURL(raw, exactBasename string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Scheme != "https" || parsed.Host != "pkgs.tailscale.com" ||
		parsed.User != nil || parsed.Opaque != "" || parsed.RawPath != "" || parsed.RawQuery != "" ||
		parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return errors.New("invalid Tailscale macOS package URL")
	}
	wantPath := "/stable/" + exactBasename
	if parsed.Path != wantPath || parsed.EscapedPath() != wantPath || raw != StablePackagesURL+exactBasename {
		return errors.New("invalid Tailscale macOS package URL")
	}
	return nil
}

func newSameOriginTailscaleHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	result := *base
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	result.Transport = validatingMacRoundTripper{next: transport}
	inherited := base.CheckRedirect
	result.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := validateMacRedirect(next, via); err != nil {
			return err
		}
		if inherited != nil {
			clonedNext, clonedVia, err := cloneRedirectPolicyInputs(next, via)
			if err != nil {
				return err
			}
			if err := inherited(clonedNext, clonedVia); err != nil {
				return err
			}
		}
		return validateMacRedirect(next, via)
	}
	return &result
}

type validatingMacRoundTripper struct {
	next http.RoundTripper
}

func (transport validatingMacRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := validateMacRequestIdentity(request); err != nil {
		return nil, errMacDownload
	}
	response, err := transport.next.RoundTrip(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, err
	}
	if validateMacFinalResponse(response, request.URL) != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, errMacDownload
	}
	if isMacRedirectStatus(response.StatusCode) && validateMacRedirectResponse(response, request, request.URL.String()) != nil {
		_ = response.Body.Close()
		return nil, errMacDownload
	}
	return response, nil
}

func validateMacRequestIdentity(request *http.Request) error {
	if request == nil || request.URL == nil || request.Method != http.MethodGet || request.Host != "" ||
		request.Body != nil || request.GetBody != nil || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		return errMacDownload
	}
	if err := validateGenericStableURL(request.URL); err != nil {
		return err
	}
	return nil
}

func validateMacFinalResponse(response *http.Response, expected *url.URL) error {
	if response == nil || response.Body == nil || expected == nil || response.Request == nil ||
		validateMacRequestIdentity(response.Request) != nil || validateGenericStableURL(expected) != nil ||
		!sameMacURLIdentity(response.Request.URL, expected) {
		return errMacDownload
	}
	return nil
}

func sameMacURLIdentity(left, right *url.URL) bool {
	return left != nil && right != nil && left.Scheme == right.Scheme && left.Opaque == right.Opaque &&
		left.User == nil && right.User == nil && left.Host == right.Host && left.Path == right.Path &&
		left.RawPath == right.RawPath && left.ForceQuery == right.ForceQuery && left.RawQuery == right.RawQuery &&
		left.Fragment == right.Fragment && left.RawFragment == right.RawFragment
}

func validateGenericStableURL(parsed *url.URL) error {
	if parsed == nil || parsed.Scheme != "https" || parsed.Host != "pkgs.tailscale.com" ||
		parsed.User != nil || parsed.Opaque != "" || parsed.RawPath != "" || parsed.RawQuery != "" ||
		parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" ||
		!strings.HasPrefix(parsed.Path, "/stable/") || parsed.EscapedPath() != parsed.Path ||
		strings.Contains(parsed.Path, "//") || strings.Contains(parsed.Path, "/../") || strings.Contains(parsed.Path, "/./") {
		return errMacDownload
	}
	return nil
}

func validateMacRedirect(next *http.Request, via []*http.Request) error {
	if len(via) == 0 || len(via) > maximumRedirectHops {
		return errMacDownload
	}
	if err := validateMacRequestIdentity(next); err != nil {
		return errMacDownload
	}
	exact := via[0]
	if err := validateMacRequestIdentity(exact); err != nil || exact.Response != nil ||
		!sameMacURLIdentity(next.URL, exact.URL) {
		return errMacDownload
	}
	exactRaw := exact.URL.String()
	for index, prior := range via {
		if err := validateMacRequestIdentity(prior); err != nil || !sameMacURLIdentity(prior.URL, exact.URL) {
			return errMacDownload
		}
		if index > 0 && validateMacRedirectResponse(prior.Response, via[index-1], exactRaw) != nil {
			return errMacDownload
		}
	}
	if validateMacRedirectResponse(next.Response, via[len(via)-1], exactRaw) != nil {
		return errMacDownload
	}
	return nil
}

func validateMacRedirectResponse(response *http.Response, prior *http.Request, exactRaw string) error {
	if response == nil || prior == nil || exactRaw == "" || !isMacRedirectStatus(response.StatusCode) ||
		validateMacFinalResponse(response, prior.URL) != nil {
		return errMacDownload
	}
	locationKeys := 0
	location := ""
	for key, values := range response.Header {
		if !strings.EqualFold(key, "Location") {
			continue
		}
		locationKeys++
		if locationKeys != 1 || len(values) != 1 {
			return errMacDownload
		}
		location = values[0]
	}
	if locationKeys != 1 || location != exactRaw {
		return errMacDownload
	}
	return nil
}

func isMacRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func cloneRedirectPolicyInputs(next *http.Request, via []*http.Request) (*http.Request, []*http.Request, error) {
	clonedNext, err := cloneRedirectRequest(next)
	if err != nil {
		return nil, nil, err
	}
	clonedVia := make([]*http.Request, len(via))
	for index, request := range via {
		clonedVia[index], err = cloneRedirectRequest(request)
		if err != nil {
			return nil, nil, err
		}
	}
	return clonedNext, clonedVia, nil
}

func cloneRedirectRequest(request *http.Request) (*http.Request, error) {
	if request == nil || request.URL == nil {
		return nil, errMacDownload
	}
	clone := request.Clone(context.Background())
	urlCopy := *request.URL
	clone.URL = &urlCopy
	clone.Header = request.Header.Clone()
	clone.Trailer = request.Trailer.Clone()
	clone.TransferEncoding = append([]string(nil), request.TransferEncoding...)
	clone.Form = cloneValues(request.Form)
	clone.PostForm = cloneValues(request.PostForm)
	clone.MultipartForm = cloneMultipartForm(request.MultipartForm)
	tlsClone, err := cloneTLSConnectionState(request.TLS)
	if err != nil {
		return nil, errMacDownload
	}
	clone.TLS = tlsClone
	clone.Body = nil
	clone.GetBody = nil
	clone.Response = nil
	clone.Cancel = nil
	return clone, nil
}

func cloneTLSConnectionState(state *tls.ConnectionState) (*tls.ConnectionState, error) {
	if state == nil {
		return nil, nil
	}
	certificates := make(map[*x509.Certificate]*x509.Certificate)
	cloneCertificate := func(certificate *x509.Certificate) (*x509.Certificate, error) {
		if certificate == nil {
			return nil, nil
		}
		if existing := certificates[certificate]; existing != nil {
			return existing, nil
		}
		cloned, err := cloneX509Certificate(certificate)
		if err != nil {
			return nil, err
		}
		certificates[certificate] = cloned
		return cloned, nil
	}
	peer := make([]*x509.Certificate, len(state.PeerCertificates))
	for index, certificate := range state.PeerCertificates {
		var err error
		peer[index], err = cloneCertificate(certificate)
		if err != nil {
			return nil, errMacDownload
		}
	}
	chains := make([][]*x509.Certificate, len(state.VerifiedChains))
	for chainIndex, chain := range state.VerifiedChains {
		chains[chainIndex] = make([]*x509.Certificate, len(chain))
		for certificateIndex, certificate := range chain {
			var err error
			chains[chainIndex][certificateIndex], err = cloneCertificate(certificate)
			if err != nil {
				return nil, errMacDownload
			}
		}
	}
	return &tls.ConnectionState{
		Version:                     state.Version,
		HandshakeComplete:           state.HandshakeComplete,
		DidResume:                   state.DidResume,
		CipherSuite:                 state.CipherSuite,
		CurveID:                     state.CurveID,
		NegotiatedProtocol:          state.NegotiatedProtocol,
		NegotiatedProtocolIsMutual:  state.NegotiatedProtocolIsMutual,
		ServerName:                  state.ServerName,
		PeerCertificates:            peer,
		VerifiedChains:              chains,
		SignedCertificateTimestamps: cloneByteSlices(state.SignedCertificateTimestamps),
		OCSPResponse:                append([]byte(nil), state.OCSPResponse...),
		TLSUnique:                   append([]byte(nil), state.TLSUnique...),
		ECHAccepted:                 state.ECHAccepted,
		HelloRetryRequest:           state.HelloRetryRequest,
	}, nil
}

func cloneX509Certificate(certificate *x509.Certificate) (*x509.Certificate, error) {
	if certificate == nil {
		return nil, nil
	}
	clone := *certificate
	clone.Raw = append([]byte(nil), certificate.Raw...)
	clone.RawTBSCertificate = append([]byte(nil), certificate.RawTBSCertificate...)
	clone.RawSubjectPublicKeyInfo = append([]byte(nil), certificate.RawSubjectPublicKeyInfo...)
	clone.RawSubject = append([]byte(nil), certificate.RawSubject...)
	clone.RawIssuer = append([]byte(nil), certificate.RawIssuer...)
	clone.Signature = append([]byte(nil), certificate.Signature...)
	if certificate.SerialNumber != nil {
		clone.SerialNumber = new(big.Int).Set(certificate.SerialNumber)
	}
	clone.Issuer = clonePKIXName(certificate.Issuer)
	clone.Subject = clonePKIXName(certificate.Subject)
	clone.Extensions = cloneExtensions(certificate.Extensions)
	clone.ExtraExtensions = cloneExtensions(certificate.ExtraExtensions)
	clone.UnhandledCriticalExtensions = cloneASN1ObjectIdentifiers(certificate.UnhandledCriticalExtensions)
	clone.ExtKeyUsage = append([]x509.ExtKeyUsage(nil), certificate.ExtKeyUsage...)
	clone.UnknownExtKeyUsage = cloneASN1ObjectIdentifiers(certificate.UnknownExtKeyUsage)
	clone.SubjectKeyId = append([]byte(nil), certificate.SubjectKeyId...)
	clone.AuthorityKeyId = append([]byte(nil), certificate.AuthorityKeyId...)
	clone.OCSPServer = append([]string(nil), certificate.OCSPServer...)
	clone.IssuingCertificateURL = append([]string(nil), certificate.IssuingCertificateURL...)
	clone.DNSNames = append([]string(nil), certificate.DNSNames...)
	clone.EmailAddresses = append([]string(nil), certificate.EmailAddresses...)
	clone.IPAddresses = cloneIPs(certificate.IPAddresses)
	clone.URIs = cloneURLs(certificate.URIs)
	clone.PermittedDNSDomains = append([]string(nil), certificate.PermittedDNSDomains...)
	clone.ExcludedDNSDomains = append([]string(nil), certificate.ExcludedDNSDomains...)
	clone.PermittedIPRanges = cloneIPNets(certificate.PermittedIPRanges)
	clone.ExcludedIPRanges = cloneIPNets(certificate.ExcludedIPRanges)
	clone.PermittedEmailAddresses = append([]string(nil), certificate.PermittedEmailAddresses...)
	clone.ExcludedEmailAddresses = append([]string(nil), certificate.ExcludedEmailAddresses...)
	clone.PermittedURIDomains = append([]string(nil), certificate.PermittedURIDomains...)
	clone.ExcludedURIDomains = append([]string(nil), certificate.ExcludedURIDomains...)
	clone.CRLDistributionPoints = append([]string(nil), certificate.CRLDistributionPoints...)
	clone.PolicyIdentifiers = cloneASN1ObjectIdentifiers(certificate.PolicyIdentifiers)
	clone.Policies = append([]x509.OID(nil), certificate.Policies...)
	clone.PolicyMappings = append([]x509.PolicyMapping(nil), certificate.PolicyMappings...)
	publicKey, err := cloneCertificatePublicKey(certificate.PublicKey)
	if err != nil {
		return nil, err
	}
	clone.PublicKey = publicKey
	return &clone, nil
}

func cloneCertificatePublicKey(publicKey any) (any, error) {
	switch key := publicKey.(type) {
	case nil:
		return nil, nil
	case *rsa.PublicKey:
		clone := *key
		if key.N != nil {
			clone.N = new(big.Int).Set(key.N)
		}
		return &clone, nil
	case *ecdsa.PublicKey:
		clone := *key
		if key.X != nil {
			clone.X = new(big.Int).Set(key.X)
		}
		if key.Y != nil {
			clone.Y = new(big.Int).Set(key.Y)
		}
		return &clone, nil
	case ed25519.PublicKey:
		return append(ed25519.PublicKey(nil), key...), nil
	case *dsa.PublicKey:
		clone := *key
		if key.Y != nil {
			clone.Y = new(big.Int).Set(key.Y)
		}
		if key.Parameters.P != nil {
			clone.Parameters.P = new(big.Int).Set(key.Parameters.P)
		}
		if key.Parameters.Q != nil {
			clone.Parameters.Q = new(big.Int).Set(key.Parameters.Q)
		}
		if key.Parameters.G != nil {
			clone.Parameters.G = new(big.Int).Set(key.Parameters.G)
		}
		return &clone, nil
	case []byte:
		return append([]byte(nil), key...), nil
	default:
		return nil, errMacDownload
	}
}

func clonePKIXName(name pkix.Name) pkix.Name {
	clone := name
	clone.Country = append([]string(nil), name.Country...)
	clone.Organization = append([]string(nil), name.Organization...)
	clone.OrganizationalUnit = append([]string(nil), name.OrganizationalUnit...)
	clone.Locality = append([]string(nil), name.Locality...)
	clone.Province = append([]string(nil), name.Province...)
	clone.StreetAddress = append([]string(nil), name.StreetAddress...)
	clone.PostalCode = append([]string(nil), name.PostalCode...)
	clone.Names = cloneAttributes(name.Names)
	clone.ExtraNames = cloneAttributes(name.ExtraNames)
	return clone
}

func cloneAttributes(attributes []pkix.AttributeTypeAndValue) []pkix.AttributeTypeAndValue {
	clone := make([]pkix.AttributeTypeAndValue, len(attributes))
	for index, attribute := range attributes {
		clone[index] = attribute
		clone[index].Type = append(asn1.ObjectIdentifier(nil), attribute.Type...)
		switch value := attribute.Value.(type) {
		case []byte:
			clone[index].Value = append([]byte(nil), value...)
		case *big.Int:
			if value != nil {
				clone[index].Value = new(big.Int).Set(value)
			}
		case asn1.RawValue:
			value.Bytes = append([]byte(nil), value.Bytes...)
			value.FullBytes = append([]byte(nil), value.FullBytes...)
			clone[index].Value = value
		}
	}
	return clone
}

func cloneExtensions(extensions []pkix.Extension) []pkix.Extension {
	clone := make([]pkix.Extension, len(extensions))
	for index, extension := range extensions {
		clone[index] = extension
		clone[index].Id = append(asn1.ObjectIdentifier(nil), extension.Id...)
		clone[index].Value = append([]byte(nil), extension.Value...)
	}
	return clone
}

func cloneASN1ObjectIdentifiers(identifiers []asn1.ObjectIdentifier) []asn1.ObjectIdentifier {
	clone := make([]asn1.ObjectIdentifier, len(identifiers))
	for index, identifier := range identifiers {
		clone[index] = append(asn1.ObjectIdentifier(nil), identifier...)
	}
	return clone
}

func cloneByteSlices(values [][]byte) [][]byte {
	clone := make([][]byte, len(values))
	for index, value := range values {
		clone[index] = append([]byte(nil), value...)
	}
	return clone
}

func cloneIPs(values []net.IP) []net.IP {
	clone := make([]net.IP, len(values))
	for index, value := range values {
		clone[index] = append(net.IP(nil), value...)
	}
	return clone
}

func cloneURLs(values []*url.URL) []*url.URL {
	clone := make([]*url.URL, len(values))
	for index, value := range values {
		if value == nil {
			continue
		}
		urlCopy := *value
		if value.User != nil {
			if password, hasPassword := value.User.Password(); hasPassword {
				urlCopy.User = url.UserPassword(value.User.Username(), password)
			} else {
				urlCopy.User = url.User(value.User.Username())
			}
		}
		clone[index] = &urlCopy
	}
	return clone
}

func cloneIPNets(values []*net.IPNet) []*net.IPNet {
	clone := make([]*net.IPNet, len(values))
	for index, value := range values {
		if value == nil {
			continue
		}
		clone[index] = &net.IPNet{IP: append(net.IP(nil), value.IP...), Mask: append(net.IPMask(nil), value.Mask...)}
	}
	return clone
}

func cloneValues(values url.Values) url.Values {
	if values == nil {
		return nil
	}
	clone := make(url.Values, len(values))
	for key, entries := range values {
		clone[key] = append([]string(nil), entries...)
	}
	return clone
}

func cloneMultipartForm(form *multipart.Form) *multipart.Form {
	if form == nil {
		return nil
	}
	clone := &multipart.Form{Value: make(map[string][]string, len(form.Value)), File: make(map[string][]*multipart.FileHeader, len(form.File))}
	for key, values := range form.Value {
		clone.Value[key] = append([]string(nil), values...)
	}
	for key, files := range form.File {
		clonedFiles := make([]*multipart.FileHeader, len(files))
		for index, file := range files {
			if file == nil {
				continue
			}
			fileCopy := *file
			fileCopy.Header = cloneMIMEHeader(file.Header)
			clonedFiles[index] = &fileCopy
		}
		clone.File[key] = clonedFiles
	}
	return clone
}

func cloneMIMEHeader(header textproto.MIMEHeader) textproto.MIMEHeader {
	if header == nil {
		return nil
	}
	clone := make(textproto.MIMEHeader, len(header))
	for key, values := range header {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}

func downloadExactMacSmall(ctx context.Context, base *http.Client, rawURL, exactBasename string, maximum int64) ([]byte, error) {
	if maximum <= 0 || validateMacReleaseURL(rawURL, exactBasename) != nil {
		return nil, errMacDownload
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errMacDownload
	}
	request.Host = ""
	response, err := newSameOriginTailscaleHTTPClient(base).Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, errMacDownload
	}
	expectedURL, parseErr := url.Parse(rawURL)
	if parseErr != nil || validateMacFinalResponse(response, expectedURL) != nil ||
		response.StatusCode != http.StatusOK {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, errMacDownload
	}
	defer response.Body.Close()
	value, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil || len(value) == 0 || int64(len(value)) > maximum {
		return nil, errMacDownload
	}
	return value, nil
}

func downloadPKG(ctx context.Context, client *http.Client, rawURL string, destination syncWriter, expectedDigest string) error {
	return downloadPKGWithLimit(ctx, client, rawURL, destination, expectedDigest, maximumPKGBytes)
}

func downloadPKGWithLimit(ctx context.Context, base *http.Client, rawURL string, destination syncWriter, expectedDigest string, maximum int64) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil || maximum <= 0 || destination == nil ||
		validateMacReleaseURL(rawURL, strings.TrimPrefix(parsed.Path, "/stable/")) != nil ||
		!sha256Pattern.MatchString(expectedDigest) {
		return errors.New("Tailscale macOS PKG download failed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return errors.New("Tailscale macOS PKG download failed")
	}
	request.Host = ""
	response, err := newSameOriginTailscaleHTTPClient(base).Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return errors.New("Tailscale macOS PKG download failed")
	}
	expectedURL, parseErr := url.Parse(rawURL)
	if parseErr != nil || validateMacFinalResponse(response, expectedURL) != nil ||
		response.StatusCode != http.StatusOK {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return errors.New("Tailscale macOS PKG download failed")
	}
	if response.ContentLength > maximum {
		_ = response.Body.Close()
		return errMacPKGSize
	}
	defer response.Body.Close()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(response.Body, maximum+1))
	if err != nil || written == 0 || written > maximum {
		return errMacPKGSize
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return errors.New("Tailscale macOS PKG SHA-256 verification failed")
	}
	if err := destination.Sync(); err != nil {
		return errors.New("flush Tailscale macOS PKG staging file")
	}
	return nil
}
