import 'dart:async';
import 'dart:io';
import 'dart:math' as math;

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import 'core_client.dart';
import 'models.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(
    ExWGApp(
      backend: Platform.isWindows ? CoreProcessClient() : PreviewBackend(),
    ),
  );
}

enum AppLanguage { ru, en }

extension on AppLanguage {
  String get code => this == AppLanguage.ru ? 'ru' : 'en';
}

class AppLanguageScope extends InheritedWidget {
  const AppLanguageScope({
    super.key,
    required this.language,
    required this.onChanged,
    required super.child,
  });

  final AppLanguage language;
  final ValueChanged<AppLanguage> onChanged;

  static AppLanguageScope of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<AppLanguageScope>()!;

  @override
  bool updateShouldNotify(AppLanguageScope oldWidget) =>
      language != oldWidget.language;
}

String tr(BuildContext context, String russian, String english) =>
    AppLanguageScope.of(context).language == AppLanguage.en ? english : russian;

class _LanguagePreference {
  static File? get _file {
    if (!Platform.isWindows) return null;
    final localAppData = Platform.environment['LOCALAPPDATA'];
    if (localAppData == null || localAppData.isEmpty) return null;
    return File(
      '$localAppData${Platform.pathSeparator}NexusClient${Platform.pathSeparator}UI${Platform.pathSeparator}language.txt',
    );
  }

  static Future<AppLanguage> load() async {
    try {
      final value = (await _file?.readAsString())?.trim().toLowerCase();
      return value == 'en' ? AppLanguage.en : AppLanguage.ru;
    } catch (_) {
      return AppLanguage.ru;
    }
  }

  static Future<void> save(AppLanguage language) async {
    try {
      final file = _file;
      if (file == null) return;
      await file.parent.create(recursive: true);
      await file.writeAsString(language.code, flush: true);
    } catch (_) {
      // A read-only profile must not prevent the VPN UI from working.
    }
  }
}

class ExWGApp extends StatefulWidget {
  const ExWGApp({
    super.key,
    required this.backend,
    this.initialLanguage,
    this.persistLanguage = true,
  });
  final ExWGBackend backend;
  final AppLanguage? initialLanguage;
  final bool persistLanguage;

  @override
  State<ExWGApp> createState() => _ExWGAppState();
}

class _ExWGAppState extends State<ExWGApp> {
  late AppLanguage _language;

  @override
  void initState() {
    super.initState();
    _language = widget.initialLanguage ?? AppLanguage.ru;
    if (widget.initialLanguage == null) unawaited(_loadLanguage());
  }

  Future<void> _loadLanguage() async {
    final language = await _LanguagePreference.load();
    if (mounted) setState(() => _language = language);
  }

  void _setLanguage(AppLanguage language) {
    if (_language == language) return;
    setState(() => _language = language);
    if (widget.persistLanguage) unawaited(_LanguagePreference.save(language));
  }

  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'Ex WG',
    debugShowCheckedModeBanner: false,
    locale: Locale(_language.code),
    supportedLocales: const [Locale('ru'), Locale('en')],
    localizationsDelegates: GlobalMaterialLocalizations.delegates,
    theme: ThemeData(
      brightness: Brightness.light,
      scaffoldBackgroundColor: const Color(0xffdfd8cc),
      colorScheme: const ColorScheme.light(
        primary: Color(0xff14967b),
        onPrimary: Colors.white,
        secondary: Color(0xff5c5952),
        tertiary: Color(0xff7a766e),
        surface: Color(0xfff8f6f1),
        onSurface: Color(0xff17181a),
        surfaceContainerHighest: Color(0xffeae4d9),
        onSurfaceVariant: Color(0xff5c5952),
        outline: Color(0xffd8d0c2),
        outlineVariant: Color(0xffe2dcd0),
        error: Color(0xffd3455b),
      ),
      fontFamily: 'ExWGSans',
      textTheme: const TextTheme(
        headlineLarge: TextStyle(
          fontSize: 28,
          height: 1.1,
          fontWeight: FontWeight.w600,
          letterSpacing: -0.5,
          color: Color(0xff17181a),
        ),
        headlineSmall: TextStyle(
          fontSize: 20,
          fontWeight: FontWeight.w600,
          color: Color(0xff17181a),
        ),
        titleMedium: TextStyle(
          fontSize: 15,
          fontWeight: FontWeight.w600,
          color: Color(0xff17181a),
        ),
        bodyMedium: TextStyle(
          fontSize: 14,
          height: 1.45,
          color: Color(0xff17181a),
        ),
        bodySmall: TextStyle(
          fontSize: 12,
          height: 1.4,
          color: Color(0xff5c5952),
        ),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          backgroundColor: const Color(0xff14967b),
          foregroundColor: Colors.white,
          disabledBackgroundColor: const Color(0x3314967b),
          disabledForegroundColor: const Color(0x80ffffff),
          minimumSize: const Size(0, 42),
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
          textStyle: const TextStyle(
            fontFamily: 'ExWGSans',
            fontSize: 13.5,
            fontWeight: FontWeight.w600,
            letterSpacing: 0.1,
          ),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(12),
          ),
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: ButtonStyle(
          minimumSize: const WidgetStatePropertyAll(Size(0, 42)),
          padding: const WidgetStatePropertyAll(
            EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          ),
          textStyle: const WidgetStatePropertyAll(
            TextStyle(
              fontFamily: 'ExWGSans',
              fontSize: 13.5,
              fontWeight: FontWeight.w600,
            ),
          ),
          foregroundColor: WidgetStateProperty.resolveWith(
            (states) => states.contains(WidgetState.disabled)
                ? const Color(0xff8a857c)
                : const Color(0xff17181a),
          ),
          backgroundColor: WidgetStateProperty.resolveWith(
            (states) => states.contains(WidgetState.hovered)
                ? Colors.white
                : const Color(0xd9ffffff),
          ),
          side: WidgetStateBorderSide.resolveWith(
            (states) => BorderSide(
              color: states.contains(WidgetState.disabled)
                  ? const Color(0x40d8d0c2)
                  : states.contains(WidgetState.focused) ||
                        states.contains(WidgetState.hovered)
                  ? const Color(0xff14967b)
                  : Colors.white,
              width: 1.0,
            ),
          ),
          shape: WidgetStatePropertyAll(
            RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
          ),
        ),
      ),
      textButtonTheme: TextButtonThemeData(
        style: TextButton.styleFrom(
          foregroundColor: const Color(0xff5c5952),
          disabledForegroundColor: const Color(0xff8a857c),
          minimumSize: const Size(0, 42),
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          textStyle: const TextStyle(
            fontFamily: 'ExWGSans',
            fontSize: 13.5,
            fontWeight: FontWeight.w500,
          ),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(10),
          ),
        ),
      ),
      iconButtonTheme: IconButtonThemeData(
        style: IconButton.styleFrom(
          foregroundColor: const Color(0xff5c5952),
          disabledForegroundColor: const Color(0xff8a857c),
          hoverColor: const Color(0x14000000),
          highlightColor: const Color(0x1f000000),
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
        ),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: const Color(0xe6ffffff),
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 16,
          vertical: 14,
        ),
        labelStyle: const TextStyle(color: Color(0xff6b675e)),
        hintStyle: const TextStyle(color: Color(0xff8a857c)),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: const BorderSide(color: Color(0xffded8cc)),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: const BorderSide(color: Color(0xff14967b), width: 1.5),
        ),
      ),
      snackBarTheme: SnackBarThemeData(
        backgroundColor: const Color(0xff17181a),
        contentTextStyle: const TextStyle(color: Colors.white),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        behavior: SnackBarBehavior.floating,
      ),
    ),
    builder: (context, child) => AppLanguageScope(
      language: _language,
      onChanged: _setLanguage,
      child: child!,
    ),
    home: ExWGShell(backend: widget.backend),
  );
}

enum AppPage { home, rules, diagnostics }

class ExWGShell extends StatefulWidget {
  const ExWGShell({super.key, required this.backend});
  final ExWGBackend backend;

  @override
  State<ExWGShell> createState() => _ExWGShellState();
}

class _ExWGShellState extends State<ExWGShell> {
  UiState _state = const UiState.empty();
  RoutingDetails _routing = const RoutingDetails.empty();
  String _diagnostics = '';
  AppPage _page = AppPage.home;
  Timer? _stateTimer;
  Timer? _debugTimer;
  bool _ready = false;
  bool _working = false;
  bool _diagnosticsUnlocked = false;
  int _versionTapCount = 0;
  DateTime? _lastVersionTapAt;
  String? _startupError;

  @override
  void initState() {
    super.initState();
    unawaited(_start());
  }

  Future<void> _start() async {
    try {
      await widget.backend.start();
      final state = await widget.backend.state();
      if (!mounted) return;
      setState(() {
        _state = state;
        _ready = true;
      });
      unawaited(_pollRouting());
      _stateTimer = Timer.periodic(
        const Duration(seconds: 1),
        (_) => unawaited(_pollState()),
      );
    } catch (error) {
      if (mounted) setState(() => _startupError = _message(error));
    }
  }

  Future<void> _pollRouting() async {
    try {
      final details = await widget.backend.routingDetails();
      if (mounted) setState(() => _routing = details);
    } catch (_) {}
  }

  Future<void> _pollState() async {
    if (!_ready || _working) return;
    try {
      final state = await widget.backend.state();
      if (mounted) setState(() => _state = state);
      if (_page == AppPage.rules && _routing.profileName.isEmpty) {
        await _pollRouting();
      }
    } catch (_) {}
  }

  Future<T?> _run<T>(
    Future<T> Function() action, {
    String? success,
    bool quiet = false,
  }) async {
    if (_working) return null;
    setState(() => _working = true);
    try {
      final result = await action();
      if (mounted && success != null) _notice(success);
      return result;
    } catch (error) {
      if (mounted && !quiet) _notice(_message(error), error: true);
      return null;
    } finally {
      if (mounted) setState(() => _working = false);
    }
  }

  String _message(Object error) {
    final text = error.toString();
    return text.startsWith('Bad state: ') ? text.substring(11) : text;
  }

