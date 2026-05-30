import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:path/path.dart' as p;

/// Starts and connects to the Traio Go backend (traio-server).
/// The server runs detached and survives app quit.
class BackendLauncher {
  BackendLauncher._();

  static const _defaultApiBaseUrl = 'http://127.0.0.1:38180';

  static String? _apiBaseUrl;

  /// Current backend base URL (from server.json or fallback).
  static String get apiBaseUrl {
    final ep = _readEndpoint();
    if (ep != null) {
      _apiBaseUrl = ep.apiUrl;
      return _apiBaseUrl!;
    }
    if (_apiBaseUrl != null) return _apiBaseUrl!;
    return _defaultApiBaseUrl;
  }

  static String get runtimeDir => _runtimeDir();

  static Future<bool> isServerRunning() async {
    final ep = _readEndpoint();
    if (ep != null && await _isHealthy(ep.apiUrl)) {
      _apiBaseUrl = ep.apiUrl;
      return true;
    }
    if (await _isHealthy(_defaultApiBaseUrl)) {
      _apiBaseUrl = _defaultApiBaseUrl;
      return true;
    }
    return false;
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

    final ep = await _waitForEndpoint(const Duration(seconds: 20));
    if (ep == null || !await _isHealthy(ep.apiUrl)) {
      throw StateError('Traio 后端启动超时');
    }
    _apiBaseUrl = ep.apiUrl;
  }

  /// Stops traio-server via API, or kills by PID file.
  static Future<void> stopServer() async {
    final ep = _readEndpoint();
    if (ep != null && await _isHealthy(ep.apiUrl)) {
      try {
        final client = HttpClient();
        final req = await client
            .postUrl(Uri.parse('${ep.apiUrl}/api/v1/server/shutdown'));
        await req.close().timeout(const Duration(seconds: 3));
        client.close();
        await _waitForServerDown(const Duration(seconds: 10));
        _apiBaseUrl = null;
        return;
      } catch (_) {}
    }
    await _killByPidFile();
    _apiBaseUrl = null;
  }

  static String binaryPath(String name) => _resolveBinary(name);

  static String _runtimeDir() {
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

  static String _endpointPath() => p.join(_runtimeDir(), 'server.json');

  static String _pidPath() => p.join(_runtimeDir(), 'traio-server.pid');

  static _Endpoint? _readEndpoint() {
    try {
      final file = File(_endpointPath());
      if (!file.existsSync()) return null;
      final map = jsonDecode(file.readAsStringSync()) as Map<String, dynamic>;
      final host = map['host']?.toString() ?? '127.0.0.1';
      final port = map['port'] as int? ?? 0;
      final apiUrl = map['api_url']?.toString() ?? 'http://$host:$port';
      return _Endpoint(host: host, port: port, apiUrl: apiUrl);
    } catch (_) {
      return null;
    }
  }

  static Future<_Endpoint?> _waitForEndpoint(Duration timeout) async {
    final deadline = DateTime.now().add(timeout);
    while (DateTime.now().isBefore(deadline)) {
      final ep = _readEndpoint();
      if (ep != null && await _isHealthy(ep.apiUrl)) return ep;
      await Future<void>.delayed(const Duration(milliseconds: 200));
    }
    return null;
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
      File(_endpointPath()).deleteSync();
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

class _Endpoint {
  const _Endpoint(
      {required this.host, required this.port, required this.apiUrl});
  final String host;
  final int port;
  final String apiUrl;
}
