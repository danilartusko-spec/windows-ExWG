//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nexusclient/internal/awg"
	"nexusclient/internal/debuglog"
	"nexusclient/internal/diagnostics"
	"nexusclient/internal/profileimport"
	"nexusclient/internal/profiles"
	"nexusclient/internal/routing"
	"nexusclient/internal/session"
	"nexusclient/internal/tunnel"
	"nexusclient/internal/updater"
)

const (
	defaultRoutingSource = "https://routing.help/"
	sharedRoutingCacheID = "global"
)

type UIProfile struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Generation string `json:"generation"`
	Routing    string `json:"routing"`
}

type UIState struct {
	Profiles        []UIProfile `json:"profiles"`
	Selected        string      `json:"selected"`
	Connected       bool        `json:"connected"`
	Busy            bool        `json:"busy"`
	Status          string      `json:"status"`
	Version         string      `json:"version"`
	UpdateAvailable bool        `json:"updateAvailable"`
	LatestVersion   string      `json:"latestVersion,omitempty"`
	RXBytes         uint64      `json:"rxBytes"`
	TXBytes         uint64      `json:"txBytes"`
	DownloadBPS     uint64      `json:"downloadBps"`
	UploadBPS       uint64      `json:"uploadBps"`
}

type ProfileEditor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Config    string `json:"config"`
	Routing   string `json:"routing"`
	Overrides string `json:"overrides"`
}

type RoutingDetails struct {
	Source               string            `json:"source"`
	Overrides            string            `json:"overrides"`
	Summary              string            `json:"summary"`
	RawProfile           string            `json:"rawProfile"`
	UpdatedAt            string            `json:"updatedAt"`
	ProfileName          string            `json:"profileName"`
	ProfileLastUpdated   string            `json:"profileLastUpdated"`
	RouteOrder           string            `json:"routeOrder"`
	DomainStrategy       string            `json:"domainStrategy"`
	GlobalProxy          bool              `json:"globalProxy"`
	FakeDNS              bool              `json:"fakeDNS"`
	UseChunkFiles        bool              `json:"useChunkFiles"`
	RemoteDNS            string            `json:"remoteDNS"`
	RemoteDNSType        string            `json:"remoteDNSType"`
	RemoteDNSDomain      string            `json:"remoteDNSDomain"`
	RemoteDNSIP          string            `json:"remoteDNSIP"`
	DomesticDNS          string            `json:"domesticDNS"`
	DomesticDNSType      string            `json:"domesticDNSType"`
	DomesticDNSDomain    string            `json:"domesticDNSDomain"`
	DomesticDNSIP        string            `json:"domesticDNSIP"`
	GeoSiteURL           string            `json:"geoSiteURL"`
	GeoSiteBytes         int               `json:"geoSiteBytes"`
	GeoIPURL             string            `json:"geoIPURL"`
	GeoIPBytes           int               `json:"geoIPBytes"`
	DirectSites          []string          `json:"directSites"`
	DirectIP             []string          `json:"directIP"`
	ProxySites           []string          `json:"proxySites"`
	ProxyIP              []string          `json:"proxyIP"`
	BlockSites           []string          `json:"blockSites"`
	BlockIP              []string          `json:"blockIP"`
	DNSHosts             map[string]string `json:"dnsHosts"`
	Warnings             []string          `json:"warnings"`
	AdditionalFieldNames []string          `json:"additionalFieldNames"`
}

type RouteCheck struct {
	Domain    string   `json:"domain"`
	Action    string   `json:"action"`
	Rule      string   `json:"rule"`
	Addresses []string `json:"addresses"`
	Paths     []string `json:"paths"`
}

type ExWGApp struct {
	mu            sync.Mutex
	store         *profiles.Store
	items         []profiles.Profile
	selectedID    string
	active        *session.Session
	busy          bool
	status        string
	closing       bool
	rxBytes       uint64
	txBytes       uint64
	downloadBPS   uint64
	uploadBPS     uint64
	routingSource      string
	routingRules       string
	generationByConfig map[string]string
	latestUpdate       *updater.UpdateInfo

	backgroundContext context.Context
	backgroundCancel  context.CancelFunc
	shutdownOnce      sync.Once
}

func NewExWGApp() (*ExWGApp, error) {
	store, err := profiles.NewStore()
	if err != nil {
		return nil, err
	}
	preferred, selectedErr := store.LoadSelected()
	app := &ExWGApp{
		store:  store,
		status: "Не подключено",
	}
	if err := app.reload(preferred); err != nil && len(app.items) == 0 {
		return nil, err
	}
	if err := app.initializeRoutingSettings(); err != nil {
		return nil, err
	}
	debuglog.Error("profiles.LoadSelected", selectedErr)
	return app, nil
}

