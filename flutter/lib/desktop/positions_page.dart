import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/api_client.dart';
import '../core/theme.dart';

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

final positionsProvider = StreamProvider<List<_Position>>((ref) async* {
  final client = ref.read(apiClientProvider);
  while (true) {
    final raw = await client.positions();
    yield raw.map(_Position.fromJson).toList();
    await Future<void>.delayed(const Duration(seconds: 10));
  }
});

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

class _Position {
  const _Position({
    required this.symbol,
    required this.quantity,
    required this.avgCost,
    required this.marketValue,
    required this.unrealized,
    required this.broker,
  });

  factory _Position.fromJson(dynamic j) {
    final m = j as Map<String, dynamic>;
    return _Position(
      symbol:      m['symbol']?.toString() ?? '',
      quantity:    (m['quantity']     as num?)?.toDouble() ?? 0,
      avgCost:     (m['avg_cost']     as num?)?.toDouble() ?? 0,
      marketValue: (m['market_value'] as num?)?.toDouble() ?? 0,
      unrealized:  (m['unrealized_pnl'] as num?)?.toDouble() ?? 0,
      broker:      m['broker']?.toString() ?? '',
    );
  }

  final String symbol;
  final double quantity;
  final double avgCost;
  final double marketValue;
  final double unrealized;
  final String broker;

  double get currentPrice  => quantity != 0 ? marketValue / quantity : 0;
  double get unrealizedPct => (avgCost > 0 && quantity > 0)
      ? (unrealized / (avgCost * quantity)) * 100
      : 0;
  bool   get isGain => unrealized >= 0;
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

class PositionsPage extends ConsumerWidget {
  const PositionsPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(positionsProvider);

    return ColoredBox(
      color: TraioTheme.bg,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _Header(async: async, ref: ref),
          const _ColumnHeader(),
          Expanded(
            child: async.when(
              data:    (list) => list.isEmpty ? _empty(context) : _PositionList(positions: list),
              loading: ()     => const Center(child: CircularProgressIndicator(strokeWidth: 1.5, color: TraioTheme.textMuted)),
              error:   (e, st) => _error(context, e, st),
            ),
          ),
          if (async.hasValue && async.value!.isNotEmpty)
            _Footer(positions: async.value!),
        ],
      ),
    );
  }

  Widget _empty(BuildContext context) => Center(
    child: Column(mainAxisSize: MainAxisSize.min, children: [
      const Icon(Icons.inbox_outlined, size: 36, color: TraioTheme.textMuted),
      const SizedBox(height: 10),
      Text('暂无持仓', style: TraioTheme.mono(context, color: TraioTheme.textMuted, size: 13)),
    ]),
  );

  Widget _error(BuildContext context, Object e, StackTrace st) => Center(
    child: Padding(
      padding: const EdgeInsets.all(24),
      child: Column(mainAxisSize: MainAxisSize.min, children: [
        const Icon(Icons.error_outline, color: TraioTheme.down, size: 28),
        const SizedBox(height: 8),
        Text('$e', style: TraioTheme.mono(context, color: TraioTheme.down, size: 12)),
      ]),
    ),
  );
}

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

class _Header extends StatelessWidget {
  const _Header({required this.async, required this.ref});
  final AsyncValue<List<_Position>> async;
  final WidgetRef ref;

