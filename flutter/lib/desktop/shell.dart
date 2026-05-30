import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/api_client.dart';
import '../core/ibkr_browser.dart';
import '../core/theme.dart';
import 'home_page.dart';
import 'positions_page.dart';
import 'settings_page.dart';

// Active nav index provider
final _navIndexProvider = StateProvider<int>((ref) => 0);

/// Main shell: icon rail | page content | title bar
class DesktopShell extends ConsumerWidget {
  const DesktopShell({super.key});

  static final _pages = <Widget>[
    const HomePage(),
    const PositionsPage(),
    const _WatchlistPanel(),
    const _ChartPlaceholder(),
    _OrderPanel(),
  ];

  static const _navItems = [
    (icon: Icons.dashboard_outlined, label: '首页'),
    (icon: Icons.pie_chart_outline, label: '持仓'),
    (icon: Icons.star_outline, label: '自选'),
    (icon: Icons.show_chart, label: 'K线'),
    (icon: Icons.receipt_long_outlined, label: '下单'),
  ];

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final idx = ref.watch(_navIndexProvider);

    return Scaffold(
      backgroundColor: TraioTheme.bg,
      body: Column(
        children: [
          // ── Title bar ──────────────────────────────────────────────────
          _TitleBar(),
          const Divider(height: 1, color: TraioTheme.border),
          // ── Body: rail + page ──────────────────────────────────────────
          Expanded(
            child: Row(
              children: [
                _NavRail(
                  selectedIndex: idx,
                  items: _navItems,
                  onSelect: (i) =>
                      ref.read(_navIndexProvider.notifier).state = i,
                ),
                const VerticalDivider(width: 1, color: TraioTheme.border),
                Expanded(child: _pages[idx]),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Title bar
// ---------------------------------------------------------------------------

class _TitleBar extends ConsumerWidget {
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Container(
      height: 38,
      color: TraioTheme.surface,
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: Row(
        children: [
          // Logo
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 18,
                height: 18,
                decoration: BoxDecoration(
                  color: TraioTheme.accent,
                  borderRadius: BorderRadius.circular(4),
                ),
                child: const Center(
                  child: Text('T',
                      style: TextStyle(
                          color: Colors.white,
                          fontSize: 11,
                          fontWeight: FontWeight.w700)),
                ),
              ),
              const SizedBox(width: 8),
              const Text('Traio',
                  style: TextStyle(
                      color: TraioTheme.textPrimary,
                      fontSize: 13,
                      fontWeight: FontWeight.w600)),
            ],
          ),
          const Spacer(),
          const _IbkrGatewayStatusChip(),
          const SizedBox(width: 4),
          InkWell(
            onTap: () => Navigator.of(context).push(
              MaterialPageRoute<void>(builder: (_) => const SettingsPage()),
            ),
            borderRadius: BorderRadius.circular(4),
            child: const Padding(
              padding: EdgeInsets.all(6),
              child: Icon(Icons.settings_outlined,
                  size: 15, color: TraioTheme.textMuted),
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Nav rail (icon-only, minimal)
// ---------------------------------------------------------------------------

class _NavRail extends StatelessWidget {
  const _NavRail({
    required this.selectedIndex,
    required this.items,
    required this.onSelect,
  });

  final int selectedIndex;
  final List<({IconData icon, String label})> items;
  final ValueChanged<int> onSelect;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 48,
      color: TraioTheme.surface,
      child: Column(
        children: [
          const SizedBox(height: 8),
          ...items.asMap().entries.map((e) {
            final selected = e.key == selectedIndex;
            return Tooltip(
              message: e.value.label,
              preferBelow: false,
              child: InkWell(
                onTap: () => onSelect(e.key),
                child: AnimatedContainer(
                  duration: const Duration(milliseconds: 120),
                  width: 48,
                  height: 44,
                  decoration: BoxDecoration(
                    color: selected
                        ? TraioTheme.accent.withValues(alpha: 0.12)
                        : Colors.transparent,
                    border: Border(
                      left: BorderSide(
                        color:
                            selected ? TraioTheme.accent : Colors.transparent,
                        width: 2,
                      ),
                    ),
                  ),
                  child: Icon(
                    e.value.icon,
                    size: 18,
                    color: selected ? TraioTheme.accent : TraioTheme.textMuted,
                  ),
                ),
              ),
            );
          }),
        ],
      ),
    );
  }
}

class _IbkrGatewayStatusChip extends ConsumerWidget {
  const _IbkrGatewayStatusChip();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final status = ref.watch(ibkrGatewayStatusProvider);
    return status.when(
      data: (s) {
        final running = s['running'] == true;
        final authenticated = s['authenticated'] == true;
        final account = s['account']?.toString() ?? '';
        final loginMode = s['login_mode']?.toString() ?? 'manual';
        final authMessage = s['auth_message']?.toString() ?? '';

        final Color dotColor;
        final String label;
        if (!running) {
          dotColor = TraioTheme.down;
          label = 'IBKR 离线';
        } else if (!authenticated) {
          dotColor = TraioTheme.warn;
          label = authMessage.isNotEmpty
              ? 'IBKR 验证失败'
              : (loginMode == 'manual' ? 'IBKR 待登录' : 'IBKR 登录中');
        } else {
          dotColor = TraioTheme.up;
          label = account.isNotEmpty ? 'IBKR $account' : 'IBKR 已连接';
        }

        return InkWell(
          onTap: () => _showDetails(context, ref, s),
          borderRadius: BorderRadius.circular(6),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Container(
                  width: 8,
                  height: 8,
                  decoration:
                      BoxDecoration(color: dotColor, shape: BoxShape.circle),
                ),
                const SizedBox(width: 6),
                Text(label, style: TraioTheme.mono(context, size: 11)),
              ],
            ),
          ),
        );
      },
      loading: () => Text('IBKR …',
          style:
              TraioTheme.mono(context, size: 11, color: TraioTheme.textMuted)),
      error: (_, __) => InkWell(
        onTap: () => ref.invalidate(ibkrGatewayStatusProvider),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Container(
              width: 8,
              height: 8,
              decoration: const BoxDecoration(
                  color: TraioTheme.down, shape: BoxShape.circle),
            ),
            const SizedBox(width: 6),
            Text('IBKR 错误',
                style:
                    TraioTheme.mono(context, size: 11, color: TraioTheme.down)),
          ],
        ),
      ),
    );
  }

  void _showDetails(
      BuildContext context, WidgetRef ref, Map<String, dynamic> s) {
    showDialog<void>(
      context: context,
      builder: (ctx) => _IbkrGatewayDialog(status: s),
    );
  }
}

class _IbkrGatewayDialog extends ConsumerStatefulWidget {
  const _IbkrGatewayDialog({required this.status});

  final Map<String, dynamic> status;

  @override
  ConsumerState<_IbkrGatewayDialog> createState() => _IbkrGatewayDialogState();
}

class _IbkrGatewayDialogState extends ConsumerState<_IbkrGatewayDialog> {
  var _reconnecting = false;
  var _watchingLogin = false;

  Future<void> _openLoginWithWatch(String loginURL) async {
    await IbkrBrowser.open(loginURL);
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('请在浏览器完成登录，完成后将自动关闭')),
    );
    unawaited(_watchLoginComplete());
  }

