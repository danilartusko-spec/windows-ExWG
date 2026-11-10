package routing

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type BoolValue bool

func (value *BoolValue) UnmarshalJSON(data []byte) error {
	var boolean bool
	if err := json.Unmarshal(data, &boolean); err == nil {
		*value = BoolValue(boolean)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return errors.New("ожидается true/false")
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(text))
	if err != nil {
		return errors.New("ожидается true/false")
	}
	*value = BoolValue(parsed)
	return nil
}

// MarshalJSON keeps provider fields that this client does not understand yet.
// They remain visible in diagnostics instead of silently disappearing after a
// cache refresh.
func (profile Profile) MarshalJSON() ([]byte, error) {
	type plain Profile
	data, err := json.Marshal(plain(profile))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for name, value := range profile.AdditionalUnknownFields {
		if _, exists := fields[name]; !exists {
			fields[name] = value
		}
	}
	return json.Marshal(fields)
}

func (profile *Profile) Warnings() []string {
	var result []string
	if profile.UseChunkFiles {
		result = append(result, "Профиль предлагает чанковое обновление GeoSite/GeoIP. Windows скачивает полные DAT: правила применяются полностью, но обновление использует больше трафика.")
	}
	for name := range profile.AdditionalUnknownFields {
		result = append(result, "Не поддерживается: "+name+" (поле сохранено)")
	}
	sort.Strings(result)
	return result
}

type Profile struct {
	Name              string            `json:"Name"`
	GlobalProxy       BoolValue         `json:"GlobalProxy"`
	UseChunkFiles     BoolValue         `json:"UseChunkFiles"`
	RemoteDNS         string            `json:"RemoteDns"`
	DomesticDNS       string            `json:"DomesticDns"`
	RemoteDNSType     string            `json:"RemoteDNSType"`
	RemoteDNSDomain   string            `json:"RemoteDNSDomain"`
	RemoteDNSIP       string            `json:"RemoteDNSIP"`
	DomesticDNSType   string            `json:"DomesticDNSType"`
	DomesticDNSDomain string            `json:"DomesticDNSDomain"`
	DomesticDNSIP     string            `json:"DomesticDNSIP"`
	GeoIPURL          string            `json:"Geoipurl"`
	GeoSiteURL        string            `json:"Geositeurl"`
	LastUpdated       string            `json:"LastUpdated"`
	DNSHosts          map[string]string `json:"DnsHosts"`
	RouteOrder        string            `json:"RouteOrder"`
	DirectSites       []string          `json:"DirectSites"`
	DirectIP          []string          `json:"DirectIp"`
	ProxySites        []string          `json:"ProxySites"`
	ProxyIP           []string          `json:"ProxyIp"`
	BlockSites        []string          `json:"BlockSites"`
	BlockIP           []string          `json:"BlockIp"`
	DomainStrategy    string            `json:"DomainStrategy"`
	FakeDNS           BoolValue         `json:"FakeDNS"`

	AdditionalUnknownFields map[string]json.RawMessage `json:"-"`
}