  @override
  Widget build(BuildContext context) {
    final positions = async.valueOrNull ?? [];
    final totalValue = positions.fold(0.0, (s, p) => s + p.marketValue);
    final totalPnl   = positions.fold(0.0, (s, p) => s + p.unrealized);
    final totalCost  = positions.fold(0.0, (s, p) => s + p.avgCost * p.quantity);
    final totalPct   = totalCost > 0 ? (totalPnl / totalCost) * 100 : 0.0;
    final isGain     = totalPnl >= 0;

    return Container(
      height: 56,
      padding: const EdgeInsets.symmetric(horizontal: 20),
      decoration: const BoxDecoration(
        color: TraioTheme.surface,
        border: Border(bottom: BorderSide(color: TraioTheme.border)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          // Title + count badge
          Text('持仓', style: const TextStyle(
            fontSize: 15, fontWeight: FontWeight.w600, color: TraioTheme.textPrimary,
          )),
          if (positions.isNotEmpty) ...[
            const SizedBox(width: 6),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
              decoration: BoxDecoration(
                color: TraioTheme.bg,
                borderRadius: BorderRadius.circular(10),
                border: Border.all(color: TraioTheme.border),
              ),
              child: Text('${positions.length}',
                style: TraioTheme.mono(context, size: 11, color: TraioTheme.textSecondary)),
            ),
          ],
          const Spacer(),
          if (positions.isNotEmpty) ...[
            _Stat(label: '总市值', value: _fmtD(totalValue, prefix: r'$')),
            const SizedBox(width: 28),
            _Stat(
              label: '浮盈',
              value: '${isGain ? '+' : '-'}\$${totalPnl.abs().toStringAsFixed(2)}'
                     '  ${isGain ? '+' : ''}${totalPct.toStringAsFixed(2)}%',
              valueColor: isGain ? TraioTheme.up : TraioTheme.down,
            ),
            const SizedBox(width: 12),
          ],
          // Refresh button
          Material(
            color: Colors.transparent,
            child: InkWell(
              onTap: () => ref.invalidate(positionsProvider),
              borderRadius: BorderRadius.circular(6),
              child: Padding(
                padding: const EdgeInsets.all(7),
                child: Icon(Icons.refresh_rounded, size: 16, color: TraioTheme.textMuted),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _Stat extends StatelessWidget {
  const _Stat({required this.label, required this.value, this.valueColor = TraioTheme.textPrimary});
  final String label;
  final String value;
  final Color  valueColor;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisAlignment: MainAxisAlignment.center,
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Text(label, style: TraioTheme.mono(context, size: 10, color: TraioTheme.textMuted)),
        const SizedBox(height: 1),
        Text(value,  style: TraioTheme.mono(context, size: 13, color: valueColor)),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Column header
// ---------------------------------------------------------------------------

class _ColumnHeader extends StatelessWidget {
  const _ColumnHeader();

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 30,
      padding: const EdgeInsets.symmetric(horizontal: 20),
      decoration: const BoxDecoration(
        color: TraioTheme.surfaceAlt,
        border: Border(bottom: BorderSide(color: TraioTheme.border)),
      ),
      child: Row(
        children: const [
          _Ch('代码',  flex: 3, left: true),
          _Ch('数量',  flex: 2),
          _Ch('均价',  flex: 2),
          _Ch('现价',  flex: 2),
          _Ch('市值',  flex: 3),
          _Ch('浮盈',  flex: 3),
          _Ch('%',    flex: 2),
          _Ch('来源',  flex: 2),
        ],
      ),
    );
  }
}

class _Ch extends StatelessWidget {
  const _Ch(this.text, {required this.flex, this.left = false});
  final String text;
  final int    flex;
  final bool   left;

  @override
  Widget build(BuildContext context) => Expanded(
    flex: flex,
    child: Text(text,
      textAlign: left ? TextAlign.left : TextAlign.right,
      style: TraioTheme.mono(context, size: 10, color: TraioTheme.textMuted),
    ),
  );
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

class _PositionList extends StatelessWidget {
  const _PositionList({required this.positions});
  final List<_Position> positions;

  @override
  Widget build(BuildContext context) {
    final brokers = positions.map((p) => p.broker).toSet().toList()..sort();

    // Build flat items: [header, row, row, ..., header, row, ...]
    final items = <_ListItem>[];
    for (final broker in brokers) {
      items.add(_ListItem.header(broker));
      final group = positions.where((p) => p.broker == broker).toList();
      for (var i = 0; i < group.length; i++) {
        items.add(_ListItem.row(group[i], isLast: i == group.length - 1));
      }
    }

    return ListView.builder(
      padding: EdgeInsets.zero,
      itemCount: items.length,
      itemBuilder: (context, i) {
        final item = items[i];
        return item.isHeader
            ? _BrokerHeader(broker: item.broker!)
            : _PositionRow(position: item.position!, isLast: item.isLast);
      },
    );
  }
}

class _ListItem {
  _ListItem.header(String broker)
      : isHeader = true, broker = broker, position = null, isLast = false;
  _ListItem.row(_Position pos, {required bool isLast})
      : isHeader = false, broker = null, position = pos, isLast = isLast;

  final bool       isHeader;
  final String?    broker;
  final _Position? position;
  final bool       isLast;
}

class _BrokerHeader extends StatelessWidget {
  const _BrokerHeader({required this.broker});
  final String broker;

  @override
  Widget build(BuildContext context) => Container(
    height: 28,
    padding: const EdgeInsets.symmetric(horizontal: 20),
    decoration: const BoxDecoration(color: TraioTheme.surfaceAlt),
    alignment: Alignment.centerLeft,
    child: Row(children: [
      Container(width: 3, height: 12,
        decoration: BoxDecoration(
          color: TraioTheme.accent,
          borderRadius: BorderRadius.circular(2),
        ),
      ),
      const SizedBox(width: 8),
      Text(broker.toUpperCase(),
        style: TraioTheme.mono(context, size: 10, color: TraioTheme.textSecondary)),
    ]),
  );
}

class _PositionRow extends StatefulWidget {
  const _PositionRow({required this.position, required this.isLast});
  final _Position position;
  final bool      isLast;

  @override
  State<_PositionRow> createState() => _PositionRowState();
}

class _PositionRowState extends State<_PositionRow> {
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final p = widget.position;
    final pnlColor = p.isGain ? TraioTheme.up   : TraioTheme.down;
    final pnlBg    = p.isGain ? TraioTheme.upBg : TraioTheme.downBg;

    return MouseRegion(
      onEnter: (_) => setState(() => _hovered = true),
      onExit:  (_) => setState(() => _hovered = false),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 80),
        color: _hovered ? const Color(0xFFF0F0F8) : TraioTheme.surface,
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          SizedBox(
            height: 44,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 20),
              child: Row(children: [
                // Symbol
                Expanded(flex: 3, child: Text(p.symbol,
                  style: const TextStyle(
                    fontSize: 13, fontWeight: FontWeight.w600,
                    color: TraioTheme.textPrimary, fontFamily: TraioTheme.monoFont,
                  ),
                )),
                // Quantity
                Expanded(flex: 2, child: _num(context, _fmtQty(p.quantity))),
                // Avg cost
                Expanded(flex: 2, child: _num(context, _fmtD(p.avgCost, prefix: r'$'))),
                // Current price
                Expanded(flex: 2, child: _num(context, _fmtD(p.currentPrice, prefix: r'$'))),
                // Market value
                Expanded(flex: 3, child: _num(context, _fmtD(p.marketValue, prefix: r'$'))),
                // Unrealized PnL pill
                Expanded(flex: 3, child: Align(
                  alignment: Alignment.centerRight,
                  child: Container(
                    padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                    decoration: BoxDecoration(
                      color: pnlBg,
                      borderRadius: BorderRadius.circular(4),
                    ),
                    child: Text(
                      '${p.isGain ? '+' : '-'}\$${p.unrealized.abs().toStringAsFixed(2)}',
                      style: TraioTheme.mono(context, size: 11, color: pnlColor),
                    ),
                  ),
                )),
                // Pct
                Expanded(flex: 2, child: Align(
                  alignment: Alignment.centerRight,
                  child: Text(
                    '${p.unrealizedPct >= 0 ? '+' : ''}${p.unrealizedPct.toStringAsFixed(2)}%',
                    style: TraioTheme.mono(context, size: 11, color: pnlColor),
                  ),
                )),
                // Broker tag
                Expanded(flex: 2, child: Align(
                  alignment: Alignment.centerRight,
                  child: Container(
                    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                    decoration: BoxDecoration(
                      color: TraioTheme.bg,
                      border: Border.all(color: TraioTheme.border),
                      borderRadius: BorderRadius.circular(4),
                    ),
                    child: Text(p.broker.toUpperCase(),
                      style: TraioTheme.mono(context, size: 9, color: TraioTheme.textMuted)),
                  ),
                )),
              ]),
            ),
          ),
          if (!widget.isLast)
            const Divider(height: 1, indent: 20, endIndent: 20, color: TraioTheme.border),
        ]),
      ),
    );
  }

