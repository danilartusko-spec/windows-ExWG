package routing

import (
	"bufio"
	"fmt"
	"net/netip"
	"strings"
)

// Overrides are user rules that are evaluated before rules downloaded from
// the routing profile. Keeping them separate means a cache refresh cannot
// overwrite local choices.
type Overrides struct {
	DNS         map[string][]string
	DirectSites []string
	DirectIP    []string
	ProxySites  []string
	ProxyIP     []string
	BlockSites  []string
	BlockIP     []string
}

func ParseOverrides(input string) (Overrides, error) {
	result := Overrides{DNS: make(map[string][]string)}
	scanner := bufio.NewScanner(strings.NewReader(input))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return Overrides{}, fmt.Errorf("строка %d: ожидается direct, proxy или block, затем правило", lineNumber)
		}
		action := Action(strings.ToLower(parts[0]))
		if strings.HasPrefix(string(action), "dns-") {
			switch string(action) {
			case "dns-direct", "dns-proxy", "dns-direct-fallback", "dns-proxy-fallback":
				if len(parts) > 3 {
					return Overrides{}, fmt.Errorf("строка %d: DNS URL и необязательный bootstrap IP", lineNumber)
				}
				result.DNS[string(action)] = append(result.DNS[string(action)], strings.Join(parts[1:], " "))
				continue
			default:
				return Overrides{}, fmt.Errorf("строка %d: неизвестная DNS-настройка", lineNumber)
			}
		}
		if action != ActionDirect && action != ActionProxy && action != ActionBlock {
			return Overrides{}, fmt.Errorf("строка %d: неизвестное действие %q", lineNumber, parts[0])
		}
		value := strings.TrimSpace(strings.Join(parts[1:], " "))
		isIP := strings.HasPrefix(strings.ToLower(value), "geoip:")
		if address, err := netip.ParseAddr(value); err == nil {
			bits := 128
			if address.Is4() {
				bits = 32
			}
			value = netip.PrefixFrom(address, bits).String()
			isIP = true
		} else if _, err := netip.ParsePrefix(value); err == nil {
			isIP = true
		}
		if !isIP && !strings.HasPrefix(strings.ToLower(value), "geosite:") {
			if _, err := parseDomainRule(value); err != nil {
				return Overrides{}, fmt.Errorf("строка %d: %w", lineNumber, err)
			}
		}

		switch action {
		case ActionDirect:
			if isIP {
				result.DirectIP = append(result.DirectIP, value)
			} else {
				result.DirectSites = append(result.DirectSites, value)
			}
		case ActionProxy:
			if isIP {
				result.ProxyIP = append(result.ProxyIP, value)
			} else {
				result.ProxySites = append(result.ProxySites, value)
			}
		case ActionBlock:
			if isIP {
				result.BlockIP = append(result.BlockIP, value)
			} else {
				result.BlockSites = append(result.BlockSites, value)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Overrides{}, err
	}
	return result, nil
}
