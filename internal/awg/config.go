package awg

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

type Version string

const (
	Version21 Version = "2.1"
	Version30 Version = "3.0"
	Version31 Version = "3.1"
)

type Field struct {
	Name  string
	Value string
	Line  int
}

type Section struct {
	Name   string
	Fields map[string]Field
}

type Config struct {
	Raw       string
	Interface Section
	Peers     []Section
	Version   Version
	Warnings  []string
}

var interfaceFields = map[string]struct{}{
	"privatekey": {}, "address": {}, "dns": {}, "listenport": {}, "mtu": {},
	"jc": {}, "jmin": {}, "jmax": {}, "j1": {}, "j2": {}, "itime": {}, "s1": {}, "s2": {}, "s3": {}, "s4": {},
	"h1": {}, "h2": {}, "h3": {}, "h4": {}, "i1": {}, "i2": {}, "i3": {},
	"i4": {}, "i5": {}, "headerprotectionkey": {}, "contentpaddingaddition": {},
	"rekeyaftertime": {}, "rekeytimeout": {}, "rejectaftertime": {},
	"keepalivetimeout": {}, "maxhandshakeattempts": {}, "randomtrailers": {},
	"disablecookies": {},
}

var peerFields = map[string]struct{}{
	"publickey": {}, "presharedkey": {}, "allowedips": {}, "endpoint": {},
	"persistentkeepalive": {},
}

var forbiddenFields = map[string]struct{}{
	"preup": {}, "postup": {}, "predown": {}, "postdown": {},
}

var version30Fields = []string{
	"headerprotectionkey", "contentpaddingaddition", "rekeyaftertime", "rekeytimeout",
	"rejectaftertime", "keepalivetimeout", "maxhandshakeattempts",
}

var version31Fields = []string{"randomtrailers", "disablecookies"}

func Parse(raw string) (*Config, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if raw == "" {
		return nil, errors.New("конфигурация пуста")
	}

	cfg := &Config{Raw: raw}
	var current *Section
	for index, sourceLine := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		lineNumber := index + 1
		line := strings.TrimSpace(sourceLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			switch strings.ToLower(strings.TrimSpace(line[1 : len(line)-1])) {
			case "interface":
				if cfg.Interface.Name != "" {
					return nil, fmt.Errorf("строка %d: секция [Interface] повторяется", lineNumber)
				}
				cfg.Interface = Section{Name: "Interface", Fields: make(map[string]Field)}
				current = &cfg.Interface
			case "peer":
				cfg.Peers = append(cfg.Peers, Section{Name: "Peer", Fields: make(map[string]Field)})
				current = &cfg.Peers[len(cfg.Peers)-1]
			default:
				return nil, fmt.Errorf("строка %d: неизвестная секция %q", lineNumber, line)
			}
			continue
		}

		if current == nil {
			return nil, fmt.Errorf("строка %d: параметр находится вне секции", lineNumber)
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("строка %d: ожидается параметр в виде Имя = значение", lineNumber)
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			return nil, fmt.Errorf("строка %d: имя и значение параметра не могут быть пустыми", lineNumber)
		}

		normalized := strings.ToLower(name)
		if _, forbidden := forbiddenFields[normalized]; forbidden {
			return nil, fmt.Errorf("строка %d: %s запрещён из-за выполнения внешних команд", lineNumber, name)
		}
		if previous, duplicate := current.Fields[normalized]; duplicate {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("параметр %s на строке %d заменяет значение со строки %d", name, lineNumber, previous.Line))
		}
		current.Fields[normalized] = Field{Name: name, Value: value, Line: lineNumber}

		known := interfaceFields
		if current.Name == "Peer" {
			known = peerFields
		}
		if _, ok := known[normalized]; !ok {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("неизвестный параметр %s на строке %d сохранён для прямой передачи движку", name, lineNumber))
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if err := validateOfficial(raw); err != nil {
		return nil, fmt.Errorf("официальный парсер AmneziaWG отклонил конфигурацию: %w", err)
	}
	cfg.Version = detectVersion(cfg.Interface)
	return cfg, nil
}

func detectVersion(section Section) Version {
	for _, name := range version31Fields {
		if _, ok := section.Fields[name]; ok {
			return Version31
		}
	}
	for _, name := range version30Fields {
		if _, ok := section.Fields[name]; ok {
			return Version30
		}
	}
	return Version21
}

