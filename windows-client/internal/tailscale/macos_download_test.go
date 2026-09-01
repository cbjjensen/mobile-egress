package tailscale

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestParseMacSHA256AcceptsOnlyDigest(t *testing.T) {
	t.Parallel()

	const lower = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const upper = "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"
	for _, raw := range []string{lower, lower + "\n", upper + "\r\n"} {
		got, err := parseMacSHA256([]byte(raw))
		if err != nil || got != lower {
			t.Fatalf("parseMacSHA256(%q) = %q/%v", raw, got, err)
		}
	}
	invalid := []string{
		" " + lower, lower + " ", lower + "  Tailscale-1.100.1-macos.pkg\n",
		lower + " *Tailscale-1.100.1-macos.pkg\n", lower + " token", lower + "\n" + lower,
		lower + "\n\n", lower + "\r", lower + "\x00", lower[:63], lower + "0", strings.Repeat("g", 64), strings.Repeat("0", 67),
	}
	for _, raw := range invalid {
		if _, err := parseMacSHA256([]byte(raw)); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestDownloadPKGEnforcesDigestSyncAndInclusiveLimit(t *testing.T) {
	t.Parallel()

	payload := []byte("signed package bytes")
	digest := sha256.Sum256(payload)
	expected := hex.EncodeToString(digest[:])
	const pkgURL = StablePackagesURL + "Tailscale-1.100.1-macos.pkg"

	destination := &recordingSyncWriter{}
	client := packageBodyClient(pkgURL, int64(len(payload)), bytes.NewReader(payload))
	if err := downloadPKGWithLimit(context.Background(), client, pkgURL, destination, expected, int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(destination.bytes.Bytes(), payload) || destination.syncs != 1 {
		t.Fatalf("destination bytes/syncs = %q/%d", destination.bytes.Bytes(), destination.syncs)
	}

	for name, test := range map[string]struct {
		body          io.Reader
		contentLength int64
		digest        string
		limit         int64
		syncErr       error
	}{
		"empty":                {body: strings.NewReader(""), contentLength: 0, digest: expected, limit: 32},
		"wrong digest":         {body: bytes.NewReader(payload), contentLength: int64(len(payload)), digest: strings.Repeat("0", 64), limit: 32},
		"truthful oversized":   {body: bytes.NewReader(payload), contentLength: 33, digest: expected, limit: 32},
		"lying short length":   {body: strings.NewReader("123456"), contentLength: 1, digest: expected, limit: 5},
		"absent length":        {body: strings.NewReader("123456"), contentLength: -1, digest: expected, limit: 5},
		"sync error":           {body: bytes.NewReader(payload), contentLength: int64(len(payload)), digest: expected, limit: 32, syncErr: errors.New("sync")},
		"partial reader error": {body: io.MultiReader(strings.NewReader("abc"), errorReader{}), contentLength: -1, digest: expected, limit: 32},
	} {
		t.Run(name, func(t *testing.T) {
			destination := &recordingSyncWriter{syncErr: test.syncErr}
			client := packageBodyClient(pkgURL, test.contentLength, test.body)
			if err := downloadPKGWithLimit(context.Background(), client, pkgURL, destination, test.digest, test.limit); err == nil {
				t.Fatal("invalid package succeeded")
			}
		})
	}
}

func TestDownloadPKGRejectsMaximumPlusOneWithoutBufferingPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("250 MiB boundary test")
	}
	const pkgURL = StablePackagesURL + "Tailscale-1.100.1-macos.pkg"
	source := &countingZeroReader{remaining: maximumPKGBytes + 1}
	destination := &countingDiscardSyncWriter{}
	const exactDigest = "930669d5c25a9dc1408dce78397077cb6ba9ed81bcf6aa2812b59f09f18d7b93"
	client := packageBodyClient(pkgURL, -1, source)
	if err := downloadPKG(context.Background(), client, pkgURL, destination, exactDigest); !errors.Is(err, errMacPKGSize) {
		t.Fatalf("250 MiB + 1 error = %v, want size rejection", err)
	}
	if source.read != maximumPKGBytes+1 || destination.written != maximumPKGBytes+1 || destination.syncs != 0 {
		t.Fatalf("overflow reads/writes/syncs = %d/%d/%d", source.read, destination.written, destination.syncs)
	}
}

func TestDownloadPKGRejectsNonOKResponseWithoutReadingOrSync(t *testing.T) {
	t.Parallel()

	const pkgURL = StablePackagesURL + "Tailscale-1.100.1-macos.pkg"
	body := &recordingBody{reader: strings.NewReader("hostile response")}
	destination := &recordingSyncWriter{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusForbidden,
			Body:          body,
			Header:        make(http.Header),
			Request:       request,
			ContentLength: int64(len("hostile response")),
		}, nil
	})}
	if err := downloadPKG(context.Background(), client, pkgURL, destination, strings.Repeat("0", 64)); err == nil {
		t.Fatal("non-OK package response succeeded")
	}
	if body.reads != 0 || !body.closed || destination.bytes.Len() != 0 || destination.syncs != 0 {
		t.Fatalf("non-OK reads/closed/writes/syncs = %d/%t/%d/%d", body.reads, body.closed, destination.bytes.Len(), destination.syncs)
	}
}

