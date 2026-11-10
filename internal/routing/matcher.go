package routing

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/xtls/xray-core/common/geodata"
	"golang.org/x/net/idna"
	"google.golang.org/protobuf/proto"
)

type Action string

const (
	ActionDirect Action = "direct"
	ActionProxy  Action = "proxy"
	ActionBlock  Action = "block"
)

type domainRule struct {
	source     string
	kind       geodata.Domain_Type
	value      string
	expression *regexp.Regexp
}

type ipRule struct {
	source   string
	prefixes []netip.Prefix
	reverse  bool
}

type ruleSet struct {
	domains []domainRule
	IPs     []ipRule
}

type Matcher struct {
	strategy    string
	order       []Action
	sets        map[Action]ruleSet
	overrides   map[Action]ruleSet
	defaultRule Action
}

func CompileMatcher(profile *Profile, geoSiteBytes, geoIPBytes []byte) (*Matcher, error) {
	return CompileMatcherWithOverrides(profile, geoSiteBytes, geoIPBytes, "")
}

func CompileMatcherWithOverrides(profile *Profile, geoSiteBytes, geoIPBytes []byte, input string) (*Matcher, error) {
	if profile == nil {
		return nil, errors.New("профиль маршрутизации не задан")
	}

	sites, err := decodeGeoSites(geoSiteBytes)
	if err != nil {
		return nil, err
	}
	geoIPs, err := decodeGeoIPs(geoIPBytes)
	if err != nil {
		return nil, err
	}

	matcher := &Matcher{
		strategy:  profile.DomainStrategy,
		order:     []Action{ActionBlock, ActionProxy, ActionDirect},
		sets:      make(map[Action]ruleSet),
		overrides: make(map[Action]ruleSet),
	}
	if bool(profile.GlobalProxy) {
		matcher.defaultRule = ActionProxy
	} else {
		matcher.defaultRule = ActionDirect
	}
	if profile.RouteOrder != "" {
		matcher.order, err = parseRouteOrder(profile.RouteOrder)
		if err != nil {
			return nil, err
		}
	}

	definitions := []struct {
		action  Action
		domains []string
		ips     []string
	}{
		{ActionDirect, profile.DirectSites, profile.DirectIP},
		{ActionProxy, profile.ProxySites, profile.ProxyIP},
		{ActionBlock, profile.BlockSites, profile.BlockIP},
	}
	for _, definition := range definitions {
		set, err := compileRuleSet(definition.domains, definition.ips, sites, geoIPs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", definition.action, err)
		}
		matcher.sets[definition.action] = set
	}
	overrides, err := ParseOverrides(input)
	if err != nil {
		return nil, fmt.Errorf("локальные правила: %w", err)
	}
	localDefinitions := []struct {
		action  Action
		domains []string
		ips     []string
	}{
		{ActionDirect, overrides.DirectSites, overrides.DirectIP},
		{ActionProxy, overrides.ProxySites, overrides.ProxyIP},
		{ActionBlock, overrides.BlockSites, overrides.BlockIP},
	}
	for _, definition := range localDefinitions {
		set, err := compileRuleSet(definition.domains, definition.ips, sites, geoIPs)
		if err != nil {
			return nil, fmt.Errorf("локальное %s: %w", definition.action, err)
		}
		matcher.overrides[definition.action] = set
	}
	return matcher, nil
}

func (matcher *Matcher) Domain(domain string) Action {
	if action, ok := matcher.MatchDomain(domain); ok {
		return action
	}
	return matcher.defaultRule
}

func (matcher *Matcher) MatchDomain(domain string) (Action, bool) {
	domain = normalizeDomain(domain)
	for _, action := range matcher.order {
		for _, rule := range matcher.overrides[action].domains {
			if rule.matches(domain) {
				return action, true
			}
		}
	}
	for _, action := range matcher.order {
		for _, rule := range matcher.sets[action].domains {
			if rule.matches(domain) {
				return action, true
			}
		}
	}
	return "", false
}

func (matcher *Matcher) IP(address netip.Addr) Action {
	if action, ok := matcher.MatchIP(address); ok {
		return action
	}
	return matcher.defaultRule
}

func (matcher *Matcher) MatchIP(address netip.Addr) (Action, bool) {
	address = address.Unmap()
	if action, ok := matcher.matchIPSets(address, matcher.overrides); ok {
		return action, true
	}
	return matcher.matchIPSets(address, matcher.sets)
}

func (matcher *Matcher) matchIPSets(address netip.Addr, sets map[Action]ruleSet) (Action, bool) {
	if !address.IsValid() {
		return "", false
	}
	for _, action := range matcher.order {
		for _, rule := range sets[action].IPs {
			matches := slices.ContainsFunc(rule.prefixes, func(prefix netip.Prefix) bool {
				return prefix.Contains(address)
			})
			if rule.reverse {
				matches = !matches
			}
			if matches {
				return action, true
			}
		}
	}
	return "", false
}