  Future<void> _watchLoginComplete() async {
    if (_watchingLogin) return;
    _watchingLogin = true;
    try {
      final client = ref.read(apiClientProvider);
      final status = await client.waitForIbkrAuthenticated();
      if (!mounted || status == null) return;
      await IbkrBrowser.closeGatewayTabs();
      ref.invalidate(ibkrGatewayStatusProvider);
      final acct = status['account']?.toString() ?? '';
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
            content: Text(acct.isNotEmpty ? 'IBKR 已连接 $acct' : 'IBKR 已连接')),
      );
      if (Navigator.canPop(context)) Navigator.pop(context);
    } finally {
      _watchingLogin = false;
    }
  }

  Future<void> _reconnect() async {
    setState(() => _reconnecting = true);
    try {
      final client = ref.read(apiClientProvider);
      await client.ibkrGatewayReconnect();
      ref.invalidate(ibkrGatewayStatusProvider);

      final status = await client.openLoginAndWait();
      if (!mounted) return;
      ref.invalidate(ibkrGatewayStatusProvider);
      Navigator.pop(context);

      if (status != null) {
        final acct = status['account']?.toString() ?? '';
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(acct.isNotEmpty ? 'IBKR 已连接 $acct' : 'IBKR 已连接')),
        );
      } else {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('登录超时，请重试')),
        );
      }
    } catch (e) {
      if (!mounted) return;
      setState(() => _reconnecting = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('重连失败: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final live = ref.watch(ibkrGatewayStatusProvider).valueOrNull;
    final s = live ?? widget.status;

    final running = s['running'] == true;
    final authenticated = s['authenticated'] == true;
    final account = s['account']?.toString() ?? '';
    final sessionAge = s['session_age_seconds'] as int? ?? 0;
    final loginMode = s['login_mode']?.toString() ?? 'manual';
    final loginURL = s['login_url']?.toString() ?? '';
    final authMessage = s['auth_message']?.toString() ?? '';
    final manualLogin = loginMode == 'manual' && !authenticated;

    return AlertDialog(
      backgroundColor: TraioTheme.surface,
      title: Text('IBKR Gateway', style: TraioTheme.mono(context)),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _detailRow(context, '运行', running ? '是' : '否'),
          _detailRow(context, '认证', authenticated ? '是' : '否'),
          _detailRow(context, '登录', loginMode == 'auto' ? '自动' : '手动'),
          if (account.isNotEmpty) _detailRow(context, '账户', account),
          if (sessionAge > 0)
            _detailRow(context, '会话时长', '${sessionAge ~/ 60} 分钟'),
          if (authMessage.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(
                authMessage,
                style:
                    TraioTheme.mono(context, size: 10, color: TraioTheme.down),
              ),
            ),
          if (manualLogin && loginURL.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(
                loginURL,
                style: TraioTheme.mono(context,
                    size: 10, color: TraioTheme.textMuted),
              ),
            ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: _reconnecting ? null : () => Navigator.pop(context),
          child: const Text('关闭'),
        ),
        if (manualLogin && loginURL.isNotEmpty)
          FilledButton(
            onPressed: (_reconnecting || _watchingLogin)
                ? null
                : () => _openLoginWithWatch(loginURL),
            child: const Text('打开登录页'),
          ),
        FilledButton(
          onPressed: _reconnecting ? null : _reconnect,
          child: _reconnecting
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('重新连接'),
        ),
      ],
    );
  }

  Widget _detailRow(BuildContext context, String k, String v) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        children: [
          SizedBox(
            width: 72,
            child: Text(k,
                style:
                    const TextStyle(color: TraioTheme.textMuted, fontSize: 12)),
          ),
          Text(v, style: TraioTheme.mono(context)),
        ],
      ),
    );
  }
}