func (app *ExWGApp) startup() {
	app.mu.Lock()
	backgroundContext, cancel := context.WithCancel(context.Background())
	app.backgroundContext = backgroundContext
	app.backgroundCancel = cancel
	app.mu.Unlock()
	cleanupCtx, cleanupCancel := context.WithTimeout(backgroundContext, 15*time.Second)
	debuglog.Error("wg.core.startup.cleanupOrphaned", tunnel.CleanupOrphaned(cleanupCtx))
	cleanupCancel()
	go app.routingUpdater(backgroundContext)
	go app.updateChecker(backgroundContext)
}

func (app *ExWGApp) shutdown() {
	// sync.Once blocks concurrent callers until the first teardown finishes,
	// so the RPC, the deferred main() call and the shutdown event cannot
	// release the core mutex while the tunnel is still being removed.
	app.shutdownOnce.Do(func() {
		app.mu.Lock()
		app.closing = true
		cancel := app.backgroundCancel
		active := app.active
		app.active = nil
		app.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if active != nil {
			debuglog.Error("wg.core.shutdown", active.Close())
		}
	})
}

func (app *ExWGApp) State() UIState {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.stateLocked()
}

func (app *ExWGApp) stateLocked() UIState {
	state := UIState{
		Profiles:    make([]UIProfile, 0, len(app.items)),
		Selected:    app.selectedID,
		Connected:   app.active != nil,
		Busy:        app.busy,
		Status:      app.status,
		Version:     debuglog.Version,
		RXBytes:     app.rxBytes,
		TXBytes:     app.txBytes,
		DownloadBPS: app.downloadBPS,
		UploadBPS:   app.uploadBPS,
	}
	if app.latestUpdate != nil && app.latestUpdate.Available {
		state.UpdateAvailable = true
		state.LatestVersion = app.latestUpdate.LatestVersion
	}
	for _, profile := range app.items {
		generation, cached := app.generationByConfig[profile.Config]
		if !cached {
			generation = "AWG"
			if parsed, err := awg.Parse(profile.Config); err == nil {
				generation = "AmneziaWG " + string(parsed.Version)
			}
			if app.generationByConfig == nil {
				app.generationByConfig = make(map[string]string, len(app.items))
			}
			app.generationByConfig[profile.Config] = generation
		}
		state.Profiles = append(state.Profiles, UIProfile{
			ID: profile.ID, Name: profile.Name, Generation: generation, Routing: profile.Routing,
		})
	}
	return state
}

func (app *ExWGApp) reload(preferred string) error {
	items, err := app.store.LoadAll()
	app.mu.Lock()
	if app.routingSource != "" {
		for index := range items {
			items[index].Routing = app.routingSource
			items[index].RoutingOverrides = app.routingRules
		}
	}
	app.items = items
	if preferred == "" {
		preferred = app.selectedID
	}
	app.selectedID = ""
	for _, item := range items {
		if item.ID == preferred {
			app.selectedID = item.ID
			break
		}
	}
	if app.selectedID == "" && len(items) > 0 {
		app.selectedID = items[0].ID
	}
	selected := app.selectedID
	app.mu.Unlock()
	debuglog.Error("profiles.SaveSelected", app.store.SaveSelected(selected))
	return err
}

func initialRoutingSettings(items []profiles.Profile, selectedID string) profiles.RoutingSettings {
	settings := profiles.RoutingSettings{Source: defaultRoutingSource}
	for _, profile := range items {
		if profile.ID == selectedID && strings.TrimSpace(profile.Routing) != "" {
			settings.Source = strings.TrimSpace(profile.Routing)
			if strings.TrimSpace(profile.RoutingOverrides) != "" {
				settings.Overrides = strings.TrimSpace(profile.RoutingOverrides)
				return settings
			}
			break
		}
	}
	var newest time.Time
	for _, profile := range items {
		if rules := strings.TrimSpace(profile.RoutingOverrides); rules != "" && (newest.IsZero() || profile.UpdatedAt.After(newest)) {
			settings.Overrides = rules
			newest = profile.UpdatedAt
		}
	}
	return settings
}

func (app *ExWGApp) initializeRoutingSettings() error {
	settings, exists, err := app.store.LoadRoutingSettings()
	if err != nil {
		return err
	}
	app.mu.Lock()
	items := append([]profiles.Profile(nil), app.items...)
	selectedID := app.selectedID
	app.mu.Unlock()
	if !exists {
		settings = initialRoutingSettings(items, selectedID)
		if err := app.store.SaveRoutingSettings(settings); err != nil {
			return err
		}
		debuglog.Event("routing.settings.migrate", "source=%s overrideBytes=%d profiles=%d", settings.Source, len(settings.Overrides), len(items))
	}
	app.mu.Lock()
	app.routingSource = settings.Source
	app.routingRules = settings.Overrides
	app.mu.Unlock()
	debuglog.Error("routing.settings.sync", app.syncRoutingProfiles(settings))
	return nil
}

