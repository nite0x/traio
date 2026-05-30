import 'package:flutter/services.dart';
import 'package:path_provider/path_provider.dart';

/// Starts the Traio Go backend in-process on mobile (iOS/Android).
///
/// Unlike desktop, where [BackendLauncher] spawns the `traio-server` binary as a
/// separate process, mobile platforms forbid launching external executables.
/// The Go backend is instead compiled into a gomobile library (Traio.xcframework
/// on iOS) that runs an HTTP server inside the app process. A small native
/// MethodChannel handler calls `Traio.StartServer(dataDir)` and returns the
/// loopback port it chose.
class EmbeddedBackend {
  static const _channel = MethodChannel('traio/backend');

  static int? _port;

  /// Whether the embedded backend has been started.
  static bool get isStarted => _port != null;

  /// The base URL of the in-process backend, e.g. http://127.0.0.1:54213.
  /// Valid only after [start] completes.
  static String get apiBaseUrl => 'http://127.0.0.1:${_port ?? 0}';

  /// Boot the in-process backend, passing the app's writable documents dir for
  /// the SQLite database. Idempotent: the native side returns the existing port
  /// if already running.
  static Future<void> start() async {
    if (_port != null) return;
    final dir = await getApplicationDocumentsDirectory();
    final port = await _channel.invokeMethod<int>('start', {
      'dataDir': dir.path,
    });
    if (port == null || port == 0) {
      throw StateError('embedded backend returned an invalid port');
    }
    _port = port;
  }
}