func TestDownloadPKGRejectsStructurallyMalformedFinalRequestWithoutReading(t *testing.T) {
	t.Parallel()

	const pkgURL = StablePackagesURL + "Tailscale-1.100.1-macos.pkg"
	body := &recordingBody{reader: strings.NewReader("hostile package")}
	destination := &recordingSyncWriter{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		urlCopy := *request.URL
		urlCopy.RawPath = urlCopy.Path
		clone.URL = &urlCopy
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: clone}, nil
	})}
	if err := downloadPKG(context.Background(), client, pkgURL, destination, strings.Repeat("0", 64)); err == nil {
		t.Fatal("malformed final package request succeeded")
	}
	if body.reads != 0 || !body.closed || destination.bytes.Len() != 0 || destination.syncs != 0 {
		t.Fatalf("malformed package reads/closed/writes/syncs = %d/%t/%d/%d", body.reads, body.closed, destination.bytes.Len(), destination.syncs)
	}
}

func TestValidateMacReleaseURLRequiresExactStableArtifact(t *testing.T) {
	t.Parallel()

	const basename = "Tailscale-1.100.1-macos.pkg"
	valid := StablePackagesURL + basename
	if err := validateMacReleaseURL(valid, basename); err != nil {
		t.Fatalf("valid URL rejected: %v", err)
	}
	invalid := []string{
		"http://pkgs.tailscale.com/stable/" + basename,
		"https://PKGS.tailscale.com/stable/" + basename,
		"https://pkgs.tailscale.com.evil.example/stable/" + basename,
		"https://pkgs.tailscale.com./stable/" + basename,
		"https://pkgs.tailscale.com:443/stable/" + basename,
		"https://user@pkgs.tailscale.com/stable/" + basename,
		"https:opaque",
		"https://pkgs.tailscale.com/%73table/" + basename,
		valid + "?",
		valid + "?x=1",
		valid + "#x",
		"https://pkgs.tailscale.com/unstable/" + basename,
		StablePackagesURL + "Tailscale-1.100.2-macos.pkg",
		StablePackagesURL + basename + ".sha256",
		StablePackagesURL + "../" + basename,
	}
	for _, candidate := range invalid {
		if err := validateMacReleaseURL(candidate, basename); err == nil {
			t.Errorf("accepted %q", candidate)
		}
	}
}

