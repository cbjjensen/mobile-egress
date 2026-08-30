package tailscale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInstallerVerifiesChecksumAuthenticodeAndPublisherBeforeElevation(t *testing.T) {
	t.Parallel()

	msi := []byte("fake signed MSI bytes")
	digest := sha256.Sum256(msi)
	digestText := hex.EncodeToString(digest[:])
	page := `<a href="tailscale-setup-1.100.1-amd64.msi">download</a>`
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch request.URL.String() {
		case StablePackagesURL:
			body = page
		case StablePackagesURL + "tailscale-setup-1.100.1-amd64.msi.sha256":
			body = digestText + "  tailscale-setup-1.100.1-amd64.msi\n"
		case StablePackagesURL + "tailscale-setup-1.100.1-amd64.msi":
			body = string(msi)
		default:
			t.Fatalf("unexpected download URL %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	verified := false
	elevated := false
	installer := Installer{
		HTTPClient: client,
		VerifyAuthenticode: func(path string) (Signature, error) {
			verified = true
			return Signature{Valid: true, Subject: "CN=Tailscale Inc., O=Tailscale Inc."}, nil
		},
		ElevatedInstall: func(path string) error {
			if !verified {
				t.Fatal("elevation occurred before signature verification")
			}
			elevated = true
			return nil
		},
	}
	release, err := installer.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !verified || !elevated || release.Version != "1.100.1" {
		t.Fatalf("Install() = %#v, verified=%t elevated=%t", release, verified, elevated)
	}
}

func TestInstallerPreservesTheSafeElevatedInstallerFailure(t *testing.T) {
	t.Parallel()

	msi := []byte("fake signed MSI bytes")
	digest := sha256.Sum256(msi)
	page := `<a href="tailscale-setup-1.100.1-amd64.msi">download</a>`
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch request.URL.String() {
		case StablePackagesURL:
			body = page
		case StablePackagesURL + "tailscale-setup-1.100.1-amd64.msi.sha256":
			body = hex.EncodeToString(digest[:])
		case StablePackagesURL + "tailscale-setup-1.100.1-amd64.msi":
			body = string(msi)
		default:
			t.Fatalf("unexpected download URL %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	installer := Installer{
		HTTPClient:         client,
		VerifyAuthenticode: func(string) (Signature, error) { return Signature{Valid: true, Subject: "CN=Tailscale Inc."}, nil },
		ElevatedInstall:    func(string) error { return errors.New("Windows Installer code 1632") },
	}
	_, err := installer.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "1632") {
		t.Fatalf("Install() error = %v, want safe Windows Installer code", err)
	}
}

func TestInstallerRejectsWrongAuthenticodePublisher(t *testing.T) {
	t.Parallel()

	if err := ValidateSignature(Signature{Valid: true, Subject: "CN=Another Publisher"}); err == nil {
		t.Fatal("ValidateSignature() accepted another publisher")
	}
	if err := ValidateSignature(Signature{Valid: false, Subject: "CN=Tailscale Inc."}); err == nil {
		t.Fatal("ValidateSignature() accepted an invalid signature")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