func (app *ExWGApp) syncRoutingProfiles(settings profiles.RoutingSettings) error {
	app.mu.Lock()
	items := append([]profiles.Profile(nil), app.items...)
	app.mu.Unlock()
	var joined error
	for index := range items {
		if items[index].Routing == settings.Source && items[index].RoutingOverrides == settings.Overrides {
			continue
		}
		items[index].Routing = settings.Source
		items[index].RoutingOverrides = settings.Overrides
		if err := app.store.Save(&items[index]); err != nil {
			joined = errors.Join(joined, fmt.Errorf("%s: %w", items[index].ID, err))
		}
	}
	app.mu.Lock()
	for index := range app.items {
		app.items[index].Routing = settings.Source
		app.items[index].RoutingOverrides = settings.Overrides
	}
	app.mu.Unlock()
	return joined
}

func (app *ExWGApp) selectedProfileLocked() (profiles.Profile, bool) {
	for _, profile := range app.items {
		if profile.ID == app.selectedID {
			return profile, true
		}
	}
	return profiles.Profile{}, false
}

func (app *ExWGApp) SelectProfile(id string) (UIState, error) {
	app.mu.Lock()
	if app.busy {
		state := app.stateLocked()
		app.mu.Unlock()
		return state, errors.New("операция уже выполняется")
	}
	var selected profiles.Profile
	found := false
	for _, profile := range app.items {
		if profile.ID == id {
			selected, found = profile, true
			break
		}
	}
	if !found {
		app.mu.Unlock()
		return app.State(), errors.New("профиль не найден")
	}
	if selected.ID == app.selectedID {
		state := app.stateLocked()
		app.mu.Unlock()
		return state, nil
	}
	previous := app.active
	previousProfile, hadPreviousProfile := app.selectedProfileLocked()
	app.selectedID = selected.ID
	if previous == nil {
		state := app.stateLocked()
		app.mu.Unlock()
		debuglog.Error("profiles.SaveSelected", app.store.SaveSelected(selected.ID))
		return state, nil
	}
	app.active = nil
	app.busy = true
	app.status = "Переключение…"
	app.rxBytes = 0
	app.txBytes = 0
	app.downloadBPS = 0
	app.uploadBPS = 0
	app.mu.Unlock()
	debuglog.Error("profiles.SaveSelected", app.store.SaveSelected(selected.ID))
	// Session.Close always runs the full teardown once; a returned error means
	// a cleanup step reported a problem, not that the old tunnel is still up.
	// InstallAndStart removes any leftover service/adapter, so keep switching
	// instead of leaving the user disconnected.
	if err := previous.Close(); err != nil {
		debuglog.Error("profiles.hotSwitch.closePrevious", err)
	}
	state, switchErr := app.startProfile(selected)
	if switchErr == nil || !hadPreviousProfile {
		return state, switchErr
	}

	debuglog.Error("profiles.hotSwitch.newProfile", switchErr)
	app.mu.Lock()
	app.selectedID = previousProfile.ID
	app.busy = true
	app.status = "Возврат предыдущего профиля…"
	app.mu.Unlock()
	debuglog.Error("profiles.SaveSelected.rollback", app.store.SaveSelected(previousProfile.ID))
	rollbackState, rollbackErr := app.startProfile(previousProfile)
	if rollbackErr != nil {
		return rollbackState, errors.Join(switchErr, fmt.Errorf("не удалось вернуть предыдущий профиль: %w", rollbackErr))
	}
	return rollbackState, fmt.Errorf("новый профиль не подключился; предыдущий профиль восстановлен: %w", switchErr)
}

func (app *ExWGApp) GetProfile(id string) (ProfileEditor, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	for _, profile := range app.items {
		if profile.ID == id {
			return ProfileEditor{ID: profile.ID, Name: profile.Name, Config: profile.Config, Routing: app.routingSource, Overrides: app.routingRules}, nil
		}
	}
	return ProfileEditor{}, errors.New("профиль не найден")
}

func (app *ExWGApp) SaveProfile(input ProfileEditor) (UIState, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Config = strings.TrimSpace(input.Config)
	input.Routing = strings.TrimSpace(input.Routing)
	if input.Routing == "" {
		input.Routing = "https://routing.help/"
	}
	parsed, err := awg.Parse(input.Config)
	if err != nil {
		return app.State(), err
	}
	if len(parsed.Peers) != 1 {
		return app.State(), fmt.Errorf("Ex WG поддерживает один [Peer]; в конфигурации найдено: %d", len(parsed.Peers))
	}
	app.mu.Lock()
	if app.busy || app.active != nil {
		app.mu.Unlock()
		return app.State(), errors.New("сначала отключите VPN, затем можно сохранить профиль")
	}
	routingSource := app.routingSource
	routingRules := app.routingRules
	app.mu.Unlock()
	profile := profiles.Profile{ID: input.ID, Name: input.Name, Config: input.Config, Routing: routingSource, RoutingOverrides: routingRules}
	if input.ID != "" {
		app.mu.Lock()
		for _, current := range app.items {
			if current.ID == input.ID {
				profile.CreatedAt = current.CreatedAt
				profile.UpdatedAt = current.UpdatedAt
				break
			}
		}
		app.mu.Unlock()
	}
	if err := app.store.Save(&profile); err != nil {
		return app.State(), err
	}
	if err := app.reload(profile.ID); err != nil {
		return app.State(), err
	}
	return app.State(), nil
}

