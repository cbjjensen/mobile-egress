// Package tailscale validates the narrow Tailscale status, package, and raw
// TCP Funnel surface used by the local Mobile Egress bridge.
package tailscale

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	StablePackagesURL = "https://pkgs.tailscale.com/stable/"
	PublicPort        = 8443
	LocalRelayTarget  = "tcp://127.0.0.1:8443"
)

var (
	amd64MSIPattern = regexp.MustCompile(`tailscale-setup-([0-9]+\.[0-9]+\.[0-9]+)-amd64\.msi`)
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Status struct {
	BackendState string `json:"backendState"`
	Online       bool   `json:"online"`
	FunnelReady  bool   `json:"funnelReady"`
	FQDN         string `json:"fqdn"`
	PublicURL    string `json:"publicUrl"`
}

func ParseFunnelStatus(raw []byte, fqdn string) (bool, error) {
	if len(raw) == 0 || len(raw) > 4<<20 || !validFunnelFQDN(fqdn) {
		return false, errors.New("Tailscale Funnel status is missing or invalid")
	}
	var wire struct {
		TCP map[string]struct {
			TCPForward    string `json:"TCPForward"`
			TerminateTLS  string `json:"TerminateTLS"`
			HTTPS         bool   `json:"HTTPS"`
			HTTP          bool   `json:"HTTP"`
			ProxyProtocol int    `json:"ProxyProtocol"`
		} `json:"TCP"`
		AllowFunnel map[string]bool `json:"AllowFunnel"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&wire); err != nil {
		return false, errors.New("Tailscale returned invalid Funnel status")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return false, errors.New("Tailscale returned invalid Funnel status")
	}
	handler, exists := wire.TCP[strconv.Itoa(PublicPort)]
	if !exists || handler.TCPForward != "127.0.0.1:8443" || handler.TerminateTLS != "" || handler.HTTPS || handler.HTTP || handler.ProxyProtocol != 0 {
		return false, nil
	}
	return wire.AllowFunnel[fqdn+":"+strconv.Itoa(PublicPort)], nil
}

type Release struct {
	Version     string `json:"version"`
	MSIURL      string `json:"msiUrl"`
	ChecksumURL string `json:"checksumUrl"`
}

func ParseStatus(raw []byte) (Status, error) {
	if len(raw) == 0 || len(raw) > 4<<20 {
		return Status{}, errors.New("Tailscale status is missing or too large")
	}
	var wire struct {
		BackendState string `json:"BackendState"`
		Self         *struct {
			DNSName string `json:"DNSName"`
			Online  bool   `json:"Online"`
		} `json:"Self"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&wire); err != nil || wire.Self == nil {
		return Status{}, errors.New("Tailscale returned invalid status")
	}
	fqdn := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(wire.Self.DNSName), "."))
	if wire.BackendState != "Running" || !wire.Self.Online || !validFunnelFQDN(fqdn) {
		return Status{}, errors.New("Tailscale is not online with a Funnel-capable ts.net name")
	}
	return Status{
		BackendState: wire.BackendState, Online: true, FQDN: fqdn,
		PublicURL: "https://" + fqdn + ":" + strconv.Itoa(PublicPort),
	}, nil
}

func FunnelArguments() []string {
	return []string{"funnel", "--bg", "--yes", "--tcp=8443", LocalRelayTarget}
}

func UnattendedArguments() []string {
	return []string{"up", "--unattended=true"}
}

func ParseStablePackagePage(raw []byte) (Release, error) {
	if len(raw) == 0 || len(raw) > 8<<20 {
		return Release{}, errors.New("Tailscale package page is missing or too large")
	}
	matches := amd64MSIPattern.FindAllSubmatch(raw, -1)
	if len(matches) == 0 {
		return Release{}, errors.New("Tailscale package page has no amd64 MSI")
	}
	bestVersion := ""
	bestParts := [3]int{}
	for _, match := range matches {
		parts, err := parseVersion(string(match[1]))
		if err != nil {
			continue
		}
		if bestVersion == "" || versionGreater(parts, bestParts) {
			bestVersion = string(match[1])
			bestParts = parts
		}
	}
	if bestVersion == "" {
		return Release{}, errors.New("Tailscale package page has no valid amd64 MSI")
	}
	filename := "tailscale-setup-" + bestVersion + "-amd64.msi"
	msiURL := StablePackagesURL + filename
	return Release{Version: bestVersion, MSIURL: msiURL, ChecksumURL: msiURL + ".sha256"}, nil
}

func ParseSHA256(raw []byte, expectedFilename string) (string, error) {
	if len(raw) == 0 || len(raw) > 4096 || path.Base(expectedFilename) != expectedFilename {
		return "", errors.New("invalid Tailscale checksum response")
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 || !sha256Pattern.MatchString(strings.ToLower(fields[0])) || strings.TrimPrefix(fields[1], "*") != expectedFilename {
		return "", errors.New("invalid Tailscale checksum response")
	}
	return strings.ToLower(fields[0]), nil
}

func ValidateReleaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "pkgs.tailscale.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid Tailscale package URL")
	}
	return nil
}

func parseVersion(value string) ([3]int, error) {
	var result [3]int
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return result, errors.New("invalid version")
	}
	for index, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return result, fmt.Errorf("invalid version component")
		}
		result[index] = parsed
	}
	return result, nil
}

func versionGreater(left, right [3]int) bool {
	for index := range left {
		if left[index] != right[index] {
			return left[index] > right[index]
		}
	}
	return false
}

func validFunnelFQDN(value string) bool {
	if !strings.HasSuffix(value, ".ts.net") || len(value) > 253 {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 4 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
				return false
			}
		}
	}
	return true
}