class _WatchlistPanel extends ConsumerStatefulWidget {
  const _WatchlistPanel();

  @override
  ConsumerState<_WatchlistPanel> createState() => _WatchlistPanelState();
}

class _WatchlistPanelState extends ConsumerState<_WatchlistPanel> {
  final _controller = TextEditingController();
  var _results = const <Instrument>[];
  var _searching = false;
  String? _searchError;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  Future<void> _search() async {
    final query = _controller.text.trim();
    if (query.isEmpty) {
      setState(() {
        _results = const [];
        _searchError = null;
      });
      return;
    }
    setState(() {
      _searching = true;
      _searchError = null;
    });
    try {
      final results =
          await ref.read(apiClientProvider).searchInstruments(query);
      if (!mounted) return;
      setState(() => _results = results);
    } catch (e) {
      if (!mounted) return;
      setState(() => _searchError = '$e');
    } finally {
      if (mounted) setState(() => _searching = false);
    }
  }

  Future<void> _add(int groupId, Instrument instrument) async {
    await ref.read(apiClientProvider).addWatchlistItem(groupId, instrument);
    ref.invalidate(watchlistItemsProvider(groupId));
    ref.invalidate(watchlistRowsProvider(groupId));
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('${instrument.symbol} 已加入自选')),
    );
  }

  Future<void> _remove(int groupId, WatchlistItem item) async {
    await ref.read(apiClientProvider).removeWatchlistItem(groupId, item.symbol);
    ref.invalidate(watchlistItemsProvider(groupId));
    ref.invalidate(watchlistRowsProvider(groupId));
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('${item.symbol} 已移除')),
    );
  }

  @override
  Widget build(BuildContext context) {
    final groups = ref.watch(watchlistGroupsProvider);
    return ColoredBox(
      color: TraioTheme.surface,
      child: groups.when(
        data: (list) {
          final group = list.isNotEmpty ? list.first : null;
          final groupId = group?.id ?? 1;
          final items = ref.watch(watchlistRowsProvider(groupId));
          return Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 10, 12, 8),
                child: Row(
                  children: [
                    Text(group?.name ?? '自选',
                        style: Theme.of(context).textTheme.labelSmall),
                    const Spacer(),
                    IconButton(
                      tooltip: '刷新',
                      onPressed: () =>
                          ref.invalidate(watchlistRowsProvider(groupId)),
                      icon: const Icon(Icons.refresh, size: 17),
                    ),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 12),
                child: Row(
                  children: [
                    Expanded(
                      child: SizedBox(
                        height: 36,
                        child: TextField(
                          controller: _controller,
                          textCapitalization: TextCapitalization.characters,
                          style: TraioTheme.mono(context),
                          decoration: const InputDecoration(
                            hintText: '搜索股票代码或名称',
                            prefixIcon: Icon(Icons.search, size: 16),
                            border: OutlineInputBorder(),
                            isDense: true,
                            contentPadding: EdgeInsets.symmetric(
                                horizontal: 10, vertical: 8),
                          ),
                          onSubmitted: (_) => _search(),
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    FilledButton.icon(
                      onPressed: _searching ? null : _search,
                      icon: _searching
                          ? const SizedBox(
                              width: 14,
                              height: 14,
                              child: CircularProgressIndicator(strokeWidth: 2))
                          : const Icon(Icons.search, size: 16),
                      label: const Text('搜索'),
                    ),
                  ],
                ),
              ),
              if (_searchError != null)
                Padding(
                  padding: const EdgeInsets.fromLTRB(12, 8, 12, 0),
                  child: Text(_searchError!,
                      style: TraioTheme.mono(context,
                          size: 11, color: TraioTheme.down)),
                ),
              if (_results.isNotEmpty)
                Container(
                  constraints: const BoxConstraints(maxHeight: 180),
                  margin: const EdgeInsets.fromLTRB(12, 8, 12, 0),
                  decoration: BoxDecoration(
                    border: Border.all(color: TraioTheme.border),
                    borderRadius: BorderRadius.circular(6),
                  ),
                  child: ListView.separated(
                    shrinkWrap: true,
                    itemCount: _results.length,
                    separatorBuilder: (_, __) =>
                        const Divider(height: 1, color: TraioTheme.border),
                    itemBuilder: (context, i) {
                      final item = _results[i];
                      final subtitle = [item.name, item.exchange, item.currency]
                          .where((e) => e.isNotEmpty)
                          .join(' · ');
                      return ListTile(
                        dense: true,
                        title:
                            Text(item.symbol, style: TraioTheme.mono(context)),
                        subtitle: subtitle.isEmpty
                            ? null
                            : Text(subtitle,
                                style: TraioTheme.mono(context,
                                    size: 11, color: TraioTheme.textMuted)),
                        trailing: IconButton(
                          tooltip: '加入自选',
                          onPressed: () => _add(groupId, item),
                          icon: const Icon(Icons.add, size: 17),
                        ),
                      );
                    },
                  ),
                ),
              const Divider(height: 17, color: TraioTheme.border),
              Expanded(
                child: items.when(
                  data: (rows) {
                    if (rows.isEmpty) {
                      return Center(
                        child: Text('暂无自选',
                            style: TraioTheme.mono(context,
                                color: TraioTheme.textMuted)),
                      );
                    }
                    return ListView.separated(
                      itemCount: rows.length,
                      separatorBuilder: (_, __) =>
                          const Divider(height: 1, color: TraioTheme.border),
                      itemBuilder: (context, i) {
                        final row = rows[i];
                        final item = row.item;
                        final subtitle = [
                          item.name,
                          item.exchange,
                          item.currency
                        ].where((e) => e.isNotEmpty).join(' · ');
                        return ListTile(
                          dense: true,
                          title: Text(item.symbol,
                              style: TraioTheme.mono(context)),
                          subtitle: subtitle.isEmpty
                              ? null
                              : Text(subtitle,
                                  style: TraioTheme.mono(context,
                                      size: 11, color: TraioTheme.textMuted)),
                          trailing: _WatchlistQuoteCell(
                            row: row,
                            onRemove: () => _remove(groupId, item),
                          ),
                        );
                      },
                    );
                  },
                  loading: () => const Center(
                      child: CircularProgressIndicator(strokeWidth: 2)),
                  error: (e, _) => Padding(
                    padding: const EdgeInsets.all(12),
                    child: Text('$e',
                        style: const TextStyle(
                            color: TraioTheme.down, fontSize: 12)),
                  ),
                ),
              ),
            ],
          );
        },
        loading: () =>
            const Center(child: CircularProgressIndicator(strokeWidth: 2)),
        error: (e, _) => Padding(
          padding: const EdgeInsets.all(12),
          child: Text('$e',
              style: const TextStyle(color: TraioTheme.down, fontSize: 12)),
        ),
      ),
    );
  }
}