func (app *ExWGApp) DeleteProfile(id string) (UIState, error) {
	app.mu.Lock()
	if app.busy || app.active != nil {
		app.mu.Unlock()
		return app.State(), errors.New("сначала отключите VPN, затем можно удалить профиль")
	}
	var target profiles.Profile
	found := false
	for _, profile := range app.items {
		if profile.ID == id {
			target, found = profile, true
			break
		}
	}
	app.mu.Unlock()
	if !found {
		return app.State(), errors.New("профиль не найден")
	}
	if err := app.store.Delete(target); err != nil {
		return app.State(), err
	}
	if err := app.reload(""); err != nil {
		return app.State(), err
	}
	if cache, err := routing.NewCache(id); err == nil {
		debuglog.Error("wg.profile.deleteCache", cache.Delete())
	}
	return app.State(), nil
}

func (app *ExWGApp) ImportProfiles(paths []string) (UIState, error) {
	app.mu.Lock()
	blocked := app.busy || app.active != nil
	routingSource := app.routingSource
	routingRules := app.routingRules
	app.mu.Unlock()
	if blocked {
		return app.State(), errors.New("сначала отключите VPN, затем можно импортировать профиль")
	}
	if len(paths) == 0 {
		return app.State(), nil
	}
	var candidates []profileimport.Candidate
	for _, path := range paths {
		loaded, err := profileimport.FromFile(path)
		if err != nil {
			return app.State(), fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		candidates = append(candidates, loaded...)
	}
	saved := make([]profiles.Profile, 0, len(candidates))
	for _, candidate := range candidates {
		profile := profiles.Profile{Name: candidate.Name, Config: candidate.Config, Routing: routingSource, RoutingOverrides: routingRules}
		if err := app.store.Save(&profile); err != nil {
			app.rollbackImportedProfiles(saved)
			return app.State(), err
		}
		saved = append(saved, profile)
	}
	preferred := ""
	if len(saved) > 0 {
		preferred = saved[len(saved)-1].ID
	}
	if err := app.reload(preferred); err != nil {
		return app.State(), err
	}
	if preferred != "" {
		debuglog.Error("profiles.SaveSelected", app.store.SaveSelected(preferred))
	}
	return app.State(), nil
}

func (app *ExWGApp) rollbackImportedProfiles(items []profiles.Profile) {
	for _, profile := range items {
		debuglog.Error("wg.import.rollback", app.store.Delete(profile))
	}
}

func (app *ExWGApp) ToggleConnection() (UIState, error) {
	app.mu.Lock()
	if app.busy {
		state := app.stateLocked()
		app.mu.Unlock()
		return state, errors.New("операция уже выполняется")
	}
	if app.active != nil {
		active := app.active
		app.active = nil
		app.busy = true
		app.status = "Отключение…"
		app.downloadBPS = 0
		app.uploadBPS = 0
		app.mu.Unlock()
		err := active.Close()
		app.mu.Lock()
		app.busy = false
		if err != nil {
			app.status = "Ошибка отключения"
		} else {
			app.status = "Не подключено"
		}
		state := app.stateLocked()
		app.mu.Unlock()
		debuglog.Error("wg.disconnect", err)
		return state, err
	}
	profile, ok := app.selectedProfileLocked()
	if !ok {
		state := app.stateLocked()
		app.mu.Unlock()
		return state, errors.New("сначала добавьте профиль")
	}
	app.busy = true
	app.status = "Подключение…"
	app.rxBytes = 0
	app.txBytes = 0
	app.downloadBPS = 0
	app.uploadBPS = 0
	app.mu.Unlock()
	return app.startProfile(profile)
}

func (app *ExWGApp) startProfile(profile profiles.Profile) (UIState, error) {
	app.mu.Lock()
	routingSource := app.routingSource
	routingRules := app.routingRules
	app.mu.Unlock()
	app.seedRoutingCache(sharedRoutingCacheID, routingSource)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	started, err := session.Start(ctx, profile.Config, routingSource, sharedRoutingCacheID, routingRules)
	cancel()
	var initialStats tunnel.RuntimeStats
	if err == nil {
		statsCtx, statsCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		initialStats, _ = tunnel.ReadRuntimeStatsQuiet(statsCtx)
		statsCancel()
	}
	app.mu.Lock()
	app.busy = false
	if err != nil {
		app.status = "Ошибка подключения"
	} else {
		app.active = started
		app.rxBytes = initialStats.RX
		app.txBytes = initialStats.TX
		if !initialStats.LastHandshake.IsZero() {
			if started.UsingRoutingFallback() {
				app.status = "Подключено · локальные правила"
			} else {
				app.status = "Подключено"
			}
		} else {
			app.status = "Ожидаем ответ"
		}
	}
	state := app.stateLocked()
	app.mu.Unlock()
	if err != nil {
		debuglog.Error("wg.connect", err)
		return state, err
	}
	go app.monitorRuntime(started)
	return state, nil
}

func (app *ExWGApp) monitorRuntime(active *session.Session) {
	startedAt := time.Now()
	graceStart := startedAt
	lastTick := startedAt
	fastPolling := true
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	var previous tunnel.RuntimeStats
	var previousAt time.Time
	readFailures := 0
	for range ticker.C {
		app.mu.Lock()
		if app.closing || app.active != active {
			app.mu.Unlock()
			return
		}
		app.mu.Unlock()
		tickNow := time.Now()
		if detectSystemWake(lastTick, tickNow) {
			debuglog.Event("wg.monitor.wake", "system clock jumped by %s; resetting health grace window", tickNow.Sub(lastTick))
			graceStart = tickNow
			readFailures = 0
			active.ResetTransports()
		}
		lastTick = tickNow
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		stats, err := tunnel.ReadRuntimeStatsQuiet(ctx)
		cancel()
		if err != nil {
			readFailures++
			if failure := runtimeHealthFailure(readFailures, time.Since(graceStart), tunnel.RuntimeStats{}, routing.ProxyHealth{}, err); failure != nil {
				app.failActiveSession(active, failure)
				return
			}
			continue
		}
		readFailures = 0
		if fastPolling && (!stats.LastHandshake.IsZero() || time.Since(startedAt) >= 10*time.Second) {
			fastPolling = false
			ticker.Reset(time.Second)
		}
		now := time.Now()
		var downloadBPS, uploadBPS uint64
		if !previousAt.IsZero() {
			seconds := now.Sub(previousAt).Seconds()
			if seconds >= 0.25 && stats.RX >= previous.RX && stats.TX >= previous.TX {
				downloadBPS = uint64(float64(stats.RX-previous.RX) / seconds)
				uploadBPS = uint64(float64(stats.TX-previous.TX) / seconds)
			}
		}
		if previousAt.IsZero() || now.Sub(previousAt) >= 250*time.Millisecond {
			previous = stats
			previousAt = now
		}
		app.mu.Lock()
		if app.active == active {
			app.rxBytes = stats.RX
			app.txBytes = stats.TX
			app.downloadBPS = downloadBPS
			app.uploadBPS = uploadBPS
			if !stats.LastHandshake.IsZero() {
				if active.UsingRoutingFallback() {
					app.status = "Подключено · локальные правила"
				} else {
					app.status = "Подключено"
				}
			} else if time.Since(startedAt) >= 30*time.Second {
				app.status = "Ожидание сервера"
			}
		}
		app.mu.Unlock()
		health := active.ProxyHealth()
		if failure := runtimeHealthFailure(readFailures, time.Since(graceStart), stats, health, nil); failure != nil {
			app.failActiveSession(active, failure)
			return
		}
	}
}

func detectSystemWake(previousTick, currentTick time.Time) bool {
	if previousTick.IsZero() {
		return false
	}
	// Round(0) strips the monotonic reading and compares wall-clock time,
	// covering both clock behaviours across Windows suspend/hibernate.
	return currentTick.Sub(previousTick) > 5*time.Second ||
		currentTick.Round(0).Sub(previousTick.Round(0)) > 5*time.Second
}

func runtimeHealthFailure(readFailures int, connectedFor time.Duration, stats tunnel.RuntimeStats, health routing.ProxyHealth, readErr error) error {
	if readFailures >= 3 {
		return fmt.Errorf("VPN-служба не отвечает три проверки подряд: %w", readErr)
	}
	if connectedFor < 30*time.Second || health.ConsecutiveFailures < 3 {
		return nil
	}
	if !stats.LastHandshake.IsZero() && time.Since(stats.LastHandshake) < 3*time.Minute {
		return nil
	}
	return fmt.Errorf("Proxy DNS недоступен %d запросов подряд и туннель не подтверждает обмен", health.ConsecutiveFailures)
}

func (app *ExWGApp) failActiveSession(active *session.Session, cause error) {
	app.mu.Lock()
	if app.closing || app.active != active {
		app.mu.Unlock()
		return
	}
	app.active = nil
	app.busy = true
	app.status = "Восстановление обычной сети…"
	app.downloadBPS = 0
	app.uploadBPS = 0
	app.mu.Unlock()
	debuglog.Error("wg.connectionLost", cause)
	cleanupErr := active.Close()
	app.mu.Lock()
	app.busy = false
	app.status = "Соединение потеряно"
	app.mu.Unlock()
	debuglog.Error("wg.connectionLost.cleanup", cleanupErr)
}

func (app *ExWGApp) RoutingDetails() (RoutingDetails, error) {
	app.mu.Lock()
	routingSource := app.routingSource
	routingRules := app.routingRules
	app.mu.Unlock()
	if strings.TrimSpace(routingSource) == "" {
		routingSource = defaultRoutingSource
	}
	app.seedRoutingCache(sharedRoutingCacheID, routingSource)
	details := RoutingDetails{
		Source:    routingSource,
		Overrides: routingRules,
		Summary:   "База правил загружается в фоне · локальные правила уже активны",
	}
	cache, err := routing.NewCache(sharedRoutingCacheID)
	if err != nil {
		return details, err
	}
	bundle, updatedAt, err := cache.Load(routingSource)
	if err != nil {
		return details, nil
	}
	data, _ := json.MarshalIndent(bundle.Profile, "", "  ")
	routingProfile := bundle.Profile
	details.RawProfile = string(data)
	details.UpdatedAt = updatedAt.Format("02.01.2006 15:04")
	details.ProfileName = routingProfile.Name
	details.ProfileLastUpdated = formatRoutingProfileTime(routingProfile.LastUpdated)
	details.RouteOrder = routingProfile.RouteOrder
	details.DomainStrategy = routingProfile.DomainStrategy
	details.GlobalProxy = bool(routingProfile.GlobalProxy)
	details.FakeDNS = bool(routingProfile.FakeDNS)
	details.UseChunkFiles = bool(routingProfile.UseChunkFiles)
	details.RemoteDNS = routingProfile.RemoteDNS
	details.RemoteDNSType = routingProfile.RemoteDNSType
	details.RemoteDNSDomain = routingProfile.RemoteDNSDomain
	details.RemoteDNSIP = routingProfile.RemoteDNSIP
	details.DomesticDNS = routingProfile.DomesticDNS
	details.DomesticDNSType = routingProfile.DomesticDNSType
	details.DomesticDNSDomain = routingProfile.DomesticDNSDomain
	details.DomesticDNSIP = routingProfile.DomesticDNSIP
	details.GeoSiteURL = routingProfile.GeoSiteURL
	details.GeoSiteBytes = len(bundle.GeoSite)
	details.GeoIPURL = routingProfile.GeoIPURL
	details.GeoIPBytes = len(bundle.GeoIP)
	details.DirectSites = append([]string(nil), routingProfile.DirectSites...)
	details.DirectIP = append([]string(nil), routingProfile.DirectIP...)
	details.ProxySites = append([]string(nil), routingProfile.ProxySites...)
	details.ProxyIP = append([]string(nil), routingProfile.ProxyIP...)
	details.BlockSites = append([]string(nil), routingProfile.BlockSites...)
	details.BlockIP = append([]string(nil), routingProfile.BlockIP...)
	details.DNSHosts = routingProfile.DNSHosts
	details.Warnings = routingProfile.Warnings()
	for name := range routingProfile.AdditionalUnknownFields {
		details.AdditionalFieldNames = append(details.AdditionalFieldNames, name)
	}
	sort.Strings(details.AdditionalFieldNames)
	details.Summary = "Проверенный набор применён · обновляется при запуске"
	return details, nil
}

func (app *ExWGApp) CheckRoute(domain string) (RouteCheck, error) {
	domain = strings.TrimSpace(domain)
	if parsed, err := url.Parse(domain); err == nil && parsed.Hostname() != "" {
		domain = parsed.Hostname()
	}
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" {
		return RouteCheck{}, errors.New("укажите домен")
	}
	app.mu.Lock()
	active := app.active
	app.mu.Unlock()
	if active == nil {
		return RouteCheck{}, errors.New("проверка фактического маршрута доступна после подключения")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	checked, err := active.CheckRoute(ctx, domain)
	if err != nil {
		return RouteCheck{}, err
	}
	return RouteCheck{
		Domain: checked.Domain, Action: string(checked.Action), Rule: checked.Rule,
		Addresses: checked.Addresses, Paths: checked.Paths,
	}, nil
}

func formatRoutingProfileTime(value string) string {
	value = strings.TrimSpace(value)
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return value
	}
	return time.Unix(seconds, 0).Local().Format("02.01.2006 15:04")
}