func TestSameOriginTailscaleClientRejectsRedirectBeforeDispatch(t *testing.T) {
	t.Parallel()

	const expected = StablePackagesURL + "Tailscale-1.100.1-macos.pkg"
	var mu sync.Mutex
	requests := []string{}
	first := true
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, request.URL.String())
		mu.Unlock()
		if first {
			first = false
			return redirectResponse(request, "https://evil.example/stable/Tailscale-1.100.1-macos.pkg"), nil
		}
		return okResponse(request, "forbidden"), nil
	})
	client := newSameOriginTailscaleHTTPClient(&http.Client{Transport: transport})
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, expected, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = ""
	if err := validateMacRequestIdentity(request); err != nil {
		t.Fatalf("valid initial request rejected: %#v: %v", request, err)
	}
	response, err := client.Do(request)
	if err == nil {
		response.Body.Close()
		t.Fatal("off-origin redirect succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(requests, []string{expected}) {
		t.Fatalf("dispatched requests = %#v", requests)
	}
}

func TestSameOriginTailscaleClientRejectsEveryMalformedRedirectBeforeDispatch(t *testing.T) {
	t.Parallel()

	const expected = StablePackagesURL + "Tailscale-1.100.1-macos.pkg"
	tests := map[string][]string{
		"missing":                       nil,
		"duplicate":                     {expected, expected},
		"HTTP":                          {"http://pkgs.tailscale.com/stable/Tailscale-1.100.1-macos.pkg"},
		"other host":                    {"https://evil.example/stable/Tailscale-1.100.1-macos.pkg"},
		"explicit port":                 {"https://pkgs.tailscale.com:443/stable/Tailscale-1.100.1-macos.pkg"},
		"userinfo":                      {"https://user@pkgs.tailscale.com/stable/Tailscale-1.100.1-macos.pkg"},
		"query":                         {expected + "?download=1"},
		"encoded path":                  {"https://pkgs.tailscale.com/%73table/Tailscale-1.100.1-macos.pkg"},
		"wrong artifact":                {StablePackagesURL + "Tailscale-1.100.2-macos.pkg"},
		"relative canonical":            {"/stable/Tailscale-1.100.1-macos.pkg"},
		"scheme relative":               {"//pkgs.tailscale.com/stable/Tailscale-1.100.1-macos.pkg"},
		"absolute dot segment":          {"https://pkgs.tailscale.com/stable/../stable/Tailscale-1.100.1-macos.pkg"},
		"relative dot segment":          {"/stable/../stable/Tailscale-1.100.1-macos.pkg"},
		"absolute current-dir segment":  {"https://pkgs.tailscale.com/stable/./Tailscale-1.100.1-macos.pkg"},
		"canonical with empty fragment": {expected + "#"},
	}
	for name, locations := range tests {
		name, locations := name, locations
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			redirectBody := &recordingBody{reader: strings.NewReader("redirect body")}
			dispatches := 0
			hookCalls := 0
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				dispatches++
				if dispatches != 1 {
					return okResponse(request, "rejected target body"), nil
				}
				header := make(http.Header)
				if locations != nil {
					header["Location"] = append([]string(nil), locations...)
				}
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     header,
					Body:       redirectBody,
					Request:    request,
				}, nil
			})
			client := newSameOriginTailscaleHTTPClient(&http.Client{
				Transport: transport,
				CheckRedirect: func(next *http.Request, via []*http.Request) error {
					hookCalls++
					if next.Response != nil || next.Body != nil || next.GetBody != nil || next.Cancel != nil {
						t.Fatal("inherited hook received redirect response or I/O handles")
					}
					for _, prior := range via {
						if prior.Response != nil || prior.Body != nil || prior.GetBody != nil || prior.Cancel != nil {
							t.Fatal("inherited hook received prior response or I/O handles")
						}
					}
					return nil
				},
			})
			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, expected, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Host = ""
			response, err := client.Do(request)
			if response != nil && response.Body != nil {
				defer response.Body.Close()
			}
			if err == nil {
				t.Fatal("malformed redirect succeeded")
			}
			if dispatches != 1 || !redirectBody.closed || hookCalls != 0 {
				t.Fatalf("redirect dispatches/closed/hook = %d/%t/%d", dispatches, redirectBody.closed, hookCalls)
			}
			// Go may bounded-drain the current 3xx body. Reads here are not
			// artifact admission and are intentionally not asserted.
		})
	}
}