func (matcher *Matcher) Decide(domain string, address netip.Addr) Action {
	domain = normalizeDomain(domain)
	address = address.Unmap()
	if action, ok := matcher.decideSets(domain, address, matcher.overrides); ok {
		return action
	}
	if action, ok := matcher.decideSets(domain, address, matcher.sets); ok {
		return action
	}
	return matcher.defaultRule
}

// Route implements the provider's DomainStrategy. In IPIfNonMatch a matched
// domain decision is authoritative and must not be changed merely because a
// resolved address also belongs to a different IP category.
func (matcher *Matcher) Route(domain string, address netip.Addr) Action {
	if strings.TrimSpace(domain) == "" {
		return matcher.IP(address)
	}
	if matcher.strategy != "IPOnDemand" {
		if action, ok := matcher.MatchDomain(domain); ok {
			return action
		}
		if matcher.strategy == "AsIs" {
			return matcher.defaultRule
		}
	}
	return matcher.Decide(domain, address)
}

func (matcher *Matcher) Trace(domain string, address netip.Addr) string {
	domain = normalizeDomain(domain)
	if address.IsValid() {
		address = address.Unmap()
	}
	domainOnly := domain != "" && matcher.strategy != "IPOnDemand"
	_, matchedDomain := matcher.MatchDomain(domain)
	if !matchedDomain && matcher.strategy != "AsIs" {
		domainOnly = false
	}
	for _, local := range []bool{true, false} {
		sets, label := matcher.sets, "profile"
		if local {
			sets, label = matcher.overrides, "local"
		}
		for _, action := range matcher.order {
			for _, rule := range sets[action].domains {
				if domain != "" && rule.matches(domain) {
					return fmt.Sprintf("%s %s %s", label, action, rule.source)
				}
			}
			if domainOnly || !address.IsValid() {
				continue
			}
			for _, rule := range sets[action].IPs {
				matches := slices.ContainsFunc(rule.prefixes, func(prefix netip.Prefix) bool { return prefix.Contains(address) })
				if rule.reverse {
					matches = !matches
				}
				if matches {
					return fmt.Sprintf("%s %s %s", label, action, rule.source)
				}
			}
		}
	}
	return "default " + string(matcher.defaultRule)
}

func (matcher *Matcher) Counts() map[string]map[string]int {
	result := make(map[string]map[string]int)
	for action, set := range matcher.sets {
		prefixes := 0
		for _, rule := range set.IPs {
			prefixes += len(rule.prefixes)
		}
		result[string(action)] = map[string]int{"domains": len(set.domains), "ipPrefixes": prefixes}
	}
	return result
}

func (matcher *Matcher) decideSets(domain string, address netip.Addr, sets map[Action]ruleSet) (Action, bool) {
	for _, action := range matcher.order {
		set := sets[action]
		for _, rule := range set.domains {
			if rule.matches(domain) {
				return action, true
			}
		}
		for _, rule := range set.IPs {
			if !address.IsValid() {
				continue
			}
			matches := slices.ContainsFunc(rule.prefixes, func(prefix netip.Prefix) bool {
				return prefix.Contains(address)
			})
			if rule.reverse {
				matches = !matches
			}
			if matches {
				return action, true
			}
		}
	}
	return "", false
}

func parseRouteOrder(value string) ([]Action, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(value)), "-")
	if len(parts) != 3 {
		return nil, errors.New("RouteOrder должен содержать direct, proxy и block")
	}
	seen := make(map[Action]bool)
	order := make([]Action, 0, 3)
	for _, part := range parts {
		action := Action(part)
		if action != ActionDirect && action != ActionProxy && action != ActionBlock {
			return nil, fmt.Errorf("неизвестное действие %q в RouteOrder", part)
		}
		if seen[action] {
			return nil, fmt.Errorf("действие %q повторяется в RouteOrder", part)
		}
		seen[action] = true
		order = append(order, action)
	}
	return order, nil
}

func compileRuleSet(domainValues, ipValues []string, sites map[string][]domainRule, geoIPs map[string]ipRule) (ruleSet, error) {
	var result ruleSet
	for _, value := range domainValues {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if category, found := strings.CutPrefix(strings.ToLower(value), "geosite:"); found {
			rules, ok := sites[category]
			if !ok {
				return ruleSet{}, fmt.Errorf("категория geosite:%s отсутствует в файле", category)
			}
			for _, rule := range rules {
				rule.source = value
				result.domains = append(result.domains, rule)
			}
			continue
		}
		rule, err := parseDomainRule(value)
		if err != nil {
			return ruleSet{}, err
		}
		result.domains = append(result.domains, rule)
	}

	for _, value := range ipValues {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if category, found := strings.CutPrefix(strings.ToLower(value), "geoip:"); found {
			inverse := strings.HasPrefix(category, "!")
			category = strings.TrimPrefix(category, "!")
			rule, ok := geoIPs[category]
			if !ok {
				return ruleSet{}, fmt.Errorf("категория geoip:%s отсутствует в файле", category)
			}
			if inverse {
				rule.reverse = !rule.reverse
			}
			rule.source = value
			result.IPs = append(result.IPs, rule)
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return ruleSet{}, fmt.Errorf("неверная сеть %q", value)
		}
		result.IPs = append(result.IPs, ipRule{source: value, prefixes: []netip.Prefix{prefix.Masked()}})
	}
	return result, nil
}