func (app *ExWGApp) SaveRouting(source, overrides string) (RoutingDetails, error) {
	source = strings.TrimSpace(source)
	overrides = strings.TrimSpace(overrides)
	if source == "" {
		source = defaultRoutingSource
	}
	if _, err := routing.ParseOverrides(overrides); err != nil {
		return RoutingDetails{}, err
	}
	app.mu.Lock()
	if app.busy {
		app.mu.Unlock()
		return RoutingDetails{}, errors.New("операция уже выполняется")
	}
	active := app.active
	previousSource := app.routingSource
	previousOverrides := app.routingRules
	if active != nil && source != previousSource {
		app.mu.Unlock()
		return RoutingDetails{}, errors.New("источник routing можно менять только при отключённом VPN")
	}
	app.busy = true
	app.mu.Unlock()
	defer func() {
		app.mu.Lock()
		app.busy = false
		app.mu.Unlock()
	}()
	sourceChanged := source != previousSource
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cache, err := routing.NewCache(sharedRoutingCacheID)
	if err == nil {
		_, _, cachedErr := cache.Load(source)
		if sourceChanged || cachedErr != nil {
			_, _, err = cache.Refresh(ctx, routing.NewLoader(), source)
		}
	}
	if err != nil {
		return RoutingDetails{}, err
	}
	if active != nil {
		if err := active.ReloadRouting(ctx, overrides); err != nil {
			return RoutingDetails{}, err
		}
	}
	settings := profiles.RoutingSettings{Source: source, Overrides: overrides}
	if err := app.store.SaveRoutingSettings(settings); err != nil {
		if active != nil {
			debuglog.Error("routing.hotUpdate.rollback", active.ReloadRouting(ctx, previousOverrides))
		}
		return RoutingDetails{}, err
	}
	app.mu.Lock()
	app.routingSource = source
	app.routingRules = overrides
	app.mu.Unlock()
	debuglog.Error("routing.settings.sync", app.syncRoutingProfiles(settings))
	debuglog.Event("routing.settings.save", "sourceChanged=%t overrideBytes=%d overrideLines=%d profiles=%d", sourceChanged, len(overrides), countNonEmptyLines(overrides), len(app.State().Profiles))
	return app.RoutingDetails()
}

