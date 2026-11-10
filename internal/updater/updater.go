package updater

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRepo       = "euloxe-dev/windows-ExWG"
	DefaultReleaseAPI = "https://api.github.com/repos/" + DefaultRepo + "/releases/latest"
	MaxInstallerBytes = 250 << 20 // 250 MiB
)

var sha256Pattern = regexp.MustCompile(`(?i)\b([a-f0-9]{64})\b`)

type UpdateInfo struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	ReleaseNotes   string `json:"releaseNotes"`
	AssetName      string `json:"assetName"`
	DownloadURL    string `json:"downloadUrl"`
	ReleasePageURL string `json:"releasePageUrl"`
	SHA256         string `json:"sha256"`
	PublishedAt    string `json:"publishedAt"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt string        `json:"published_at"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

// CompareVersions normalizes a and b by trimming whitespace and leading v/V,
// stripping any -... or +... suffix, and comparing MAJOR.MINOR.PATCH integers.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func CompareVersions(a, b string) int {
	va := parseVersion(a)
	vb := parseVersion(b)
	for i := 0; i < len(va); i++ {
		if va[i] < vb[i] {
			return -1
		}
		if va[i] > vb[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(raw string) [3]int {
	s := cleanVersion(raw)
	if idx := strings.IndexAny(s, "-+"); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return [3]int{}
	}
	parts := strings.Split(s, ".")
	var out [3]int
	for i := 0; i < len(parts) && i < len(out); i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil || n < 0 {
			return [3]int{}
		}
		out[i] = n
	}
	return out
}

func cleanVersion(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	return strings.TrimSpace(s)
}

func userAgent(currentVersion string) string {
	v := cleanVersion(currentVersion)
	if v == "" {
		v = "0.0.0"
	}
	return "ExWG-Windows-Updater/" + v
}

// Check queries the GitHub Releases API (or custom apiURL / EXWG_UPDATE_URL)
// and returns metadata about the latest available installer release.
func Check(ctx context.Context, currentVersion string, apiURL string) (UpdateInfo, error) {
	targetURL := strings.TrimSpace(apiURL)
	if targetURL == "" {
		targetURL = strings.TrimSpace(os.Getenv("EXWG_UPDATE_URL"))
	}
	if targetURL == "" {
		targetURL = DefaultReleaseAPI
	}

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return UpdateInfo{CurrentVersion: strings.TrimSpace(currentVersion)}, fmt.Errorf("создание запроса обновления: %w", err)
	}
	req.Header.Set("User-Agent", userAgent(currentVersion))
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return UpdateInfo{CurrentVersion: strings.TrimSpace(currentVersion)}, fmt.Errorf("запрос GitHub Releases API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return UpdateInfo{CurrentVersion: strings.TrimSpace(currentVersion)}, fmt.Errorf("GitHub Releases API вернул HTTP %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return UpdateInfo{CurrentVersion: strings.TrimSpace(currentVersion)}, fmt.Errorf("чтение ответа GitHub Releases API: %w", err)
	}

	var release githubRelease
	if err := json.Unmarshal(bodyBytes, &release); err != nil {
		return UpdateInfo{CurrentVersion: strings.TrimSpace(currentVersion)}, fmt.Errorf("разбор JSON релиза: %w", err)
	}

	latestVersion := cleanVersion(release.TagName)
	if latestVersion == "" {
		latestVersion = cleanVersion(release.Name)
	}

	chosen := selectInstallerAsset(release.Assets)
	var assetName, downloadURL, sha256Hex string
	if chosen != nil {
		assetName = strings.TrimSpace(chosen.Name)
		downloadURL = strings.TrimSpace(chosen.BrowserDownloadURL)
		sha256Hex = extractDigestSHA256(chosen.Digest)
	}
	if sha256Hex == "" {
		sha256Hex = extractSHA256FromText(release.Body, assetName)
	}
	if sha256Hex == "" && chosen != nil {
		sha256Hex = fetchChecksumAsset(reqCtx, client, currentVersion, release.Assets, assetName)
	}

	available := !release.Draft &&
		latestVersion != "" &&
		downloadURL != "" &&
		CompareVersions(currentVersion, latestVersion) < 0

	return UpdateInfo{
		Available:      available,
		CurrentVersion: strings.TrimSpace(currentVersion),
		LatestVersion:  latestVersion,
		ReleaseNotes:   strings.TrimSpace(release.Body),
		AssetName:      assetName,
		DownloadURL:    downloadURL,
		ReleasePageURL: strings.TrimSpace(release.HTMLURL),
		SHA256:         sha256Hex,
		PublishedAt:    strings.TrimSpace(release.PublishedAt),
	}, nil
}

