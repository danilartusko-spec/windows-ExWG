//go:build windows

package diagnostics

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"nexusclient/internal/awg"
	"nexusclient/internal/debuglog"
	"nexusclient/internal/routing"
	"nexusclient/internal/tunnel"
	"nexusclient/internal/windowsroutes"
)

type Level string

const (
	LevelPass Level = "pass"
	LevelWarn Level = "warn"
	LevelFail Level = "fail"
)

type Check struct {
	Name   string
	Level  Level
	Detail string
}

type Report struct {
	CreatedAt time.Time
	Overall   string
	Checks    []Check
	Debug     string
}

type adapterInfo = windowsroutes.InterfaceInfo

func Run(ctx context.Context, configText string) (report Report) {
	return RunWithRouting(ctx, configText, "", "", "")
}

func RunWithRouting(ctx context.Context, configText, routingSource, routingProfileID, routingOverrides string) (report Report) {
	report = Report{CreatedAt: time.Now(), Overall: "Соединение не подтверждено"}
	defer func() { report.Debug += "\n" + SystemSnapshot() }()
	config, configErr := awg.Parse(configText)
	if configErr != nil {
		report.add("AWG-конфигурация", LevelFail, debuglog.ErrorDetail(configErr))
	} else {
		detail := fmt.Sprintf("поколение %s, peer: %d", config.Version, len(config.Peers))
		if _, ok := config.Interface.Fields["headerprotectionkey"]; ok {
			detail += ", HeaderProtectionKey: есть"
		}
		if _, ok := config.Interface.Fields["randomtrailers"]; ok {
			detail += ", RandomTrailers: есть"
		}
		if mtu, ok := config.Interface.Fields["mtu"]; ok {
			detail += ", MTU: " + mtu.Value
		}
		report.add("AWG-конфигурация", LevelPass, detail)
	}
	var routingProfile *routing.Profile
	var routingMatcher *routing.Matcher
	if routingProfileID != "" && routingSource != "" {
		cache, err := routing.NewCache(routingProfileID)
		if err == nil {
			var bundle *routing.Bundle
			var updated time.Time
			bundle, updated, err = cache.Load(routingSource)
			if err == nil {
				routingProfile = bundle.Profile
				routingMatcher, err = routing.CompileMatcherWithOverrides(bundle.Profile, bundle.GeoSite, bundle.GeoIP, routingOverrides)
				if err == nil {
					geoSiteHash, geoIPHash := sha256.Sum256(bundle.GeoSite), sha256.Sum256(bundle.GeoIP)
					detail := fmt.Sprintf("name=%s updated=%s source=%s routeOrder=%s domainStrategy=%s fakeDNS=%t globalProxy=%t counts=%v geosite.bytes=%d geosite.sha256=%x geoip.bytes=%d geoip.sha256=%x warnings=%v unknownFields=%v",
						bundle.Profile.Name, updated.Format(time.RFC3339Nano), routingSource, bundle.Profile.RouteOrder, bundle.Profile.DomainStrategy, bool(bundle.Profile.FakeDNS), bool(bundle.Profile.GlobalProxy), routingMatcher.Counts(), len(bundle.GeoSite), geoSiteHash, len(bundle.GeoIP), geoIPHash, bundle.Profile.Warnings(), bundle.Profile.AdditionalUnknownFields)
					report.add("Routing bundle", LevelPass, detail)
					report.Debug += detail + "\n"
				}
			}
		}
		if err != nil {
			report.add("Routing bundle", LevelFail, debuglog.ErrorDetail(err))
		}
	}

	serviceRunning, serviceDetail := checkService()
	if serviceRunning {
		report.add("Windows-служба VPN", LevelPass, serviceDetail)
	} else {
		report.add("Windows-служба VPN", LevelFail, serviceDetail)
		report.Debug += "VPN probes skipped: SCM does not report Running. OS snapshot is still collected.\n"
		return report
	}

	adapter, err := readAdapter(ctx)
	if err != nil {
		report.add("Wintun-интерфейс", LevelFail, debuglog.ErrorDetail(err))
		return report
	}
	level := LevelPass
	if !adapter.Up {
		level = LevelFail
	}
	status := "поднят"
	if !adapter.Up {
		status = "неактивен"
	}
	report.add("Wintun-интерфейс", level, fmt.Sprintf("%s, индекс %d, MTU %d", status, adapter.Index, adapter.MTU))

	runtimeStats, runtimeErr := tunnel.ReadRuntimeStats(ctx)
	report.Debug += fmt.Sprintf("AWG runtime: peers=%d tx_bytes=%d rx_bytes=%d last_handshake=%s\nUAPI (key fields omitted):\n%s\n", runtimeStats.Peers, runtimeStats.TX, runtimeStats.RX, runtimeStats.LastHandshake.Format(time.RFC3339Nano), runtimeStats.Raw)
	if runtimeErr != nil {
		report.add("Движок AWG", LevelFail, debuglog.ErrorDetail(runtimeErr))
	} else {
		report.add("Счётчики AWG", trafficLevel(runtimeStats.TX, runtimeStats.RX), fmt.Sprintf("отправлено %s, получено %s", formatBytes(runtimeStats.TX), formatBytes(runtimeStats.RX)))
		if runtimeStats.LastHandshake.IsZero() {
			report.add("Рукопожатие с сервером", LevelFail, "last_handshake_time_sec=0; handshake не зарегистрирован движком")
		} else {
			age := time.Since(runtimeStats.LastHandshake)
			report.add("Рукопожатие с сервером", LevelPass, fmt.Sprintf("%s назад; тихий туннель не считается неисправным только по возрасту handshake", formatDuration(age)))
		}
	}

	probeDomain := "example.com."
	if routingProfile != nil {
		if selected := sampleDomain(routingProfile.ProxySites); selected != "" {
			probeDomain = dns.Fqdn(selected)
		} else if selected := sampleDomain(routingProfile.DirectSites); selected != "" {
			probeDomain = dns.Fqdn(selected)
		}
	}
	dnsAddresses, dnsErr := queryDNS(ctx, probeDomain)
	if dnsErr != nil {
		report.add("Внутренний DNS", LevelFail, debuglog.ErrorDetail(dnsErr))
	} else {
		report.add("Внутренний DNS", LevelPass, fmt.Sprintf("domain=%s addresses=%v", probeDomain, dnsAddresses))
	}

	foreignThroughTunnel := false
	foreignRoute, foreignErr := bestRoute(ctx, "1.1.1.1")
	if foreignErr != nil {
		report.add("Иностранный маршрут", LevelFail, debuglog.ErrorDetail(foreignErr))
	} else if foreignRoute == adapter.Index {
		foreignThroughTunnel = true
		report.add("Иностранный маршрут", LevelPass, fmt.Sprintf("через AWG-интерфейс %d", foreignRoute))
	} else {
		report.add("Иностранный маршрут", LevelFail, fmt.Sprintf("ушёл через интерфейс %d вместо AWG %d", foreignRoute, adapter.Index))
	}

	if routingProfile != nil && routingMatcher != nil {
		for _, item := range []struct {
			name   string
			action routing.Action
			rules  []string
		}{{"DNS/маршрут Direct", routing.ActionDirect, routingProfile.DirectSites}, {"DNS/маршрут Proxy", routing.ActionProxy, routingProfile.ProxySites}} {
			domain := sampleDomain(item.rules)
			if domain == "" {
				report.add(item.name, LevelWarn, "в профиле нет простого domain:/full: правила для безопасного контрольного запроса")
				continue
			}
			addresses, err := queryDNS(ctx, dns.Fqdn(domain))
			if err != nil || len(addresses) == 0 {
				report.add(item.name, LevelFail, debuglog.ErrorDetail(err))
				continue
			}
			expected := routingMatcher.Route(domain, addresses[0])
			index, err := bestRoute(ctx, addresses[0].String())
			level := LevelPass
			if err != nil || expected != item.action || (item.action == routing.ActionProxy && index != adapter.Index) || (item.action == routing.ActionDirect && index == adapter.Index) {
				level = LevelFail
			}
			report.add(item.name, level, fmt.Sprintf("domain=%s ip=%s expected=%s rule=%s interface=%d tunnelInterface=%d error=%v", domain, addresses[0], expected, routingMatcher.Trace(domain, addresses[0]), index, adapter.Index, err))
		}
	}

	if !foreignThroughTunnel {
		report.add("HTTPS через VPN", LevelFail, "не проверялся: контрольный маршрут не направлен в AWG")
	} else if err := checkHTTPS(ctx, tunnelAddress(config)); err != nil {
		report.add("HTTPS через VPN", LevelFail, debuglog.ErrorDetail(err))
	} else {
		report.add("HTTPS через VPN", LevelPass, "контрольный запрос выполнен")
	}

	failures := report.count(LevelFail)
	warnings := report.count(LevelWarn)
	if failures == 0 {
		report.Overall = "Соединение установлено и проверено"
		if warnings > 0 {
			report.Overall = "Соединение работает с предупреждениями"
		}
		return report
	}
	report.Overall = fmt.Sprintf("Проверки завершены: ошибок=%d предупреждений=%d. Исходные данные ниже.", failures, warnings)
	return report
}

