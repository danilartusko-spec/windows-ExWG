package routing

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestLoaderFollowsRoutingHelpStyleRedirect(t *testing.T) {
	deepLink := "happ://routing/onadd/" + base64.StdEncoding.EncodeToString([]byte(profileJSON))
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{deepLink}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    request,
			}, nil
		}),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	profile, err := NewLoaderWithClient(client).LoadProfile(context.Background(), "https://routing.help/")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "RoscomVPN" {
		t.Fatalf("unexpected profile %q", profile.Name)
	}
}

func TestLoaderRejectsPlainHTTP(t *testing.T) {
	_, err := NewLoaderWithClient(http.DefaultClient).LoadProfile(context.Background(), "http://routing.help/")
	if err == nil {
		t.Fatal("expected HTTP URL rejection")
	}
}

func TestLoaderUsesOfficialSourceWhenRoutingHelpShortcutIsUnavailable(t *testing.T) {
	deepLink := "happ://routing/onadd/" + base64.StdEncoding.EncodeToString([]byte(profileJSON))
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Hostname() {
		case routingHelpHost:
			return nil, errors.New("shortcut timeout")
		case "raw.githubusercontent.com":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(deepLink)),
				Request:    request,
			}, nil
		default:
			return nil, errors.New("unexpected host")
		}
	})}
	profile, err := NewLoaderWithClient(client).LoadProfile(context.Background(), "https://routing.help/")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "RoscomVPN" {
		t.Fatalf("unexpected profile %q", profile.Name)
	}
}

func TestPublicAddressRejectsLocalNetworks(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "::1", "fe80::1"} {
		if publicAddress(mustAddr(t, value)) {
			t.Fatalf("%s unexpectedly accepted as public", value)
		}
	}
	if !publicAddress(mustAddr(t, "1.1.1.1")) {
		t.Fatal("public address rejected")
	}
}

func mustAddr(t *testing.T, value string) netip.Addr {
	t.Helper()
	address, err := netip.ParseAddr(value)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func TestLoaderCustomURLNeverTouchesRoutingHelp(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Hostname(), "routing.help") || strings.Contains(request.URL.Hostname(), "githubusercontent.com") {
			t.Fatalf("custom URL must never contact routing.help or its fallback: %s", request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"DirectSites":["domain:ru"],"ProxySites":["domain:example.com"]}`)),
			Request:    request,
		}, nil
	})}
	bundle, err := NewLoaderWithClient(client).LoadBundle(context.Background(), "https://rules.myprovider.example/routing.json")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Profile.Name != "rules.myprovider.example" {
		t.Fatalf("expected hostname as default profile name, got %q", bundle.Profile.Name)
	}
}