func ParseProfile(input string) (*Profile, error) {
	input = strings.TrimSpace(strings.TrimPrefix(input, "\ufeff"))
	if input == "" {
		return nil, errors.New("профиль маршрутизации пуст")
	}

	if strings.HasPrefix(strings.ToLower(input), "happ://") {
		decoded, err := decodeHappLink(input)
		if err != nil {
			return nil, err
		}
		input = strings.TrimSpace(decoded)
	} else if embedded, ok := extractEmbeddedHappLink(input); ok {
		decoded, err := decodeHappLink(embedded)
		if err != nil {
			return nil, err
		}
		input = strings.TrimSpace(decoded)
	} else if !strings.HasPrefix(input, "{") {
		if decoded, err := decodeBase64Payload(input); err == nil {
			trimmed := strings.TrimSpace(strings.TrimPrefix(decoded, "\ufeff"))
			if strings.HasPrefix(trimmed, "{") {
				input = trimmed
			} else if strings.HasPrefix(strings.ToLower(trimmed), "happ://") {
				if fromHapp, err := decodeHappLink(trimmed); err == nil {
					input = strings.TrimSpace(fromHapp)
				}
			}
		}
	}

	if !strings.HasPrefix(input, "{") {
		if plainProfile, ok := parsePlainRuleProfile(input); ok {
			return plainProfile, nil
		}
	}

	var allFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &allFields); err != nil {
		return nil, fmt.Errorf("не удалось прочитать JSON профиля: %w", err)
	}
	var profile Profile
	if err := json.Unmarshal([]byte(input), &profile); err != nil {
		return nil, fmt.Errorf("неверные поля профиля: %w", err)
	}

	known := map[string]struct{}{
		"name": {}, "globalproxy": {}, "usechunkfiles": {}, "remotedns": {}, "domesticdns": {},
		"remotednstype": {}, "remotednsdomain": {}, "remotednsip": {}, "domesticdnstype": {},
		"domesticdnsdomain": {}, "domesticdnsip": {}, "geoipurl": {}, "geositeurl": {},
		"lastupdated": {}, "dnshosts": {}, "routeorder": {}, "directsites": {}, "directip": {},
		"proxysites": {}, "proxyip": {}, "blocksites": {}, "blockip": {}, "domainstrategy": {},
		"fakedns": {},
	}
	hasGlobalProxy := false
	profile.AdditionalUnknownFields = make(map[string]json.RawMessage)
	for name, value := range allFields {
		lower := strings.ToLower(name)
		if lower == "globalproxy" {
			hasGlobalProxy = true
		}
		if _, ok := known[lower]; !ok {
			profile.AdditionalUnknownFields[name] = value
		}
	}
	if !hasGlobalProxy && len(profile.ProxySites) == 0 && len(profile.ProxyIP) == 0 {
		profile.GlobalProxy = true
	}

	if err := profile.Validate(); err != nil {
		return nil, err
	}
	return &profile, nil
}

func extractEmbeddedHappLink(input string) (string, bool) {
	lower := strings.ToLower(input)
	idx := strings.Index(lower, "happ://routing/")
	if idx < 0 {
		return "", false
	}
	rest := input[idx:]
	end := strings.IndexAny(rest, " \t\r\n\"'<>")
	if end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest), true
}

func parsePlainRuleProfile(input string) (*Profile, bool) {
	overrides, err := ParseOverrides(input)
	if err != nil {
		return nil, false
	}
	ruleCount := len(overrides.DirectSites) + len(overrides.DirectIP) +
		len(overrides.ProxySites) + len(overrides.ProxyIP) +
		len(overrides.BlockSites) + len(overrides.BlockIP) +
		len(overrides.DNS)
	if ruleCount == 0 {
		return nil, false
	}
	profile := &Profile{
		Name:           "Custom",
		GlobalProxy:    true,
		RouteOrder:     "block-proxy-direct",
		DomainStrategy: "IPIfNonMatch",
		DirectSites:    overrides.DirectSites,
		DirectIP:       overrides.DirectIP,
		ProxySites:     overrides.ProxySites,
		ProxyIP:        overrides.ProxyIP,
		BlockSites:     overrides.BlockSites,
		BlockIP:        overrides.BlockIP,
		DNSHosts:       make(map[string]string),
	}
	if directDNS := overrides.DNS["dns-direct"]; len(directDNS) > 0 {
		applyPlainDNSEndpoint(&profile.DomesticDNSType, &profile.DomesticDNS, &profile.DomesticDNSIP, directDNS[len(directDNS)-1])
	}
	if proxyDNS := overrides.DNS["dns-proxy"]; len(proxyDNS) > 0 {
		applyPlainDNSEndpoint(&profile.RemoteDNSType, &profile.RemoteDNS, &profile.RemoteDNSIP, proxyDNS[len(proxyDNS)-1])
	}
	if err := profile.Validate(); err != nil {
		return nil, false
	}
	return profile, true
}

func applyPlainDNSEndpoint(dnsType, dnsServer, dnsIP *string, raw string) {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) == 0 {
		return
	}
	*dnsServer = parts[0]
	if len(parts) >= 2 {
		*dnsIP = parts[1]
	}
	if parsed, err := url.Parse(parts[0]); err == nil {
		switch strings.ToLower(parsed.Scheme) {
		case "https":
			*dnsType = "doh"
		case "tls":
			*dnsType = "dot"
		case "tcp":
			*dnsType = "tcp"
		case "udp":
			*dnsType = "udp"
		}
	}
}