func (app *ExWGApp) RefreshRouting(reset bool) (RoutingDetails, error) {
	app.mu.Lock()
	if app.busy || (reset && app.active != nil) {
		app.mu.Unlock()
		return RoutingDetails{}, errors.New("операция сейчас недоступна")
	}
	active := app.active
	routingSource := app.routingSource
	routingRules := app.routingRules
	app.busy = true
	app.mu.Unlock()
	if reset || strings.TrimSpace(routingSource) == "" {
		routingSource = defaultRoutingSource
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cache, err := routing.NewCache(sharedRoutingCacheID)
	if err == nil {
		_, _, err = cache.Refresh(ctx, routing.NewLoader(), routingSource)
	}
	if err == nil && reset {
		settings := profiles.RoutingSettings{Source: routingSource}
		err = app.store.SaveRoutingSettings(settings)
		if err == nil {
			app.mu.Lock()
			app.routingSource = routingSource
			app.routingRules = ""
			app.mu.Unlock()
			debuglog.Error("routing.settings.sync", app.syncRoutingProfiles(settings))
			debuglog.Event("routing.settings.reset", "profiles=%d", len(app.State().Profiles))
		}
	}
	if err == nil && active != nil && !reset {
		err = active.ReloadRouting(ctx, routingRules)
	}
	app.mu.Lock()
	app.busy = false
	app.mu.Unlock()
	if err != nil {
		debuglog.Error("wg.routing.refresh", err)
		return RoutingDetails{}, err
	}
	return app.RoutingDetails()
}

func countNonEmptyLines(value string) int {
	count := 0
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func (app *ExWGApp) SetDiagnosticsEnabled(enabled bool) (bool, error) {
	err := debuglog.SetEnabled(enabled, false)
	if j := debuglog.Default(); j != nil {
		log.SetOutput(j)
	} else {
		log.SetOutput(io.Discard)
	}
	return debuglog.Enabled(), err
}

func (app *ExWGApp) DiagnosticsPreview() string {
	if journal := debuglog.Default(); journal != nil {
		_ = journal.Sync()
	}
	return debuglog.Combined(192 << 10)
}

func (app *ExWGApp) RunDiagnostics() (string, error) {
	_, _ = app.SetDiagnosticsEnabled(true)
	app.mu.Lock()
	profile, ok := app.selectedProfileLocked()
	app.mu.Unlock()
	if !ok {
		return app.DiagnosticsPreview(), errors.New("сначала добавьте профиль")
	}
	app.runDiagnostics(profile)
	return app.DiagnosticsPreview(), nil
}

func (app *ExWGApp) runDiagnostics(profile profiles.Profile) {
	app.mu.Lock()
	routingSource := app.routingSource
	routingRules := app.routingRules
	app.mu.Unlock()
	app.seedRoutingCache(sharedRoutingCacheID, routingSource)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	report := diagnostics.RunWithRouting(ctx, profile.Config, routingSource, sharedRoutingCacheID, routingRules)
	debuglog.Event("wg.diagnostics", "\n%s", report.Text())
}

func (app *ExWGApp) FullDiagnostics() string {
	return app.fullDiagnostics()
}

func (app *ExWGApp) SaveDiagnostics(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("путь для TXT пуст")
	}
	if err := debuglog.WriteTXT(path, app.fullDiagnostics()); err != nil {
		return "", err
	}
	return path, nil
}

func (app *ExWGApp) fullDiagnostics() string {
	if journal := debuglog.Default(); journal != nil {
		_ = journal.Sync()
	}
	return fmt.Sprintf("Ex WG %s\nExported %s\n\n", debuglog.Version, time.Now().Format(time.RFC3339Nano)) + debuglog.Combined(0)
}

func (app *ExWGApp) routingUpdater(ctx context.Context) {
	app.refreshDueRouting(ctx, false)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			app.refreshDueRouting(ctx, false)
		}
	}
}

