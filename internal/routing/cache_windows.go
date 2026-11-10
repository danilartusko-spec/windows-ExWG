//go:build windows

package routing

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"nexusclient/internal/debuglog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const cacheFormatVersion = 2

const CacheMaxAge = 72 * time.Hour

var routingCacheMutex sync.Mutex

type memoryCacheEntry struct {
	source    string
	modTime   time.Time
	size      int64
	updatedAt time.Time
	bundle    *Bundle
}

var (
	memoryCacheMutex   sync.Mutex
	memoryRoutingCache = make(map[string]memoryCacheEntry)
)

type Cache struct {
	path string
}

type cacheMetadata struct {
	Version       int       `json:"version"`
	Source        string    `json:"source"`
	UpdatedAt     time.Time `json:"updatedAt"`
	ProfileSHA256 string    `json:"profileSHA256"`
	GeoSiteSHA256 string    `json:"geoSiteSHA256,omitempty"`
	GeoIPSHA256   string    `json:"geoIPSHA256,omitempty"`
}

func (cache *Cache) Delete() error {
	routingCacheMutex.Lock()
	defer routingCacheMutex.Unlock()
	memoryCacheMutex.Lock()
	delete(memoryRoutingCache, cache.path)
	memoryCacheMutex.Unlock()
	var errs []error
	for _, path := range []string{cache.path, cache.path + ".previous"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func NewCache(profileID string) (*Cache, error) {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		return nil, errors.New("Windows не сообщила папку LOCALAPPDATA")
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" || filepath.Base(profileID) != profileID || strings.ContainsAny(profileID, `\/:*?"<>|`) {
		return nil, errors.New("неверный идентификатор routing-кэша")
	}
	directory := filepath.Join(base, "NexusClient", "RoutingCache")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	return &Cache{path: filepath.Join(directory, profileID+".nroute")}, nil
}

func (cache *Cache) Load(source string) (*Bundle, time.Time, error) {
	routingCacheMutex.Lock()
	defer routingCacheMutex.Unlock()
	return cache.load(strings.TrimSpace(source))
}

func (cache *Cache) Refresh(ctx context.Context, loader *Loader, source string) (*Bundle, time.Time, error) {
	debuglog.Event("routing.cache.Refresh", "cache=%s", cache.path)
	source = strings.TrimSpace(source)
	bundle, err := loader.LoadBundle(ctx, source)
	if err != nil {
		debuglog.Error("routing.cache.download", err)
		return nil, time.Time{}, err
	}
	updatedAt := time.Now()
	routingCacheMutex.Lock()
	defer routingCacheMutex.Unlock()
	if err := cache.save(source, updatedAt, bundle); err != nil {
		return nil, time.Time{}, err
	}
	return bundle, updatedAt, nil
}

// Store publishes an already validated bundle into another cache slot. Routing
// settings are client-wide, while older Ex WG builds created one cache per VPN
// profile; copying the newest good generation prevents a newly imported server
// from needlessly waiting for the same routing.help download again.
func (cache *Cache) Store(source string, updatedAt time.Time, bundle *Bundle) error {
	source = strings.TrimSpace(source)
	if bundle == nil || bundle.Profile == nil {
		return errors.New("routing-пакет пуст")
	}
	if _, err := CompileMatcher(bundle.Profile, bundle.GeoSite, bundle.GeoIP); err != nil {
		return err
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	routingCacheMutex.Lock()
	defer routingCacheMutex.Unlock()
	return cache.save(source, updatedAt, bundle)
}

func (cache *Cache) LoadOrRefresh(ctx context.Context, loader *Loader, source string, maxAge time.Duration) (*Bundle, time.Time, error) {
	source = strings.TrimSpace(source)
	cached, cachedAt, cachedErr := cache.Load(source)
	if cachedErr == nil && time.Since(cachedAt) >= 0 && time.Since(cachedAt) < maxAge {
		return cached, cachedAt, nil
	}
	updated, updatedAt, err := cache.Refresh(ctx, loader, source)
	if err == nil {
		return updated, updatedAt, nil
	}
	if cachedErr == nil {
		debuglog.Event("routing.cache.fallback", "refresh failed; retaining cache=%s updatedAt=%s error:\n%s", cache.path, cachedAt, debuglog.ErrorDetail(err))
		return cached, cachedAt, nil
	}
	return nil, time.Time{}, err
}

func (cache *Cache) load(source string) (*Bundle, time.Time, error) {
	if info, statErr := os.Stat(cache.path); statErr == nil {
		memoryCacheMutex.Lock()
		entry, ok := memoryRoutingCache[cache.path]
		memoryCacheMutex.Unlock()
		if ok && entry.source == source && entry.modTime.Equal(info.ModTime()) && entry.size == info.Size() && entry.bundle != nil {
			return entry.bundle, entry.updatedAt, nil
		}
	}
	bundle, updatedAt, err := cache.loadPath(cache.path, source)
	if err == nil {
		if info, statErr := os.Stat(cache.path); statErr == nil {
			memoryCacheMutex.Lock()
			memoryRoutingCache[cache.path] = memoryCacheEntry{
				source:    source,
				modTime:   info.ModTime(),
				size:      info.Size(),
				updatedAt: updatedAt,
				bundle:    bundle,
			}
			memoryCacheMutex.Unlock()
		}
		return bundle, updatedAt, nil
	}
	backup, backupAt, backupErr := cache.loadPath(cache.path+".previous", source)
	if backupErr == nil {
		debuglog.Event("routing.cache.fallback", "current cache invalid; using previous version: %s", debuglog.ErrorDetail(err))
		return backup, backupAt, nil
	}
	return nil, time.Time{}, errors.Join(err, backupErr)
}

func (cache *Cache) loadPath(path, source string) (*Bundle, time.Time, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer reader.Close()
	var metadata cacheMetadata
	var profileData, geoSite, geoIP []byte
	for _, entry := range reader.File {
		switch entry.Name {
		case "metadata.json":
			data, err := readZipEntry(entry, 64<<10)
			if err != nil {
				return nil, time.Time{}, err
			}
			if err := json.Unmarshal(data, &metadata); err != nil {
				return nil, time.Time{}, err
			}
		case "profile.json":
			profileData, err = readZipEntry(entry, maxProfileBytes)
			if err != nil {
				return nil, time.Time{}, err
			}
		case "geosite.dat":
			geoSite, err = readZipEntry(entry, maxGeoDataBytes)
			if err != nil {
				return nil, time.Time{}, err
			}
		case "geoip.dat":
			geoIP, err = readZipEntry(entry, maxGeoDataBytes)
			if err != nil {
				return nil, time.Time{}, err
			}
		}
	}
	if metadata.Version != cacheFormatVersion || metadata.Source != source || metadata.UpdatedAt.IsZero() {
		return nil, time.Time{}, errors.New("routing-кэш устарел или относится к другой ссылке")
	}
	if metadata.ProfileSHA256 != hashHex(profileData) || metadata.GeoSiteSHA256 != optionalHashHex(geoSite) || metadata.GeoIPSHA256 != optionalHashHex(geoIP) {
		return nil, time.Time{}, errors.New("routing-кэш повреждён: SHA-256 не совпадает")
	}
	profile, err := ParseProfile(string(profileData))
	if err != nil {
		return nil, time.Time{}, err
	}
	if _, err := CompileMatcher(profile, geoSite, geoIP); err != nil {
		return nil, time.Time{}, err
	}
	return &Bundle{Profile: profile, GeoSite: geoSite, GeoIP: geoIP}, metadata.UpdatedAt, nil
}

func (cache *Cache) save(source string, updatedAt time.Time, bundle *Bundle) error {
	if bundle == nil || bundle.Profile == nil {
		return errors.New("routing-пакет пуст")
	}
	profileData, err := json.Marshal(bundle.Profile)
	if err != nil {
		return err
	}
	metadataData, err := json.Marshal(cacheMetadata{
		Version: cacheFormatVersion, Source: source, UpdatedAt: updatedAt,
		ProfileSHA256: hashHex(profileData), GeoSiteSHA256: optionalHashHex(bundle.GeoSite), GeoIPSHA256: optionalHashHex(bundle.GeoIP),
	})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(cache.path), "routing-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	archive := zip.NewWriter(temporary)
	for _, item := range []struct {
		name string
		data []byte
	}{
		{"metadata.json", metadataData},
		{"profile.json", profileData},
		{"geosite.dat", bundle.GeoSite},
		{"geoip.dat", bundle.GeoIP},
	} {
		writer, err := archive.Create(item.name)
		if err != nil {
			_ = archive.Close()
			_ = temporary.Close()
			return err
		}
		if _, err := writer.Write(item.data); err != nil {
			_ = archive.Close()
			_ = temporary.Close()
			return err
		}
	}
	if err := archive.Close(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	backupPath := cache.path + ".previous"
	_ = os.Remove(backupPath)
	currentMoved := false
	if err := os.Rename(cache.path, backupPath); err == nil {
		currentMoved = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("не удалось сохранить предыдущий routing-кэш: %w", err)
	}
	if err := os.Rename(temporaryPath, cache.path); err != nil {
		if currentMoved {
			_ = os.Rename(backupPath, cache.path)
		}
		return fmt.Errorf("не удалось заменить routing-кэш: %w", err)
	}
	removeTemporary = false
	if info, statErr := os.Stat(cache.path); statErr == nil {
		memoryCacheMutex.Lock()
		memoryRoutingCache[cache.path] = memoryCacheEntry{
			source:    source,
			modTime:   info.ModTime(),
			size:      info.Size(),
			updatedAt: updatedAt,
			bundle:    bundle,
		}
		memoryCacheMutex.Unlock()
	}
	return nil
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func optionalHashHex(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return hashHex(data)
}

func readZipEntry(entry *zip.File, limit int64) ([]byte, error) {
	if int64(entry.UncompressedSize64) > limit {
		return nil, errors.New("файл routing-кэша превышает допустимый размер")
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("файл routing-кэша превышает допустимый размер")
	}
	return data, nil
}