final watchlistGroupsProvider =
    FutureProvider<List<WatchlistGroup>>((ref) async {
  return ref.read(apiClientProvider).watchlistGroups();
});

final watchlistItemsProvider =
    FutureProvider.family<List<WatchlistItem>, int>((ref, groupId) async {
  return ref.read(apiClientProvider).watchlistItems(groupId);
});

final watchlistRowsProvider =
    FutureProvider.family<List<_WatchlistRow>, int>((ref, groupId) async {
  final api = ref.read(apiClientProvider);
  final items = await api.watchlistItems(groupId);
  final futures = await Future.wait<dynamic>([
    api.quotesByConids(items.map((item) => item.conid)).catchError(
          (_) => const <Quote>[],
        ),
    api.positions().catchError((_) => const <dynamic>[]),
  ]);
  final quotes = {
    for (final quote in futures[0] as List<Quote>)
      if (quote.conid > 0) quote.conid: quote,
  };
  final heldSymbols = <String>{};
  for (final raw in futures[1] as List<dynamic>) {
    final map = Map<String, dynamic>.from(raw as Map);
    final quantity = (map['quantity'] as num?)?.toDouble() ?? 0;
    final symbol = map['symbol']?.toString().trim().toUpperCase() ?? '';
    if (quantity != 0 && symbol.isNotEmpty) heldSymbols.add(symbol);
  }
  return [
    for (final item in items)
      _WatchlistRow(
        item: item,
        quote: quotes[item.conid],
        hasPosition: heldSymbols.contains(item.symbol.toUpperCase()),
      ),
  ];
});