  Widget _num(BuildContext context, String t) => Align(
    alignment: Alignment.centerRight,
    child: Text(t, style: TraioTheme.mono(context, size: 12, color: TraioTheme.textSecondary)),
  );
}

// ---------------------------------------------------------------------------
// Footer
// ---------------------------------------------------------------------------

class _Footer extends StatelessWidget {
  const _Footer({required this.positions});
  final List<_Position> positions;

  @override
  Widget build(BuildContext context) {
    final totalCost  = positions.fold(0.0, (s, p) => s + p.avgCost * p.quantity);
    final totalValue = positions.fold(0.0, (s, p) => s + p.marketValue);
    final totalPnl   = positions.fold(0.0, (s, p) => s + p.unrealized);
    final totalPct   = totalCost > 0 ? (totalPnl / totalCost) * 100 : 0.0;
    final isGain     = totalPnl >= 0;
    final pnlColor   = isGain ? TraioTheme.up : TraioTheme.down;

    return Container(
      height: 38,
      padding: const EdgeInsets.symmetric(horizontal: 20),
      decoration: const BoxDecoration(
        color: TraioTheme.surface,
        border: Border(top: BorderSide(color: TraioTheme.border)),
      ),
      child: Row(children: [
        Text('合计', style: TraioTheme.mono(context, size: 11, color: TraioTheme.textMuted)),
        const Spacer(),
        Text('成本 ${_fmtD(totalCost, prefix: r'$')}',
          style: TraioTheme.mono(context, size: 11, color: TraioTheme.textMuted)),
        const SizedBox(width: 24),
        Text('市值 ${_fmtD(totalValue, prefix: r'$')}',
          style: TraioTheme.mono(context, size: 11, color: TraioTheme.textPrimary)),
        const SizedBox(width: 24),
        Text(
          '${isGain ? '+' : '-'}\$${totalPnl.abs().toStringAsFixed(2)}'
          '  ${isGain ? '+' : ''}${totalPct.toStringAsFixed(2)}%',
          style: TraioTheme.mono(context, size: 11, color: pnlColor),
        ),
      ]),
    );
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

String _fmtD(double v, {String prefix = ''}) {
  if (v >= 1000000) return '$prefix${(v / 1000000).toStringAsFixed(2)}M';
  if (v >= 1000)    return '$prefix${(v / 1000).toStringAsFixed(2)}K';
  return '$prefix${v.toStringAsFixed(2)}';
}

String _fmtQty(double v) =>
    v == v.truncateToDouble() ? v.toInt().toString() : v.toStringAsFixed(4);
