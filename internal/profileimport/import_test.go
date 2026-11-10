package profileimport

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testConfig = `[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Address = 10.0.0.2/32
Jc = 4
Jmin = 40
Jmax = 70
S1 = 10
S2 = 20
H1 = 1
H2 = 2
H3 = 3
H4 = 4
[Peer]
PublicKey = AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=
AllowedIPs = 0.0.0.0/0
Endpoint = 203.0.113.1:51820
`

func TestFromTextValidatesConfiguration(t *testing.T) {
	candidate, err := FromText("test", testConfig)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Name != "test" || candidate.Generation == "" {
		t.Fatalf("candidate=%+v", candidate)
	}
}

func TestZIPIsAllOrNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, contents := range map[string]string{"good.conf": testConfig, "bad.conf": "not a config"} {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write([]byte(contents))
	}
	_ = archive.Close()
	_ = file.Close()
	if candidates, err := FromFile(path); err == nil || len(candidates) != 0 {
		t.Fatalf("expected atomic validation failure, candidates=%d err=%v", len(candidates), err)
	}
}

func TestRejectsOversizedText(t *testing.T) {
	if _, err := FromText("large", strings.Repeat("x", maxConfigurationSize+1)); err == nil {
		t.Fatal("expected size error")
	}
}

func TestRejectsMultiplePeersDuringImport(t *testing.T) {
	multiple := testConfig + `[Peer]
PublicKey = AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=
AllowedIPs = 10.0.0.0/8
Endpoint = 203.0.113.2:51820
`
	if _, err := FromText("multiple", multiple); err == nil || !strings.Contains(err.Error(), "один [Peer]") {
		t.Fatalf("expected an early one-peer error, got %v", err)
	}
}
