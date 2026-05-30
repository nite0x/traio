import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/api_client.dart';
import '../core/backend_launcher.dart';
import '../core/theme.dart';

/// Process management for traio-server and IBKR Gateway.
class ServiceControlsPanel extends ConsumerStatefulWidget {
  const ServiceControlsPanel({super.key});

  @override
  ConsumerState<ServiceControlsPanel> createState() => _ServiceControlsPanelState();
}

class _ServiceControlsPanelState extends ConsumerState<ServiceControlsPanel> {
  Timer? _timer;
  var _busy = false;
  var _serverRunning = false;
  Map<String, dynamic>? _serverInfo;
  Map<String, dynamic>? _gatewayStatus;

  @override
  void initState() {
    super.initState();
    _refresh();
    _timer = Timer.periodic(const Duration(seconds: 3), (_) => _refresh());
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    final running = await BackendLauncher.isServerRunning();
    Map<String, dynamic>? serverInfo;
    Map<String, dynamic>? gw;

    if (running) {
      try {
        serverInfo = await ref.read(apiClientProvider).serverStatus();
      } catch (_) {}
      try {
        gw = await ref.read(apiClientProvider).ibkrGatewayStatus();
      } catch (_) {}
    }

    if (!mounted) return;
    setState(() {
      _serverRunning = running;
      _serverInfo = serverInfo;
      _gatewayStatus = gw;
    });
  }

  Future<void> _run(Future<void> Function() action, String ok) async {
    setState(() => _busy = true);
    try {
      await action();
      refreshBackendEndpoint(ref);
      ref.invalidate(ibkrGatewayStatusProvider);
      await Future<void>.delayed(const Duration(milliseconds: 500));
      await _refresh();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(ok)));
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('操作失败: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _startServer() => _run(() async {
        await BackendLauncher.startServer();
      }, 'traio-server 已启动');

  Future<void> _stopServer() => _run(() async {
        try {
          await ref.read(apiClientProvider).serverShutdown();
        } catch (_) {}
        await BackendLauncher.stopServer();
      }, 'traio-server 已停止');

  Future<void> _startGateway() => _run(() async {
        await ref.read(apiClientProvider).ibkrGatewayStart();
        await Future<void>.delayed(const Duration(seconds: 2));
      }, 'IBKR Gateway 启动中');

  Future<void> _stopGateway() => _run(() async {
        await ref.read(apiClientProvider).ibkrGatewayStop();
        await Future<void>.delayed(const Duration(seconds: 1));
      }, 'IBKR Gateway 已停止');

  @override
  Widget build(BuildContext context) {
    final gwRunning = _gatewayStatus?['running'] == true;
    final gwAuth = _gatewayStatus?['authenticated'] == true;
    final gwAccount = _gatewayStatus?['account']?.toString() ?? '';
    final pid = _serverInfo?['pid']?.toString() ?? '';
    final apiUrl = _serverInfo?['api_url']?.toString() ?? '';

    String gwLabel;
    if (!_serverRunning) {
      gwLabel = '未知';
    } else if (!gwRunning) {
      gwLabel = '已停止';
    } else if (gwAuth) {
      gwLabel = gwAccount.isNotEmpty ? '已连接 $gwAccount' : '已认证';
    } else {
      gwLabel = '运行中，待登录';
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _hint('关闭 Traio 窗口后 traio-server 与 Gateway 继续在后台运行'),
        _serviceRow(
          context,
          name: 'traio-server',
          status: _serverRunning ? '运行中${pid.isNotEmpty ? ' (PID $pid)' : ''}' : '已停止',
          statusColor: _serverRunning ? TraioTheme.up : TraioTheme.textMuted,
          detail: apiUrl.isNotEmpty ? apiUrl : null,
          canStart: !_serverRunning,
          canStop: _serverRunning,
          onStart: _startServer,
          onStop: _stopServer,
        ),
        const SizedBox(height: 8),
        _serviceRow(
          context,
          name: 'IBKR Gateway',
          status: gwLabel,
          statusColor: gwAuth
              ? TraioTheme.up
              : gwRunning
                  ? TraioTheme.warn
                  : TraioTheme.textMuted,
          canStart: _serverRunning && !gwRunning,
          canStop: _serverRunning && gwRunning,
          onStart: _startGateway,
          onStop: _stopGateway,
          disabled: !_serverRunning,
        ),
      ],
    );
  }

  Widget _hint(String text) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Text(text, style: TraioTheme.mono(context, size: 11, color: TraioTheme.textMuted)),
    );
  }

  Widget _serviceRow(
    BuildContext context, {
    required String name,
    required String status,
    required Color statusColor,
    String? detail,
    required bool canStart,
    required bool canStop,
    required VoidCallback onStart,
    required VoidCallback onStop,
    bool disabled = false,
  }) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: TraioTheme.surface,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: TraioTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(name, style: TraioTheme.mono(context, size: 12)),
                    const SizedBox(height: 4),
                    Text(status, style: TraioTheme.mono(context, size: 11, color: statusColor)),
                    if (detail != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 2),
                        child: Text(detail, style: TraioTheme.mono(context, size: 10, color: TraioTheme.textMuted)),
                      ),
                  ],
                ),
              ),
              OutlinedButton(
                onPressed: (_busy || disabled || !canStart) ? null : onStart,
                child: const Text('启动'),
              ),
              const SizedBox(width: 8),
              OutlinedButton(
                onPressed: (_busy || disabled || !canStop) ? null : onStop,
                child: const Text('停止'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