func TestRedirectRawLocationIsRevalidatedAfterInheritedHook(t *testing.T) {
	t.Parallel()

	const expected = StablePackagesURL + "Tailscale-1.100.1-macos.pkg"
	redirectBody := &recordingBody{reader: strings.NewReader("redirect")}
	dispatches := 0
	var realRedirect *http.Response
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		dispatches++
		if dispatches != 1 {
			return okResponse(request, "must not dispatch"), nil
		}
		realRedirect = &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{expected}},
			Body:       redirectBody,
			Request:    request,
		}
		return realRedirect, nil
	})
	client := newSameOriginTailscaleHTTPClient(&http.Client{
		Transport: transport,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if next.Response != nil || len(via) != 1 || via[0].Response != nil {
				t.Fatal("inherited hook received a live redirect response")
			}
			realRedirect.Header["Location"][0] = expected + "#"
			return nil
		},
	})
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, expected, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = ""
	if _, err := client.Do(request); err == nil {
		t.Fatal("post-hook raw Location mutation was accepted")
	}
	if dispatches != 1 || !redirectBody.closed {
		t.Fatalf("redirect dispatches/closed = %d/%t", dispatches, redirectBody.closed)
	}
}

func TestSameOriginTailscaleClientRejectsSixthRedirect(t *testing.T) {
	t.Parallel()

	const expected = StablePackagesURL + "Tailscale-1.100.1-macos.pkg"
	dispatches := 0
	redirectBodies := []*recordingBody{}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		dispatches++
		body := &recordingBody{reader: strings.NewReader("redirect")}
		redirectBodies = append(redirectBodies, body)
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{expected}},
			Body:       body,
			Request:    request,
		}, nil
	})
	client := newSameOriginTailscaleHTTPClient(&http.Client{Transport: transport})
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, expected, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = ""
	if _, err := client.Do(request); err == nil {
		t.Fatal("sixth redirect succeeded")
	}
	if dispatches != 6 {
		t.Fatalf("redirect dispatches = %d, want 6", dispatches)
	}
	for index, body := range redirectBodies {
		if !body.closed {
			t.Fatalf("redirect body %d was not closed", index)
		}
	}
}

func TestDownloadExactMacSmallAcceptsSameOriginExactRedirectChain(t *testing.T) {
	t.Parallel()

	const basename = "Tailscale-1.100.1-macos.pkg.sha256"
	const expected = StablePackagesURL + basename
	redirectBody := &recordingBody{reader: strings.NewReader("redirect")}
	dispatches := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		dispatches++
		if dispatches == 1 {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{expected}},
				Body:       redirectBody,
				Request:    request,
			}, nil
		}
		return okResponse(request, strings.Repeat("0", 64)), nil
	})}
	value, err := downloadExactMacSmall(context.Background(), client, expected, basename, maximumMacChecksumBytes)
	if err != nil || string(value) != strings.Repeat("0", 64) {
		t.Fatalf("same-origin exact redirect = %q/%v", value, err)
	}
	if dispatches != 2 || !redirectBody.closed {
		t.Fatalf("same-origin redirect dispatches/closed = %d/%t", dispatches, redirectBody.closed)
	}
}

func TestDownloadExactMacSmallRejectsOffOriginStartBeforeTransport(t *testing.T) {
	t.Parallel()

	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("must not dispatch")
	})}
	if _, err := downloadExactMacSmall(
		context.Background(), client,
		"https://evil.example/stable/Tailscale-1.100.1-macos.pkg.sha256",
		"Tailscale-1.100.1-macos.pkg.sha256", maximumMacChecksumBytes,
	); err == nil {
		t.Fatal("off-origin starting URL succeeded")
	}
	if called {
		t.Fatal("off-origin starting URL reached the transport")
	}
}

func TestDownloadExactMacSmallRejectsPartialReaderAtBoundary(t *testing.T) {
	t.Parallel()

	const basename = "Tailscale-1.100.1-macos.pkg.sha256"
	const exact = StablePackagesURL + basename
	body := &recordingBody{reader: &partialErrorReader{value: []byte(strings.Repeat("0", 64))}}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: request}, nil
	})}
	if value, err := downloadExactMacSmall(context.Background(), client, exact, basename, 64); err == nil || value != nil {
		t.Fatalf("partial checksum = %q/%v", value, err)
	}
	if !body.closed || body.reads == 0 {
		t.Fatalf("partial checksum closed/reads = %t/%d", body.closed, body.reads)
	}
}