class _WatchlistRow {
  const _WatchlistRow({
    required this.item,
    required this.quote,
    required this.hasPosition,
  });

  final WatchlistItem item;
  final Quote? quote;
  final bool hasPosition;
}

class _WatchlistQuoteCell extends StatelessWidget {
  const _WatchlistQuoteCell({required this.row, required this.onRemove});

  final _WatchlistRow row;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    final quote = row.quote;
    final color = quote == null
        ? TraioTheme.textMuted
        : quote.changePct > 0
            ? TraioTheme.up
            : quote.changePct < 0
                ? TraioTheme.down
                : TraioTheme.textMuted;
    return SizedBox(
      width: 132,
      child: Row(
        mainAxisAlignment: MainAxisAlignment.end,
        children: [
          Expanded(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Row(
                  mainAxisAlignment: MainAxisAlignment.end,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (row.hasPosition) ...[
                      const Icon(Icons.account_balance_wallet_outlined,
                          size: 12, color: TraioTheme.textMuted),
                      const SizedBox(width: 4),
                    ],
                    Text(
                      quote == null || quote.last == 0
                          ? '--'
                          : quote.last.toStringAsFixed(2),
                      style: TraioTheme.mono(context, size: 12, color: color),
                    ),
                  ],
                ),
                Text(
                  quote == null
                      ? '--'
                      : '${quote.changePct.toStringAsFixed(2)}%',
                  style: TraioTheme.mono(context, size: 11, color: color),
                ),
              ],
            ),
          ),
          IconButton(
            tooltip: '移除',
            onPressed: onRemove,
            icon: const Icon(Icons.close, size: 17),
          ),
        ],
      ),
    );
  }
}

class _ChartPlaceholder extends StatelessWidget {
  const _ChartPlaceholder();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text('K 线 / 指标', style: Theme.of(context).textTheme.labelSmall),
          const SizedBox(height: 8),
          Text(
            'TradingView Lightweight Charts（WebView）',
            style: TraioTheme.mono(context, color: TraioTheme.textMuted),
          ),
        ],
      ),
    );
  }
}

class _OrderPanel extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return ColoredBox(
      color: TraioTheme.surface,
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text('下单 / 账户', style: Theme.of(context).textTheme.labelSmall),
            const SizedBox(height: 16),
            Text('Schwab + IBKR',
                style: TraioTheme.mono(context, color: TraioTheme.textMuted)),
            const Spacer(),
            FilledButton(onPressed: () {}, child: const Text('限价买入')),
          ],
        ),
      ),
    );
  }
}