  void _notice(String message, {bool error = false}) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(
        SnackBar(
          content: Row(
            children: [
              Icon(
                error
                    ? Icons.error_outline_rounded
                    : Icons.check_circle_outline,
                color: error
                    ? const Color(0xffff7b7b)
                    : const Color(0xfff4f5f5),
              ),
              const SizedBox(width: 10),
              Expanded(child: Text(message)),
            ],
          ),
        ),
      );
  }

  Future<void> _changePage(AppPage page) async {
    setState(() => _page = page);
    _debugTimer?.cancel();
    if (page == AppPage.rules) {
      await _pollRouting();
    }
    if (page == AppPage.diagnostics) {
      await _refreshDebug(preview: true);
      if (_page == AppPage.diagnostics) {
        _debugTimer = Timer.periodic(
          const Duration(seconds: 3),
          (_) => unawaited(_refreshDebug(preview: true)),
        );
      }
    }
  }

  Future<void> _onVersionTap() async {
    final now = DateTime.now();
    if (_lastVersionTapAt == null ||
        now.difference(_lastVersionTapAt!) >
            const Duration(milliseconds: 2500)) {
      _versionTapCount = 1;
    } else {
      _versionTapCount += 1;
    }
    _lastVersionTapAt = now;
    if (_versionTapCount < 5) return;
    _versionTapCount = 0;
    if (!_diagnosticsUnlocked) {
      setState(() => _diagnosticsUnlocked = true);
      try {
        await widget.backend.setDiagnosticsEnabled(true);
      } catch (_) {}
      if (!mounted) return;
      await _changePage(AppPage.diagnostics);
    } else {
      await _hideDiagnostics();
    }
  }

  Future<void> _hideDiagnostics() async {
    _versionTapCount = 0;
    _debugTimer?.cancel();
    setState(() {
      _diagnosticsUnlocked = false;
      if (_page == AppPage.diagnostics) {
        _page = AppPage.home;
      }
    });
    try {
      await widget.backend.setDiagnosticsEnabled(false);
    } catch (_) {}
  }

  Future<void> _refreshDebug({required bool preview}) async {
    if (preview) {
      try {
        final result = await widget.backend.diagnosticsPreview();
        if (mounted) setState(() => _diagnostics = result);
      } catch (_) {}
      return;
    }
    final result = await _run(widget.backend.runDiagnostics);
    if (mounted && result != null) setState(() => _diagnostics = result);
  }

  Future<void> _toggle() async {
    final result = await _run(widget.backend.toggleConnection);
    if (result != null && mounted) setState(() => _state = result);
  }

  Future<void> _selectProfile(String id) async {
    if (_working || id == _state.selected) return;
    if (!_state.connected) {
      setState(() => _state = _state.copyWith(selected: id));
      try {
        final result = await widget.backend.selectProfile(id);
        if (mounted) setState(() => _state = result);
      } catch (error) {
        if (mounted) _notice(_message(error), error: true);
      }
      return;
    }
    setState(
      () => _state = _state.copyWith(
        selected: id,
        status: tr(context, 'Переключение…', 'Switching…'),
      ),
    );
    final result = await _run(() => widget.backend.selectProfile(id));
    if (result != null && mounted) setState(() => _state = result);
  }

  Future<void> _editProfile(String id) async {
    final profile = await _run(() => widget.backend.getProfile(id));
    if (profile == null) return;
    if (!mounted) return;
    final result = await showDialog<ProfileEditor>(
      context: context,
      barrierDismissible: false,
      builder: (_) => ProfileDialog(profile: profile),
    );
    if (result == null) return;
    if (!mounted) return;
    final state = await _run(
      () => widget.backend.saveProfile(result),
      success: tr(context, 'Профиль сохранён', 'Profile saved'),
    );
    if (state != null && mounted) setState(() => _state = state);
  }

  Future<void> _deleteProfile(UiProfile profile) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        backgroundColor: const Color(0xfff7f5f0),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(20),
          side: const BorderSide(color: Color(0xe6ffffff), width: 1.0),
        ),
        title: Text(
          tr(context, 'Удалить «${profile.name}»?', 'Delete “${profile.name}”?'),
          style: const TextStyle(
            color: Color(0xff17181a),
            fontWeight: FontWeight.w600,
          ),
        ),
        content: Text(
          tr(
            context,
            'Профиль будет удалён с этого компьютера без возможности восстановления.',
            'This profile will be permanently removed from this computer.',
          ),
          style: const TextStyle(color: Color(0xff5c5952), height: 1.45),
        ),
        actions: [
          _PressableScale(
            enabled: true,
            child: TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: Text(tr(context, 'Отмена', 'Cancel')),
            ),
          ),
          _PressableScale(
            enabled: true,
            child: FilledButton(
              onPressed: () => Navigator.pop(context, true),
              style: FilledButton.styleFrom(
                backgroundColor: const Color(0xffd3455b),
                foregroundColor: Colors.white,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
              ),
              child: Text(tr(context, 'Удалить профиль', 'Delete profile')),
            ),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    if (!mounted) return;
    final result = await _run(
      () => widget.backend.deleteProfile(profile.id),
      success: tr(context, 'Профиль удалён', 'Profile deleted'),
    );
    if (result != null && mounted) setState(() => _state = result);
  }

  Future<void> _importProfiles({required bool qrImage}) async {
    final group = XTypeGroup(
      label: qrImage
          ? tr(context, 'QR-код Ex WG', 'Ex WG QR code')
          : tr(context, 'Профили Ex WG', 'Ex WG profiles'),
      extensions: qrImage
          ? const ['png', 'jpg', 'jpeg', 'gif']
          : const ['conf', 'zip'],
    );
    final files = await openFiles(acceptedTypeGroups: [group]);
    if (files.isEmpty) return;
    if (!mounted) return;
    final state = await _run(
      () => widget.backend.importProfiles(
        files.map((file) => file.path).toList(),
      ),
      success: tr(context, 'Импорт завершён', 'Import completed'),
    );
    if (state != null && mounted) setState(() => _state = state);
  }

  Future<void> _saveRouting(String source, String overrides) async {
    final result = await _run(
      () => widget.backend.saveRouting(source, overrides),
      success: tr(context, 'Правила сохранены', 'Rules saved'),
    );
    if (result != null && mounted) setState(() => _routing = result);
  }

  Future<void> _refreshRouting({bool reset = false}) async {
    final result = await _run(
      () => widget.backend.refreshRouting(reset: reset),
      success: reset
          ? tr(context, 'Правила восстановлены', 'Rules restored')
          : tr(context, 'Правила обновлены', 'Rules updated'),
    );
    if (result != null && mounted) setState(() => _routing = result);
  }

  Future<RouteCheckResult?> _checkRoute(String domain) =>
      _run(() => widget.backend.checkRoute(domain));

  Future<void> _saveDebug() async {
    final location = await getSaveLocation(
      suggestedName:
          'ExWG-Debug-${DateTime.now().toIso8601String().replaceAll(':', '-')}.txt',
      acceptedTypeGroups: const [
        XTypeGroup(label: 'Text', extensions: ['txt']),
      ],
    );
    if (location == null) return;
    if (!mounted) return;
    final path = await _run(
      () => widget.backend.saveDiagnostics(location.path),
    );
    if (path != null && mounted) {
      _notice(tr(context, 'TXT сохранён: $path', 'TXT saved: $path'));
    }
  }

  Future<void> _checkUpdates({bool silentIfUpToDate = false}) async {
    final info = await _run(
      widget.backend.checkForUpdates,
      quiet: silentIfUpToDate,
    );
    if (!mounted || info == null) return;
    if (info.available) {
      setState(() {
        _state = _state.copyWith(
          updateAvailable: true,
          latestVersion: info.latestVersion,
        );
      });
      await _showUpdateDialog(info);
    } else if (!silentIfUpToDate) {
      _notice(
        tr(
          context,
          'Установлена актуальная версия (${_state.version.isEmpty ? kAppVersion : _state.version})',
          'You have the latest version (${_state.version.isEmpty ? kAppVersion : _state.version})',
        ),
      );
    }
  }

  Future<void> _showUpdateDialog(UpdateInfo info) async {
    final installed = await showDialog<bool>(
      context: context,
      builder: (_) => UpdateDialog(
        info: info,
        onInstall: widget.backend.installUpdate,
      ),
    );
    if (installed == true && mounted) {
      _notice(
        tr(
          context,
          'Запуск установщика обновления…',
          'Launching update installer…',
        ),
      );
    }
  }

  @override
  void dispose() {
    _stateTimer?.cancel();
    _debugTimer?.cancel();
    unawaited(widget.backend.dispose());
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    body: Stack(
      fit: StackFit.expand,
      children: [
        const _Atmosphere(),
        if (_startupError != null)
          _StartupError(text: _startupError!)
        else
          Row(
            children: [
              Sidebar(
                key: const ValueKey('sidebar'),
                page: _page,
                onPage: _changePage,
                version: _state.version,
                updateAvailable: _state.updateAvailable,
                latestVersion: _state.latestVersion,
                showDiagnostics: _diagnosticsUnlocked,
                onVersionTap: _onVersionTap,
                onUpdateTap: () => _checkUpdates(silentIfUpToDate: false),
              ),
              Expanded(
                child: Stack(
                  children: [
                    IndexedStack(
                      index: _page.index,
                      children: [
                        HomePage(
                          state: _state,
                          disabled: !_ready || _working || _state.busy,
                          onToggle: _toggle,
                          onSelect: _selectProfile,
                          onImportConfigs: () =>
                              _importProfiles(qrImage: false),
                          onImportQr: () => _importProfiles(qrImage: true),
                          onEdit: (profile) => _editProfile(profile.id),
                          onDelete: _deleteProfile,
                        ),
                        RulesPage(
                          details: _routing,
                          disabled: !_ready || _working || _state.busy,
                          sourceLocked: _state.connected,
                          onSave: _saveRouting,
                          onRefresh: () => _refreshRouting(),
                          onReset: () => _refreshRouting(reset: true),
                          onCheckRoute: _checkRoute,
                          connected: _state.connected,
                        ),
                        DiagnosticsPage(
                          text: _diagnostics,
                          disabled: !_ready || _working,
                          onCheck: () => _refreshDebug(preview: false),
                          onCopy: () async {
                            final notice = tr(
                              context,
                              'Диагностика скопирована',
                              'Diagnostics copied',
                            );
                            await Clipboard.setData(
                              ClipboardData(text: _diagnostics),
                            );
                            if (mounted) {
                              _notice(notice);
                            }
                          },
                          onSave: _saveDebug,
                          onHide: _hideDiagnostics,
                        ),
                      ],
                    ),
                    Positioned(
                      right: 28,
                      top: 24,
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          if (_working || _state.busy) ...[
                            const SizedBox.square(
                              dimension: 20,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            ),
                            const SizedBox(width: 12),
                          ],
                          const _LanguageToggle(),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
      ],
    ),
  );
}

class _Atmosphere extends StatelessWidget {
  const _Atmosphere();
  @override
  Widget build(BuildContext context) => IgnorePointer(
    child: DecoratedBox(
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [Color(0xffcfc4b4), Color(0xffdfd8cc), Color(0xffc2b5a3)],
          stops: [0.0, 0.52, 1.0],
        ),
      ),
      child: CustomPaint(painter: _GlowPainter()),
    ),
  );
}

class _GlowPainter extends CustomPainter {
  @override
  void paint(Canvas canvas, Size size) {
    void glow(Offset center, double radius, Color color) {
      final paint = Paint()
        ..shader = RadialGradient(
          colors: [color, color.withValues(alpha: 0)],
        ).createShader(Rect.fromCircle(center: center, radius: radius));
      canvas.drawCircle(center, radius, paint);
    }

    // Soft alabaster ambient glow
    glow(
      Offset(size.width * .45, size.height * .20),
      size.width * .70,
      const Color(0x8cfffdf9),
    );
    // Emerald ambient glow
    glow(
      Offset(size.width * .90, size.height * .85),
      size.width * .65,
      const Color(0x2114967b),
    );
    // Subtle warm stone depth
    glow(
      Offset(size.width * .20, size.height * .90),
      size.width * .50,
      const Color(0x145c4f3e),
    );
  }

  @override
  bool shouldRepaint(covariant CustomPainter oldDelegate) => false;
}

class Sidebar extends StatelessWidget {
  const Sidebar({
    super.key,
    required this.page,
    required this.onPage,
    required this.version,
    this.updateAvailable = false,
    this.latestVersion = '',
    this.showDiagnostics = false,
    this.onVersionTap,
    this.onUpdateTap,
  });
  final AppPage page;
  final ValueChanged<AppPage> onPage;
  final String version;
  final bool updateAvailable;
  final String latestVersion;
  final bool showDiagnostics;
  final VoidCallback? onVersionTap;
  final VoidCallback? onUpdateTap;

  String get _versionLabel {
    final trimmed = version.trim();
    if (trimmed.isEmpty) return kAppVersion.isEmpty ? '' : 'v$kAppVersion';
    return trimmed.startsWith('v') ? trimmed : 'v$trimmed';
  }

  String get _latestVersionLabel {
    final trimmed = latestVersion.trim();
    if (trimmed.isEmpty) return '';
    return trimmed.startsWith('v') ? trimmed : 'v$trimmed';
  }

  @override
  Widget build(BuildContext context) => Container(
    width: 224,
    decoration: const BoxDecoration(
      gradient: LinearGradient(
        begin: Alignment.topCenter,
        end: Alignment.bottomCenter,
        colors: [Color(0xD6F5F2EC), Color(0xC7EBE6DD)],
      ),
      border: Border(right: BorderSide(color: Color(0xC7FFFFFF), width: 1.0)),
    ),
    padding: const EdgeInsets.fromLTRB(20, 28, 20, 24),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const _Wordmark(),
        const SizedBox(height: 34),
        _NavItem(
          icon: Icons.home_outlined,
          label: tr(context, 'Главная', 'Home'),
          selected: page == AppPage.home,
          onTap: () => onPage(AppPage.home),
        ),
        const SizedBox(height: 8),
        _NavItem(
          icon: Icons.shield_outlined,
          label: tr(context, 'Правила', 'Rules'),
          selected: page == AppPage.rules,
          onTap: () => onPage(AppPage.rules),
        ),
        if (showDiagnostics) ...[
          const SizedBox(height: 8),
          _NavItem(
            icon: Icons.monitor_heart_outlined,
            label: tr(context, 'Диагностика', 'Diagnostics'),
            selected: page == AppPage.diagnostics,
            onTap: () => onPage(AppPage.diagnostics),
          ),
        ],
        const Spacer(),
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            MouseRegion(
              cursor: SystemMouseCursors.click,
              child: GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: onVersionTap,
                child: Padding(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 6,
                    vertical: 2,
                  ),
                  child: Text(
                    _versionLabel,
                    key: const ValueKey('sidebar-version'),
                    style: const TextStyle(
                      color: Color(0xff7a766e),
                      fontSize: 12,
                      fontWeight: FontWeight.w500,
                      letterSpacing: 0.2,
                      fontFeatures: [FontFeature.tabularFigures()],
                    ),
                  ),
                ),
              ),
            ),
            if (updateAvailable)
              Flexible(
                child: Tooltip(
                  message: tr(
                    context,
                    'Установить обновление $_latestVersionLabel',
                    'Install update $_latestVersionLabel',
                  ),
                  child: MouseRegion(
                    cursor: SystemMouseCursors.click,
                    child: GestureDetector(
                      key: const ValueKey('update-available-badge'),
                      behavior: HitTestBehavior.opaque,
                      onTap: onUpdateTap,
                      child: Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 8,
                          vertical: 3,
                        ),
                        decoration: BoxDecoration(
                          color: const Color(0xffdcf2eb),
                          border: Border.all(color: const Color(0x6614967b)),
                          borderRadius: BorderRadius.circular(999),
                        ),
                        child: Text(
                          tr(
                            context,
                            'Обновить · $_latestVersionLabel',
                            'Update · $_latestVersionLabel',
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: Color(0xff0f7b63),
                            fontSize: 11,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
              )
            else
              Tooltip(
                message: tr(
                  context,
                  'Проверить обновления',
                  'Check for updates',
                ),
                child: MouseRegion(
                  cursor: SystemMouseCursors.click,
                  child: GestureDetector(
                    key: const ValueKey('check-updates-button'),
                    behavior: HitTestBehavior.opaque,
                    onTap: onUpdateTap,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 8,
                        vertical: 3,
                      ),
                      decoration: BoxDecoration(
                        color: const Color(0xccffffff),
                        border: Border.all(color: Colors.white),
                        borderRadius: BorderRadius.circular(999),
                      ),
                      child: const Text(
                        'AWG 3.1',
                        style: TextStyle(
                          color: Color(0xff5c5952),
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  ),
                ),
              ),
          ],
        ),
      ],
    ),
  );
}

class _Wordmark extends StatelessWidget {
  const _Wordmark();
  @override
  Widget build(BuildContext context) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [
      Container(
        width: 32,
        height: 32,
        decoration: const BoxDecoration(
          color: Color(0xff17181a),
          shape: BoxShape.circle,
        ),
        child: const Center(
          child: SizedBox.square(
            dimension: 18,
            child: CustomPaint(painter: _ExWGMarkPainter()),
          ),
        ),
      ),
      const SizedBox(width: 10),
      const Text(
        'Ex WG',
        style: TextStyle(
          color: Color(0xff17181a),
          fontSize: 24,
          fontWeight: FontWeight.w600,
          letterSpacing: -0.5,
        ),
      ),
    ],
  );
}

class _PressableScale extends StatefulWidget {
  const _PressableScale({
    required this.enabled,
    required this.child,
    this.pressedScale = 0.97,
  });

  final bool enabled;
  final Widget child;
  final double pressedScale;

  @override
  State<_PressableScale> createState() => _PressableScaleState();
}

class _PressableScaleState extends State<_PressableScale> {
  bool _pressed = false;

  void _setPressed(bool value) {
    if (_pressed == value) return;
    setState(() => _pressed = value);
  }

  @override
  Widget build(BuildContext context) {
    final disableAnimations =
        MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final active = widget.enabled && !disableAnimations && _pressed;
    return Listener(
      behavior: HitTestBehavior.translucent,
      onPointerDown: widget.enabled ? (_) => _setPressed(true) : null,
      onPointerUp: (_) => _setPressed(false),
      onPointerCancel: (_) => _setPressed(false),
      child: AnimatedScale(
        scale: active ? widget.pressedScale : 1.0,
        duration: disableAnimations
            ? Duration.zero
            : const Duration(milliseconds: 140),
        curve: Curves.easeOutCubic,
        child: widget.child,
      ),
    );
  }
}

class _LanguageToggle extends StatelessWidget {
  const _LanguageToggle();

  @override
  Widget build(BuildContext context) {
    final scope = AppLanguageScope.of(context);
    return Semantics(
      label: tr(context, 'Язык интерфейса', 'Interface language'),
      child: Container(
        height: 30,
        padding: const EdgeInsets.all(2),
        decoration: BoxDecoration(
          color: const Color(0xccd8d1c5),
          border: Border.all(color: const Color(0xccffffff), width: 1.0),
          borderRadius: BorderRadius.circular(8),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            _LanguageSegment(
              label: 'RU',
              selected: scope.language == AppLanguage.ru,
              onTap: () => scope.onChanged(AppLanguage.ru),
            ),
            _LanguageSegment(
              label: 'EN',
              selected: scope.language == AppLanguage.en,
              onTap: () => scope.onChanged(AppLanguage.en),
            ),
          ],
        ),
      ),
    );
  }
}

