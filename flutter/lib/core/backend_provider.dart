import 'dart:io' show Platform;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'embedded_backend.dart';

/// Boots the in-process Go backend on iOS and exposes its loopback base URL.
///
/// Mobile v1 is embedded-only: no remote server mode. IBKR and related
/// capabilities are unavailable on this build.
final embeddedBackendProvider = FutureProvider<String>((ref) async {
  if (kIsWeb || !Platform.isIOS) {
    throw UnsupportedError(
      'Traio mobile v1 requires iOS with the embedded Go backend',
    );
  }
  await EmbeddedBackend.start();
  return EmbeddedBackend.apiBaseUrl;
});
