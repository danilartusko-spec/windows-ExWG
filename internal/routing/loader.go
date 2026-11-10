package routing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"nexusclient/internal/debuglog"
)

const (
	maxProfileBytes             = 2 << 20
	maxGeoDataBytes             = 64 << 20
	maxRedirects                = 5
	routingHelpHost             = "routing.help"
	routingHelpOfficialDeepLink = "https://raw.githubusercontent.com/hydraponique/roscomvpn-routing/main/HAPP/DEFAULT.DEEPLINK"
)

type Loader struct {
	client *http.Client
}

type Bundle struct {
	Profile *Profile
	GeoSite []byte
	GeoIP   []byte
}

func NewLoader() *Loader {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = publicDialContext
	transport.ForceAttemptHTTP2 = true
	return &Loader{client: &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func NewLoaderWithClient(client *http.Client) *Loader {
	return &Loader{client: client}
}

func (loader *Loader) LoadProfile(ctx context.Context, input string) (*Profile, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "{") || strings.HasPrefix(strings.ToLower(input), "happ://") {
		return ParseProfile(input)
	}
	parsed, err := url.Parse(input)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		if plainProfile, ok := parsePlainRuleProfile(input); ok {
			return plainProfile, nil
		}
		return nil, errors.New("ожидается JSON, happ://routing/... или публичная HTTPS-ссылка")
	}

	profile, primaryErr := loader.loadProfileURL(ctx, parsed)
	if primaryErr == nil {
		if profile.Name == "" || profile.Name == "Custom" {
			profile.Name = parsed.Hostname()
		}
		return profile, nil
	}
	if !strings.EqualFold(parsed.Hostname(), routingHelpHost) {
		return nil, primaryErr
	}
	fallback, err := url.Parse(routingHelpOfficialDeepLink)
	if err != nil {
		return nil, primaryErr
	}
	profile, fallbackErr := loader.loadProfileURL(ctx, fallback)
	if fallbackErr == nil {
		debuglog.Event("routing.profile.fallback", "shortcut=%s officialSource=%s", parsed.Redacted(), fallback.Redacted())
		return profile, nil
	}
	return nil, errors.Join(primaryErr, fmt.Errorf("официальный резервный источник: %w", fallbackErr))
}

func (loader *Loader) loadProfileURL(ctx context.Context, parsed *url.URL) (*Profile, error) {
	current := parsed
	for range maxRedirects {
		body, location, err := loader.fetch(ctx, current.String(), maxProfileBytes)
		if err != nil {
			return nil, err
		}
		if location == "" {
			return ParseProfile(string(body))
		}
		if strings.HasPrefix(strings.ToLower(location), "happ://") {
			return ParseProfile(location)
		}
		next, err := current.Parse(location)
		if err != nil || !strings.EqualFold(next.Scheme, "https") || next.Host == "" {
			return nil, errors.New("ссылка перенаправляет на неподдерживаемый адрес")
		}
		current = next
	}
	return nil, errors.New("слишком много перенаправлений профиля")
}

func (loader *Loader) LoadGeoData(ctx context.Context, sourceURL string) ([]byte, error) {
	parsed, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return nil, errors.New("geo-файл должен загружаться по HTTPS")
	}
	current := parsed
	for range maxRedirects {
		body, location, err := loader.fetch(ctx, current.String(), maxGeoDataBytes)
		if err != nil {
			return nil, err
		}
		if location == "" {
			return body, nil
		}
		next, err := current.Parse(location)
		if err != nil || !strings.EqualFold(next.Scheme, "https") || next.Host == "" {
			return nil, errors.New("geo-файл перенаправлен на неподдерживаемый адрес")
		}
		current = next
	}
	return nil, errors.New("слишком много перенаправлений geo-файла")
}

func (loader *Loader) LoadBundle(ctx context.Context, input string) (*Bundle, error) {
	profile, err := loader.LoadProfile(ctx, input)
	if err != nil {
		return nil, err
	}
	bundle := &Bundle{Profile: profile}
	if profile.GeoSiteURL != "" {
		bundle.GeoSite, err = loader.LoadGeoData(ctx, profile.GeoSiteURL)
		if err != nil {
			return nil, err
		}
	}
	if profile.GeoIPURL != "" {
		bundle.GeoIP, err = loader.LoadGeoData(ctx, profile.GeoIPURL)
		if err != nil {
			return nil, err
		}
	}
	if _, err := CompileMatcher(profile, bundle.GeoSite, bundle.GeoIP); err != nil {
		return nil, err
	}
	return bundle, nil
}

func (loader *Loader) fetch(ctx context.Context, sourceURL string, limit int64) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", "NexusClient/0.1")
	request.Header.Set("Accept", "application/json, application/octet-stream, text/plain")
	response, err := loader.client.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("не удалось загрузить %s: %w", request.URL.Host, err)
	}
	defer response.Body.Close()
	debuglog.Event("routing.HTTP", "url=%s status=%s protocol=%s contentLength=%d", request.URL.Redacted(), response.Status, response.Proto, response.ContentLength)

	if response.StatusCode >= 300 && response.StatusCode < 400 {
		location := strings.TrimSpace(response.Header.Get("Location"))
		if location == "" {
			return nil, "", fmt.Errorf("сервер вернул перенаправление %d без адреса", response.StatusCode)
		}
		return nil, location, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("сервер %s вернул HTTP %d", request.URL.Host, response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, "", fmt.Errorf("ответ превышает разрешённый размер %d байт", limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > limit {
		return nil, "", fmt.Errorf("ответ превышает разрешённый размер %d байт", limit)
	}
	digest := sha256.Sum256(data)
	debuglog.Event("routing.download", "url=%s protocol=%s bytes=%d sha256=%x", request.URL.Redacted(), response.Proto, len(data), digest)
	return data, "", nil
}

func publicDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, candidate := range addresses {
		if !publicAddress(candidate) {
			continue
		}
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("адрес назначения не является публичным")
}

func publicAddress(address netip.Addr) bool {
	address = address.Unmap()
	return address.IsValid() &&
		!address.IsPrivate() &&
		!address.IsLoopback() &&
		!address.IsLinkLocalUnicast() &&
		!address.IsLinkLocalMulticast() &&
		!address.IsMulticast() &&
		!address.IsUnspecified()
}