func parseDomainRule(value string) (domainRule, error) {
	kind := geodata.Domain_Domain
	normalized := value
	if prefix, body, found := strings.Cut(value, ":"); found {
		switch strings.ToLower(prefix) {
		case "domain":
			kind, normalized = geodata.Domain_Domain, body
		case "full":
			kind, normalized = geodata.Domain_Full, body
		case "keyword":
			kind, normalized = geodata.Domain_Substr, body
		case "regexp", "regex":
			kind, normalized = geodata.Domain_Regex, body
		}
	}
	normalized = strings.TrimSpace(normalized)
	if kind == geodata.Domain_Domain || kind == geodata.Domain_Full {
		normalized = normalizeDomain(normalized)
	} else {
		normalized = strings.ToLower(normalized)
	}
	rule := domainRule{source: value, kind: kind, value: normalized}
	if rule.value == "" {
		return domainRule{}, fmt.Errorf("пустое доменное правило %q", value)
	}
	if kind == geodata.Domain_Regex {
		expression, err := regexp.Compile("(?i)" + rule.value)
		if err != nil {
			return domainRule{}, fmt.Errorf("неверное регулярное выражение %q: %w", value, err)
		}
		rule.expression = expression
	}
	return rule, nil
}

func (rule domainRule) matches(domain string) bool {
	switch rule.kind {
	case geodata.Domain_Full:
		return domain == rule.value
	case geodata.Domain_Domain:
		return domain == rule.value || strings.HasSuffix(domain, "."+rule.value)
	case geodata.Domain_Substr:
		return strings.Contains(domain, rule.value)
	case geodata.Domain_Regex:
		return rule.expression != nil && rule.expression.MatchString(domain)
	default:
		return false
	}
}

func decodeGeoSites(data []byte) (map[string][]domainRule, error) {
	result := make(map[string][]domainRule)
	if len(data) == 0 {
		return result, nil
	}
	var list geodata.GeoSiteList
	if err := proto.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("не удалось прочитать geosite.dat: %w", err)
	}
	for _, entry := range list.Entry {
		code := strings.ToLower(entry.Code)
		for _, source := range entry.Domain {
			value := strings.ToLower(source.Value)
			if source.Type == geodata.Domain_Domain || source.Type == geodata.Domain_Full {
				value = normalizeDomain(source.Value)
			}
			rule := domainRule{source: "geosite:" + code, kind: source.Type, value: value}
			if source.Type == geodata.Domain_Regex {
				expression, err := regexp.Compile("(?i)" + source.Value)
				if err != nil {
					return nil, fmt.Errorf("geosite:%s содержит неверное регулярное выражение: %w", code, err)
				}
				rule.expression = expression
			}
			result[code] = append(result[code], rule)
			for _, attribute := range source.Attribute {
				if attribute.Key != "" {
					result[code+"@"+strings.ToLower(attribute.Key)] = append(result[code+"@"+strings.ToLower(attribute.Key)], rule)
				}
			}
		}
	}
	return result, nil
}

func decodeGeoIPs(data []byte) (map[string]ipRule, error) {
	result := make(map[string]ipRule)
	if len(data) == 0 {
		return result, nil
	}
	var list geodata.GeoIPList
	if err := proto.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("не удалось прочитать geoip.dat: %w", err)
	}
	for _, entry := range list.Entry {
		rule := ipRule{source: "geoip:" + strings.ToLower(entry.Code), reverse: entry.ReverseMatch}
		for _, cidr := range entry.Cidr {
			var address netip.Addr
			switch len(cidr.Ip) {
			case 4:
				address = netip.AddrFrom4([4]byte(cidr.Ip))
			case 16:
				address = netip.AddrFrom16([16]byte(cidr.Ip))
			default:
				return nil, fmt.Errorf("geoip:%s содержит IP длиной %d байт", entry.Code, len(cidr.Ip))
			}
			prefix, err := address.Prefix(int(cidr.Prefix))
			if err != nil {
				return nil, fmt.Errorf("geoip:%s содержит неверный префикс %d", entry.Code, cidr.Prefix)
			}
			rule.prefixes = append(rule.prefixes, prefix.Masked())
		}
		result[strings.ToLower(entry.Code)] = rule
	}
	return result, nil
}

func normalizeDomain(value string) string {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	converted, err := idna.Lookup.ToASCII(value)
	if err == nil {
		return strings.ToLower(converted)
	}
	return value
}
