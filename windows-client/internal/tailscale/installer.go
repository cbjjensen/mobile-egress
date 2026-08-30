package tailscale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maximumMSIBytes = 200 << 20

var tailscalePublisherPattern = regexp.MustCompile(`(?i)(?:^|,)\s*(?:CN|O)=Tailscale Inc\.(?:,|$)`)

type Signature struct {
	Valid   bool   `json:"valid"`
	Subject string `json:"subject"`
}

type Installer struct {
	HTTPClient         *http.Client
	VerifyAuthenticode func(string) (Signature, error)
	ElevatedInstall    func(string) error
}

func (installer Installer) Install(ctx context.Context) (Release, error) {
	if installer.VerifyAuthenticode == nil || installer.ElevatedInstall == nil {
		return Release{}, errors.New("Tailscale installer verification and elevation are required")
	}
	client := installer.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	page, err := downloadSmall(ctx, client, StablePackagesURL, 8<<20)
	if err != nil {
		return Release{}, errors.New("download Tailscale stable package index")
	}
	release, err := ParseStablePackagePage(page)
	if err != nil {
		return Release{}, err
	}
	if err := ValidateReleaseURL(release.MSIURL); err != nil {
		return Release{}, err
	}
	checksumResponse, err := downloadSmall(ctx, client, release.ChecksumURL, 4096)
	if err != nil {
		return Release{}, errors.New("download Tailscale MSI checksum")
	}
	expectedDigest, err := ParseSHA256(checksumResponse, filepath.Base(release.MSIURL))
	if err != nil {
		return Release{}, err
	}
	file, err := os.CreateTemp("", "tailscale-*.msi")
	if err != nil {
		return Release{}, errors.New("create Tailscale MSI staging file")
	}
	path := file.Name()
	defer os.Remove(path)
	if err := downloadMSI(ctx, client, release.MSIURL, file, expectedDigest); err != nil {
		_ = file.Close()
		return Release{}, err
	}
	if err := file.Close(); err != nil {
		return Release{}, errors.New("close Tailscale MSI staging file")
	}
	signature, err := installer.VerifyAuthenticode(path)
	if err != nil {
		return Release{}, errors.New("verify Tailscale Authenticode signature")
	}
	if err := ValidateSignature(signature); err != nil {
		return Release{}, err
	}
	if err := installer.ElevatedInstall(path); err != nil {
		return Release{}, fmt.Errorf("Tailscale installation was cancelled or failed: %w", err)
	}
	return release, nil
}

func ValidateSignature(signature Signature) error {
	if !signature.Valid || !tailscalePublisherPattern.MatchString(strings.TrimSpace(signature.Subject)) {
		return errors.New("Tailscale MSI does not have a valid Tailscale Inc. Authenticode signature")
	}
	return nil
}

func downloadSmall(ctx context.Context, client *http.Client, rawURL string, maximum int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	value, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil || int64(len(value)) == 0 || int64(len(value)) > maximum {
		return nil, errors.New("download is missing or too large")
	}
	return value, nil
}

func downloadMSI(ctx context.Context, client *http.Client, rawURL string, destination *os.File, expectedDigest string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("download Tailscale MSI")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download Tailscale MSI: HTTP %d", response.StatusCode)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(response.Body, maximumMSIBytes+1))
	if err != nil || written == 0 || written > maximumMSIBytes {
		return errors.New("Tailscale MSI is missing or too large")
	}
	if digest := hex.EncodeToString(hash.Sum(nil)); digest != strings.ToLower(expectedDigest) {
		return errors.New("Tailscale MSI SHA-256 verification failed")
	}
	if err := destination.Sync(); err != nil {
		return errors.New("flush Tailscale MSI staging file")
	}
	return nil
}