func (app *ExWGApp) refreshDueRouting(ctx context.Context, force bool) {
	if ctx.Err() != nil {
		return
	}
	app.mu.Lock()
	source := strings.TrimSpace(app.routingSource)
	rules := app.routingRules
	active := app.active
	app.mu.Unlock()
	if source == "" {
		source = defaultRoutingSource
	}
	app.seedRoutingCache(sharedRoutingCacheID, source)
	cache, err := routing.NewCache(sharedRoutingCacheID)
	if err != nil {
		debuglog.Error("wg.routing.cache", err)
		return
	}
	_, updatedAt, loadErr := cache.Load(source)
	if !strings.HasPrefix(strings.ToLower(source), "https://") {
		if loadErr == nil {
			return
		}
	} else if !force && loadErr == nil {
		age := time.Since(updatedAt)
		if age >= 0 && age < routing.CacheMaxAge {
			return
		}
	}
	operationContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
	_, _, refreshErr := cache.Refresh(operationContext, routing.NewLoader(), source)
	if refreshErr == nil && active != nil && active.UsingRoutingFallback() {
		debuglog.Error("wg.routing.hotApplyFallback", active.ReloadRouting(operationContext, rules))
	}
	cancel()
	debuglog.Error("wg.routing.refresh", refreshErr)
}

