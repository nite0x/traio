import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/api_client.dart';
import '../core/theme.dart';
import 'service_controls.dart';

class SettingsPage extends ConsumerStatefulWidget {
  const SettingsPage({super.key});

  @override
  ConsumerState<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends ConsumerState<SettingsPage> {
  final _formKey = GlobalKey<FormState>();
  bool _loading = true;
  bool _saving = false;
  String? _error;

  final _controllers = <String, TextEditingController>{};

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    for (final c in _controllers.values) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final data = await ref.read(apiClientProvider).getSettings();
      _applySettings(data);
    } catch (e) {
      _error = '$e';
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _loadDefaults() async {
    try {
      final data = await ref.read(apiClientProvider).getSettingsDefaults();
      _applySettings(data);
      if (mounted) {
        setState(() {});
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('已恢复默认值（未保存）')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('加载默认值失败: $e')),
        );
      }
    }
  }

  void _applySettings(Map<String, dynamic> data) {
    for (final c in _controllers.values) {
      c.dispose();
    }
    _controllers.clear();

    void reg(String key, dynamic value, {bool obscure = false}) {
      _controllers[key] = TextEditingController(text: value?.toString() ?? '');
    }

    final server = data['server'] as Map? ?? {};
    reg('server.host', server['host']);
    reg('server.port', server['port']);

    final db = data['database'] as Map? ?? {};
    reg('database.path', db['path']);

    final snap = data['snaptrade'] as Map? ?? {};
    reg('snaptrade.client_id', snap['client_id']);
    reg('snaptrade.consumer_key', snap['consumer_key']);

    final schwab = data['schwab'] as Map? ?? {};
    reg('schwab.client_id', schwab['client_id']);
    reg('schwab.client_secret', schwab['client_secret'], obscure: true);
    reg('schwab.redirect_uri', schwab['redirect_uri']);

    final ibkr = data['ibkr'] as Map? ?? {};
    reg('ibkr.sub_account', ibkr['sub_account']);
    reg('ibkr.password', ibkr['password'], obscure: true);
    reg('ibkr.totp_secret', ibkr['totp_secret'], obscure: true);
    reg('ibkr.gateway_dir', ibkr['gateway_dir']);
    reg('ibkr.bundled_gateway_dir', ibkr['bundled_gateway_dir']);
    reg('ibkr.gateway_port', ibkr['gateway_port']);
    reg('ibkr.gateway_url', ibkr['gateway_url']);
    reg('ibkr.download_proxy', ibkr['download_proxy']);

    final finnhub = data['finnhub'] as Map? ?? {};
    reg('finnhub.api_key', finnhub['api_key'], obscure: true);

    final claude = data['claude'] as Map? ?? {};
    reg('claude.api_key', claude['api_key'], obscure: true);
    reg('claude.model', claude['model']);
  }

  Map<String, dynamic> _buildPayload() {
    int? port(String key) => int.tryParse(_controllers[key]?.text ?? '');

    return {
      'server': {
        'host': _controllers['server.host']!.text.trim(),
        'port': port('server.port') ?? 0,
      },
      'database': {
        'path': _controllers['database.path']!.text.trim(),
      },
      'snaptrade': {
        'client_id': _controllers['snaptrade.client_id']!.text.trim(),
        'consumer_key': _controllers['snaptrade.consumer_key']!.text.trim(),
      },
      'schwab': {
        'client_id': _controllers['schwab.client_id']!.text.trim(),
        'client_secret': _controllers['schwab.client_secret']!.text.trim(),
        'redirect_uri': _controllers['schwab.redirect_uri']!.text.trim(),
      },
      'ibkr': {
        'sub_account': _controllers['ibkr.sub_account']!.text.trim(),
        'password': _controllers['ibkr.password']!.text.trim(),
        'totp_secret': _controllers['ibkr.totp_secret']!.text.trim(),
        'gateway_dir': _controllers['ibkr.gateway_dir']!.text.trim(),
        'bundled_gateway_dir': _controllers['ibkr.bundled_gateway_dir']!.text.trim(),
        'gateway_port': port('ibkr.gateway_port') ?? 5680,
        'gateway_url': _controllers['ibkr.gateway_url']!.text.trim(),
        'download_proxy': _controllers['ibkr.download_proxy']!.text.trim(),
      },
      'finnhub': {
        'api_key': _controllers['finnhub.api_key']!.text.trim(),
      },
      'claude': {
        'api_key': _controllers['claude.api_key']!.text.trim(),
        'model': _controllers['claude.model']!.text.trim(),
      },
    };
  }

  Future<void> _save() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() => _saving = true);
    try {
      await ref.read(apiClientProvider).putSettings(_buildPayload());
      ref.invalidate(ibkrGatewayStatusProvider);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('设置已保存')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('保存失败: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: TraioTheme.bg,
      appBar: AppBar(
        backgroundColor: TraioTheme.surface,
        title: Text('设置', style: TraioTheme.mono(context)),
        actions: [
          TextButton(onPressed: _loading ? null : _loadDefaults, child: const Text('恢复默认')),
          TextButton(
            onPressed: _saving || _loading ? null : _save,
            child: _saving
                ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
                : const Text('保存'),
          ),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator(strokeWidth: 2))
          : _error != null
              ? Center(child: Text(_error!, style: const TextStyle(color: TraioTheme.down)))
              : Form(
                  key: _formKey,
                  child: ListView(
                    padding: const EdgeInsets.all(16),
                    children: [
                      _hint('所有配置保存在本地数据库，无需 config.yaml'),
                      _section('后台服务', const [
                        ServiceControlsPanel(),
                      ]),
                      _section('服务', [
                        _field('server.host', '监听地址', hint: '127.0.0.1'),
                        _field('server.port', '端口', hint: '0 = 随机', numbersOnly: true),
                        _hint('端口 0 表示随机可用端口，避免冲突；修改后需在「后台服务」中重启 traio-server'),
                      ]),
                      _section('数据库', [
                        _field('database.path', 'SQLite 路径'),
                        _hint('修改数据库路径后需在「后台服务」中重启 traio-server'),
                      ]),
                      _section('IBKR', [
                        _hint('留空账号/密码/TOTP → Gateway 启动后浏览器手动登录'),
                        _field('ibkr.sub_account', '子账号'),
                        _field('ibkr.password', '密码', obscure: true),
                        _field('ibkr.totp_secret', 'TOTP Secret', obscure: true),
                        _field('ibkr.gateway_port', 'Gateway 端口', hint: '5680', numbersOnly: true),
                        _field('ibkr.gateway_url', 'Gateway URL', hint: 'https://localhost:5680'),
                        _field('ibkr.bundled_gateway_dir', '捆绑 Gateway 目录'),
                        _field('ibkr.gateway_dir', '运行时 Gateway 目录'),
                        _field('ibkr.download_proxy', '下载代理', hint: 'http://127.0.0.1:7897'),
                      ]),
                      _section('Schwab', [
                        _field('schwab.client_id', 'Client ID'),
                        _field('schwab.client_secret', 'Client Secret', obscure: true),
                        _field('schwab.redirect_uri', 'Redirect URI', hint: 'https://127.0.0.1:8182'),
                      ]),
                      _section('SnapTrade', [
                        _field('snaptrade.client_id', 'Client ID'),
                        _field('snaptrade.consumer_key', 'Consumer Key'),
                      ]),
                      _section('Finnhub', [
                        _field('finnhub.api_key', 'API Key', obscure: true),
                      ]),
                      _section('Claude', [
                        _field('claude.api_key', 'API Key', obscure: true),
                        _field('claude.model', 'Model', hint: 'claude-sonnet-4-20250514'),
                      ]),
                      const SizedBox(height: 32),
                    ],
                  ),
                ),
    );
  }

  Widget _section(String title, List<Widget> children) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SizedBox(height: 16),
        Text(title, style: TraioTheme.mono(context, size: 13, color: TraioTheme.textMuted)),
        const SizedBox(height: 8),
        ...children,
      ],
    );
  }

  Widget _hint(String text) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Text(text, style: TraioTheme.mono(context, size: 11, color: TraioTheme.textMuted)),
    );
  }

  Widget _field(
    String key,
    String label, {
    String? hint,
    bool obscure = false,
    bool numbersOnly = false,
  }) {
    final ctrl = _controllers[key];
    if (ctrl == null) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: TextFormField(
        controller: ctrl,
        obscureText: obscure,
        style: TraioTheme.mono(context, size: 12),
        decoration: InputDecoration(
          labelText: label,
          hintText: hint,
          filled: true,
          fillColor: TraioTheme.surface,
          border: OutlineInputBorder(borderRadius: BorderRadius.circular(6), borderSide: const BorderSide(color: TraioTheme.border)),
          enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(6), borderSide: const BorderSide(color: TraioTheme.border)),
          labelStyle: const TextStyle(color: TraioTheme.textMuted, fontSize: 12),
          hintStyle: const TextStyle(color: TraioTheme.textMuted, fontSize: 11),
          contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        ),
        keyboardType: numbersOnly ? TextInputType.number : TextInputType.text,
        validator: (v) {
          if (numbersOnly && key.endsWith('.port')) {
            if (v == null || v.isEmpty) return null;
            final n = int.tryParse(v);
            if (n == null || n < 0) return '请输入有效端口（0=随机）';
          }
          return null;
        },
      ),
    );
  }
}