func decodeHappLink(link string) (string, error) {
	parsed, err := url.Parse(link)
	if err != nil {
		return "", fmt.Errorf("неверная Happ-ссылка: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "happ") || !strings.EqualFold(parsed.Host, "routing") {
		return "", errors.New("поддерживаются только happ://routing/add и happ://routing/onadd")
	}
	parts := strings.SplitN(strings.TrimPrefix(parsed.EscapedPath(), "/"), "/", 2)
	if len(parts) != 2 || (parts[0] != "add" && parts[0] != "onadd") {
		if len(parts) == 1 && parts[0] == "off" {
			return "", errors.New("ссылка отключает маршрутизацию и не содержит профиль")
		}
		return "", errors.New("неверный формат Happ-маршрутизации")
	}
	payload, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", fmt.Errorf("неверная кодировка ссылки: %w", err)
	}
	decoded, err := decodeBase64Payload(payload)
	if err != nil {
		return "", fmt.Errorf("профиль в ссылке не является Base64: %w", err)
	}
	return decoded, nil
}

func decodeBase64Payload(payload string) (string, error) {
	compact := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(payload))
	if compact == "" {
		return "", errors.New("пустой Base64")
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(compact); err == nil {
			return string(decoded), nil
		}
	}
	return "", errors.New("некорректный Base64")
}

func (profile *Profile) Validate() error {
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" {
		profile.Name = "Custom"
	}
	if profile.RouteOrder == "" {
		profile.RouteOrder = "block-proxy-direct"
	}
	if profile.DomainStrategy == "" {
		profile.DomainStrategy = "IPIfNonMatch"
	}
	if strings.TrimSpace(profile.RemoteDNS) == "" &&
		strings.TrimSpace(profile.RemoteDNSDomain) == "" &&
		strings.TrimSpace(profile.RemoteDNSIP) == "" {
		profile.RemoteDNSType = "doh"
		profile.RemoteDNSDomain = "dns.google"
		profile.RemoteDNSIP = "8.8.8.8"
	}
	if strings.TrimSpace(profile.DomesticDNS) == "" &&
		strings.TrimSpace(profile.DomesticDNSDomain) == "" &&
		strings.TrimSpace(profile.DomesticDNSIP) == "" {
		profile.DomesticDNSType = "doh"
		profile.DomesticDNSDomain = "cloudflare-dns.com"
		profile.DomesticDNSIP = "1.1.1.1"
	}
	switch profile.DomainStrategy {
	case "AsIs", "IPIfNonMatch", "IPOnDemand":
	default:
		return fmt.Errorf("неподдерживаемая DomainStrategy %q", profile.DomainStrategy)
	}
	for _, kind := range []string{profile.DomesticDNSType, profile.RemoteDNSType} {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "", "doh", "dot", "dou", "udp", "tcp":
		default:
			return fmt.Errorf("неподдерживаемый DNS type %q", kind)
		}
	}
	for _, rules := range []*[]string{&profile.DirectIP, &profile.ProxyIP, &profile.BlockIP} {
		for index, value := range *rules {
			if address, err := netip.ParseAddr(strings.TrimSpace(value)); err == nil {
				(*rules)[index] = netip.PrefixFrom(address, address.BitLen()).String()
			}
		}
	}
	ipRules := make([]string, 0, len(profile.DirectIP)+len(profile.ProxyIP)+len(profile.BlockIP))
	ipRules = append(ipRules, profile.DirectIP...)
	ipRules = append(ipRules, profile.ProxyIP...)
	ipRules = append(ipRules, profile.BlockIP...)
	for _, value := range ipRules {
		if strings.HasPrefix(strings.ToLower(value), "geoip:") {
			continue
		}
		if _, err := netip.ParsePrefix(strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("неверное IP-правило %q", value)
		}
	}
	for _, source := range []struct {
		name  string
		value string
	}{{"Geoipurl", profile.GeoIPURL}, {"Geositeurl", profile.GeoSiteURL}} {
		if source.value == "" {
			continue
		}
		parsed, err := url.Parse(source.value)
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
			return fmt.Errorf("%s должен быть HTTPS-ссылкой", source.name)
		}
	}
	normalizedHosts := make(map[string]string, len(profile.DNSHosts))
	for domain, address := range profile.DNSHosts {
		if strings.TrimSpace(domain) == "" {
			return errors.New("DnsHosts содержит пустой домен")
		}
		if _, err := netip.ParseAddr(address); err != nil {
			return fmt.Errorf("DnsHosts[%s] содержит неверный IP", domain)
		}
		normalizedHosts[normalizeDomain(domain)] = address
	}
	profile.DNSHosts = normalizedHosts
	return nil
}