class _LanguageSegment extends StatelessWidget {
  const _LanguageSegment({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    selected: selected,
    child: _PressableScale(
      enabled: true,
      pressedScale: 0.96,
      child: Material(
        color: selected ? Colors.white : Colors.transparent,
        borderRadius: BorderRadius.circular(6),
        elevation: selected ? 1 : 0,
        shadowColor: const Color(0x1f000000),
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(6),
          child: SizedBox(
            width: 34,
            height: 24,
            child: Center(
              child: Text(
                label,
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
                  color: selected
                      ? const Color(0xff17181a)
                      : const Color(0xff6b675e),
                ),
              ),
            ),
          ),
        ),
      ),
    ),
  );
}

class _ExWGMarkPainter extends CustomPainter {
  const _ExWGMarkPainter();

  @override
  void paint(Canvas canvas, Size size) {
    final path = Path()
      ..moveTo(size.width * .84, size.height * .14)
      ..lineTo(size.width * .36, size.height * .14)
      ..cubicTo(
        size.width * .18,
        size.height * .14,
        size.width * .13,
        size.height * .24,
        size.width * .13,
        size.height * .36,
      )
      ..cubicTo(
        size.width * .13,
        size.height * .47,
        size.width * .20,
        size.height * .51,
        size.width * .36,
        size.height * .51,
      )
      ..lineTo(size.width * .73, size.height * .51)
      ..moveTo(size.width * .73, size.height * .51)
      ..lineTo(size.width * .36, size.height * .51)
      ..cubicTo(
        size.width * .20,
        size.height * .51,
        size.width * .13,
        size.height * .57,
        size.width * .13,
        size.height * .69,
      )
      ..cubicTo(
        size.width * .13,
        size.height * .82,
        size.width * .20,
        size.height * .88,
        size.width * .36,
        size.height * .88,
      )
      ..lineTo(size.width * .84, size.height * .88);
    canvas.drawPath(
      path,
      Paint()
        ..color = const Color(0xfff7f5f0)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 2.0
        ..strokeCap = StrokeCap.round
        ..strokeJoin = StrokeJoin.round,
    );
  }

  @override
  bool shouldRepaint(covariant CustomPainter oldDelegate) => false;
}

class _NavItem extends StatelessWidget {
  const _NavItem({
    required this.icon,
    required this.label,
    required this.selected,
    required this.onTap,
  });
  final IconData icon;
  final String label;
  final bool selected;
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    selected: selected,
    child: _PressableScale(
      enabled: true,
      child: Container(
        height: 50,
        decoration: BoxDecoration(
          color: selected ? Colors.white : Colors.transparent,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(
            color: selected ? const Color(0xf2ffffff) : Colors.transparent,
            width: 1.0,
          ),
          boxShadow: selected
              ? const [
                  BoxShadow(
                    color: Color(0x17584c3c),
                    blurRadius: 16,
                    offset: Offset(0, 6),
                  ),
                ]
              : null,
        ),
        child: Material(
          color: Colors.transparent,
          borderRadius: BorderRadius.circular(14),
          child: InkWell(
            onTap: onTap,
            borderRadius: BorderRadius.circular(14),
            hoverColor: const Color(0x66ffffff),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12),
              child: Row(
                children: [
                  AnimatedContainer(
                    duration: const Duration(milliseconds: 180),
                    curve: Curves.easeOutCubic,
                    width: 3,
                    height: selected ? 20 : 0,
                    decoration: BoxDecoration(
                      color: const Color(0xff14967b),
                      borderRadius: BorderRadius.circular(99),
                    ),
                  ),
                  SizedBox(width: selected ? 10 : 2),
                  Icon(
                    icon,
                    size: 20,
                    color: selected
                        ? const Color(0xff17181a)
                        : const Color(0xff5c5952),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(
                      label,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: selected
                            ? const Color(0xff17181a)
                            : const Color(0xff5c5952),
                        fontSize: 14.5,
                        fontWeight:
                            selected ? FontWeight.w600 : FontWeight.w500,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    ),
  );
}

class PageFrame extends StatelessWidget {
  const PageFrame({
    super.key,
    required this.title,
    this.subtitle = '',
    required this.child,
  });
  final String title;
  final String subtitle;
  final Widget child;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(34, 30, 34, 28),
    child: Align(
      alignment: Alignment.topCenter,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 1060),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(title, style: Theme.of(context).textTheme.headlineLarge),
            if (subtitle.isNotEmpty) ...[
              const SizedBox(height: 7),
              Text(subtitle, style: Theme.of(context).textTheme.bodySmall),
            ],
            const SizedBox(height: 26),
            Expanded(child: child),
          ],
        ),
      ),
    ),
  );
}

class GlassCard extends StatelessWidget {
  const GlassCard({
    super.key,
    required this.child,
    this.padding = const EdgeInsets.all(20),
  });
  final Widget child;
  final EdgeInsets padding;
  @override
  Widget build(BuildContext context) => Container(
    padding: padding,
    decoration: BoxDecoration(
      gradient: const LinearGradient(
        begin: Alignment.topLeft,
        end: Alignment.bottomRight,
        colors: [Color(0xe0f8f6f1), Color(0xccf0ece5)],
      ),
      borderRadius: BorderRadius.circular(20),
      border: Border.all(color: const Color(0xd9ffffff), width: 1.0),
      boxShadow: const [
        BoxShadow(
          color: Color(0x145c4f3e),
          blurRadius: 26,
          offset: Offset(0, 10),
        ),
      ],
    ),
    child: child,
  );
}

class HomePage extends StatelessWidget {
  const HomePage({
    super.key,
    required this.state,
    required this.disabled,
    required this.onToggle,
    required this.onSelect,
    required this.onImportConfigs,
    required this.onImportQr,
    required this.onEdit,
    required this.onDelete,
  });
  final UiState state;
  final bool disabled;
  final VoidCallback onToggle;
  final ValueChanged<String> onSelect;
  final VoidCallback onImportConfigs;
  final VoidCallback onImportQr;
  final ValueChanged<UiProfile> onEdit;
  final ValueChanged<UiProfile> onDelete;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(28, 18, 28, 18),
      child: Align(
        alignment: Alignment.topCenter,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 1060),
          child: LayoutBuilder(
            builder: (context, constraints) {
              final profilesHeight = math.min(
                340.0,
                math.max(210.0, constraints.maxHeight - 330.0),
              );
              return Column(
                children: [
                  Expanded(
                    child: _ConnectionPanel(
                      profile: state.selectedProfile,
                      state: state,
                      disabled: disabled || state.selectedProfile == null,
                      onToggle: onToggle,
                    ),
                  ),
                  const Divider(
                    height: 1,
                    thickness: 1,
                    color: Color(0x265c4f3e),
                  ),
                  SizedBox(
                    key: const ValueKey('profiles-panel'),
                    height: profilesHeight,
                    child: _ProfilesPanel(
                      state: state,
                      selectDisabled: disabled,
                      manageDisabled: disabled || state.connected,
                      onSelect: onSelect,
                      onImportConfigs: onImportConfigs,
                      onImportQr: onImportQr,
                      onEdit: onEdit,
                      onDelete: onDelete,
                    ),
                  ),
                ],
              );
            },
          ),
        ),
      ),
    );
  }
}