func TestRedirectHookIsolationDiscardsMutationAndPreservesVeto(t *testing.T) {
	t.Parallel()

	const expected = StablePackagesURL + "Tailscale-1.100.1-macos.pkg"
	var dispatched []*http.Request
	call := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		dispatched = append(dispatched, request.Clone(context.Background()))
		call++
		if call == 1 {
			return redirectResponse(request, expected), nil
		}
		return okResponse(request, "ok"), nil
	})
	base := &http.Client{
		Transport: transport,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			next.URL.Host = "evil.example"
			next.Host = "evil.example"
			next.Method = http.MethodPost
			next.Header.Set("X-Mutated", "true")
			next.TransferEncoding = []string{"chunked"}
			next.Form = url.Values{"mutated": {"true"}}
			via[0].URL.Host = "evil.example"
			via[0].Header.Set("X-Via-Mutated", "true")
			return nil
		},
	}
	client := newSameOriginTailscaleHTTPClient(base)
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, expected, nil)
	request.Host = ""
	request.Header.Set("X-Original", "yes")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(dispatched) != 2 {
		t.Fatalf("dispatch count = %d", len(dispatched))
	}
	for _, got := range dispatched {
		if got.URL.String() != expected || got.Method != http.MethodGet || got.Host != "" || got.Header.Get("X-Mutated") != "" {
			t.Fatalf("hook mutation reached transport: %#v", got)
		}
	}
	if request.URL.String() != expected || request.Header.Get("X-Via-Mutated") != "" {
		t.Fatalf("hook mutated original request: %#v", request)
	}

	veto := errors.New("stricter policy")
	vetoClient := newSameOriginTailscaleHTTPClient(&http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return veto },
	})
	call = 0
	dispatched = nil
	request, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, expected, nil)
	request.Host = ""
	if _, err := vetoClient.Do(request); !errors.Is(err, veto) {
		t.Fatalf("redirect veto = %v, want %v", err, veto)
	}
	if len(dispatched) != 1 {
		t.Fatalf("veto dispatched %d requests", len(dispatched))
	}
}

func TestRedirectHookDelayedMutationCannotReachDispatchedRequest(t *testing.T) {
	const expected = StablePackagesURL + "Tailscale-1.100.1-macos.pkg"
	releaseMutation := make(chan struct{})
	mutationDone := make(chan struct{})
	dispatches := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		dispatches++
		if dispatches == 1 {
			return redirectResponse(request, expected), nil
		}
		close(releaseMutation)
		<-mutationDone
		if request.URL.String() != expected || request.Method != http.MethodGet || request.Host != "" ||
			request.Header.Get("X-Delayed") != "" || request.Form.Get("delayed") != "" ||
			len(request.TransferEncoding) != 0 {
			return nil, errors.New("delayed hook mutation reached transport")
		}
		return okResponse(request, "ok"), nil
	})
	client := newSameOriginTailscaleHTTPClient(&http.Client{
		Transport: transport,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			go func() {
				defer close(mutationDone)
				<-releaseMutation
				next.URL.Host = "evil.example"
				next.Method = http.MethodPost
				next.Host = "evil.example"
				next.Header.Set("X-Delayed", "true")
				next.Form = url.Values{"delayed": []string{"true"}}
				next.PostForm = url.Values{"delayed": []string{"true"}}
				next.TransferEncoding = []string{"chunked"}
				via[0].URL.Host = "evil.example"
				via[0].Header.Set("X-Delayed-Via", "true")
			}()
			return nil
		},
	})
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, expected, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = ""
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if dispatches != 2 || request.URL.String() != expected || request.Header.Get("X-Delayed-Via") != "" {
		t.Fatalf("dispatches/original = %d/%#v", dispatches, request)
	}
}