func selectInstallerAsset(assets []githubAsset) *githubAsset {
	var fallback *githubAsset
	for i := range assets {
		asset := &assets[i]
		nameLower := strings.ToLower(strings.TrimSpace(asset.Name))
		if !strings.HasSuffix(nameLower, ".exe") {
			continue
		}
		if strings.HasPrefix(nameLower, "ex-wg-setup-") || strings.HasPrefix(nameLower, "exwg-setup-") {
			return asset
		}
		if fallback == nil {
			fallback = asset
		}
	}
	return fallback
}

func extractDigestSHA256(digest string) string {
	d := strings.ToLower(strings.TrimSpace(digest))
	if !strings.HasPrefix(d, "sha256:") {
		return ""
	}
	candidate := strings.TrimPrefix(d, "sha256:")
	if isHex64(candidate) {
		return candidate
	}
	return ""
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func extractSHA256FromText(text string, assetName string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	assetLower := strings.ToLower(strings.TrimSpace(assetName))

	if assetLower != "" {
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), assetLower) {
				if m := sha256Pattern.FindString(line); m != "" {
					return strings.ToLower(m)
				}
				for j := i + 1; j < len(lines) && j <= i+2; j++ {
					if m := sha256Pattern.FindString(lines[j]); m != "" {
						return strings.ToLower(m)
					}
				}
			}
		}
	}

	for i, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "ex-wg-setup-") || strings.Contains(lower, "exwg-setup-") {
			if m := sha256Pattern.FindString(line); m != "" {
				return strings.ToLower(m)
			}
			for j := i + 1; j < len(lines) && j <= i+2; j++ {
				if m := sha256Pattern.FindString(lines[j]); m != "" {
					return strings.ToLower(m)
				}
			}
		}
	}

	if m := sha256Pattern.FindString(text); m != "" {
		return strings.ToLower(m)
	}
	return ""
}

func fetchChecksumAsset(ctx context.Context, client *http.Client, currentVersion string, assets []githubAsset, installerName string) string {
	for _, asset := range assets {
		nameLower := strings.ToLower(strings.TrimSpace(asset.Name))
		isChecksum := strings.HasSuffix(nameLower, ".sha256") ||
			nameLower == "sha256sums" ||
			nameLower == "checksums.txt" ||
			strings.Contains(nameLower, "sha256")
		if !isChecksum {
			continue
		}
		if asset.Size > 64<<10 || strings.TrimSpace(asset.BrowserDownloadURL) == "" {
			continue
		}
		if _, err := validateDownloadURL(asset.BrowserDownloadURL); err != nil {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", userAgent(currentVersion))
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		if sum := extractSHA256FromText(string(data), installerName); sum != "" {
			return sum
		}
	}
	return ""
}

func validateDownloadURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("некорректный URL установщика: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	switch strings.ToLower(u.Scheme) {
	case "https":
		trusted := host == "github.com" ||
			host == "api.github.com" ||
			host == "objects.githubusercontent.com" ||
			host == "releases.githubusercontent.com" ||
			host == "githubusercontent.com" ||
			strings.HasSuffix(host, ".githubusercontent.com") ||
			strings.HasSuffix(host, ".github.com")
		if !trusted {
			return nil, fmt.Errorf("недоверенный хост загрузки обновления: %s", host)
		}
	case "http":
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			return nil, fmt.Errorf("загрузка обновления по HTTP разрешена только для localhost, получен: %s", host)
		}
	default:
		return nil, fmt.Errorf("неподдерживаемая схема URL загрузки: %s", u.Scheme)
	}
	return u, nil
}