class _ConnectionPanel extends StatelessWidget {
  const _ConnectionPanel({
    required this.profile,
    required this.state,
    required this.disabled,
    required this.onToggle,
  });
  final UiProfile? profile;
  final UiState state;
  final bool disabled;
  final VoidCallback onToggle;
  @override
  Widget build(BuildContext context) => Padding(
    key: const ValueKey('connection-card'),
    padding: const EdgeInsets.fromLTRB(0, 4, 0, 4),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          tr(context, 'Подключение', 'Connection'),
          style: Theme.of(context).textTheme.headlineLarge,
        ),
        Expanded(
          child: Center(
            child: FittedBox(
              fit: BoxFit.scaleDown,
              alignment: Alignment.center,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  ConnectionOrb(
                    connected: state.connected,
                    busy: state.busy,
                    enabled: !disabled,
                    onTap: onToggle,
                  ),
                  const SizedBox(height: 10),
                  Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 10,
                          vertical: 3.5,
                        ),
                        decoration: BoxDecoration(
                          color: state.connected
                              ? const Color(0xffdcf2eb)
                              : const Color(0xffe5dfd4),
                          border: Border.all(
                            color: state.connected
                                ? const Color(0xffb5e2d4)
                                : const Color(0xffd2c9ba),
                          ),
                          borderRadius: BorderRadius.circular(999),
                        ),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Container(
                              width: 7,
                              height: 7,
                              decoration: BoxDecoration(
                                color: state.connected
                                    ? const Color(0xff14967b)
                                    : const Color(0xff8a857c),
                                shape: BoxShape.circle,
                              ),
                            ),
                            const SizedBox(width: 6),
                            Text(
                              _connectionStatus(context, state),
                              key: ValueKey(
                                '${state.status}-${state.connected}-${state.busy}',
                              ),
                              style: TextStyle(
                                color: state.connected
                                    ? const Color(0xff0f7b63)
                                    : const Color(0xff5c5952),
                                fontSize: 11.5,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                          ],
                        ),
                      ),
                      const SizedBox(width: 8),
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 9,
                          vertical: 3.5,
                        ),
                        decoration: BoxDecoration(
                          color: const Color(0xe6ffffff),
                          border: Border.all(color: Colors.white),
                          borderRadius: BorderRadius.circular(999),
                          boxShadow: const [
                            BoxShadow(color: Color(0x0f5c4f3e), blurRadius: 6),
                          ],
                        ),
                        child: const Text(
                          'AWG',
                          style: TextStyle(
                            color: Color(0xff4a4843),
                            fontSize: 11,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 5),
                  Text(
                    profile?.name ??
                        tr(context, 'Профиль не выбран', 'No profile selected'),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      color: Color(0xff17181a),
                      fontSize: 17.5,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 6),
                  Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 4,
                    ),
                    decoration: BoxDecoration(
                      color: const Color(0xb3ffffff),
                      border: Border.all(color: const Color(0xe6ffffff)),
                      borderRadius: BorderRadius.circular(999),
                      boxShadow: const [
                        BoxShadow(color: Color(0x0f5c4f3e), blurRadius: 10),
                      ],
                    ),
                    child: _TrafficSummary(state: state),
                  ),
                ],
              ),
            ),
          ),
        ),
      ],
    ),
  );
}

class _TrafficSummary extends StatelessWidget {
  const _TrafficSummary({required this.state});

  final UiState state;

  @override
  Widget build(BuildContext context) => Text.rich(
    TextSpan(
      style: const TextStyle(
        color: Color(0xff5c5952),
        fontSize: 11.5,
        fontWeight: FontWeight.w500,
        fontFeatures: [FontFeature.tabularFigures()],
      ),
      children: [
        TextSpan(text: '${tr(context, 'Скорость', 'Speed')}  '),
        const WidgetSpan(
          alignment: PlaceholderAlignment.middle,
          child: Icon(
            Icons.arrow_downward_rounded,
            size: 12,
            color: Color(0xff14967b),
          ),
        ),
        const TextSpan(text: ' '),
        TextSpan(
          text: _formatRate(context, state.downloadBps),
          style: const TextStyle(
            color: Color(0xff14967b),
            fontWeight: FontWeight.w600,
          ),
        ),
        const TextSpan(text: '  ·  '),
        const WidgetSpan(
          alignment: PlaceholderAlignment.middle,
          child: Icon(
            Icons.arrow_upward_rounded,
            size: 12,
            color: Color(0xff5c5952),
          ),
        ),
        TextSpan(
          text:
              ' ${_formatRate(context, state.uploadBps)}     ${tr(context, 'Всего', 'Total')}  ',
        ),
        const WidgetSpan(
          alignment: PlaceholderAlignment.middle,
          child: Icon(
            Icons.arrow_downward_rounded,
            size: 12,
            color: Color(0xff5c5952),
          ),
        ),
        TextSpan(text: ' ${_formatBytes(context, state.rxBytes)}  ·  '),
        const WidgetSpan(
          alignment: PlaceholderAlignment.middle,
          child: Icon(
            Icons.arrow_upward_rounded,
            size: 12,
            color: Color(0xff5c5952),
          ),
        ),
        TextSpan(text: ' ${_formatBytes(context, state.txBytes)}'),
      ],
    ),
    key: const ValueKey('traffic-summary'),
    maxLines: 1,
    overflow: TextOverflow.ellipsis,
    textAlign: TextAlign.center,
  );
}

String _connectionStatus(BuildContext context, UiState state) {
  final localized = switch (state.status.trim()) {
    'Запуск ядра…' => tr(context, 'Запуск ядра…', 'Starting core…'),
    'Не подключено' => tr(context, 'Не подключено', 'Disconnected'),
    'Подключение…' => tr(context, 'Подключение…', 'Connecting…'),
    'Отключение…' => tr(context, 'Отключение…', 'Disconnecting…'),
    'Переключение…' => tr(context, 'Переключение…', 'Switching…'),
    'Ошибка подключения' => tr(
      context,
      'Ошибка подключения',
      'Connection error',
    ),
    'Ошибка отключения' => tr(
      context,
      'Ошибка отключения',
      'Disconnection error',
    ),
    'Ожидаем ответ' => tr(context, 'Ожидаем ответ', 'Waiting for response'),
    'Подключено' => tr(context, 'Подключено', 'Connected'),
    'Ожидание сервера' => tr(context, 'Ожидание сервера', 'Waiting for server'),
    final value => value,
  };
  if (state.busy) {
    return state.status.isEmpty
        ? tr(context, 'Переключение…', 'Switching…')
        : localized;
  }
  if (state.connected) {
    return state.status.isEmpty
        ? tr(context, 'Подключено', 'Connected')
        : localized;
  }
  final status = state.status.trim();
  if (status.isEmpty || status == 'Не подключено') {
    return tr(context, 'VPN выключен', 'VPN is off');
  }
  return localized;
}

class _PowerPainter extends CustomPainter {
  const _PowerPainter({required this.color});
  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    final power = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 2.4
      ..strokeCap = StrokeCap.round;
    final rect = Rect.fromLTWH(
      size.width * .14,
      size.height * .14,
      size.width * .72,
      size.height * .72,
    );
    canvas.drawArc(rect, -math.pi * .24, math.pi * 1.48, false, power);
    canvas.drawLine(
      Offset(size.width * .5, size.height * .05),
      Offset(size.width * .5, size.height * .48),
      power,
    );
  }

  @override
  bool shouldRepaint(covariant _PowerPainter oldDelegate) =>
      oldDelegate.color != color;
}

