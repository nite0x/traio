import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:syncfusion_flutter_charts/charts.dart';

import '../core/api_client.dart';
import '../core/backend_launcher.dart';
import '../core/theme.dart';

final accountEquityProvider =
    StreamProvider<AccountEquityResponse>((ref) async* {
  ref.watch(apiClientProvider);
  while (true) {
    if (!await BackendLauncher.isServerRunning()) {
      await Future<void>.delayed(const Duration(milliseconds: 500));
      continue;
    }
    final client = ref.read(apiClientProvider);
    try {
      yield await client.accountEquity();
      await Future<void>.delayed(const Duration(seconds: 60));
    } on DioException catch (e) {
      if (e.type == DioExceptionType.connectionError ||
          e.type == DioExceptionType.connectionTimeout) {
        await Future<void>.delayed(const Duration(seconds: 2));
        continue;
      }
      rethrow;
    }
  }
});

class HomePage extends ConsumerWidget {
  const HomePage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(accountEquityProvider);
    return ColoredBox(
      color: TraioTheme.bg,
      child: async.when(
        data: (data) => _HomeContent(
            data: data, onRefresh: () => ref.invalidate(accountEquityProvider)),
        loading: () => const Center(
            child: CircularProgressIndicator(
                strokeWidth: 1.5, color: TraioTheme.textMuted)),
        error: (e, _) => _ErrorView(
            error: e, onRefresh: () => ref.invalidate(accountEquityProvider)),
      ),
    );
  }
}

class _HomeContent extends StatelessWidget {
  const _HomeContent({required this.data, required this.onRefresh});

  final AccountEquityResponse data;
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    final points = data.points.where((p) => p.value != 0).toList()
      ..sort((a, b) => a.time.compareTo(b.time));
    final summary = data.summary;
    final first = points.isNotEmpty ? points.first.value : 0.0;
    final last = points.isNotEmpty ? points.last.value : summary.netLiquidation;
    final change = first != 0 ? last - first : 0.0;
    final changePct = first != 0 ? change / first * 100 : 0.0;
    final currency = summary.currency.isNotEmpty
        ? summary.currency
        : (points.isNotEmpty ? points.last.currency : 'USD');

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _Header(
          value: last,
          currency: currency,
          change: change,
          changePct: changePct,
          accountId: summary.accountId,
          warning: data.warning,
          onRefresh: onRefresh,
        ),
        Expanded(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Expanded(child: _EquityChart(points: points)),
                const SizedBox(height: 16),
                _SummaryStrip(summary: summary, currency: currency),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({
    required this.value,
    required this.currency,
    required this.change,
    required this.changePct,
    required this.accountId,
    required this.warning,
    required this.onRefresh,
  });

  final double value;
  final String currency;
  final double change;
  final double changePct;
  final String accountId;
  final String? warning;
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    final isGain = change >= 0;
    return Container(
      height: 72,
      padding: const EdgeInsets.symmetric(horizontal: 20),
      decoration: const BoxDecoration(
        color: TraioTheme.surface,
        border: Border(bottom: BorderSide(color: TraioTheme.border)),
      ),
      child: Row(
        children: [
          Column(
            mainAxisAlignment: MainAxisAlignment.center,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('首页',
                  style: TextStyle(
                      fontSize: 15,
                      fontWeight: FontWeight.w600,
                      color: TraioTheme.textPrimary)),
              const SizedBox(height: 2),
              Text(accountId.isNotEmpty ? 'IBKR $accountId' : 'IBKR 账户权益',
                  style: TraioTheme.mono(context,
                      size: 11, color: TraioTheme.textMuted)),
            ],
          ),
          const Spacer(),
          if (warning != null && warning!.isNotEmpty) ...[
            Tooltip(
              message: warning!,
              child: const Icon(Icons.info_outline,
                  size: 16, color: TraioTheme.warn),
            ),
            const SizedBox(width: 16),
          ],
          _Metric(label: '净值', value: '${_fmt(value)} $currency'),
          const SizedBox(width: 28),
          _Metric(
            label: '区间变化',
            value:
                '${isGain ? '+' : '-'}${_fmt(change.abs())}  ${isGain ? '+' : ''}${changePct.toStringAsFixed(2)}%',
            color: isGain ? TraioTheme.up : TraioTheme.down,
          ),
          const SizedBox(width: 12),
          IconButton(
            onPressed: onRefresh,
            tooltip: '刷新',
            icon: const Icon(Icons.refresh_rounded,
                size: 16, color: TraioTheme.textMuted),
          ),
        ],
      ),
    );
  }
}

class _EquityChart extends StatelessWidget {
  const _EquityChart({required this.points});

  final List<AccountEquityPoint> points;

