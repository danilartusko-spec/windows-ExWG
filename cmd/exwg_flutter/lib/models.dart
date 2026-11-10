/// Release version injected by build-flutter-windows.ps1 from /VERSION.
/// Empty in tests and `flutter run`; the core reports the real version.
const String kAppVersion = String.fromEnvironment('EXWG_VERSION');

class UiProfile {
  const UiProfile({
    required this.id,
    required this.name,
    required this.generation,
    required this.routing,
    this.preparing = false,
    this.routingError = '',
  });

  final String id;
  final String name;
  final String generation;
  final String routing;
  final bool preparing;
  final String routingError;

  factory UiProfile.fromJson(Map<String, dynamic> json) => UiProfile(
    id: json['id'] as String? ?? '',
    name: json['name'] as String? ?? 'Без названия',
    generation: json['generation'] as String? ?? 'AmneziaWG',
    routing: json['routing'] as String? ?? 'https://routing.help/',
    preparing: json['preparing'] as bool? ?? false,
    routingError: json['routingError'] as String? ?? '',
  );
}

class UiState {
  const UiState({
    required this.profiles,
    required this.selected,
    required this.connected,
    required this.busy,
    required this.status,
    required this.version,
    required this.rxBytes,
    required this.txBytes,
    required this.downloadBps,
    required this.uploadBps,
    this.updateAvailable = false,
    this.latestVersion = '',
  });

  const UiState.empty()
    : profiles = const [],
      selected = '',
      connected = false,
      busy = false,
      status = 'Запуск ядра…',
      version = kAppVersion,
      rxBytes = 0,
      txBytes = 0,
      downloadBps = 0,
      uploadBps = 0,
      updateAvailable = false,
      latestVersion = '';

  final List<UiProfile> profiles;
  final String selected;
  final bool connected;
  final bool busy;
  final String status;
  final String version;
  final int rxBytes;
  final int txBytes;
  final int downloadBps;
  final int uploadBps;
  final bool updateAvailable;
  final String latestVersion;

  UiProfile? get selectedProfile {
    for (final profile in profiles) {
      if (profile.id == selected) return profile;
    }
    return profiles.isEmpty ? null : profiles.first;
  }

  UiState copyWith({
    List<UiProfile>? profiles,
    String? selected,
    bool? connected,
    bool? busy,
    String? status,
    bool? updateAvailable,
    String? latestVersion,
  }) => UiState(
    profiles: profiles ?? this.profiles,
    selected: selected ?? this.selected,
    connected: connected ?? this.connected,
    busy: busy ?? this.busy,
    status: status ?? this.status,
    version: version,
    rxBytes: rxBytes,
    txBytes: txBytes,
    downloadBps: downloadBps,
    uploadBps: uploadBps,
    updateAvailable: updateAvailable ?? this.updateAvailable,
    latestVersion: latestVersion ?? this.latestVersion,
  );

  factory UiState.fromJson(Map<String, dynamic> json) => UiState(
    profiles: (json['profiles'] as List<dynamic>? ?? const [])
        .whereType<Map<String, dynamic>>()
        .map(UiProfile.fromJson)
        .toList(growable: false),
    selected: json['selected'] as String? ?? '',
    connected: json['connected'] as bool? ?? false,
    busy: json['busy'] as bool? ?? false,
    status: json['status'] as String? ?? 'Не подключено',
    version: json['version'] as String? ?? kAppVersion,
    rxBytes: (json['rxBytes'] as num?)?.toInt() ?? 0,
    txBytes: (json['txBytes'] as num?)?.toInt() ?? 0,
    downloadBps: (json['downloadBps'] as num?)?.toInt() ?? 0,
    uploadBps: (json['uploadBps'] as num?)?.toInt() ?? 0,
    updateAvailable: json['updateAvailable'] as bool? ?? false,
    latestVersion: json['latestVersion'] as String? ?? '',
  );
}

class UpdateInfo {
  const UpdateInfo({
    required this.available,
    required this.currentVersion,
    required this.latestVersion,
    required this.releaseNotes,
    required this.assetName,
    required this.downloadUrl,
    required this.releasePageUrl,
    required this.sha256,
    required this.publishedAt,
  });