class ConnectionOrb extends StatelessWidget {
  const ConnectionOrb({
    super.key,
    required this.connected,
    required this.busy,
    required this.enabled,
    required this.onTap,
  });
  final bool connected;
  final bool busy;
  final bool enabled;
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      label: connected
          ? tr(context, 'Отключить VPN', 'Disconnect VPN')
          : tr(context, 'Подключить VPN', 'Connect VPN'),
      child: TweenAnimationBuilder<double>(
        tween: Tween(end: connected ? 1 : 0),
        duration: const Duration(milliseconds: 220),
        curve: Curves.easeOutCubic,
        builder: (context, activity, child) => Opacity(
          opacity: enabled ? 1 : .6,
          child: SizedBox.square(
            dimension: 184,
            child: _PressableScale(
              enabled: enabled && !busy,
              pressedScale: 0.97,
              child: Stack(
                alignment: Alignment.center,
                children: [
                  Container(
                    width: 184,
                    height: 184,
                    decoration: BoxDecoration(
                      shape: BoxShape.circle,
                      gradient: connected
                          ? const LinearGradient(
                              begin: Alignment.topLeft,
                              end: Alignment.bottomRight,
                              colors: [Color(0xf2ffffff), Color(0xd9d7f2eb)],
                            )
                          : const LinearGradient(
                              begin: Alignment.topLeft,
                              end: Alignment.bottomRight,
                              colors: [Color(0xebffffff), Color(0xd9e8e2d8)],
                            ),
                      border: Border.all(
                        color: connected
                            ? const Color(0x7314967b)
                            : const Color(0xf2ffffff),
                        width: connected ? 1.5 : 1.0,
                      ),
                      boxShadow: [
                        if (connected)
                          const BoxShadow(
                            color: Color(0x3d118a70),
                            blurRadius: 36,
                            offset: Offset(0, 16),
                          )
                        else
                          const BoxShadow(
                            color: Color(0x24584c3c),
                            blurRadius: 32,
                            offset: Offset(0, 14),
                          ),
                      ],
                    ),
                  ),
                  Container(
                    width: 170,
                    height: 170,
                    decoration: BoxDecoration(
                      shape: BoxShape.circle,
                      gradient: connected
                          ? const RadialGradient(
                              center: Alignment(-.36, -.44),
                              radius: 1.05,
                              colors: [
                                Color(0xff1fa689),
                                Color(0xff12876e),
                                Color(0xff0d6e59),
                              ],
                            )
                          : const RadialGradient(
                              center: Alignment(-.36, -.44),
                              radius: 1.05,
                              colors: [
                                Color(0xfffdfcfa),
                                Color(0xffefece5),
                                Color(0xffe4decf),
                              ],
                            ),
                      boxShadow: [
                        BoxShadow(
                          color: connected
                              ? const Color(0x26000000)
                              : const Color(0x14000000),
                          blurRadius: 8,
                          offset: const Offset(0, 4),
                        ),
                      ],
                    ),
                    child: Material(
                      color: Colors.transparent,
                      shape: const CircleBorder(),
                      child: InkWell(
                        customBorder: const CircleBorder(),
                        onTap: enabled && !busy ? onTap : null,
                        child: Column(
                          mainAxisAlignment: MainAxisAlignment.center,
                          children: [
                            if (busy)
                              SizedBox.square(
                                dimension: 34,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2.2,
                                  color: connected
                                      ? Colors.white
                                      : const Color(0xff17181a),
                                ),
                              )
                            else
                              SizedBox.square(
                                dimension: 44,
                                child: CustomPaint(
                                  painter: _PowerPainter(
                                    color: connected
                                        ? Colors.white
                                        : const Color(0xff17181a),
                                  ),
                                ),
                              ),
                            const SizedBox(height: 13),
                            Text(
                              busy
                                  ? statefulOrbLabel(context, connected)
                                  : connected
                                  ? tr(context, 'Отключить', 'Disconnect')
                                  : tr(context, 'Подключить', 'Connect'),
                              style: TextStyle(
                                fontSize: 14.5,
                                fontWeight: FontWeight.w600,
                                color: connected
                                    ? Colors.white
                                    : const Color(0xff17181a),
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

String statefulOrbLabel(BuildContext context, bool connected) => connected
    ? tr(context, 'Отключение…', 'Disconnecting…')
    : tr(context, 'Подключение…', 'Connecting…');

class _ProfilesPanel extends StatelessWidget {
  const _ProfilesPanel({
    required this.state,
    required this.selectDisabled,
    required this.manageDisabled,
    required this.onSelect,
    required this.onImportConfigs,
    required this.onImportQr,
    required this.onEdit,
    required this.onDelete,
  });
  final UiState state;
  final bool selectDisabled;
  final bool manageDisabled;
  final ValueChanged<String> onSelect;
  final VoidCallback onImportConfigs;
  final VoidCallback onImportQr;
  final ValueChanged<UiProfile> onEdit;
  final ValueChanged<UiProfile> onDelete;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(0, 14, 0, 0),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  tr(context, 'Профили', 'Profiles'),
                  style: const TextStyle(
                    color: Color(0xff3d3b37),
                    fontSize: 15,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                if (state.profiles.isNotEmpty) ...[
                  const SizedBox(width: 7),
                  Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 7,
                      vertical: 1.5,
                    ),
                    decoration: BoxDecoration(
                      color: const Color(0xffe5dfd4),
                      borderRadius: BorderRadius.circular(999),
                    ),
                    child: Text(
                      '${state.profiles.length}',
                      style: const TextStyle(
                        color: Color(0xff5c5952),
                        fontSize: 11.5,
                        fontWeight: FontWeight.w600,
                        fontFeatures: [FontFeature.tabularFigures()],
                      ),
                    ),
                  ),
                ],
              ],
            ),
            Text(
              tr(context, 'routing.help · активен', 'routing.help · active'),
              style: const TextStyle(
                color: Color(0xff7a766e),
                fontSize: 11.5,
                fontWeight: FontWeight.w400,
              ),
            ),
          ],
        ),
        const SizedBox(height: 10),
        Expanded(
          child: state.profiles.isEmpty
              ? Center(
                  child: Text(
                    tr(context, 'Профилей пока нет', 'No profiles yet'),
                    textAlign: TextAlign.center,
                    style: const TextStyle(color: Color(0xff7a766e)),
                  ),
                )
              : ListView.separated(
                  itemCount: state.profiles.length,
                  separatorBuilder: (_, __) => const SizedBox(height: 6),
                  itemBuilder: (context, index) {
                    final profile = state.profiles[index];
                    return _ProfileTile(
                      profile: profile,
                      selected: profile.id == state.selected,
                      selectDisabled: selectDisabled,
                      manageDisabled: manageDisabled,
                      onTap: () => onSelect(profile.id),
                      onEdit: () => onEdit(profile),
                      onDelete: () => onDelete(profile),
                    );
                  },
                ),
        ),
        const SizedBox(height: 8),
        Row(
          children: [
            Expanded(
              child: _ProfileImportButton(
                key: const ValueKey('import-configs'),
                icon: Icons.add_rounded,
                label: tr(context, 'Добавить .conf / .zip', 'Add .conf / .zip'),
                onPressed: manageDisabled ? null : onImportConfigs,
                isQr: false,
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: _ProfileImportButton(
                key: const ValueKey('import-qr'),
                icon: Icons.qr_code_rounded,
                label: tr(context, 'Добавить QR', 'Add QR'),
                onPressed: manageDisabled ? null : onImportQr,
                isQr: true,
              ),
            ),
          ],
        ),
      ],
    ),
  );
}

class _ProfileImportButton extends StatelessWidget {
  const _ProfileImportButton({
    super.key,
    required this.icon,
    required this.label,
    required this.onPressed,
    this.isQr = false,
  });

  final IconData icon;
  final String label;
  final VoidCallback? onPressed;
  final bool isQr;

  @override
  Widget build(BuildContext context) => _PressableScale(
    enabled: onPressed != null,
    child: OutlinedButton(
      onPressed: onPressed,
      style: OutlinedButton.styleFrom(
        backgroundColor: const Color(0xd9ffffff),
        foregroundColor: const Color(0xff17181a),
        side: const BorderSide(color: Colors.white),
        elevation: 1,
        shadowColor: const Color(0x0f584c3c),
        minimumSize: const Size.fromHeight(42),
        padding: const EdgeInsets.symmetric(horizontal: 12),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
        ),
      ),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          if (isQr)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1.5),
              decoration: BoxDecoration(
                color: const Color(0xffdcf2eb),
                borderRadius: BorderRadius.circular(5),
              ),
              child: const Text(
                'QR',
                style: TextStyle(
                  color: Color(0xff0f7b63),
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                ),
              ),
            )
          else
            Container(
              width: 22,
              height: 22,
              decoration: BoxDecoration(
                color: const Color(0xfff2efe9),
                borderRadius: BorderRadius.circular(6),
              ),
              child: const Icon(
                Icons.add_rounded,
                size: 16,
                color: Color(0xff14967b),
              ),
            ),
          const SizedBox(width: 8),
          Flexible(
            child: Text(
              label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                fontSize: 12.5,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        ],
      ),
    ),
  );
}

class _ProfileTile extends StatelessWidget {
  const _ProfileTile({
    required this.profile,
    required this.selected,
    required this.selectDisabled,
    required this.manageDisabled,
    required this.onTap,
    required this.onEdit,
    required this.onDelete,
  });
  final UiProfile profile;
  final bool selected;
  final bool selectDisabled;
  final bool manageDisabled;
  final VoidCallback onTap;
  final VoidCallback onEdit;
  final VoidCallback onDelete;
  @override
  Widget build(BuildContext context) => AnimatedContainer(
    duration: const Duration(milliseconds: 180),
    curve: Curves.easeOutCubic,
    decoration: BoxDecoration(
      color: selected ? Colors.white : const Color(0xb8f6f3ee),
      borderRadius: BorderRadius.circular(14),
      border: Border.all(
        color: selected ? const Color(0x7314967b) : const Color(0xbfffffff),
        width: selected ? 1.2 : 1.0,
      ),
      boxShadow: selected
          ? const [
              BoxShadow(
                color: Color(0x14584c3c),
                blurRadius: 18,
                offset: Offset(0, 6),
              ),
            ]
          : null,
    ),
    child: SizedBox(
      height: 48,
      child: Row(
        children: [
          AnimatedContainer(
            duration: const Duration(milliseconds: 180),
            width: 3.5,
            height: selected ? 24 : 0,
            decoration: BoxDecoration(
              color: const Color(0xff14967b),
              borderRadius: BorderRadius.circular(3),
            ),
          ),
          Expanded(
            child: Semantics(
              button: true,
              selected: selected,
              child: InkWell(
                onTap: selectDisabled ? null : onTap,
                borderRadius: const BorderRadius.horizontal(
                  left: Radius.circular(14),
                ),
                child: Row(
                  children: [
                    const SizedBox(width: 14),
                    Icon(
                      selected ? Icons.shield_rounded : Icons.shield_outlined,
                      size: 21,
                      color: selected
                          ? const Color(0xff14967b)
                          : const Color(0xff7a766e),
                    ),
                    const SizedBox(width: 12),
                    Flexible(
                      child: Text(
                        profile.name,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          fontSize: 14.5,
                          fontWeight:
                              selected ? FontWeight.w600 : FontWeight.w500,
                          color: const Color(0xff17181a),
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 8,
                        vertical: 2.5,
                      ),
                      decoration: BoxDecoration(
                        color: selected
                            ? const Color(0xffebf5f2)
                            : Colors.white,
                        border: selected
                            ? null
                            : Border.all(color: const Color(0xffe2dcd0)),
                        borderRadius: BorderRadius.circular(999),
                      ),
                      child: Text(
                        profile.generation,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          color: selected
                              ? const Color(0xff0f7b63)
                              : const Color(0xff5c5952),
                          fontSize: 11.5,
                          fontWeight:
                              selected ? FontWeight.w600 : FontWeight.w500,
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    if (profile.preparing) ...[
                      const SizedBox(
                        width: 15,
                        height: 15,
                        child: CircularProgressIndicator(
                          strokeWidth: 1.6,
                          color: Color(0xff14967b),
                        ),
                      ),
                      const SizedBox(width: 5),
                    ] else if (profile.routingError.isNotEmpty) ...[
                      Tooltip(
                        message: profile.routingError,
                        child: const Icon(
                          Icons.warning_amber_rounded,
                          size: 17,
                          color: Color(0xffd7a75d),
                        ),
                      ),
                      const SizedBox(width: 4),
                    ],
                    if (selected) ...[
                      const Icon(
                        Icons.check_circle_rounded,
                        size: 16,
                        color: Color(0xff14967b),
                      ),
                      const SizedBox(width: 4),
                    ],
                  ],
                ),
              ),
            ),
          ),
          IconButton(
            key: ValueKey('edit-${profile.id}'),
            onPressed: manageDisabled ? null : onEdit,
            tooltip: tr(context, 'Изменить', 'Edit'),
            icon: const Icon(Icons.edit_outlined, size: 18),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints.tightFor(width: 40, height: 48),
            splashRadius: 18,
            hoverColor: const Color(0x1a14967b),
            color: const Color(0xff6e6b64),
            disabledColor: const Color(0xffb5b0a4),
          ),
          IconButton(
            key: ValueKey('delete-${profile.id}'),
            onPressed: manageDisabled ? null : onDelete,
            tooltip: tr(context, 'Удалить', 'Delete'),
            icon: const Icon(Icons.delete_outline_rounded, size: 19),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints.tightFor(width: 40, height: 48),
            splashRadius: 18,
            hoverColor: const Color(0x24d3455b),
            color: const Color(0xff6e6b64),
            disabledColor: const Color(0xffb5b0a4),
          ),
          const SizedBox(width: 4),
        ],
      ),
    ),
  );
}

class RulesPage extends StatefulWidget {
  const RulesPage({
    super.key,
    required this.details,
    required this.disabled,
    required this.sourceLocked,
    required this.onSave,
    required this.onRefresh,
    required this.onReset,
    required this.onCheckRoute,
    required this.connected,
  });
  final RoutingDetails details;
  final bool disabled;
  final bool sourceLocked;
  final Future<void> Function(String, String) onSave;
  final VoidCallback onRefresh;
  final VoidCallback onReset;
  final Future<RouteCheckResult?> Function(String) onCheckRoute;
  final bool connected;
  @override
  State<RulesPage> createState() => _RulesPageState();
}

class _RulesPageState extends State<RulesPage> {
  late final TextEditingController _source;
  late final TextEditingController _overrides;
  late final TextEditingController _newRule;
  late final TextEditingController _search;
  late final TextEditingController _routeCheck;
  RouteCheckResult? _routeCheckResult;
  String _newAction = 'proxy';
  @override
  void initState() {
    super.initState();
    _source = TextEditingController(text: widget.details.source);
    _overrides = TextEditingController(text: widget.details.overrides);
    _newRule = TextEditingController();
    _search = TextEditingController();
    _routeCheck = TextEditingController();
  }

  @override
  void didUpdateWidget(covariant RulesPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.details.source != widget.details.source) {
      _source.text = widget.details.source;
    }
    if (oldWidget.details.overrides != widget.details.overrides) {
      _overrides.text = widget.details.overrides;
    }
  }

  @override
  void dispose() {
    _source.dispose();
    _overrides.dispose();
    _newRule.dispose();
    _search.dispose();
    _routeCheck.dispose();
    super.dispose();
  }

  Future<void> _setRuleAction(String rule, String? action) async {
    final normalized = _normalizeRoutingRule(rule);
    if (normalized == null) return;
    final kept = _overrides.text.split('\n').where((line) {
      final trimmed = line.trim();
      final match = RegExp(
        r'^(direct|proxy|block)\s+(.+)$',
        caseSensitive: false,
      ).firstMatch(trimmed);
      return match == null ||
          match.group(2)!.trim().toLowerCase() != normalized.toLowerCase();
    }).toList();
    if (action != null) kept.add('$action $normalized');
    final updated = kept.where((line) => line.trim().isNotEmpty).join('\n');
    setState(() => _overrides.text = updated);
    await widget.onSave(_source.text, updated);
  }

  Future<void> _addRule() async {
    final normalized = _normalizeRoutingRule(_newRule.text);
    if (normalized == null) return;
    _newRule.clear();
    await _setRuleAction(normalized, _newAction);
  }

  Future<void> _runRouteCheck() async {
    final result = await widget.onCheckRoute(_routeCheck.text);
    if (mounted && result != null) setState(() => _routeCheckResult = result);
  }

  @override
  Widget build(BuildContext context) => PageFrame(
    title: tr(context, 'Правила / Маршрутизация', 'Rules / Routing'),
    child: SingleChildScrollView(
      child: Column(
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                flex: 6,
                child: GlassCard(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Row(
                        mainAxisAlignment: MainAxisAlignment.spaceBetween,
                        children: [
                          Text(
                            tr(context, 'Источник правил', 'Rules source'),
                            style: const TextStyle(
                              fontSize: 17,
                              fontWeight: FontWeight.w700,
                              color: Color(0xff17181a),
                            ),
                          ),
                          Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 10,
                              vertical: 3,
                            ),
                            decoration: BoxDecoration(
                              color: const Color(0xffdcf2eb),
                              border: Border.all(
                                color: const Color(0xffb5e2d4),
                              ),
                              borderRadius: BorderRadius.circular(999),
                            ),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Container(
                                  width: 7,
                                  height: 7,
                                  decoration: const BoxDecoration(
                                    color: Color(0xff14967b),
                                    shape: BoxShape.circle,
                                  ),
                                ),
                                const SizedBox(width: 6),
                                Text(
                                  tr(context, 'Активна', 'Active'),
                                  style: const TextStyle(
                                    color: Color(0xff0f7b63),
                                    fontSize: 11.5,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                              ],
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 6),
                      Text(
                        _localizedRoutingSummary(
                          context,
                          widget.details.summary,
                        ),
                        style: Theme.of(context).textTheme.bodySmall,
                      ),
                      const SizedBox(height: 16),
                      TextField(
                        controller: _source,
                        enabled: !widget.disabled && !widget.sourceLocked,
                        decoration: InputDecoration(
                          labelText: tr(
                            context,
                            'Ссылка на профиль правил',
                            'Routing profile URL',
                          ),
                          hintText: 'https://routing.help/',
                        ),
                      ),
                      const SizedBox(height: 13),
                      Row(
                        children: [
                          Expanded(
                            child: _CompactButton(
                              icon: Icons.save_outlined,
                              label: tr(context, 'Сохранить', 'Save'),
                              onPressed: widget.disabled || widget.sourceLocked
                                  ? null
                                  : () => widget.onSave(
                                      _source.text,
                                      _overrides.text,
                                    ),
                            ),
                          ),
                          const SizedBox(width: 10),
                          Expanded(
                            child: _CompactButton(
                              icon: Icons.refresh_rounded,
                              label: tr(
                                context,
                                'Обновить сейчас',
                                'Update now',
                              ),
                              isFilled: true,
                              onPressed: widget.disabled
                                  ? null
                                  : widget.onRefresh,
                            ),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ),
              const SizedBox(width: 18),
              Expanded(
                flex: 4,
                child: GlassCard(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Text(
                        tr(context, 'Параметры профиля', 'Profile settings'),
                        style: const TextStyle(
                          fontSize: 17,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                      const SizedBox(height: 15),
                      _InfoLine(
                        label: tr(context, 'Название', 'Name'),
                        value: widget.details.profileName,
                      ),
                      const SizedBox(height: 9),
                      _InfoLine(
                        label: tr(context, 'Порядок проверки', 'Check order'),
                        value: _formatRouteOrder(
                          context,
                          widget.details.routeOrder,
                        ),
                      ),
                      const SizedBox(height: 9),
                      _InfoLine(
                        label: tr(
                          context,
                          'Проверка доменов',
                          'Domain matching',
                        ),
                        value: _formatDomainStrategy(
                          context,
                          widget.details.domainStrategy,
                        ),
                      ),
                      const SizedBox(height: 9),
                      _InfoLine(
                        label: tr(context, 'Обновлено', 'Updated'),
                        value: widget.details.updatedAt,
                      ),
                      if (widget.details.profileLastUpdated.isNotEmpty) ...[
                        const SizedBox(height: 9),
                        _InfoLine(
                          label: tr(
                            context,
                            'Редакция базы',
                            'Database revision',
                          ),
                          value: widget.details.profileLastUpdated,
                        ),
                      ],
                    ],
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 18),
          GlassCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(
                  tr(context, 'Проверить маршрут', 'Check route'),
                  style: const TextStyle(
                    fontSize: 17,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 5),
                Text(
                  widget.connected
                      ? tr(
                          context,
                          'Показывает фактическое правило, адрес и сетевой путь.',
                          'Shows the effective rule, address, and network path.',
                        )
                      : tr(
                          context,
                          'Подключите VPN для проверки фактического пути.',
                          'Connect the VPN to inspect the actual path.',
                        ),
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    Expanded(
                      child: TextField(
                        key: const ValueKey('route-check-domain'),
                        controller: _routeCheck,
                        enabled: widget.connected && !widget.disabled,
                        onSubmitted: (_) => _runRouteCheck(),
                        decoration: InputDecoration(
                          isDense: true,
                          hintText: tr(
                            context,
                            'Например, updates.cdn-apple.com',
                            'For example, updates.cdn-apple.com',
                          ),
                        ),
                      ),
                    ),
                    const SizedBox(width: 10),
                    _PressableScale(
                      enabled: widget.connected && !widget.disabled,
                      child: FilledButton.icon(
                        onPressed: widget.connected && !widget.disabled
                            ? _runRouteCheck
                            : null,
                        icon: const Icon(Icons.route_rounded, size: 18),
                        label: Text(tr(context, 'Проверить', 'Check')),
                      ),
                    ),
                  ],
                ),
                if (_routeCheckResult != null) ...[
                  const SizedBox(height: 12),
                  _RouteCheckView(result: _routeCheckResult!),
                ],
              ],
            ),
          ),
          const SizedBox(height: 18),
          GlassCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(
                  tr(
                    context,
                    'Состав routing-пакета',
                    'Routing package contents',
                  ),
                  style: const TextStyle(
                    fontSize: 17,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 5),
                Text(
                  tr(
                    context,
                    'Базы доменов, IP-адресов, DNS-серверы и списки правил маршрутизации.',
                    'Domain and IP databases, DNS servers, and traffic routing rule lists.',
                  ),
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                const SizedBox(height: 14),
                _GeoAssetRow(
                  name: 'GeoSite',
                  description: tr(
                    context,
                    'База категорий доменов',
                    'Domain categories database',
                  ),
                  bytes: widget.details.geoSiteBytes,
                  url: widget.details.geoSiteURL,
                ),
                const SizedBox(height: 9),
                _GeoAssetRow(
                  name: 'GeoIP',
                  description: tr(
                    context,
                    'База диапазонов IP-адресов',
                    'IP ranges database',
                  ),
                  bytes: widget.details.geoIPBytes,
                  url: widget.details.geoIPURL,
                ),
                const SizedBox(height: 12),
                _DNSDetails(details: widget.details),
                const SizedBox(height: 8),
                TextField(
                  key: const ValueKey('routing-search'),
                  controller: _search,
                  onChanged: (_) => setState(() {}),
                  decoration: InputDecoration(
                    isDense: true,
                    prefixIcon: const Icon(Icons.search_rounded, size: 19),
                    hintText: tr(context, 'Найти правило', 'Find a rule'),
                    suffixIcon: _search.text.isEmpty
                        ? null
                        : IconButton(
                            onPressed: () => setState(_search.clear),
                            tooltip: tr(context, 'Очистить', 'Clear'),
                            icon: const Icon(Icons.close_rounded, size: 18),
                          ),
                  ),
                ),
                const SizedBox(height: 6),
                _RuleDetails(
                  label: 'Direct',
                  description: tr(
                    context,
                    'Напрямую без VPN',
                    'Direct without VPN',
                  ),
                  sourceAction: 'direct',
                  color: const Color(0xfff2f3f3),
                  sites: widget.details.directSites,
                  addresses: widget.details.directIP,
                  overrides: _overrideActions(_overrides.text),
                  disabled: widget.disabled,
                  onMove: _setRuleAction,
                  query: _search.text,
                ),
                _RuleDetails(
                  label: 'Proxy',
                  description: tr(context, 'Через VPN', 'Via VPN'),
                  sourceAction: 'proxy',
                  color: const Color(0xffc8cbcc),
                  sites: widget.details.proxySites,
                  addresses: widget.details.proxyIP,
                  overrides: _overrideActions(_overrides.text),
                  disabled: widget.disabled,
                  onMove: _setRuleAction,
                  query: _search.text,
                ),
                _RuleDetails(
                  label: 'Block',
                  description: tr(context, 'Блокировка', 'Blocked'),
                  sourceAction: 'block',
                  color: const Color(0xffff7b7b),
                  sites: widget.details.blockSites,
                  addresses: widget.details.blockIP,
                  overrides: _overrideActions(_overrides.text),
                  disabled: widget.disabled,
                  onMove: _setRuleAction,
                  query: _search.text,
                ),
              ],
            ),
          ),
          const SizedBox(height: 18),
          GlassCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(
                  tr(
                    context,
                    'Локальные правила для всех конфигураций',
                    'Local rules for all configurations',
                  ),
                  style: const TextStyle(
                    fontSize: 17,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 5),
                Text(
                  tr(
                    context,
                    'Правило имеет приоритет над общей базой, применяется сразу и действует для всех VPN-профилей.',
                    'A rule takes priority over the shared database, applies immediately, and stays active across all VPN profiles.',
                  ),
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                const SizedBox(height: 14),
                Row(
                  children: [
                    SizedBox(
                      width: 124,
                      child: DropdownButtonFormField<String>(
                        initialValue: _newAction,
                        decoration: const InputDecoration(isDense: true),
                        items: const [
                          DropdownMenuItem(
                            value: 'direct',
                            child: Text('Direct'),
                          ),
                          DropdownMenuItem(
                            value: 'proxy',
                            child: Text('Proxy'),
                          ),
                          DropdownMenuItem(
                            value: 'block',
                            child: Text('Block'),
                          ),
                        ],
                        onChanged: widget.disabled
                            ? null
                            : (value) => setState(() => _newAction = value!),
                      ),
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: TextField(
                        key: const ValueKey('new-routing-rule'),
                        controller: _newRule,
                        enabled: !widget.disabled,
                        onSubmitted: (_) => _addRule(),
                        decoration: InputDecoration(
                          isDense: true,
                          hintText: tr(
                            context,
                            'geosite:apple, geoip:private или apple.com',
                            'geosite:apple, geoip:private, or apple.com',
                          ),
                        ),
                      ),
                    ),
                    const SizedBox(width: 10),
                    _PressableScale(
                      enabled: !widget.disabled,
                      child: FilledButton.icon(
                        key: const ValueKey('add-routing-rule'),
                        onPressed: widget.disabled ? null : _addRule,
                        icon: const Icon(Icons.add_rounded, size: 18),
                        label: Text(tr(context, 'Добавить', 'Add')),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: _overrides,
                  enabled: !widget.disabled,
                  onChanged: (_) => setState(() {}),
                  minLines: 4,
                  maxLines: 8,
                  style: const TextStyle(fontSize: 13),
                  decoration: const InputDecoration(
                    hintText:
                        'proxy geosite:apple\ndirect domain:gov.ru\nblock domain:ads.example',
                  ),
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    _PressableScale(
                      enabled: !widget.disabled,
                      child: FilledButton.icon(
                        onPressed: widget.disabled
                            ? null
                            : () =>
                                  widget.onSave(_source.text, _overrides.text),
                        icon: const Icon(Icons.check_rounded, size: 18),
                        label: Text(tr(context, 'Применить', 'Apply')),
                      ),
                    ),
                    const SizedBox(width: 10),
                    _PressableScale(
                      enabled: !widget.disabled && !widget.sourceLocked,
                      child: TextButton.icon(
                        onPressed: widget.disabled || widget.sourceLocked
                            ? null
                            : widget.onReset,
                        icon: const Icon(Icons.restart_alt_rounded),
                        label: Text(
                          tr(
                            context,
                            'Восстановить с нуля',
                            'Reset and reload',
                          ),
                        ),
                      ),
                    ),
                  ],
                ),
                if (widget.details.rawProfile.isNotEmpty) ...[
                  const SizedBox(height: 8),
                  ExpansionTile(
                    tilePadding: EdgeInsets.zero,
                    title: Text(
                      tr(
                        context,
                        'Показать исходный routing-профиль',
                        'Show raw routing profile',
                      ),
                      style: const TextStyle(fontSize: 13),
                    ),
                    children: [
                      Container(
                        width: double.infinity,
                        constraints: const BoxConstraints(maxHeight: 260),
                        padding: const EdgeInsets.all(14),
                        decoration: BoxDecoration(
                          color: const Color(0xffeae4d9),
                          borderRadius: BorderRadius.circular(12),
                          border: Border.all(color: const Color(0xffd8d0c2)),
                        ),
                        child: SingleChildScrollView(
                          child: SelectableText(
                            widget.details.rawProfile,
                            style: const TextStyle(
                              fontFamily: 'Consolas',
                              fontSize: 12,
                              color: Color(0xff232527),
                            ),
                          ),
                        ),
                      ),
                    ],
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    ),
  );
}

class _InfoLine extends StatelessWidget {
  const _InfoLine({required this.label, required this.value});
  final String label;
  final String value;
  @override
  Widget build(BuildContext context) => Row(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(label, style: const TextStyle(color: Color(0xff6e7175))),
      const SizedBox(width: 10),
      Expanded(
        child: Text(
          value.isEmpty ? '—' : value,
          textAlign: TextAlign.end,
          overflow: TextOverflow.ellipsis,
          maxLines: 2,
          style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 12),
        ),
      ),
    ],
  );
}

class _RouteCheckView extends StatelessWidget {
  const _RouteCheckView({required this.result});
  final RouteCheckResult result;

  @override
  Widget build(BuildContext context) {
    final action = _routingActionLabel(context, result.action);
    final lines = <String>[];
    for (var index = 0; index < result.addresses.length; index++) {
      final rawPath = index < result.paths.length ? result.paths[index] : '';
      final path = switch (rawPath) {
        'exwg' => 'Ex WG',
        'direct' => tr(context, 'Обычная сеть', 'Regular network'),
        'unavailable' => tr(context, 'Маршрут недоступен', 'Route unavailable'),
        _ => rawPath.isEmpty ? '—' : rawPath,
      };
      lines.add('${result.addresses[index]}  ·  $path');
    }
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: const Color(0x0a17181a),
        border: Border.all(color: const Color(0x1817181a)),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '${result.domain}  ·  $action',
            style: const TextStyle(fontWeight: FontWeight.w700),
          ),
          const SizedBox(height: 5),
          Text(
            result.rule.isEmpty
                ? tr(context, 'Правило по умолчанию', 'Default rule')
                : result.rule,
            style: const TextStyle(fontSize: 11.5, color: Color(0xff6e7175)),
          ),
          if (lines.isNotEmpty) ...[
            const SizedBox(height: 7),
            ...lines.map(
              (line) => Padding(
                padding: const EdgeInsets.only(top: 2),
                child: Text(line, style: const TextStyle(fontSize: 11.5)),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _GeoAssetRow extends StatelessWidget {
  const _GeoAssetRow({
    required this.name,
    required this.description,
    required this.bytes,
    required this.url,
  });
  final String name;
  final String description;
  final int bytes;
  final String url;
  @override
  Widget build(BuildContext context) => Container(
    padding: const EdgeInsets.all(12),
    decoration: BoxDecoration(
      color: const Color(0x0a17181a),
      borderRadius: BorderRadius.circular(12),
      border: Border.all(color: const Color(0x1417181a)),
    ),
    child: Row(
      children: [
        const Icon(Icons.storage_rounded, size: 18, color: Color(0xff6e7175)),
        const SizedBox(width: 10),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Text(
                    name,
                    style: const TextStyle(fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(width: 8),
                  Flexible(
                    child: Text(
                      description,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        fontSize: 11.5,
                        color: Color(0xff6e7175),
                      ),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 3),
              Text(
                url.isEmpty
                    ? tr(
                        context,
                        'Не используется профилем',
                        'Not used by this profile',
                      )
                    : url,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ],
          ),
        ),
        const SizedBox(width: 12),
        Text(
          _formatBytes(context, bytes),
          style: const TextStyle(
            fontWeight: FontWeight.w700,
            fontFeatures: [FontFeature.tabularFigures()],
          ),
        ),
      ],
    ),
  );
}

class _DNSDetails extends StatelessWidget {
  const _DNSDetails({required this.details});
  final RoutingDetails details;
  @override
  Widget build(BuildContext context) => ExpansionTile(
    tilePadding: EdgeInsets.zero,
    childrenPadding: const EdgeInsets.only(bottom: 10),
    title: const Text('DNS', style: TextStyle(fontWeight: FontWeight.w700)),
    subtitle: Text(
      'Direct: ${_formatDnsShort(details.domesticDNSType, details.domesticDNS, details.domesticDNSDomain)}   /   Proxy: ${_formatDnsShort(details.remoteDNSType, details.remoteDNS, details.remoteDNSDomain)}',
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
    ),
    children: [
      _InfoLine(
        label: tr(context, 'Direct DNS (напрямую)', 'Direct DNS (bypass VPN)'),
        value: _dnsDescription(
          details.domesticDNSType,
          details.domesticDNS,
          details.domesticDNSDomain,
          details.domesticDNSIP,
        ),
      ),
      const SizedBox(height: 7),
      _InfoLine(
        label: tr(context, 'Proxy DNS (через VPN)', 'Proxy DNS (via VPN)'),
        value: _dnsDescription(
          details.remoteDNSType,
          details.remoteDNS,
          details.remoteDNSDomain,
          details.remoteDNSIP,
        ),
      ),
      const SizedBox(height: 7),
      _InfoLine(
        label: tr(
          context,
          'Статические домены (DNS hosts)',
          'Static domains (DNS hosts)',
        ),
        value: '${details.dnsHosts.length}',
      ),
      if (details.dnsHosts.isNotEmpty) ...[
        const SizedBox(height: 8),
        SelectableText(
          details.dnsHosts.entries
              .map((item) => '${item.key} → ${item.value}')
              .join('\n'),
          style: const TextStyle(
            fontFamily: 'Consolas',
            fontSize: 11.5,
            color: Color(0xff232527),
          ),
        ),
      ],
    ],
  );
}

class _RuleDetails extends StatelessWidget {
  const _RuleDetails({
    required this.label,
    required this.description,
    required this.sourceAction,
    required this.sites,
    required this.addresses,
    required this.color,
    required this.overrides,
    required this.disabled,
    required this.onMove,
    required this.query,
  });
  final String label;
  final String description;
  final String sourceAction;
  final List<String> sites;
  final List<String> addresses;
  final Color color;
  final Map<String, String> overrides;
  final bool disabled;
  final Future<void> Function(String, String?) onMove;
  final String query;
  @override
  Widget build(BuildContext context) {
    final needle = query.trim().toLowerCase();
    final filtered = [...sites, ...addresses]
        .where((rule) => needle.isEmpty || rule.toLowerCase().contains(needle))
        .toList(growable: false);
    return ExpansionTile(
      key: ValueKey('routing-section-$sourceAction-$needle'),
      initiallyExpanded: needle.isNotEmpty,
      tilePadding: EdgeInsets.zero,
      childrenPadding: const EdgeInsets.only(bottom: 12),
      title: Row(
        children: [
          Container(
            width: 8,
            height: 8,
            decoration: BoxDecoration(
              color: color,
              shape: BoxShape.circle,
              boxShadow: [
                BoxShadow(color: color.withValues(alpha: .5), blurRadius: 8),
              ],
            ),
          ),
          const SizedBox(width: 9),
          Text(label, style: const TextStyle(fontWeight: FontWeight.w700)),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              description,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 11.5, color: Color(0xff6e7175)),
            ),
          ),
          Text(
            tr(
              context,
              '${sites.length} сайтов · ${addresses.length} IP',
              '${sites.length} sites · ${addresses.length} IP',
            ),
            style: const TextStyle(fontSize: 11, color: Color(0xff6e7175)),
          ),
        ],
      ),
      children: filtered.isEmpty
          ? [
              Align(
                alignment: Alignment.centerLeft,
                child: Text(
                  needle.isEmpty
                      ? tr(
                          context,
                          'В профиле нет правил этой категории',
                          'This profile has no rules in this category',
                        )
                      : tr(
                          context,
                          'Поиск не нашёл правил',
                          'No matching rules',
                        ),
                  style: const TextStyle(
                    fontSize: 11.5,
                    color: Color(0xff6e7175),
                  ),
                ),
              ),
            ]
          : filtered
                .map(
                  (rule) => _RoutingRuleRow(
                    rule: rule,
                    sourceAction: sourceAction,
                    effectiveAction: overrides[rule.toLowerCase()],
                    disabled: disabled,
                    onMove: (action) => onMove(rule, action),
                  ),
                )
                .toList(growable: false),
    );
  }
}

class _RoutingRuleRow extends StatelessWidget {
  const _RoutingRuleRow({
    required this.rule,
    required this.sourceAction,
    required this.effectiveAction,
    required this.disabled,
    required this.onMove,
  });

  final String rule;
  final String sourceAction;
  final String? effectiveAction;
  final bool disabled;
  final ValueChanged<String?> onMove;

  @override
  Widget build(BuildContext context) => ConstrainedBox(
    key: ValueKey('routing-rule-$rule'),
    constraints: const BoxConstraints(minHeight: 34),
    child: Row(
      children: [
        Expanded(
          child: Text(
            rule,
            style: const TextStyle(fontSize: 11.5, color: Color(0xff232527)),
          ),
        ),
        if (effectiveAction != null && effectiveAction != sourceAction)
          Text(
            '→ ${_routingActionLabel(context, effectiveAction!)}',
            style: const TextStyle(fontSize: 11, color: Color(0xff14967b), fontWeight: FontWeight.w600),
          ),
        PopupMenuButton<String>(
          enabled: !disabled,
          tooltip: tr(context, 'Изменить правило', 'Change rule'),
          icon: const Icon(Icons.swap_horiz_rounded, size: 18),
          onSelected: (value) => onMove(value == 'profile' ? null : value),
          itemBuilder: (context) => [
            const PopupMenuItem(value: 'direct', child: Text('Direct')),
            const PopupMenuItem(value: 'proxy', child: Text('Proxy')),
            const PopupMenuItem(value: 'block', child: Text('Block')),
            PopupMenuItem(
              value: 'profile',
              child: Text(
                tr(context, 'По профилю', 'Use profile rule'),
              ),
            ),
          ],
        ),
      ],
    ),
  );
}

String _formatBytes(BuildContext context, int bytes) {
  if (bytes <= 0) return '—';
  if (bytes < 1024) return '$bytes B';
  if (bytes < 1024 * 1024) {
    return '${(bytes / 1024).toStringAsFixed(1)} ${tr(context, 'КиБ', 'KiB')}';
  }
  if (bytes < 1024 * 1024 * 1024) {
    return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} ${tr(context, 'МиБ', 'MiB')}';
  }
  return '${(bytes / (1024 * 1024 * 1024)).toStringAsFixed(2)} ${tr(context, 'ГиБ', 'GiB')}';
}

String _formatRate(BuildContext context, int bytesPerSecond) =>
    bytesPerSecond <= 0
    ? '0 B/s'
    : '${_formatBytes(context, bytesPerSecond)}/s';

String _formatDnsProtocol(String type) => switch (type.trim().toLowerCase()) {
  'doh' => 'DoH (HTTPS)',
  'dot' => 'DoT (TLS)',
  'dou' || 'udp' => 'UDP',
  'tcp' => 'TCP',
  final other => other,
};

String _formatDnsShort(String type, String value, String domain) {
  final proto = _formatDnsProtocol(type);
  final host = value.trim().isNotEmpty ? value.trim() : domain.trim();
  return [
    if (proto.isNotEmpty) proto,
    host.isEmpty ? '—' : host,
  ].join(' · ');
}

String _dnsDescription(String type, String value, String domain, String ip) => [
  _formatDnsProtocol(type),
  value,
  domain,
  ip,
].where((item) => item.trim().isNotEmpty).toSet().join(' · ');

String _formatRouteOrder(BuildContext context, String raw) {
  final trimmed = raw.trim();
  if (trimmed.isEmpty) return '';
  return trimmed
      .split('-')
      .map(
        (part) => switch (part.trim().toLowerCase()) {
          'direct' => 'Direct',
          'proxy' => 'Proxy',
          'block' => 'Block',
          final other => other,
        },
      )
      .join(' / ');
}

String _formatDomainStrategy(BuildContext context, String raw) =>
    switch (raw.trim()) {
      'IPIfNonMatch' => tr(
        context,
        'Домен, затем IP',
        'Domain, then IP',
      ),
      'IPOnDemand' => tr(context, 'Домен и IP', 'Domain and IP'),
      'AsIs' => tr(context, 'Только домен', 'Domain only'),
      final other => other,
    };

Map<String, String> _overrideActions(String input) {
  final result = <String, String>{};
  for (final line in input.split('\n')) {
    final match = RegExp(
      r'^(direct|proxy|block)\s+(.+)$',
      caseSensitive: false,
    ).firstMatch(line.trim());
    if (match != null) {
      result[match.group(2)!.trim().toLowerCase()] = match
          .group(1)!
          .toLowerCase();
    }
  }
  return result;
}

String? _normalizeRoutingRule(String input) {
  var value = input.trim();
  if (value.isEmpty) return null;
  final uri = Uri.tryParse(value);
  if (uri != null && uri.hasScheme && uri.host.isNotEmpty) value = uri.host;
  final lower = value.toLowerCase();
  const prefixes = [
    'domain:',
    'full:',
    'keyword:',
    'regexp:',
    'geosite:',
    'geoip:',
  ];
  if (prefixes.any(lower.startsWith)) return value;
  final slash = value.indexOf('/');
  final address = slash < 0 ? value : value.substring(0, slash);
  if (InternetAddress.tryParse(address) != null) return value;
  if (value.startsWith('*.')) value = value.substring(2);
  if (value.startsWith('.')) value = value.substring(1);
  return 'domain:$value';
}

String _routingActionLabel(BuildContext context, String action) =>
    switch (action) {
      'direct' => 'Direct',
      'proxy' => 'Proxy',
      'block' => 'Block',
      _ => tr(context, 'По профилю', 'Profile'),
    };

String _localizedRoutingSummary(BuildContext context, String summary) =>
    switch (summary) {
      'Кэш ещё не загружен' ||
      'База правил загружается в фоне · локальные правила уже активны' => tr(
        context,
        'База правил загружается в фоне · локальные правила уже активны',
        'Rules database is downloading in the background · local rules are active',
      ),
      'Проверенный набор применён · обновляется при запуске' => tr(
        context,
        'Проверенный набор применён · обновляется при запуске',
        'Verified rule set applied · updates automatically on launch',
      ),
      final other => other,
    };

class _CompactButton extends StatelessWidget {
  const _CompactButton({
    super.key,
    required this.icon,
    required this.label,
    required this.onPressed,
    this.isFilled = false,
  });
  final IconData icon;
  final String label;
  final VoidCallback? onPressed;
  final bool isFilled;
  @override
  Widget build(BuildContext context) => _PressableScale(
    enabled: onPressed != null,
    child: isFilled
        ? FilledButton.icon(
            onPressed: onPressed,
            icon: Icon(icon, size: 17),
            label: Text(label, maxLines: 1, overflow: TextOverflow.ellipsis),
            style: FilledButton.styleFrom(
              minimumSize: const Size.fromHeight(42),
              backgroundColor: const Color(0xff14967b),
              foregroundColor: Colors.white,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
              ),
            ),
          )
        : OutlinedButton.icon(
            onPressed: onPressed,
            icon: Icon(icon, size: 17),
            label: Text(label, maxLines: 1, overflow: TextOverflow.ellipsis),
            style: OutlinedButton.styleFrom(
              minimumSize: const Size.fromHeight(42),
              backgroundColor: const Color(0xd9ffffff),
              foregroundColor: const Color(0xff17181a),
              side: const BorderSide(color: Colors.white),
              elevation: 1,
              shadowColor: const Color(0x0f584c3c),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
              ),
            ),
          ),
  );
}

class DiagnosticsPage extends StatelessWidget {
  const DiagnosticsPage({
    super.key,
    required this.text,
    required this.disabled,
    required this.onCheck,
    required this.onCopy,
    required this.onSave,
    required this.onHide,
  });
  final String text;
  final bool disabled;
  final VoidCallback onCheck;
  final VoidCallback onCopy;
  final VoidCallback onSave;
  final VoidCallback onHide;
  @override
  Widget build(BuildContext context) => PageFrame(
    title: tr(context, 'Диагностика / Debug', 'Diagnostics / Debug'),
    subtitle: tr(
      context,
      'Полный исходный журнал с кодами Win32, WSA, Go и AWG',
      'Complete raw log with Win32, WSA, Go and AWG codes',
    ),
    child: Column(
      children: [
        Expanded(
          child: GlassCard(
            padding: const EdgeInsets.all(4),
            child: Container(
              width: double.infinity,
              padding: const EdgeInsets.all(16),
              decoration: BoxDecoration(
                color: const Color(0xffeae4d9),
                border: Border.all(color: const Color(0xffd8d0c2)),
                borderRadius: BorderRadius.circular(15),
              ),
              child: SingleChildScrollView(
                child: SelectableText(
                  text.isEmpty
                      ? tr(
                          context,
                          'Журнал загружается…',
                          'Loading diagnostics…',
                        )
                      : text,
                  style: const TextStyle(
                    fontFamily: 'Consolas',
                    fontSize: 12.5,
                    height: 1.45,
                    color: Color(0xff232527),
                  ),
                ),
              ),
            ),
          ),
        ),
        const SizedBox(height: 15),
        Row(
          children: [
            ConstrainedBox(
              constraints: const BoxConstraints(minWidth: 132),
              child: IntrinsicWidth(
                child: _CompactButton(
                  icon: Icons.monitor_heart_outlined,
                  label: tr(context, 'Проверить', 'Run check'),
                  isFilled: true,
                  onPressed: disabled ? null : onCheck,
                ),
              ),
            ),
            const SizedBox(width: 10),
            ConstrainedBox(
              constraints: const BoxConstraints(minWidth: 132),
              child: IntrinsicWidth(
                child: _CompactButton(
                  icon: Icons.copy_rounded,
                  label: tr(context, 'Копировать', 'Copy'),
                  onPressed: disabled ? null : onCopy,
                ),
              ),
            ),
            const SizedBox(width: 10),
            ConstrainedBox(
              constraints: const BoxConstraints(minWidth: 144),
              child: IntrinsicWidth(
                child: _CompactButton(
                  icon: Icons.save_alt_rounded,
                  label: tr(context, 'Сохранить TXT', 'Save TXT'),
                  onPressed: disabled ? null : onSave,
                ),
              ),
            ),
            const SizedBox(width: 10),
            ConstrainedBox(
              constraints: const BoxConstraints(minWidth: 112),
              child: IntrinsicWidth(
                child: _CompactButton(
                  key: const ValueKey('hide-diagnostics'),
                  icon: Icons.visibility_off_outlined,
                  label: tr(context, 'Скрыть', 'Hide'),
                  onPressed: disabled ? null : onHide,
                ),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                tr(
                  context,
                  'Логи не очищаются при смене вкладки',
                  'Logs persist when switching sections',
                ),
                textAlign: TextAlign.end,
                maxLines: 2,
                style: const TextStyle(fontSize: 11, color: Color(0xff7a766e)),
              ),
            ),
          ],
        ),
      ],
    ),
  );
}

class ProfileDialog extends StatefulWidget {
  const ProfileDialog({super.key, required this.profile});
  final ProfileEditor profile;
  @override
  State<ProfileDialog> createState() => _ProfileDialogState();
}

class _ProfileDialogState extends State<ProfileDialog> {
  late final TextEditingController _name;
  late final TextEditingController _config;
  @override
  void initState() {
    super.initState();
    _name = TextEditingController(text: widget.profile.name);
    _config = TextEditingController(text: widget.profile.config);
  }

  @override
  void dispose() {
    _name.dispose();
    _config.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Dialog(
    backgroundColor: const Color(0xfff7f5f0),
    shape: RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(20),
      side: const BorderSide(color: Color(0xe6ffffff), width: 1.0),
    ),
    child: SizedBox(
      width: 680,
      height: 610,
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    widget.profile.id.isEmpty
                        ? tr(context, 'Новый профиль', 'New profile')
                        : tr(context, 'Изменить профиль', 'Edit profile'),
                    style: Theme.of(context).textTheme.headlineSmall,
                  ),
                ),
                IconButton(
                  onPressed: () => Navigator.pop(context),
                  tooltip: tr(context, 'Закрыть', 'Close'),
                  icon: const Icon(Icons.close_rounded),
                ),
              ],
            ),
            const SizedBox(height: 16),
            TextField(
              controller: _name,
              decoration: InputDecoration(
                labelText: tr(context, 'Название', 'Name'),
              ),
            ),
            const SizedBox(height: 13),
            Expanded(
              child: TextField(
                controller: _config,
                expands: true,
                minLines: null,
                maxLines: null,
                textAlignVertical: TextAlignVertical.top,
                style: const TextStyle(fontFamily: 'Consolas', fontSize: 12.5),
                decoration: InputDecoration(
                  labelText: tr(
                    context,
                    'AmneziaWG-конфигурация',
                    'AmneziaWG configuration',
                  ),
                  alignLabelWithHint: true,
                ),
              ),
            ),
            const SizedBox(height: 16),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                _PressableScale(
                  enabled: true,
                  child: TextButton(
                    onPressed: () => Navigator.pop(context),
                    child: Text(tr(context, 'Отмена', 'Cancel')),
                  ),
                ),
                const SizedBox(width: 8),
                _PressableScale(
                  enabled: true,
                  child: FilledButton.icon(
                    onPressed: () {
                      if (_name.text.trim().isEmpty ||
                          _config.text.trim().isEmpty) {
                        ScaffoldMessenger.of(context).showSnackBar(
                          SnackBar(
                            content: Text(
                              tr(
                                context,
                                'Укажите название и вставьте AWG-конфигурацию',
                                'Enter a name and paste the AWG configuration',
                              ),
                            ),
                          ),
                        );
                        return;
                      }
                      Navigator.pop(
                        context,
                        ProfileEditor(
                          id: widget.profile.id,
                          name: _name.text,
                          config: _config.text,
                          routing: widget.profile.routing,
                          overrides: widget.profile.overrides,
                        ),
                      );
                    },
                    icon: const Icon(Icons.check_rounded, size: 18),
                    label: Text(tr(context, 'Сохранить', 'Save')),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    ),
  );
}

class UpdateDialog extends StatefulWidget {
  const UpdateDialog({
    super.key,
    required this.info,
    required this.onInstall,
  });

  final UpdateInfo info;
  final Future<bool> Function() onInstall;

  @override
  State<UpdateDialog> createState() => _UpdateDialogState();
}

class _UpdateDialogState extends State<UpdateDialog> {
  bool _installing = false;
  String? _error;

  String _formatVersion(String v) {
    final trimmed = v.trim();
    if (trimmed.isEmpty) return 'v0.0.0';
    return trimmed.startsWith('v') ? trimmed : 'v$trimmed';
  }

  Future<void> _install() async {
    if (_installing) return;
    setState(() {
      _installing = true;
      _error = null;
    });
    try {
      final ok = await widget.onInstall();
      if (!mounted) return;
      Navigator.pop(context, ok);
    } catch (error) {
      if (!mounted) return;
      final text = error.toString();
      setState(() {
        _installing = false;
        _error = text.startsWith('Bad state: ') ? text.substring(11) : text;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final notes = widget.info.releaseNotes.trim();
    return Dialog(
      backgroundColor: const Color(0xfff7f5f0),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(20),
        side: const BorderSide(color: Color(0xe6ffffff), width: 1.0),
      ),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 500),
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Container(
                    width: 40,
                    height: 40,
                    decoration: BoxDecoration(
                      color: const Color(0xffdcf2eb),
                      borderRadius: BorderRadius.circular(12),
                      border: Border.all(color: const Color(0x4d14967b)),
                    ),
                    child: const Icon(
                      Icons.system_update_alt_rounded,
                      color: Color(0xff14967b),
                      size: 20,
                    ),
                  ),
                  const SizedBox(width: 14),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          tr(
                            context,
                            'Доступно обновление Ex WG',
                            'Ex WG update available',
                          ),
                          style: Theme.of(context).textTheme.headlineSmall,
                        ),
                        const SizedBox(height: 4),
                        Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 8,
                            vertical: 2,
                          ),
                          decoration: BoxDecoration(
                            color: const Color(0xffdcf2eb),
                            borderRadius: BorderRadius.circular(999),
                          ),
                          child: Text(
                            '${_formatVersion(widget.info.currentVersion)} → ${_formatVersion(widget.info.latestVersion)}',
                            style: const TextStyle(
                              color: Color(0xff0f7b63),
                              fontSize: 11.5,
                              fontWeight: FontWeight.w600,
                              fontFeatures: [FontFeature.tabularFigures()],
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                  IconButton(
                    onPressed: _installing ? null : () => Navigator.pop(context),
                    tooltip: tr(context, 'Закрыть', 'Close'),
                    icon: const Icon(Icons.close_rounded),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              Text(
                tr(
                  context,
                  'Установщик будет проверен по SHA-256 и установлен автоматически с сохранением всех профилей и правил маршрутизации.',
                  'The installer will be verified via SHA-256 and installed automatically while keeping all profiles and routing rules.',
                ),
                style: const TextStyle(
                  color: Color(0xff5c5952),
                  fontSize: 13.5,
                  height: 1.45,
                ),
              ),
              if (notes.isNotEmpty) ...[
                const SizedBox(height: 14),
                ConstrainedBox(
                  constraints: const BoxConstraints(maxHeight: 200),
                  child: Container(
                    padding: const EdgeInsets.all(14),
                    decoration: BoxDecoration(
                      color: const Color(0xffeae4d9),
                      border: Border.all(color: const Color(0xffd8d0c2)),
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: SingleChildScrollView(
                      child: SelectableText(
                        notes,
                        style: const TextStyle(
                          color: Color(0xff232527),
                          fontSize: 12.5,
                          height: 1.45,
                        ),
                      ),
                    ),
                  ),
                ),
              ],
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(
                  _error!,
                  style: const TextStyle(
                    color: Color(0xffd3455b),
                    fontSize: 12.5,
                    height: 1.4,
                  ),
                ),
              ],
              const SizedBox(height: 20),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  _PressableScale(
                    enabled: !_installing,
                    child: TextButton(
                      onPressed: _installing
                          ? null
                          : () => Navigator.pop(context, false),
                      child: Text(tr(context, 'Позже', 'Later')),
                    ),
                  ),
                  const SizedBox(width: 8),
                  _PressableScale(
                    enabled: !_installing,
                    child: FilledButton.icon(
                      key: const ValueKey('confirm-install-update'),
                      onPressed: _installing ? null : _install,
                      icon: _installing
                          ? const SizedBox.square(
                              dimension: 16,
                              child: CircularProgressIndicator(
                                strokeWidth: 2,
                                color: Colors.white,
                              ),
                            )
                          : const Icon(Icons.download_rounded, size: 18),
                      label: Text(
                        tr(
                          context,
                          'Скачать и установить',
                          'Download & install',
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _StartupError extends StatelessWidget {
  const _StartupError({required this.text});
  final String text;
  @override
  Widget build(BuildContext context) => Center(
    child: SizedBox(
      width: 620,
      child: GlassCard(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(
              Icons.error_outline_rounded,
              size: 44,
              color: Color(0xffd3455b),
            ),
            const SizedBox(height: 14),
            Text(
              tr(
                context,
                'Не удалось запустить VPN-ядро',
                'Could not start the VPN core',
              ),
              style: const TextStyle(
                fontSize: 20,
                fontWeight: FontWeight.w700,
                color: Color(0xff17181a),
              ),
            ),
            const SizedBox(height: 9),
            SelectableText(
              text,
              textAlign: TextAlign.center,
              style: const TextStyle(color: Color(0xff5c5952)),
            ),
            const SizedBox(height: 12),
            Text(
              tr(
                context,
                'Проверьте, что ExWGCore.exe находится рядом с ExWG.exe.',
                'Make sure ExWGCore.exe is next to ExWG.exe.',
              ),
              style: const TextStyle(fontSize: 12, color: Color(0xff7a766e)),
            ),
          ],
        ),
      ),
    ),
  );
}