  @override
  Widget build(BuildContext context) {
    if (points.isEmpty) {
      return Center(
        child: Text('暂无账户权益数据',
            style: TraioTheme.mono(context,
                color: TraioTheme.textMuted, size: 13)),
      );
    }

    final sorted = List<AccountEquityPoint>.from(points)
      ..sort((a, b) => a.time.compareTo(b.time));
    final values = sorted.map((p) => p.value).toList();
    var minV = values.reduce((a, b) => a < b ? a : b);
    var maxV = values.reduce((a, b) => a > b ? a : b);
    var pad = (maxV - minV).abs() * 0.12;
    if (pad == 0) pad = maxV.abs() * 0.05;

    var xMin = sorted.first.time;
    var xMax = sorted.last.time;
    if (sorted.length == 1 || xMin.isAtSameMomentAs(xMax)) {
      xMin = sorted.first.time.subtract(const Duration(days: 30));
      xMax = sorted.first.time.add(const Duration(days: 1));
    } else {
      xMin = xMin.subtract(const Duration(hours: 12));
      xMax = xMax.add(const Duration(hours: 12));
    }

    return DecoratedBox(
      decoration: BoxDecoration(
        color: TraioTheme.surface,
        border: Border.all(color: TraioTheme.border),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 10, 16, 10),
        child: SfCartesianChart(
          plotAreaBorderWidth: 0,
          primaryXAxis: DateTimeAxis(
            minimum: xMin,
            maximum: xMax,
            majorGridLines: const MajorGridLines(width: 0),
            axisLine: const AxisLine(width: 0),
            labelStyle:
                TraioTheme.mono(context, size: 10, color: TraioTheme.textMuted),
          ),
          primaryYAxis: NumericAxis(
            minimum: minV - pad,
            maximum: maxV + pad,
            opposedPosition: true,
            majorGridLines:
                const MajorGridLines(color: TraioTheme.border, width: 0.7),
            axisLine: const AxisLine(width: 0),
            labelStyle:
                TraioTheme.mono(context, size: 10, color: TraioTheme.textMuted),
          ),
          tooltipBehavior: TooltipBehavior(enable: true),
          series: <CartesianSeries<AccountEquityPoint, DateTime>>[
            AreaSeries<AccountEquityPoint, DateTime>(
              dataSource: sorted,
              xValueMapper: (p, _) => p.time,
              yValueMapper: (p, _) => p.value,
              color: TraioTheme.accent.withValues(alpha: 0.10),
              borderColor: TraioTheme.accent,
              borderWidth: 2,
              animationDuration: 250,
              markerSettings: const MarkerSettings(
                isVisible: true,
                height: 6,
                width: 6,
                color: TraioTheme.accent,
                borderColor: TraioTheme.accent,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _SummaryStrip extends StatelessWidget {
  const _SummaryStrip({required this.summary, required this.currency});

  final AccountSummary summary;
  final String currency;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 72,
      child: Row(
        children: [
          Expanded(
              child: _StatTile(
                  label: '现金',
                  value: '${_fmt(summary.totalCashValue)} $currency')),
          const SizedBox(width: 10),
          Expanded(
              child: _StatTile(
                  label: '持仓市值',
                  value: '${_fmt(summary.grossPositionValue)} $currency')),
          const SizedBox(width: 10),
          Expanded(
              child: _StatTile(
                  label: '未实现盈亏',
                  value: '${_fmtSigned(summary.unrealizedPnl)} $currency',
                  color: summary.unrealizedPnl >= 0
                      ? TraioTheme.up
                      : TraioTheme.down)),
          const SizedBox(width: 10),
          Expanded(
              child: _StatTile(
                  label: '购买力',
                  value: '${_fmt(summary.buyingPower)} $currency')),
        ],
      ),
    );
  }
}

class _Metric extends StatelessWidget {
  const _Metric(
      {required this.label,
      required this.value,
      this.color = TraioTheme.textPrimary});
  final String label;
  final String value;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisAlignment: MainAxisAlignment.center,
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Text(label,
            style: TraioTheme.mono(context,
                size: 10, color: TraioTheme.textMuted)),
        const SizedBox(height: 2),
        Text(value, style: TraioTheme.mono(context, size: 13, color: color)),
      ],
    );
  }
}

class _StatTile extends StatelessWidget {
  const _StatTile(
      {required this.label,
      required this.value,
      this.color = TraioTheme.textPrimary});
  final String label;
  final String value;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        color: TraioTheme.surface,
        border: Border.all(color: TraioTheme.border),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Text(label,
                style: TraioTheme.mono(context,
                    size: 10, color: TraioTheme.textMuted)),
            const SizedBox(height: 5),
            Text(value,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TraioTheme.mono(context, size: 13, color: color)),
          ],
        ),
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  const _ErrorView({required this.error, required this.onRefresh});

  final Object error;
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.error_outline, color: TraioTheme.down, size: 28),
          const SizedBox(height: 8),
          Text('$error',
              style:
                  TraioTheme.mono(context, color: TraioTheme.down, size: 12)),
          const SizedBox(height: 12),
          TextButton.icon(
            onPressed: onRefresh,
            icon: const Icon(Icons.refresh_rounded, size: 16),
            label: const Text('重试'),
          ),
        ],
      ),
    );
  }
}

String _fmt(double value) {
  final sign = value < 0 ? '-' : '';
  final abs = value.abs();
  return '$sign${abs.toStringAsFixed(2)}';
}

String _fmtSigned(double value) {
  return '${value >= 0 ? '+' : '-'}${value.abs().toStringAsFixed(2)}';
}
