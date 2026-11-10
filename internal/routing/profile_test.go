package routing

import (
	"encoding/base64"
	"strings"
	"testing"
)

const profileJSON = `{
  "Name": "RoscomVPN",
  "GlobalProxy": "true",
  "UseChunkFiles": "true",
  "RemoteDNSIP": "8.8.8.8",
  "DomesticDNSIP": "77.88.8.8",
  "Geoipurl": "https://example.com/geoip.dat",
  "Geositeurl": "https://example.com/geosite.dat",
  "RouteOrder": "block-proxy-direct",
  "DirectSites": ["geosite:category-ru"],
  "DirectIp": ["geoip:private", "10.0.0.0/8"],
  "ProxySites": ["geosite:youtube"],
  "ProxyIp": [],
  "BlockSites": ["geosite:category-ads"],
  "BlockIp": [],
  "FakeDNS": "false"
}`

func TestParsesHappDeepLink(t *testing.T) {
	link := "happ://routing/onadd/" + base64.StdEncoding.EncodeToString([]byte(profileJSON))
	profile, err := ParseProfile(link)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "RoscomVPN" || !bool(profile.GlobalProxy) {
		t.Fatalf("unexpected profile: %#v", profile)
	}
	if len(profile.DirectSites) != 1 || profile.DirectSites[0] != "geosite:category-ru" {
		t.Fatalf("unexpected direct sites: %#v", profile.DirectSites)
	}
}

func TestAcceptsBooleanValues(t *testing.T) {
	profile, err := ParseProfile(`{"Name":"test","GlobalProxy":false,"FakeDNS":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if bool(profile.GlobalProxy) || !bool(profile.FakeDNS) {
		t.Fatalf("unexpected booleans: %#v", profile)
	}
}

func TestUseChunkFilesWarningExplainsFullDatFallback(t *testing.T) {
	profile, err := ParseProfile(profileJSON)
	if err != nil {
		t.Fatal(err)
	}
	warnings := profile.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "правила применяются полностью") || !strings.Contains(warnings[0], "больше трафика") {
		t.Fatalf("unexpected warning: %#v", warnings)
	}
}

func TestRejectsInsecureGeoDataURL(t *testing.T) {
	_, err := ParseProfile(`{"Name":"test","Geoipurl":"http://example.com/geoip.dat"}`)
	if err == nil {
		t.Fatal("expected insecure URL rejection")
	}
}

func TestParsesUniversalCustomFormatsAndDefaultsDNS(t *testing.T) {
	minimal, err := ParseProfile(`{"directSites":["domain:ru"],"blockIp":["203.0.113.10"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if minimal.Name != "Custom" || !bool(minimal.GlobalProxy) {
		t.Fatalf("unexpected minimal profile: %#v", minimal)
	}
	if minimal.RemoteDNSIP != "8.8.8.8" || minimal.DomesticDNSIP != "1.1.1.1" {
		t.Fatalf("expected default DNS IPs, got remote=%q domestic=%q", minimal.RemoteDNSIP, minimal.DomesticDNSIP)
	}
	if len(minimal.AdditionalUnknownFields) != 0 {
		t.Fatalf("camelCase known fields must not be marked unknown: %v", minimal.AdditionalUnknownFields)
	}

	b64 := base64.StdEncoding.EncodeToString([]byte(profileJSON))
	fromB64, err := ParseProfile(b64)
	if err != nil || fromB64.Name != "RoscomVPN" {
		t.Fatalf("base64 JSON parse failed: profile=%#v err=%v", fromB64, err)
	}

	htmlWithHapp := `<html><body><a href="happ://routing/onadd/` + b64 + `">Import</a></body></html>`
	fromHTML, err := ParseProfile(htmlWithHapp)
	if err != nil || fromHTML.Name != "RoscomVPN" {
		t.Fatalf("embedded happ link parse failed: profile=%#v err=%v", fromHTML, err)
	}

	plainRules := "# custom rules\ndirect domain:gov.ru\nproxy domain:youtube.com\nblock 203.0.113.0/24\ndns-proxy https://dns.google/dns-query\n"
	fromPlain, err := ParseProfile(plainRules)
	if err != nil {
		t.Fatalf("plain rule profile parse failed: %v", err)
	}
	if len(fromPlain.DirectSites) != 1 || len(fromPlain.ProxySites) != 1 || len(fromPlain.BlockIP) != 1 {
		t.Fatalf("unexpected plain rules profile: %#v", fromPlain)
	}
	bootstrap, err := DNSBootstrapAddresses(fromPlain, Overrides{})
	if err != nil || len(bootstrap[ActionProxy]) == 0 || bootstrap[ActionProxy][0].String() != "8.8.8.8" {
		t.Fatalf("expected automatic bootstrap 8.8.8.8 for dns.google, got %v (err=%v)", bootstrap, err)
	}
}
