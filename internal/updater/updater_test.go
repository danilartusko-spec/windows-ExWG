package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{name: "patch upgrade", a: "0.9.7", b: "0.9.8", want: -1},
		{name: "leading v equal", a: "v0.9.8", b: "0.9.8", want: 0},
		{name: "minor upgrade", a: "0.9.8", b: "0.10.0", want: -1},
		{name: "major downgrade", a: "1.0.0", b: "0.9.9", want: 1},
		{name: "prerelease suffix stripped", a: "v0.9.8-rc1", b: "0.9.8+build1", want: 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CompareVersions(tc.a, tc.b); got != tc.want {
				t.Fatalf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestCheckAndDownloadVerifyWithMockServer(t *testing.T) {
	t.Parallel()

	payload := append([]byte("MZ"), []byte("mock-inno-setup-pe-installer-binary-payload-0.9.8")...)
	sumBytes := sha256.Sum256(payload)
	expectedSHA := hex.EncodeToString(sumBytes[:])

	var serverURL string
	useDigestField := true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			asset := map[string]any{
				"name":                 "Ex-WG-Setup-1.0.0.exe",
				"browser_download_url": serverURL + "/download/Ex-WG-Setup-1.0.0.exe",
				"size":                 len(payload),
			}
			bodyText := "Release 0.9.8 notes\nImproved stability."
			if useDigestField {
				asset["digest"] = "sha256:" + expectedSHA
			} else {
				bodyText = "Release 0.9.8 notes\nEx-WG-Setup-1.0.0.exe: `" + strings.ToUpper(expectedSHA) + "`"
			}
			rel := map[string]any{
				"tag_name":     "v0.9.8",
				"name":         "Ex WG 0.9.8",
				"body":         bodyText,
				"html_url":     "https://github.com/euloxe-dev/windows-ExWG/releases/tag/v0.9.8",
				"published_at": "2026-11-08T00:00:00Z",
				"draft":        false,
				"prerelease":   false,
				"assets":       []any{asset},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rel)
		case "/download/Ex-WG-Setup-1.0.0.exe":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	serverURL = srv.URL

	ctx := context.Background()

	// 1. Check with digest field in asset.
	info, err := Check(ctx, "0.9.7", srv.URL+"/releases/latest")
	if err != nil {
		t.Fatalf("Check with digest returned error: %v", err)
	}
	if !info.Available {
		t.Fatal("expected Available = true")
	}
	if info.LatestVersion != "0.9.8" {
		t.Fatalf("LatestVersion = %q, want 0.9.8", info.LatestVersion)
	}
	if info.AssetName != "Ex-WG-Setup-1.0.0.exe" {
		t.Fatalf("AssetName = %q, want Ex-WG-Setup-1.0.0.exe", info.AssetName)
	}
	if info.SHA256 != expectedSHA {
		t.Fatalf("SHA256 = %q, want %q", info.SHA256, expectedSHA)
	}
	if !strings.Contains(info.ReleaseNotes, "Release 0.9.8 notes") {
		t.Fatalf("unexpected ReleaseNotes: %q", info.ReleaseNotes)
	}

	// 2. Check with SHA-256 embedded in release body.
	useDigestField = false
	infoFromBody, err := Check(ctx, "0.9.7", srv.URL+"/releases/latest")
	if err != nil {
		t.Fatalf("Check with body SHA-256 returned error: %v", err)
	}
	if infoFromBody.SHA256 != expectedSHA {
		t.Fatalf("SHA256 from body = %q, want %q", infoFromBody.SHA256, expectedSHA)
	}

	// 3. DownloadAndVerify succeeds and writes final .exe.
	destDir := t.TempDir()
	installerPath, err := DownloadAndVerify(ctx, infoFromBody, destDir)
	if err != nil {
		t.Fatalf("DownloadAndVerify returned error: %v", err)
	}
	wantPath := filepath.Join(destDir, "Ex-WG-Setup-1.0.0.exe")
	if installerPath != wantPath {
		t.Fatalf("installerPath = %q, want %q", installerPath, wantPath)
	}
	writtenBytes, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatalf("failed reading downloaded installer: %v", err)
	}
	if string(writtenBytes) != string(payload) {
		t.Fatalf("downloaded content mismatch: got %d bytes, want %d bytes", len(writtenBytes), len(payload))
	}

	// 4. Tampered SHA-256 is rejected and .part is deleted.
	tamperedDir := t.TempDir()
	tamperedInfo := infoFromBody
	tamperedInfo.SHA256 = strings.Repeat("a", 64)
	if _, err := DownloadAndVerify(ctx, tamperedInfo, tamperedDir); err == nil {
		t.Fatal("expected error for tampered SHA-256, got nil")
	}
	partFile := filepath.Join(tamperedDir, "Ex-WG-Setup-1.0.0.exe.part")
	if _, statErr := os.Stat(partFile); !os.IsNotExist(statErr) {
		t.Fatalf(".part file must be removed after checksum mismatch, statErr=%v", statErr)
	}
}