  const UpdateInfo.empty()
    : available = false,
      currentVersion = '',
      latestVersion = '',
      releaseNotes = '',
      assetName = '',
      downloadUrl = '',
      releasePageUrl = '',
      sha256 = '',
      publishedAt = '';

  final bool available;
  final String currentVersion;
  final String latestVersion;
  final String releaseNotes;
  final String assetName;
  final String downloadUrl;
  final String releasePageUrl;
  final String sha256;
  final String publishedAt;

  factory UpdateInfo.fromJson(Map<String, dynamic> json) => UpdateInfo(
    available: json['available'] as bool? ?? false,
    currentVersion: json['currentVersion'] as String? ?? '',
    latestVersion: json['latestVersion'] as String? ?? '',
    releaseNotes: json['releaseNotes'] as String? ?? '',
    assetName: json['assetName'] as String? ?? '',
    downloadUrl: json['downloadUrl'] as String? ?? '',
    releasePageUrl: json['releasePageUrl'] as String? ?? '',
    sha256: json['sha256'] as String? ?? '',
    publishedAt: json['publishedAt'] as String? ?? '',
  );
}

class RouteCheckResult {
  const RouteCheckResult({
    required this.domain,
    required this.action,
    required this.rule,
    required this.addresses,
    required this.paths,
  });

  final String domain;
  final String action;
  final String rule;
  final List<String> addresses;
  final List<String> paths;

  factory RouteCheckResult.fromJson(Map<String, dynamic> json) =>
      RouteCheckResult(
        domain: json['domain'] as String? ?? '',
        action: json['action'] as String? ?? '',
        rule: json['rule'] as String? ?? '',
        addresses: (json['addresses'] as List<dynamic>? ?? const [])
            .whereType<String>()
            .toList(growable: false),
        paths: (json['paths'] as List<dynamic>? ?? const [])
            .whereType<String>()
            .toList(growable: false),
      );
}

class ProfileEditor {
  const ProfileEditor({
    this.id = '',
    this.name = '',
    this.config = '',
    this.routing = 'https://routing.help/',
    this.overrides = '',
  });

  final String id;
  final String name;
  final String config;
  final String routing;
  final String overrides;

  factory ProfileEditor.fromJson(Map<String, dynamic> json) => ProfileEditor(
    id: json['id'] as String? ?? '',
    name: json['name'] as String? ?? '',
    config: json['config'] as String? ?? '',
    routing: json['routing'] as String? ?? 'https://routing.help/',
    overrides: json['overrides'] as String? ?? '',
  );

  Map<String, dynamic> toJson() => {
    'id': id,
    'name': name,
    'config': config,
    'routing': routing,
    'overrides': overrides,
  };
}

class RoutingDetails {
  const RoutingDetails({
    required this.source,
    required this.overrides,
    required this.summary,
    required this.rawProfile,
    required this.updatedAt,
    required this.profileName,
    required this.profileLastUpdated,
    required this.routeOrder,
    required this.domainStrategy,
    required this.globalProxy,
    required this.fakeDNS,
    required this.useChunkFiles,
    required this.remoteDNS,
    required this.remoteDNSType,
    required this.remoteDNSDomain,
    required this.remoteDNSIP,
    required this.domesticDNS,
    required this.domesticDNSType,
    required this.domesticDNSDomain,
    required this.domesticDNSIP,
    required this.geoSiteURL,
    required this.geoSiteBytes,
    required this.geoIPURL,
    required this.geoIPBytes,
    required this.directSites,
    required this.directIP,
    required this.proxySites,
    required this.proxyIP,
    required this.blockSites,
    required this.blockIP,
    required this.dnsHosts,
    required this.warnings,
    required this.additionalFieldNames,
  });