func sampleDomain(rules []string) string {
	for _, rule := range rules {
		kind, value, found := strings.Cut(strings.TrimSpace(rule), ":")
		if !found {
			kind, value = "domain", strings.TrimSpace(rule)
		}
		switch strings.ToLower(kind) {
		case "full":
			return strings.TrimSpace(value)
		case "domain":
			return "www." + strings.TrimSpace(value)
		}
	}
	return ""
}

func (report *Report) add(name string, level Level, detail string) {
	report.Checks = append(report.Checks, Check{Name: name, Level: level, Detail: strings.TrimSpace(detail)})
}

func (report Report) count(level Level) int {
	count := 0
	for _, check := range report.Checks {
		if check.Level == level {
			count++
		}
	}
	return count
}

func (report Report) Text() string {
	var builder strings.Builder
	builder.WriteString("Ex WG — полная локальная диагностика\r\n")
	builder.WriteString(report.CreatedAt.Format("02.01.2006 15:04:05"))
	builder.WriteString("\r\n\r\n")
	builder.WriteString(report.Overall)
	builder.WriteString("\r\n\r\n")
	for _, check := range report.Checks {
		mark := "✓"
		if check.Level == LevelWarn {
			mark = "!"
		}
		if check.Level == LevelFail {
			mark = "✗"
		}
		fmt.Fprintf(&builder, "%s %s: %s\r\n", mark, check.Name, check.Detail)
	}
	builder.WriteString("\r\n--- ORIGINAL SYSTEM / AWG DETAILS ---\r\n")
	builder.WriteString(report.Debug)
	builder.WriteString("\r\nЖурнал содержит адреса и сведения о сети. Перед отправкой проверьте содержимое. Причины не подменяются предположениями.\r\n")
	return builder.String()
}