func TestCloneRedirectPolicyInputsDeepCopiesMutableRequestIdentity(t *testing.T) {
	t.Parallel()

	parsed, err := url.Parse(StablePackagesURL + "Tailscale-1.100.1-macos.pkg")
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		Raw:          []byte("certificate"),
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{Organization: []string{"original"}},
		Extensions:   []pkix.Extension{{Id: []int{1, 2, 3}, Value: []byte("extension")}},
	}
	request := &http.Request{
		Method:           http.MethodGet,
		URL:              parsed,
		Header:           http.Header{"X-Header": []string{"original"}},
		Trailer:          http.Header{"X-Trailer": []string{"original"}},
		TransferEncoding: []string{"identity"},
		Form:             url.Values{"form": []string{"original"}},
		PostForm:         url.Values{"post": []string{"original"}},
		MultipartForm: &multipart.Form{
			Value: map[string][]string{"value": []string{"original"}},
			File: map[string][]*multipart.FileHeader{
				"file": {{Filename: "original", Header: textproto.MIMEHeader{"X-File": []string{"original"}}}},
			},
		},
		TLS: &tls.ConnectionState{
			ServerName:                  "pkgs.tailscale.com",
			PeerCertificates:            []*x509.Certificate{certificate},
			VerifiedChains:              [][]*x509.Certificate{{certificate}},
			SignedCertificateTimestamps: [][]byte{[]byte("sct")},
			OCSPResponse:                []byte("ocsp"),
			TLSUnique:                   []byte("unique"),
		},
		Body:     io.NopCloser(strings.NewReader("must be removed")),
		GetBody:  func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("must be removed")), nil },
		Response: &http.Response{StatusCode: http.StatusFound},
		Cancel:   make(chan struct{}),
	}
	clone, via, err := cloneRedirectPolicyInputs(request, []*http.Request{request})
	if err != nil {
		t.Fatal(err)
	}
	if clone.Body != nil || clone.GetBody != nil || clone.Response != nil || clone.Cancel != nil ||
		via[0].Body != nil || via[0].GetBody != nil || via[0].Response != nil || via[0].Cancel != nil {
		t.Fatal("redirect policy clone retained an I/O or cancellation handle")
	}

	mutate := func(value *http.Request) {
		value.URL.Host = "evil.example"
		value.Header["X-Header"][0] = "mutated"
		value.Trailer["X-Trailer"][0] = "mutated"
		value.TransferEncoding[0] = "chunked"
		value.Form["form"][0] = "mutated"
		value.PostForm["post"][0] = "mutated"
		value.MultipartForm.Value["value"][0] = "mutated"
		value.MultipartForm.File["file"][0].Filename = "mutated"
		value.MultipartForm.File["file"][0].Header["X-File"][0] = "mutated"
		value.TLS.OCSPResponse[0] = 'X'
		value.TLS.TLSUnique[0] = 'X'
		value.TLS.SignedCertificateTimestamps[0][0] = 'X'
		value.TLS.PeerCertificates[0].Raw[0] = 'X'
		value.TLS.PeerCertificates[0].SerialNumber.SetInt64(99)
		value.TLS.PeerCertificates[0].Subject.Organization[0] = "mutated"
		value.TLS.PeerCertificates[0].Extensions[0].Value[0] = 'X'
		value.TLS.VerifiedChains[0][0].Raw[0] = 'Y'
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		mutate(clone)
		mutate(via[0])
	}()
	<-done
	if request.URL.Host != "pkgs.tailscale.com" || request.Header.Get("X-Header") != "original" ||
		request.Trailer.Get("X-Trailer") != "original" || request.TransferEncoding[0] != "identity" ||
		request.Form.Get("form") != "original" || request.PostForm.Get("post") != "original" ||
		request.MultipartForm.Value["value"][0] != "original" ||
		request.MultipartForm.File["file"][0].Filename != "original" ||
		request.MultipartForm.File["file"][0].Header.Get("X-File") != "original" ||
		string(request.TLS.OCSPResponse) != "ocsp" || string(request.TLS.TLSUnique) != "unique" ||
		string(request.TLS.SignedCertificateTimestamps[0]) != "sct" ||
		string(certificate.Raw) != "certificate" || certificate.SerialNumber.Int64() != 7 ||
		certificate.Subject.Organization[0] != "original" || string(certificate.Extensions[0].Value) != "extension" {
		t.Fatal("delayed redirect-hook mutation escaped a detached clone")
	}
}

