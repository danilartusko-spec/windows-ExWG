import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'models.dart';

abstract class ExWGBackend {
  Future<void> start();
  Future<UiState> state();
  Future<UiState> selectProfile(String id);
  Future<ProfileEditor> getProfile(String id);
  Future<UiState> saveProfile(ProfileEditor profile);
  Future<UiState> deleteProfile(String id);
  Future<UiState> importProfiles(List<String> paths);
  Future<UiState> toggleConnection();
  Future<RoutingDetails> routingDetails();
  Future<RoutingDetails> saveRouting(String source, String overrides);
  Future<RoutingDetails> refreshRouting({bool reset = false});
  Future<RouteCheckResult> checkRoute(String domain);
  Future<bool> setDiagnosticsEnabled(bool enabled);
  Future<String> diagnosticsPreview();
  Future<String> runDiagnostics();
  Future<String> fullDiagnostics();
  Future<String> saveDiagnostics(String path);
  Future<UpdateInfo> checkForUpdates();
  Future<bool> installUpdate();
  Future<void> dispose();
}

class CoreProcessClient implements ExWGBackend {
  Process? _process;
  StreamSubscription<String>? _stdoutSubscription;
  StreamSubscription<String>? _stderrSubscription;
  final Map<int, Completer<dynamic>> _pending = {};
  int _nextId = 1;
  String _stderrTail = '';

  @override
  Future<void> start() async {
    if (_process != null) return;
    if (!Platform.isWindows) {
      throw UnsupportedError('ExWGCore запускается только в Windows');
    }
    final directory = File(Platform.resolvedExecutable).parent.path;
    final executable = '$directory${Platform.pathSeparator}ExWGCore.exe';
    if (!File(executable).existsSync()) {
      throw StateError(
        'Не найден ExWGCore.exe рядом с ExWG.exe: $executable',
      );
    }
    final process = await Process.start(executable, const ['--ipc-stdio']);
    _process = process;
    _stdoutSubscription = process.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen(_handleLine, onError: _failAll);
    _stderrSubscription = process.stderr
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen((line) {
          _stderrTail = '$_stderrTail\n$line';
          if (_stderrTail.length > 12000) {
            _stderrTail = _stderrTail.substring(_stderrTail.length - 12000);
          }
        });
    unawaited(
      process.exitCode.then((code) {
        if (_process == process) {
          _failAll(
            StateError(
              'ExWGCore завершился с кодом $code${_stderrTail.trim().isEmpty ? '' : '\n${_stderrTail.trim()}'}',
            ),
          );
          _process = null;
        }
      }),
    );
    await state().timeout(const Duration(seconds: 12));
  }

  void _handleLine(String line) {
    try {
      final json = jsonDecode(line) as Map<String, dynamic>;
      final id = (json['id'] as num?)?.toInt() ?? 0;
      final completer = _pending.remove(id);
      if (completer == null) return;
      final error = json['error'] as String? ?? '';
      if (error.isNotEmpty) {
        completer.completeError(StateError(error));
      } else {
        completer.complete(json['result']);
      }
    } catch (error, stack) {
      _failAll(error, stack);
    }
  }

  void _failAll(Object error, [StackTrace? stack]) {
    final completers = _pending.values.toList(growable: false);
    _pending.clear();
    for (final completer in completers) {
      if (!completer.isCompleted) completer.completeError(error, stack);
    }
  }

  Future<dynamic> _call(String method, [Map<String, dynamic>? params]) {
    final process = _process;
    if (process == null) throw StateError('ExWGCore не запущен');
    final id = _nextId++;
    final completer = Completer<dynamic>();
    _pending[id] = completer;
    process.stdin.writeln(
      jsonEncode({
        'id': id,
        'method': method,
        if (params != null) 'params': params,
      }),
    );
    return completer.future.timeout(
      const Duration(minutes: 2),
      onTimeout: () {
        _pending.remove(id);
        throw TimeoutException('ExWGCore не ответил: $method');
      },
    );
  }

  Map<String, dynamic> _map(dynamic value) =>
      Map<String, dynamic>.from(value as Map<dynamic, dynamic>);

  @override
  Future<UiState> state() async => UiState.fromJson(_map(await _call('state')));

  @override
  Future<UiState> selectProfile(String id) async =>
      UiState.fromJson(_map(await _call('selectProfile', {'id': id})));

  @override
  Future<ProfileEditor> getProfile(String id) async =>
      ProfileEditor.fromJson(_map(await _call('getProfile', {'id': id})));

  @override
  Future<UiState> saveProfile(ProfileEditor profile) async =>
      UiState.fromJson(_map(await _call('saveProfile', profile.toJson())));

  @override
  Future<UiState> deleteProfile(String id) async =>
      UiState.fromJson(_map(await _call('deleteProfile', {'id': id})));

  @override
  Future<UiState> importProfiles(List<String> paths) async =>
      UiState.fromJson(_map(await _call('importProfiles', {'paths': paths})));

  @override
  Future<UiState> toggleConnection() async =>
      UiState.fromJson(_map(await _call('toggleConnection')));