func checkService() (bool, string) {
	manager, err := mgr.Connect()
	if err != nil {
		return false, "SCM.Connect:\n" + debuglog.ErrorDetail(err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(tunnel.ServiceName)
	if err != nil {
		return false, "SCM.OpenService(" + tunnel.ServiceName + "):\n" + debuglog.ErrorDetail(err)
	}
	defer service.Close()
	status, err := service.Query()
	if err != nil {
		return false, "SCM.Query:\n" + debuglog.ErrorDetail(err)
	}
	detail := fmt.Sprintf("service=%s status=%+v\n", tunnel.ServiceName, status)
	config, configErr := service.Config()
	if configErr == nil {
		detail += fmt.Sprintf("config=%+v\n", config)
	} else {
		detail += "SCM.Config: " + debuglog.ErrorDetail(configErr)
	}
	if status.State != svc.Running {
		return false, detail
	}
	return true, detail
}

func readAdapter(ctx context.Context) (adapterInfo, error) {
	if err := ctx.Err(); err != nil {
		return adapterInfo{}, err
	}
	return windowsroutes.InterfaceByAlias(tunnel.ProfileName)
}

func queryDNS(ctx context.Context, domain string) ([]netip.Addr, error) {
	message := new(dns.Msg)
	message.SetQuestion(domain, dns.TypeA)
	response, rtt, err := (&dns.Client{Timeout: 8 * time.Second}).ExchangeContext(ctx, message, "127.0.0.1:53")
	if err != nil {
		debuglog.Error("probe.DNS "+domain, err)
		return nil, err
	}
	debuglog.Event("probe.DNS", "domain=%s server=127.0.0.1:53 rtt=%s\n%s", domain, rtt, response.String())
	if response.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("DNS RCODE=%d (%s); server=127.0.0.1:53; domain=%s; rtt=%s\n%s", response.Rcode, dns.RcodeToString[response.Rcode], domain, rtt, response.String())
	}
	var addresses []netip.Addr
	for _, answer := range response.Answer {
		if record, ok := answer.(*dns.A); ok {
			if address, ok := netip.AddrFromSlice(record.A); ok {
				addresses = append(addresses, address.Unmap())
			}
		}
	}
	return addresses, nil
}

func bestRoute(ctx context.Context, target string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	address, err := netip.ParseAddr(target)
	if err != nil {
		return 0, err
	}
	return windowsroutes.BestRouteIndex(address.Unmap())
}

func checkHTTPS(ctx context.Context, source netip.Addr) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://1.1.1.1/cdn-cgi/trace", nil)
	if err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	if source.IsValid() {
		dialer.LocalAddr = &net.TCPAddr{IP: net.IP(source.AsSlice())}
	}
	client := &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			Proxy:             nil,
			DialContext:       dialer.DialContext,
			ForceAttemptHTTP2: true,
		},
	}
	defer client.CloseIdleConnections()
	debuglog.Event("probe.HTTPS", "url=%s source=%s proxy=disabled", request.URL, source)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	debuglog.Event("probe.HTTPS.response", "status=%s protocol=%s headers=%v body(first4096)=%s", response.Status, response.Proto, response.Header, body)
	if readErr != nil {
		return fmt.Errorf("HTTPS response body: %w", readErr)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTPS url=%s status=%s protocol=%s body(first4096)=%s", request.URL, response.Status, response.Proto, body)
	}
	return nil
}

func trafficLevel(tx, rx uint64) Level {
	if rx > 0 && tx > 0 {
		return LevelPass
	}
	if tx > 0 {
		return LevelFail
	}
	return LevelWarn
}

func tunnelAddress(config *awg.Config) netip.Addr {
	if config == nil {
		return netip.Addr{}
	}
	field, ok := config.Interface.Fields["address"]
	if !ok {
		return netip.Addr{}
	}
	for _, value := range strings.Split(field.Value, ",") {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(value)); err == nil && prefix.Addr().Is4() {
			return prefix.Addr().Unmap()
		}
	}
	return netip.Addr{}
}

func formatDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	if duration < time.Minute {
		return fmt.Sprintf("%d сек", int(duration.Seconds()))
	}
	return fmt.Sprintf("%d мин", int(duration.Minutes()))
}

func formatBytes(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d Б", value)
	}
	divisor, exponent := uint64(unit), 0
	for reduced := value / unit; reduced >= unit && exponent < 3; reduced /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %cБ", float64(value)/float64(divisor), []rune("КМГТ")[exponent])
}