func TestDownloadExactMacSmallRejectsMalformedFinalResponseWithoutReading(t *testing.T) {
	t.Parallel()

	const basename = "Tailscale-1.100.1-macos.pkg.sha256"
	const exact = StablePackagesURL + basename
	tests := []struct {
		name     string
		response func(*http.Request, *recordingBody) *http.Response
	}{
		{"nil request", func(_ *http.Request, body *recordingBody) *http.Response {
			return &http.Response{StatusCode: http.StatusOK, Body: body}
		}},
		{"nil URL", func(request *http.Request, body *recordingBody) *http.Response {
			return &http.Response{StatusCode: http.StatusOK, Body: body, Request: &http.Request{Method: request.Method}}
		}},
		{"wrong URL", func(request *http.Request, body *recordingBody) *http.Response {
			clone := request.Clone(request.Context())
			clone.URL, _ = url.Parse(StablePackagesURL + "other")
			return &http.Response{StatusCode: http.StatusOK, Body: body, Request: clone}
		}},
		{"same serialization with RawPath", func(request *http.Request, body *recordingBody) *http.Response {
			clone := request.Clone(request.Context())
			urlCopy := *request.URL
			urlCopy.RawPath = urlCopy.Path
			clone.URL = &urlCopy
			return &http.Response{StatusCode: http.StatusOK, Body: body, Request: clone}
		}},
		{"request body", func(request *http.Request, body *recordingBody) *http.Response {
			clone := request.Clone(request.Context())
			clone.Body = io.NopCloser(strings.NewReader("unexpected"))
			return &http.Response{StatusCode: http.StatusOK, Body: body, Request: clone}
		}},
		{"request GetBody", func(request *http.Request, body *recordingBody) *http.Response {
			clone := request.Clone(request.Context())
			clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("unexpected")), nil }
			return &http.Response{StatusCode: http.StatusOK, Body: body, Request: clone}
		}},
		{"request ContentLength", func(request *http.Request, body *recordingBody) *http.Response {
			clone := request.Clone(request.Context())
			clone.ContentLength = 1
			return &http.Response{StatusCode: http.StatusOK, Body: body, Request: clone}
		}},
		{"request transfer encoding", func(request *http.Request, body *recordingBody) *http.Response {
			clone := request.Clone(request.Context())
			clone.TransferEncoding = []string{"identity"}
			return &http.Response{StatusCode: http.StatusOK, Body: body, Request: clone}
		}},
		{"nil body", func(request *http.Request, _ *recordingBody) *http.Response {
			return &http.Response{StatusCode: http.StatusOK, Request: request}
		}},
		{"bad status", func(request *http.Request, body *recordingBody) *http.Response {
			return &http.Response{StatusCode: http.StatusForbidden, Body: body, Request: request}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &recordingBody{reader: strings.NewReader("secret")}
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return test.response(request, body), nil
			})}
			if _, err := downloadExactMacSmall(context.Background(), client, exact, basename, 66); err == nil {
				t.Fatal("malformed response succeeded")
			}
			if body.reads != 0 {
				t.Fatalf("body reads = %d", body.reads)
			}
			if test.name != "nil body" && !body.closed {
				t.Fatal("body was not closed")
			}
		})
	}
}

func TestDownloadExactMacSmallRejectsNilResponseWithoutPanic(t *testing.T) {
	t.Parallel()

	const basename = "Tailscale-1.100.1-macos.pkg.sha256"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, nil })}
	if _, err := downloadExactMacSmall(context.Background(), client, StablePackagesURL+basename, basename, maximumMacChecksumBytes); err == nil {
		t.Fatal("nil transport response succeeded")
	}
}