func (cfg *Config) validate() error {
	if cfg.Interface.Name == "" {
		return errors.New("отсутствует секция [Interface]")
	}
	if len(cfg.Peers) == 0 {
		return errors.New("отсутствует секция [Peer]")
	}
	if err := requireKey(cfg.Interface, "privatekey", "PrivateKey"); err != nil {
		return err
	}
	if err := validatePrefixes(cfg.Interface, "address", "Address"); err != nil {
		return err
	}

	for index, peer := range cfg.Peers {
		if err := requireKey(peer, "publickey", fmt.Sprintf("Peer %d PublicKey", index+1)); err != nil {
			return err
		}
		if value, ok := peer.Fields["presharedkey"]; ok && !validKey(value.Value) {
			return fmt.Errorf("строка %d: PresharedKey должен содержать 32 байта", value.Line)
		}
		if err := validatePrefixes(peer, "allowedips", fmt.Sprintf("Peer %d AllowedIPs", index+1)); err != nil {
			return err
		}
		endpoint, ok := peer.Fields["endpoint"]
		if !ok {
			return fmt.Errorf("Peer %d: отсутствует Endpoint", index+1)
		}
		if err := validateEndpoint(endpoint.Value); err != nil {
			return fmt.Errorf("строка %d: неверный Endpoint: %w", endpoint.Line, err)
		}
	}

	if field, ok := cfg.Interface.Fields["headerprotectionkey"]; ok {
		if !validKey(field.Value) {
			return fmt.Errorf("строка %d: HeaderProtectionKey должен содержать 32 байта", field.Line)
		}
		for _, name := range []string{"s1", "s2", "s3", "s4"} {
			padding, ok := cfg.Interface.Fields[name]
			if !ok {
				return fmt.Errorf("HeaderProtectionKey требует %s", strings.ToUpper(name))
			}
			number, err := strconv.Atoi(padding.Value)
			if err != nil || number < 12 {
				return fmt.Errorf("строка %d: %s должен быть не меньше 12 при HeaderProtectionKey", padding.Line, strings.ToUpper(name))
			}
		}
	}

	if jmin, ok := cfg.Interface.Fields["jmin"]; ok {
		if jmax, ok := cfg.Interface.Fields["jmax"]; ok {
			minValue, minErr := strconv.Atoi(jmin.Value)
			maxValue, maxErr := strconv.Atoi(jmax.Value)
			if minErr != nil || maxErr != nil || minValue < 0 || maxValue < minValue {
				return fmt.Errorf("строки %d/%d: Jmin и Jmax заданы неверно", jmin.Line, jmax.Line)
			}
		}
	}
	return nil
}

func requireKey(section Section, normalizedName, displayName string) error {
	field, ok := section.Fields[normalizedName]
	if !ok {
		return fmt.Errorf("отсутствует %s", displayName)
	}
	if !validKey(field.Value) {
		return fmt.Errorf("строка %d: %s должен содержать 32 байта", field.Line, displayName)
	}
	return nil
}

func validKey(value string) bool {
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == 32 {
		return true
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validatePrefixes(section Section, normalizedName, displayName string) error {
	field, ok := section.Fields[normalizedName]
	if !ok {
		return fmt.Errorf("отсутствует %s", displayName)
	}
	for _, rawPrefix := range strings.Split(field.Value, ",") {
		if _, err := netip.ParsePrefix(strings.TrimSpace(rawPrefix)); err != nil {
			return fmt.Errorf("строка %d: %s содержит неверную сеть %q", field.Line, displayName, strings.TrimSpace(rawPrefix))
		}
	}
	return nil
}

func validateEndpoint(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return err
	}
	if strings.TrimSpace(host) == "" {
		return errors.New("пустой адрес")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("порт должен быть от 1 до 65535")
	}
	return nil
}

func (cfg *Config) ForSplitRouting() (string, error) {
	if len(cfg.Peers) != 1 {
		return "", errors.New("внутренняя маршрутизация MVP поддерживает один [Peer]")
	}
	// Always capture both families. If the peer has no usable IPv6, packets
	// selected for Proxy must fail inside the tunnel instead of silently using
	// the physical IPv6 default route. Explicit Direct /128 routes can still be
	// installed by the routing layer when physical IPv6 is available.
	allowed := "0.0.0.0/1, 128.0.0.0/1, ::/1, 8000::/1"

	lines := strings.Split(strings.ReplaceAll(cfg.Raw, "\r\n", "\n"), "\n")
	section := ""
	replaced := false
	filtered := make([]string, 0, len(lines))
	for _, source := range lines {
		line := strings.TrimSpace(source)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			filtered = append(filtered, source)
			continue
		}
		name, _, ok := strings.Cut(line, "=")
		if ok {
			normalized := strings.ToLower(strings.TrimSpace(name))
			// Omit [Interface] DNS from the runtime tunnel configuration so
			// amneziawg-windows does not configure raw IPv4/IPv6 DNS servers
			// on the Nexus adapter that bypass Ex WG's local 127.0.0.1:53
			// split-DNS router.
			if section == "interface" && normalized == "dns" {
				continue
			}
			if section == "peer" && normalized == "allowedips" && !replaced {
				filtered = append(filtered, "AllowedIPs = "+allowed)
				replaced = true
				continue
			}
		}
		filtered = append(filtered, source)
	}
	if !replaced {
		return "", errors.New("в секции [Peer] не найден AllowedIPs")
	}
	return strings.Join(filtered, "\r\n") + "\r\n", nil
}
