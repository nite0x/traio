import 'dart:io' show Platform;

import 'package:flutter/material.dart';

import 'core/backend_launcher.dart';
import 'desktop/app.dart';
import 'mobile/app.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  final isDesktop = Platform.isMacOS || Platform.isWindows || Platform.isLinux;

  if (isDesktop && _shouldAutoStartBackend()) {
    await _startBackend();
  }

  runApp(isDesktop ? const TraioDesktopApp() : const TraioMobileApp());
}

bool _shouldAutoStartBackend() {
  final value = Platform.environment['TRAIO_SKIP_BACKEND_AUTO_START'];
  return value != '1' && value?.toLowerCase() != 'true';
}

Future<void> _startBackend() async {
  try {
    await BackendLauncher.ensureStarted();
  } catch (e) {
    debugPrint('backend launch: $e');
  }
}