  const RoutingDetails.empty()
    : source = 'https://routing.help/',
      overrides = '',
      summary = 'База правил загружается в фоне · локальные правила уже активны',
      rawProfile = '',
      updatedAt = '',
      profileName = '',
      profileLastUpdated = '',
      routeOrder = '',
      domainStrategy = '',
      globalProxy = false,
      fakeDNS = false,
      useChunkFiles = false,
      remoteDNS = '',
      remoteDNSType = '',
      remoteDNSDomain = '',
      remoteDNSIP = '',
      domesticDNS = '',
      domesticDNSType = '',
      domesticDNSDomain = '',
      domesticDNSIP = '',
      geoSiteURL = '',
      geoSiteBytes = 0,
      geoIPURL = '',
      geoIPBytes = 0,
      directSites = const [],
      directIP = const [],
      proxySites = const [],
      proxyIP = const [],
      blockSites = const [],
      blockIP = const [],
      dnsHosts = const {},
      warnings = const [],
      additionalFieldNames = const [];

  final String source;
  final String overrides;
  final String summary;
  final String rawProfile;
  final String updatedAt;
  final String profileName;
  final String profileLastUpdated;
  final String routeOrder;
  final String domainStrategy;
  final bool globalProxy;
  final bool fakeDNS;
  final bool useChunkFiles;
  final String remoteDNS;
  final String remoteDNSType;
  final String remoteDNSDomain;
  final String remoteDNSIP;
  final String domesticDNS;
  final String domesticDNSType;
  final String domesticDNSDomain;
  final String domesticDNSIP;
  final String geoSiteURL;
  final int geoSiteBytes;
  final String geoIPURL;
  final int geoIPBytes;
  final List<String> directSites;
  final List<String> directIP;
  final List<String> proxySites;
  final List<String> proxyIP;
  final List<String> blockSites;
  final List<String> blockIP;
  final Map<String, String> dnsHosts;
  final List<String> warnings;
  final List<String> additionalFieldNames;

  factory RoutingDetails.fromJson(Map<String, dynamic> json) => RoutingDetails(
    source: json['source'] as String? ?? 'https://routing.help/',
    overrides: json['overrides'] as String? ?? '',
    summary: json['summary'] as String? ?? '',
    rawProfile: json['rawProfile'] as String? ?? '',
    updatedAt: json['updatedAt'] as String? ?? '',
    profileName: json['profileName'] as String? ?? '',
    profileLastUpdated: json['profileLastUpdated'] as String? ?? '',
    routeOrder: json['routeOrder'] as String? ?? '',
    domainStrategy: json['domainStrategy'] as String? ?? '',
    globalProxy: json['globalProxy'] as bool? ?? false,
    fakeDNS: json['fakeDNS'] as bool? ?? false,
    useChunkFiles: json['useChunkFiles'] as bool? ?? false,
    remoteDNS: json['remoteDNS'] as String? ?? '',
    remoteDNSType: json['remoteDNSType'] as String? ?? '',
    remoteDNSDomain: json['remoteDNSDomain'] as String? ?? '',
    remoteDNSIP: json['remoteDNSIP'] as String? ?? '',
    domesticDNS: json['domesticDNS'] as String? ?? '',
    domesticDNSType: json['domesticDNSType'] as String? ?? '',
    domesticDNSDomain: json['domesticDNSDomain'] as String? ?? '',
    domesticDNSIP: json['domesticDNSIP'] as String? ?? '',
    geoSiteURL: json['geoSiteURL'] as String? ?? '',
    geoSiteBytes: (json['geoSiteBytes'] as num?)?.toInt() ?? 0,
    geoIPURL: json['geoIPURL'] as String? ?? '',
    geoIPBytes: (json['geoIPBytes'] as num?)?.toInt() ?? 0,
    directSites: _strings(json['directSites']),
    directIP: _strings(json['directIP']),
    proxySites: _strings(json['proxySites']),
    proxyIP: _strings(json['proxyIP']),
    blockSites: _strings(json['blockSites']),
    blockIP: _strings(json['blockIP']),
    dnsHosts: _stringMap(json['dnsHosts']),
    warnings: _strings(json['warnings']),
    additionalFieldNames: _strings(json['additionalFieldNames']),
  );

  static List<String> _strings(dynamic value) => value is List
      ? value.whereType<String>().toList(growable: false)
      : const [];

  static Map<String, String> _stringMap(dynamic value) => value is Map
      ? value.map((key, item) => MapEntry('$key', '$item'))
      : const {};
}
