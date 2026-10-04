package clientapp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type setupRoundTripper func(*http.Request) (*http.Response, error)

func (f setupRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func lookupResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestSetupDiscoveryQueriesBothFamiliesWithoutSensitiveData(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	client := &http.Client{Transport: setupRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.RawQuery != "" || r.Body != nil || len(r.Header.Values("Authorization")) != 0 || len(r.Cookies()) != 0 {
			t.Error("lookup included unexpected request data")
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Error("lookup lacks five-second overall deadline")
		}
		mu.Lock()
		seen[r.URL.Host] = true
		mu.Unlock()
		if r.URL.Host == "api.ipify.org" {
			return lookupResponse("8.8.8.8\n"), nil
		}
		if r.URL.Host == "api6.ipify.org" {
			return lookupResponse("2606:4700:4700::1111"), nil
		}
		return nil, errors.New("unexpected provider")
	})}
	result, err := discoverPublicAddress(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if result.Address != "8.8.8.8" || result.Endpoint != "https://8.8.8.8:8443" || result.IPv6 != "2606:4700:4700::1111" || result.Provider != "ipify" || len(seen) != 2 {
		t.Fatalf("unexpected suggestion: %#v", result)
	}
}

func TestSetupDiscoveryIPv6FallbackAndInvalidResponses(t *testing.T) {
	for _, invalid := range []string{"10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254", "203.0.113.1", "not an IP", strings.Repeat("8", 129), "2606:4700:4700::1111"} {
		t.Run(invalid[:min(len(invalid), 18)], func(t *testing.T) {
			client := &http.Client{Transport: setupRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "api.ipify.org" {
					return lookupResponse(invalid), nil
				}
				return lookupResponse("2606:4700:4700::1111"), nil
			})}
			result, err := discoverPublicAddress(context.Background(), client)
			if err != nil || result.Endpoint != "https://[2606:4700:4700::1111]:8443" || result.IPv4 != "" {
				t.Fatalf("invalid IPv4 accepted or IPv6 malformed: %#v %v", result, err)
			}
		})
	}
}

func TestSetupDiscoveryRejectsRedirectsAndSanitizesFailure(t *testing.T) {
	var calls int
	var mu sync.Mutex
	client := &http.Client{Transport: setupRoundTripper(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://untrusted.example/private"}}, Body: io.NopCloser(strings.NewReader("sensitive-provider-response"))}, nil
	})}
	_, err := discoverPublicAddress(context.Background(), client)
	if err == nil || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "untrusted") || calls != 2 {
		t.Fatalf("redirect followed or unsafe failure: calls=%d err=%v", calls, err)
	}
}

func TestSetupDiscoveryConcurrentAndCancelable(t *testing.T) {
	started := make(chan struct{}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Transport: setupRoundTripper(func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	done := make(chan error, 1)
	go func() { _, err := discoverPublicAddress(ctx, client); done <- err }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("lookups were not concurrent")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not bound lookup")
	}
}

func TestSetupInfoOnlyOffersUsefulLocalAddresses(t *testing.T) {
	addresses := []net.Addr{&net.IPNet{IP: net.ParseIP("127.0.0.1")}, &net.IPNet{IP: net.ParseIP("10.0.0.5")}, &net.IPNet{IP: net.ParseIP("10.0.0.5")}, &net.IPNet{IP: net.ParseIP("::1")}, &net.IPNet{IP: net.ParseIP("fe80::1")}, &net.IPNet{IP: net.ParseIP("2606:4700::1234")}}
	info := makeSetupInfo("darwin", "  My Mac\n ", addresses)
	if info.Platform != "darwin" || info.SuggestedName != "My Mac" || info.DefaultBindAddress != ":8443" || info.DefaultPublicPort != 8443 || strings.Join(info.LocalAddresses, ",") != "10.0.0.5,2606:4700::1234" {
		t.Fatalf("unexpected setup information: %#v", info)
	}
}
