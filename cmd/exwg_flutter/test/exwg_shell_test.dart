import 'package:exwg_windows/core_client.dart';
import 'package:exwg_windows/main.dart';
import 'package:exwg_windows/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUpAll(() async {
    final defaultComparator = goldenFileComparator;
    if (defaultComparator is LocalFileComparator) {
      goldenFileComparator = _TolerantGoldenComparator(
        defaultComparator.basedir.resolve('exwg_shell_test.dart'),
        maxDiffPercent: .02,
      );
    }
    await (FontLoader(
      'ExWGSans',
    )..addFont(rootBundle.load('assets/fonts/Roboto-Regular.ttf'))).load();
    await (FontLoader(
      'MaterialIcons',
    )..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'))).load();
  });

  testWidgets('shell keeps stable navigation and fixed connection orb', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1220, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      ExWGApp(
        backend: PreviewBackend(delay: Duration.zero),
        initialLanguage: AppLanguage.ru,
        persistLanguage: false,
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Ex WG'), findsOneWidget);
    expect(find.byKey(const ValueKey('sidebar-version')), findsOneWidget);
    expect(find.text('v0.0.0'), findsOneWidget);
    expect(find.text('Диагностика'), findsNothing);
    expect(find.text('Подключение'), findsOneWidget);
    expect(tester.getSize(find.byKey(const ValueKey('sidebar'))).width, 224);
    expect(tester.getSize(find.byType(ConnectionOrb)), const Size(184, 184));
    expect(
      tester.getSize(find.byKey(const ValueKey('profiles-panel'))).height,
      340,
    );
    expect(find.byKey(const ValueKey('traffic-summary')), findsOneWidget);
    expect(find.text('Добавить .conf / .zip'), findsOneWidget);
    expect(find.text('Добавить QR'), findsOneWidget);
    expect(find.text('Ex WG Main'), findsNWidgets(2));
    expect(find.text('Reserve'), findsOneWidget);
    expect(find.text('AmneziaWG 3.1'), findsOneWidget);
    expect(find.text('AmneziaWG 2.1'), findsOneWidget);
    expect(find.byKey(const ValueKey('edit-nexus-1')), findsOneWidget);
    expect(find.byKey(const ValueKey('delete-nexus-1')), findsOneWidget);
    expect(find.byTooltip('Изменить'), findsNWidgets(2));
    expect(find.byTooltip('Удалить'), findsNWidgets(2));
    expect(find.byIcon(Icons.file_download_outlined), findsNothing);

    await tester.tap(find.text('Правила'));
    await tester.pumpAndSettle();
    expect(find.text('Правила / Маршрутизация'), findsOneWidget);
    expect(tester.getSize(find.byKey(const ValueKey('sidebar'))).width, 224);

    for (var i = 0; i < 5; i++) {
      await tester.tap(find.byKey(const ValueKey('sidebar-version')));
    }
    await tester.pumpAndSettle();
    expect(find.text('Диагностика'), findsOneWidget);
    expect(find.text('Диагностика / Debug'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('hide-diagnostics')));
    await tester.pumpAndSettle();
    expect(find.text('Диагностика'), findsNothing);
    expect(find.text('Подключение'), findsOneWidget);

    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('profile hot switch keeps its position', (tester) async {
    tester.view.physicalSize = const Size(1080, 720);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final backend = PreviewBackend(delay: Duration.zero);

    await tester.pumpWidget(
      ExWGApp(
        backend: backend,
        initialLanguage: AppLanguage.ru,
        persistLanguage: false,
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Reserve'));
    await tester.pumpAndSettle();

    expect(find.text('Ex WG Main'), findsOneWidget);
    expect(find.text('Reserve'), findsNWidgets(2));
    final firstProfile = tester.getTopLeft(find.text('Ex WG Main')).dy;
    final secondProfile = tester.getTopLeft(find.text('Reserve').last).dy;
    expect(firstProfile, lessThan(secondProfile));
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('routing rule can move from Direct to Proxy', (tester) async {
    tester.view.physicalSize = const Size(1220, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final backend = PreviewBackend(delay: Duration.zero);

    await tester.pumpWidget(
      ExWGApp(
        backend: backend,
        initialLanguage: AppLanguage.ru,
        persistLanguage: false,
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Правила'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('Direct'));
    await tester.tap(find.text('Direct'));
    await tester.pumpAndSettle();
    final apple = find.byKey(const ValueKey('routing-rule-geosite:apple'));
    await tester.ensureVisible(apple);
    await tester.tap(
      find.descendant(
        of: apple,
        matching: find.byType(PopupMenuButton<String>),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Proxy').last);
    await tester.pumpAndSettle();

    expect(backend.lastSavedOverrides, contains('proxy geosite:apple'));
    expect(backend.lastSavedOverrides, isNot(contains('direct geosite:apple')));
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('render home preview', (tester) async {
    tester.view.physicalSize = const Size(1220, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      RepaintBoundary(
        child: ExWGApp(
          backend: PreviewBackend(delay: Duration.zero),
          initialLanguage: AppLanguage.ru,
          persistLanguage: false,
        ),
      ),
    );
    await tester.pumpAndSettle();
    await expectLater(
      find.byType(ExWGShell),
      matchesGoldenFile('goldens/exwg-home.png'),
    );
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('render routing package details', (tester) async {
    tester.view.physicalSize = const Size(1220, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      RepaintBoundary(
        child: ExWGApp(
          backend: PreviewBackend(delay: Duration.zero),
          initialLanguage: AppLanguage.ru,
          persistLanguage: false,
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Правила'));
    await tester.pumpAndSettle();
    expect(find.text('Состав routing-пакета'), findsOneWidget);
    expect(find.text('GeoSite'), findsOneWidget);
    expect(find.text('GeoIP'), findsOneWidget);
    expect(find.text('Block / Proxy / Direct'), findsOneWidget);
    expect(find.text('Домен, затем IP'), findsOneWidget);
    expect(find.textContaining('FakeDNS'), findsNothing);
    expect(find.textContaining('Global proxy'), findsNothing);
    expect(find.textContaining('полный DAT'), findsNothing);
    expect(find.text('Совместимость и дополнительные поля'), findsNothing);
    await expectLater(
      find.byType(ExWGShell),
      matchesGoldenFile('goldens/exwg-rules.png'),
    );
    await tester.ensureVisible(
      find.text('Локальные правила для всех конфигураций'),
    );
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('new-routing-rule')), findsOneWidget);
    expect(find.byKey(const ValueKey('add-routing-rule')), findsOneWidget);
    await expectLater(
      find.byType(ExWGShell),
      matchesGoldenFile('goldens/exwg-rules-editor.png'),
    );
    await tester.ensureVisible(find.text('Direct'));
    await tester.tap(find.text('Direct'));
    await tester.pumpAndSettle();
    expect(find.textContaining('geosite:private'), findsOneWidget);

    await tester.ensureVisible(find.byKey(const ValueKey('routing-search')));
    await tester.enterText(
      find.byKey(const ValueKey('routing-search')),
      'github',
    );
    await tester.pumpAndSettle();
    expect(find.text('geosite:github'), findsOneWidget);
    expect(find.text('geosite:private'), findsNothing);

    await tester.ensureVisible(
      find.byKey(const ValueKey('route-check-domain')),
    );
    await tester.enterText(
      find.byKey(const ValueKey('route-check-domain')),
      'updates.cdn-apple.com',
    );
    final routeCheckButton = find.widgetWithText(FilledButton, 'Проверить');
    await tester.ensureVisible(routeCheckButton);
    await tester.pumpAndSettle();
    await tester.tap(routeCheckButton);
    await tester.pumpAndSettle();
    expect(find.textContaining('updates.cdn-apple.com'), findsWidgets);
    expect(find.textContaining('140.82.121.4'), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('minimum window size has no layout overflow', (tester) async {
    tester.view.physicalSize = const Size(980, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      ExWGApp(
        backend: PreviewBackend(delay: Duration.zero),
        initialLanguage: AppLanguage.ru,
        persistLanguage: false,
      ),
    );
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(
      tester.getSize(find.byKey(const ValueKey('profiles-panel'))).height,
      274,
    );

    await tester.tap(find.text('Правила'));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);

    for (var i = 0; i < 5; i++) {
      await tester.tap(find.byKey(const ValueKey('sidebar-version')));
    }
    await tester.pump();
    await tester.pump();
    expect(tester.takeException(), isNull);

    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('language switch translates the complete shell', (tester) async {
    tester.view.physicalSize = const Size(1220, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      ExWGApp(
        backend: PreviewBackend(delay: Duration.zero),
        initialLanguage: AppLanguage.ru,
        persistLanguage: false,
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.text('EN'));
    await tester.pumpAndSettle();
    expect(find.text('Connection'), findsOneWidget);
    expect(find.text('Profiles'), findsOneWidget);
    expect(find.text('Home'), findsOneWidget);
    expect(find.text('Rules'), findsOneWidget);
    expect(find.text('Diagnostics'), findsNothing);
    await expectLater(
      find.byType(ExWGShell),
      matchesGoldenFile('goldens/exwg-home-en.png'),
    );

    await tester.tap(find.text('Rules'));
    await tester.pumpAndSettle();
    expect(find.text('Rules / Routing'), findsOneWidget);
    expect(find.text('Routing package contents'), findsOneWidget);

    for (var i = 0; i < 5; i++) {
      await tester.tap(find.byKey(const ValueKey('sidebar-version')));
    }
    await tester.pumpAndSettle();
    expect(find.text('Diagnostics'), findsOneWidget);
    expect(find.text('Diagnostics / Debug'), findsOneWidget);
    expect(find.text('Run check'), findsOneWidget);
    expect(find.text('Save TXT'), findsOneWidget);
    expect(find.text('Hide'), findsOneWidget);

    await tester.tap(find.text('RU'));
    await tester.pumpAndSettle();
    expect(find.text('Диагностика / Debug'), findsOneWidget);
    expect(find.text('Скрыть'), findsOneWidget);

    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('in-app update badge opens update dialog and installs update', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1220, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final backend = PreviewBackend(delay: Duration.zero)
      ..previewUpdate = const UpdateInfo(
        available: true,
        currentVersion: '0.0.0',
        latestVersion: '1.0.1',
        releaseNotes: '• Быстрое обновление в 1 клик',
        assetName: 'Ex-WG-Setup-1.0.1.exe',
        downloadUrl:
            'https://github.com/euloxe-dev/windows-ExWG/releases/download/v1.0.1/Ex-WG-Setup-1.0.1.exe',
        releasePageUrl:
            'https://github.com/euloxe-dev/windows-ExWG/releases/tag/v1.0.1',
        sha256:
            '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
        publishedAt: '2026-11-08T00:00:00Z',
      );

    await tester.pumpWidget(
      ExWGApp(
        backend: backend,
        initialLanguage: AppLanguage.ru,
        persistLanguage: false,
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('check-updates-button')));
    await tester.pumpAndSettle();

    expect(find.text('Доступно обновление Ex WG'), findsOneWidget);
    expect(find.textContaining('1.0.1'), findsWidgets);
    expect(find.textContaining('Быстрое обновление в 1 клик'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('confirm-install-update')));
    await tester.pumpAndSettle();

    expect(backend.updateInstalled, isTrue);
    expect(find.byKey(const ValueKey('update-available-badge')), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
  });
}

class _TolerantGoldenComparator extends LocalFileComparator {
  _TolerantGoldenComparator(super.testFile, {required this.maxDiffPercent});

  final double maxDiffPercent;

  @override
  Future<bool> compare(Uint8List imageBytes, Uri golden) async {
    final result = await GoldenFileComparator.compareLists(
      imageBytes,
      await getGoldenBytes(golden),
    );
    if (result.diffPercent <= maxDiffPercent) {
      result.dispose();
      return true;
    }
    final error = await generateFailureOutput(result, golden, basedir);
    result.dispose();
    throw FlutterError(error);
  }
}