func (app *ExWGApp) newestRoutingBundle(source string) (*routing.Bundle, time.Time) {
	app.mu.Lock()
	items := append([]profiles.Profile(nil), app.items...)
	app.mu.Unlock()
	cacheIDs := make([]string, 0, len(items)+1)
	cacheIDs = append(cacheIDs, sharedRoutingCacheID)
	for _, profile := range items {
		if profile.ID != "" && profile.ID != sharedRoutingCacheID {
			cacheIDs = append(cacheIDs, profile.ID)
		}
	}
	var newest *routing.Bundle
	var newestAt time.Time
	for _, id := range cacheIDs {
		cache, err := routing.NewCache(id)
		if err != nil {
			continue
		}
		bundle, updatedAt, err := cache.Load(source)
		if err == nil && (newest == nil || updatedAt.After(newestAt)) {
			newest = bundle
			newestAt = updatedAt
		}
	}
	return newest, newestAt
}

func (app *ExWGApp) seedRoutingCache(targetID, source string) bool {
	target, err := routing.NewCache(targetID)
	if err != nil {
		debuglog.Error("wg.routing.seed.target", err)
		return false
	}
	if _, _, err := target.Load(source); err == nil {
		return true
	}
	bundle, updatedAt := app.newestRoutingBundle(source)
	if bundle == nil {
		return false
	}
	if err := target.Store(source, updatedAt, bundle); err != nil {
		debuglog.Error("wg.routing.seed.store", err)
		return false
	}
	debuglog.Event("wg.routing.seed", "reused last-known-good cache for profile=%s cacheTime=%s", targetID, updatedAt)
	return true
}

func (app *ExWGApp) parentContext() context.Context {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.backgroundContext != nil {
		return app.backgroundContext
	}
	return context.Background()
}

func (app *ExWGApp) updateChecker(ctx context.Context) {
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	_, _ = app.CheckForUpdates()
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = app.CheckForUpdates()
		}
	}
}

func (app *ExWGApp) CheckForUpdates() (updater.UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(app.parentContext(), 20*time.Second)
	defer cancel()
	info, err := updater.Check(ctx, debuglog.Version, "")
	if err != nil {
		debuglog.Error("wg.updater.check", err)
		return updater.UpdateInfo{CurrentVersion: debuglog.Version}, err
	}
	app.mu.Lock()
	app.latestUpdate = &info
	app.mu.Unlock()
	return info, nil
}

func (app *ExWGApp) InstallUpdate() (bool, error) {
	app.mu.Lock()
	var cached updater.UpdateInfo
	hasCached := app.latestUpdate != nil && app.latestUpdate.Available
	if hasCached {
		cached = *app.latestUpdate
	}
	app.mu.Unlock()

	info := cached
	if !hasCached {
		checked, err := app.CheckForUpdates()
		if err != nil {
			return false, err
		}
		info = checked
	}
	if !info.Available {
		return false, errors.New("обновление не найдено")
	}

	dir, err := updater.UpdatesDirectory()
	if err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(app.parentContext(), 5*time.Minute)
	defer cancel()

	installerPath, err := updater.DownloadAndVerify(ctx, info, dir)
	if err != nil {
		debuglog.Error("wg.updater.download", err)
		return false, err
	}

	app.mu.Lock()
	active := app.active
	app.active = nil
	if active != nil {
		app.status = "Отключение для обновления…"
		app.downloadBPS = 0
		app.uploadBPS = 0
	}
	app.mu.Unlock()
	if active != nil {
		debuglog.Error("wg.updater.disconnect", active.Close())
	}

	if err := updater.LaunchInstaller(installerPath); err != nil {
		debuglog.Error("wg.updater.launch", err)
		return false, err
	}
	debuglog.Event("wg.updater.install", "version=%s path=%s", info.LatestVersion, installerPath)

	go func() {
		time.Sleep(350 * time.Millisecond)
		postQuitToFlutterWindow()
		app.shutdown()
		if journal := debuglog.Default(); journal != nil {
			_ = journal.Sync()
		}
		os.Exit(0)
	}()

	return true, nil
}