  @override
  Future<RoutingDetails> routingDetails() async =>
      RoutingDetails.fromJson(_map(await _call('routingDetails')));

  @override
  Future<RoutingDetails> saveRouting(String source, String overrides) async =>
      RoutingDetails.fromJson(
        _map(
          await _call('saveRouting', {
            'source': source,
            'overrides': overrides,
          }),
        ),
      );

  @override
  Future<RoutingDetails> refreshRouting({bool reset = false}) async =>
      RoutingDetails.fromJson(
        _map(await _call('refreshRouting', {'reset': reset})),
      );

  @override
  Future<RouteCheckResult> checkRoute(String domain) async =>
      RouteCheckResult.fromJson(
        _map(await _call('checkRoute', {'domain': domain})),
      );

  @override
  Future<bool> setDiagnosticsEnabled(bool enabled) async =>
      await _call('setDiagnosticsEnabled', {'enabled': enabled}) as bool? ??
      enabled;

  @override
  Future<String> diagnosticsPreview() async =>
      await _call('diagnosticsPreview') as String? ?? '';

  @override
  Future<String> runDiagnostics() async =>
      await _call('runDiagnostics') as String? ?? '';

  @override
  Future<String> fullDiagnostics() async =>
      await _call('fullDiagnostics') as String? ?? '';

  @override
  Future<String> saveDiagnostics(String path) async =>
      await _call('saveDiagnostics', {'path': path}) as String? ?? path;

  @override
  Future<UpdateInfo> checkForUpdates() async =>
      UpdateInfo.fromJson(_map(await _call('checkForUpdates')));

  @override
  Future<bool> installUpdate() async =>
      await _call('installUpdate') as bool? ?? false;

  @override
  Future<void> dispose() async {
    final process = _process;
    if (process != null) {
      try {
        await _call('shutdown').timeout(const Duration(seconds: 12));
      } catch (_) {
        process.kill();
      }
      try {
        await process.stdin.close();
      } catch (_) {
        // The core may close its pipe immediately after the shutdown response.
      }
    }
    _process = null;
    await _stdoutSubscription?.cancel();
    await _stderrSubscription?.cancel();
    _failAll(StateError('Ex WG закрыт'));
  }
}

class PreviewBackend implements ExWGBackend {
  PreviewBackend({this.delay = const Duration(milliseconds: 80)});

  final Duration delay;
  String lastSavedOverrides = '';
  bool diagnosticsEnabled = false;
  UpdateInfo? previewUpdate;
  bool updateInstalled = false;
  var _state = const UiState(
    profiles: [
      UiProfile(
        id: 'nexus-1',
        name: 'Ex WG Main',
        generation: 'AmneziaWG 3.1',
        routing: 'https://routing.help/',
      ),
      UiProfile(
        id: 'reserve',
        name: 'Reserve',
        generation: 'AmneziaWG 2.1',
        routing: 'https://routing.help/',
      ),
    ],
    selected: 'nexus-1',
    connected: true,
    busy: false,
    status: 'Подключено',
    version: '0.0.0',
    rxBytes: 73400320,
    txBytes: 12582912,
    downloadBps: 2713600,
    uploadBps: 286720,
  );

  Future<T> _later<T>(T value) async {
    if (delay == Duration.zero) return value;
    await Future<void>.delayed(delay);
    return value;
  }

  UiState _copy({String? selected, bool? connected, String? status}) => UiState(
    profiles: _state.profiles,
    selected: selected ?? _state.selected,
    connected: connected ?? _state.connected,
    busy: false,
    status: status ?? _state.status,
    version: _state.version,
    rxBytes: connected == false ? 0 : _state.rxBytes,
    txBytes: connected == false ? 0 : _state.txBytes,
    downloadBps: connected == false ? 0 : _state.downloadBps,
    uploadBps: connected == false ? 0 : _state.uploadBps,
    updateAvailable: _state.updateAvailable,
    latestVersion: _state.latestVersion,
  );

  @override
  Future<void> start() async {}
  @override
  Future<UiState> state() => _later(_state);
  @override
  Future<UiState> selectProfile(String id) async {
    _state = _copy(selected: id);
    return _later(_state);
  }

  @override
  Future<ProfileEditor> getProfile(String id) => _later(
    ProfileEditor(
      id: id,
      name: _state.profiles.firstWhere((profile) => profile.id == id).name,
      config:
          '[Interface]\nPrivateKey = ••••••••\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = ••••••••\nEndpoint = vpn.example:443',
    ),
  );

  @override
  Future<UiState> saveProfile(ProfileEditor profile) => _later(_state);
  @override
  Future<UiState> deleteProfile(String id) => _later(_state);
  @override
  Future<UiState> importProfiles(List<String> paths) => _later(_state);
  @override
  Future<UiState> toggleConnection() async {
    final connected = !_state.connected;
    _state = _copy(
      connected: connected,
      status: connected ? 'Подключено' : 'Не подключено',
    );
    return _later(_state);
  }

