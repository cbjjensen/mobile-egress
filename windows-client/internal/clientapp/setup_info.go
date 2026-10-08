package clientapp

import (
	"context"
	"errors"
	"io"
	"mobile-egress/internal/destinationpolicy"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
)

type SetupInformation struct {
	RuntimeMode        string   `json:"runtimeMode"`
	Platform           string   `json:"platform"`
	SuggestedName      string   `json:"suggestedName"`
	LocalAddresses     []string `json:"localAddresses"`
	DefaultBindAddress string   `json:"defaultBindAddress"`
	DefaultPublicPort  uint16   `json:"defaultPublicPort"`
}

// SetupInfo is local, unprivileged information; it performs no network lookup.
func (app *App) SetupInfo() SetupInformation {
	hostname, _ := os.Hostname()
	addresses, _ := net.InterfaceAddrs()
	info := makeSetupInfo(runtime.GOOS, hostname, addresses)
	info.RuntimeMode = "service"
	if mode, ok := app.service.(interface{ RuntimeMode() string }); ok {
		info.RuntimeMode = mode.RuntimeMode()
	}
	return info
}

func makeSetupInfo(platform, hostname string, addresses []net.Addr) SetupInformation {
	name := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, hostname))
	if name == "" {
		name = "My computer"
	}
	if runes := []rune(name); len(runes) > 80 {
		name = string(runes[:80])
	}
	unique := make(map[string]bool)
	for _, address := range addresses {
		ipnet, ok := address.(*net.IPNet)
		if !ok || !ipnet.IP.IsGlobalUnicast() || ipnet.IP.IsLoopback() {
			continue
		}
		unique[ipnet.IP.String()] = true
	}
	local := make([]string, 0, len(unique))
	for address := range unique {
		local = append(local, address)
	}
	sort.Strings(local)
	if len(local) > 16 {
		local = local[:16]
	}
	return SetupInformation{Platform: platform, SuggestedName: name, LocalAddresses: local, DefaultBindAddress: ":8443", DefaultPublicPort: 8443}
}

type AddressSuggestion struct {
	Address  string `json:"address"`
	Endpoint string `json:"endpoint"`
	IPv4     string `json:"ipv4"`
	IPv6     string `json:"ipv6"`
	Provider string `json:"provider"`
}

// DiscoverPublicAddress only suggests an outgoing address, never reachability.
// The GUI invokes this explicitly during setup; the service has no dependency
// on this provider and no identities or pairing material enter these requests.
func (app *App) DiscoverPublicAddress() (AddressSuggestion, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	return discoverPublicAddress(app.lifetime, &http.Client{Transport: transport})
}

func discoverPublicAddress(parent context.Context, client *http.Client) (AddressSuggestion, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	bounded := *client
	bounded.Timeout = 5 * time.Second
	bounded.Jar = nil
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	type result struct {
		address string
		ipv6    bool
	}
	results := make(chan result, 2)
	for _, provider := range []struct {
		endpoint string
		ipv6     bool
	}{{"https://api.ipify.org", false}, {"https://api6.ipify.org", true}} {
		go func() {
			value := result{ipv6: provider.ipv6}
			defer func() { results <- value }()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.endpoint, nil)
			if err != nil {
				return
			}
			response, err := bounded.Do(request)
			if err != nil {
				return
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return
			}
			raw, err := io.ReadAll(io.LimitReader(response.Body, 129))
			if err != nil || len(raw) > 128 {
				return
			}
			address, err := netip.ParseAddr(strings.TrimSpace(string(raw)))
			if err != nil || address.Is6() != provider.ipv6 || address.Zone() != "" || destinationpolicy.ValidatePublicTCPAddress(address, 8443) != nil {
				return
			}
			value.address = address.String()
		}()
	}
	suggestion := AddressSuggestion{Provider: "ipify"}
collect:
	for range 2 {
		select {
		case value := <-results:
			if value.ipv6 {
				suggestion.IPv6 = value.address
			} else {
				suggestion.IPv4 = value.address
			}
		case <-ctx.Done():
			break collect
		}
	}
	suggestion.Address = suggestion.IPv4
	if suggestion.Address == "" {
		suggestion.Address = suggestion.IPv6
	}
	if suggestion.Address == "" || parent.Err() != nil {
		return AddressSuggestion{}, errors.New("Public address lookup was unavailable. Enter your public IP or hostname manually, or retry the lookup.")
	}
	suggestion.Endpoint = "https://" + net.JoinHostPort(suggestion.Address, "8443")
	return suggestion, nil
}