func TestDownloadExactMacSmallRejectsPartialReadersAtExactBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawURL   string
		basename string
		maximum  int64
	}{
		{name: "index", rawURL: StablePackagesURL, basename: "", maximum: maximumMacPackagePageBytes},
		{name: "checksum", rawURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg.sha256", basename: "Tailscale-1.100.1-macos.pkg.sha256", maximum: maximumMacChecksumBytes},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &generatedPartialErrorReader{remaining: test.maximum}
			body := &recordingBody{reader: source}
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: request}, nil
			})}
			if value, err := downloadExactMacSmall(context.Background(), client, test.rawURL, test.basename, test.maximum); err == nil || value != nil {
				t.Fatalf("partial bounded response = %d bytes/%v", len(value), err)
			}
			if source.read != test.maximum || !body.closed {
				t.Fatalf("partial bounded read/closed = %d/%t, want %d/true", source.read, body.closed, test.maximum)
			}
		})
	}
}

func redirectResponse(request *http.Request, location string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{location}},
		Body:       io.NopCloser(strings.NewReader("redirect")),
		Request:    request,
	}
}

func okResponse(request *http.Request, value string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(value)),
		Request:    request,
	}
}

type recordingBody struct {
	reader io.Reader
	reads  int
	closed bool
}

func (body *recordingBody) Read(destination []byte) (int, error) {
	body.reads++
	return body.reader.Read(destination)
}

func (body *recordingBody) Close() error {
	body.closed = true
	return nil
}

func packageBodyClient(rawURL string, contentLength int64, body io.Reader) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != rawURL {
			return nil, errors.New("unexpected URL")
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        make(http.Header),
			Body:          io.NopCloser(body),
			ContentLength: contentLength,
			Request:       request,
		}, nil
	})}
}

type recordingSyncWriter struct {
	bytes   bytes.Buffer
	syncs   int
	syncErr error
}

func (writer *recordingSyncWriter) Write(value []byte) (int, error) {
	return writer.bytes.Write(value)
}

func (writer *recordingSyncWriter) Sync() error {
	writer.syncs++
	return writer.syncErr
}

type discardSyncWriter struct{}

func (discardSyncWriter) Write(value []byte) (int, error) { return len(value), nil }
func (discardSyncWriter) Sync() error                     { return nil }

type zeroReader struct{}

func (zeroReader) Read(value []byte) (int, error) {
	for index := range value {
		value[index] = 0
	}
	return len(value), nil
}

type countingZeroReader struct {
	remaining int64
	read      int64
}

func (reader *countingZeroReader) Read(value []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(value)) > reader.remaining {
		value = value[:reader.remaining]
	}
	for index := range value {
		value[index] = 0
	}
	reader.remaining -= int64(len(value))
	reader.read += int64(len(value))
	return len(value), nil
}

type countingDiscardSyncWriter struct {
	written int64
	syncs   int
}

func (writer *countingDiscardSyncWriter) Write(value []byte) (int, error) {
	writer.written += int64(len(value))
	return len(value), nil
}

func (writer *countingDiscardSyncWriter) Sync() error {
	writer.syncs++
	return nil
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("reader failed") }

type partialErrorReader struct {
	value []byte
	done  bool
}

func (reader *partialErrorReader) Read(destination []byte) (int, error) {
	if reader.done {
		return 0, io.EOF
	}
	reader.done = true
	count := copy(destination, reader.value)
	return count, errors.New("reader failed after partial bytes")
}

type generatedPartialErrorReader struct {
	remaining int64
	read      int64
}

func (reader *generatedPartialErrorReader) Read(destination []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, errors.New("reader failed at exact boundary")
	}
	if int64(len(destination)) > reader.remaining {
		destination = destination[:reader.remaining]
	}
	for index := range destination {
		destination[index] = '0'
	}
	reader.remaining -= int64(len(destination))
	reader.read += int64(len(destination))
	if reader.remaining == 0 {
		return len(destination), errors.New("reader failed at exact boundary")
	}
	return len(destination), nil
}