  @override
  Future<RoutingDetails> routingDetails() => _later(
    const RoutingDetails(
      source: 'https://routing.help/',
      overrides: 'proxy geosite:apple\ndirect domain:gov.ru',
      summary: 'Проверенный набор применён · обновляется при запуске',
      rawProfile:
          '[Routing]\nRouteOrder = block,direct,proxy\nDNS.Direct = system\nDNS.Proxy = https://1.1.1.1/dns-query',
      updatedAt: 'сегодня, 12:40',
      profileName: 'RoscomVPN',
      profileLastUpdated: 'сегодня, 11:38',
      routeOrder: 'block-proxy-direct',
      domainStrategy: 'IPIfNonMatch',
      globalProxy: true,
      fakeDNS: false,
      useChunkFiles: true,
      remoteDNS: '8.8.8.8',
      remoteDNSType: 'DoH',
      remoteDNSDomain: 'dns.google',
      remoteDNSIP: '8.8.8.8',
      domesticDNS: '77.88.8.8',
      domesticDNSType: 'DoH',
      domesticDNSDomain: 'common.dot.dns.yandex.net',
      domesticDNSIP: '77.88.8.8',
      geoSiteURL: 'https://cdn.jsdelivr.net/geosite.dat',
      geoSiteBytes: 67824,
      geoIPURL: 'https://cdn.jsdelivr.net/geoip.dat',
      geoIPBytes: 428058,
      directSites: [
        'geosite:private',
        'geosite:category-ru',
        'geosite:whitelist',
        'geosite:microsoft',
        'geosite:apple',
        'geosite:epicgames',
        'geosite:riot',
        'geosite:escapefromtarkov',
        'geosite:steam',
        'geosite:twitch',
        'geosite:pinterest',
        'geosite:faceit',
      ],
      directIP: ['geoip:private', 'geoip:direct'],
      proxySites: [
        'geosite:google-play',
        'geosite:github',
        'geosite:twitch-ads',
        'geosite:youtube',
        'geosite:telegram',
      ],
      proxyIP: [],
      blockSites: [
        'geosite:win-spy',
        'geosite:torrent',
        'geosite:category-ads',
      ],
      blockIP: [],
      dnsHosts: {
        'lkfl2.nalog.ru': '213.24.64.175',
        'lknpd.nalog.ru': '213.24.64.181',
      },
      warnings: [
        'Профиль предлагает чанковое обновление GeoSite/GeoIP. Windows скачивает полные DAT: правила применяются полностью, но обновление использует больше трафика.',
      ],
      additionalFieldNames: [],
    ),
  );
  @override
  Future<RoutingDetails> saveRouting(String source, String overrides) {
    lastSavedOverrides = overrides;
    return routingDetails();
  }

  @override
  Future<RoutingDetails> refreshRouting({bool reset = false}) =>
      routingDetails();
  @override
  Future<RouteCheckResult> checkRoute(String domain) => _later(
    RouteCheckResult(
      domain: domain,
      action: 'proxy',
      rule: 'profile proxy geosite:github',
      addresses: const ['140.82.121.4'],
      paths: const ['exwg'],
    ),
  );
  @override
  Future<bool> setDiagnosticsEnabled(bool enabled) {
    diagnosticsEnabled = enabled;
    return _later(diagnosticsEnabled);
  }

  @override
  Future<String> diagnosticsPreview() => _later(_debugText);
  @override
  Future<String> runDiagnostics() => _later('$_debugText\nprobe.result=ok');
  @override
  Future<String> fullDiagnostics() => _later(_debugText);
  @override
  Future<String> saveDiagnostics(String path) => _later(path);
  @override
  Future<UpdateInfo> checkForUpdates() async {
    final info =
        previewUpdate ??
        UpdateInfo(
          available: false,
          currentVersion: _state.version,
          latestVersion: _state.version,
          releaseNotes: '',
          assetName: '',
          downloadUrl: '',
          releasePageUrl: '',
          sha256: '',
          publishedAt: '',
        );
    _state = UiState(
      profiles: _state.profiles,
      selected: _state.selected,
      connected: _state.connected,
      busy: _state.busy,
      status: _state.status,
      version: _state.version,
      rxBytes: _state.rxBytes,
      txBytes: _state.txBytes,
      downloadBps: _state.downloadBps,
      uploadBps: _state.uploadBps,
      updateAvailable: info.available,
      latestVersion: info.latestVersion,
    );
    return _later(info);
  }

  @override
  Future<bool> installUpdate() async {
    updateInstalled = true;
    return _later(true);
  }

  @override
  Future<void> dispose() async {}
}

const _debugText = '''[Ex WG Diagnostics]
shell=Flutter 0.0.0
core=Go/AWG official
os=Windows 11 amd64
interface=Ex WG index=27 mtu=1420 flags=up|running
route.direct=gov.ru, *.ru, *.рф
route.proxy=default IPv4/IPv6
dns.direct=system
dns.proxy=DoH HTTP/2
last.win32=0
last.wsa=0
status=ready''';