// DownloadAndVerify downloads the installer asset to destDir, verifies the PE
// MZ signature and SHA-256 digest (when provided), and returns the final path.
func DownloadAndVerify(ctx context.Context, info UpdateInfo, destDir string) (string, error) {
	parsedURL, err := validateDownloadURL(info.DownloadURL)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(destDir) == "" {
		return "", errors.New("папка для загрузки обновления не указана")
	}
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return "", fmt.Errorf("создание папки обновлений: %w", err)
	}

	rawName := strings.TrimSpace(info.AssetName)
	if rawName == "" {
		rawName = filepath.Base(parsedURL.Path)
	}
	safeName := filepath.Base(rawName)
	if safeName == "" || safeName == "." || safeName == ".." || !strings.HasSuffix(strings.ToLower(safeName), ".exe") {
		return "", fmt.Errorf("некорректное имя файла установщика: %q", info.AssetName)
	}

	partPath := filepath.Join(destDir, safeName+".part")
	finalPath := filepath.Join(destDir, safeName)
	_ = os.Remove(partPath)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("слишком много перенаправлений при загрузке установщика")
			}
			_, redirectErr := validateDownloadURL(req.URL.String())
			return redirectErr
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("создание запроса загрузки установщика: %w", err)
	}
	req.Header.Set("User-Agent", userAgent(info.CurrentVersion))

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("скачивание установщика: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("сервер загрузки вернул HTTP %d", resp.StatusCode)
	}

	partFile, err := os.OpenFile(partPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return "", fmt.Errorf("создание временного файла установщика: %w", err)
	}
	removePart := func() {
		_ = partFile.Close()
		_ = os.Remove(partPath)
	}

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, MaxInstallerBytes+1)
	written, err := io.Copy(io.MultiWriter(partFile, hasher), limited)
	if err != nil {
		removePart()
		return "", fmt.Errorf("запись файла установщика: %w", err)
	}
	if written > MaxInstallerBytes {
		removePart()
		return "", fmt.Errorf("размер установщика превышает допустимый лимит (%d МиБ)", MaxInstallerBytes>>20)
	}
	if written < 2 {
		removePart()
		return "", errors.New("скачанный файл установщика повреждён или пуст")
	}

	var magic [2]byte
	if _, err := partFile.ReadAt(magic[:], 0); err != nil || magic[0] != 'M' || magic[1] != 'Z' {
		removePart()
		return "", errors.New("скачанный файл не является исполняемым файлом Windows PE (отсутствует заголовок MZ)")
	}

	actualHex := strings.ToLower(hex.EncodeToString(hasher.Sum(nil)))
	expectedHex := strings.ToLower(strings.TrimSpace(info.SHA256))
	if expectedHex != "" {
		if !isHex64(expectedHex) || subtle.ConstantTimeCompare([]byte(actualHex), []byte(expectedHex)) != 1 {
			removePart()
			return "", fmt.Errorf("контрольная сумма SHA-256 установщика не совпала: ожидалась %s, получена %s", info.SHA256, actualHex)
		}
	}

	if err := partFile.Sync(); err != nil {
		removePart()
		return "", fmt.Errorf("синхронизация файла установщика: %w", err)
	}
	if err := partFile.Close(); err != nil {
		_ = os.Remove(partPath)
		return "", fmt.Errorf("закрытие временного файла установщика: %w", err)
	}

	if err := os.Rename(partPath, finalPath); err != nil {
		_ = os.Remove(partPath)
		return "", fmt.Errorf("переименование файла установщика: %w", err)
	}

	return finalPath, nil
}
