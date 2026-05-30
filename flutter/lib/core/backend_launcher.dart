import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:path/path.dart' as p;

/// Starts and connects to the Traio Go backend (traio-server).
/// The server runs detached and survives app quit.
class BackendLauncher {
  BackendLauncher._();

  static const _defaultApiBaseUrl = 'http://127.0.0.1:38180';

  /// Current backend base URL. Dev and desktop always use the fixed local port.
  static String get apiBaseUrl => _defaultApiBaseUrl;

  static String get runtimeDir => _runtimeDir();

  static Future<bool> isServerRunning() async {
    return _isHealthy(_defaultApiBaseUrl);
  }

  static Future<void> ensureStarted() async {
    if (await isServerRunning()) {
      return;
    }
    await startServer();
  }

  /// Starts traio-server as a detached background process.
  static Future<void> startServer() async {
    if (await isServerRunning()) {
      return;
    }

    final bin = _resolveBinary('traio-server');
    final workDir = _resolveWorkDir();
    final runtimeDir = _runtimeDir();

    if (!File(bin).existsSync()) {
      throw StateError(
        '找不到 traio-server: $bin\n'
        '请先运行: make build-binaries',
      );
    }

    Directory(runtimeDir).createSync(recursive: true);

    await Process.start(
      bin,
      const [],
      workingDirectory: workDir,
      environment: {
        ...Platform.environment,
        'TRAIO_RUNTIME_DIR': runtimeDir,
      },
      mode: ProcessStartMode.detached,
    );

    if (!await _waitForHealthy(const Duration(seconds: 20))) {
      throw StateError('Traio 后端启动超时');
    }
  }

  /// Stops traio-server via API, or kills by PID file.
  static Future<void> stopServer() async {
    if (await _isHealthy(_defaultApiBaseUrl)) {
      try {
        final client = HttpClient();
        client.findProxy = (uri) => 'DIRECT';
        final req = await client
            .postUrl(Uri.parse('$_defaultApiBaseUrl/api/v1/server/shutdown'));
        await req.close().timeout(const Duration(seconds: 3));
        client.close();
        await _waitForServerDown(const Duration(seconds: 10));
        return;
      } catch (_) {}
    }
    await _killByPidFile();
  }

  static String binaryPath(String name) => _resolveBinary(name);

  static String _runtimeDir() {
    final env = Platform.environment['TRAIO_RUNTIME_DIR'];
    if (env != null && env.isNotEmpty) {
      return env;
    }
    if (Platform.isMacOS || Platform.isLinux) {
      final home = Platform.environment['HOME'];
      if (home != null && home.isNotEmpty) {
        return p.join(home, 'Library', 'Application Support', 'Traio');
      }
    }
    if (Platform.isWindows) {
      final appData = Platform.environment['APPDATA'];
      if (appData != null && appData.isNotEmpty) {
        return p.join(appData, 'Traio');
      }
    }
    return p.normalize(p.join(Directory.current.path, 'traio-data'));
  }

  static String _pidPath() => p.join(_runtimeDir(), 'traio-server.pid');

  static Future<bool> _waitForHealthy(Duration timeout) async {
    final deadline = DateTime.now().add(timeout);
    while (DateTime.now().isBefore(deadline)) {
      if (await _isHealthy(_defaultApiBaseUrl)) return true;
      await Future<void>.delayed(const Duration(milliseconds: 200));
    }
    return false;
  }

  static Future<void> _waitForServerDown(Duration timeout) async {
    final deadline = DateTime.now().add(timeout);
    while (DateTime.now().isBefore(deadline)) {
      if (!await isServerRunning()) return;
      await Future<void>.delayed(const Duration(milliseconds: 300));
    }
  }

  static Future<void> _killByPidFile() async {
    try {
      final pid = int.parse(File(_pidPath()).readAsStringSync().trim());
      final proc = await Process.start('kill', [pid.toString()]);
      await proc.exitCode;
    } catch (_) {}
    try {
      File(_pidPath()).deleteSync();
    } catch (_) {}
  }

  static String _resolveBinary(String name) {
    final envKey = name == 'traio-server'
        ? 'TRAIO_SERVER_BIN'
        : name == 'traio-mcp'
            ? 'TRAIO_MCP_BIN'
            : 'TRAIO_${name.replaceAll('-', '_').toUpperCase()}_BIN';
    final env = Platform.environment[envKey];
    if (env != null && env.isNotEmpty) {
      return env;
    }

    if (Platform.isMacOS) {
      final exe = Platform.resolvedExecutable;
      final resources =
          p.normalize(p.join(p.dirname(exe), '..', 'Resources', name));
      if (File(resources).existsSync()) {
        return resources;
      }
    }

    final candidates = <String>[
      p.normalize(p.join(Directory.current.path, '..', 'bin', name)),
      p.normalize(p.join(Directory.current.path, 'bin', name)),
      p.normalize(p.join(Directory.current.path, '..', '..', 'bin', name)),
    ];
    for (final c in candidates) {
      if (File(c).existsSync()) return c;
    }
    return candidates.first;
  }

  static String _resolveWorkDir() {
    if (Platform.isMacOS) {
      final exe = Platform.resolvedExecutable;
      final resources = p.normalize(p.join(p.dirname(exe), '..', 'Resources'));
      if (Directory(resources).existsSync()) {
        return resources;
      }
    }
    return p.normalize(p.join(Directory.current.path, '..'));
  }

  static Future<bool> _isHealthy(String base) async {
    try {
      final client = HttpClient();
      client.findProxy = (uri) => 'DIRECT';
      final req = await client.getUrl(Uri.parse('$base/health'));
      final resp = await req.close().timeout(const Duration(seconds: 2));
      final body = await resp.transform(utf8.decoder).join();
      client.close();
      if (resp.statusCode != 200) return false;
      final map = jsonDecode(body) as Map<String, dynamic>;
      return map['service']?.toString() == 'traio';
    } catch (_) {
      return false;
    }
  }
}
