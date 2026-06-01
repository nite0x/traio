import 'package:flutter/material.dart';

import 'mock_data.dart';
import 'shared_widgets.dart';
import 'traio_tokens.dart';

class HoldingsDeskPage extends StatelessWidget {
  const HoldingsDeskPage({super.key});

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(0, 40, 0, 110),
      children: [
        const PageHeader(
          kicker: '投资产权重与账户归集',
          title: '持仓',
          trailing: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              _HeaderAction(icon: Icons.search_rounded),
              SizedBox(width: 8),
              _HeaderAction(icon: Icons.tune_rounded),
            ],
          ),
        ),
        const SizedBox(height: 34),
        const HoldingsOverviewSection(),
      ],
    );
  }
}

class HoldingsOverviewSection extends StatelessWidget {
  const HoldingsOverviewSection(
      {super.key, this.showSummary = true, this.flexible = false});

  final bool showSummary;

  /// When true the grid expands to fill available height (for overview page).
  final bool flexible;

  @override
  Widget build(BuildContext context) {
    final grid = _DesktopHoldingsGrid(flexible: flexible);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (showSummary) ...[
          SummaryStrip(
            cells: [
              const SummaryCellData(
                  label: '持仓市值',
                  value: '\$250,220',
                  caption: '7 个持仓 · 占总资产 85.5%'),
              SummaryCellData(
                  label: '未实现盈亏',
                  value: fmtSignedUsd(32066, dp: 0),
                  caption: '+12.7% 持仓回报',
                  valueColor: TraioTokens.up),
              SummaryCellData(
                  label: '今日盈亏',
                  value: fmtSignedUsd(3847, dp: 0),
                  caption: '+1.37% 今日',
                  valueColor: TraioTokens.up),
              SummaryCellData(
                  label: '累计已实现',
                  value: fmtSignedUsd(40343, dp: 0),
                  caption: '今年至今',
                  valueColor: TraioTokens.up),
            ],
          ),
          const SizedBox(height: 22),
        ],
        if (flexible) Expanded(child: grid) else grid,
      ],
    );
  }
}

class _DesktopHoldingsGrid extends StatelessWidget {
  const _DesktopHoldingsGrid({this.flexible = false});

  final bool flexible;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        if (constraints.maxWidth < 920) {
          return Column(
            children: [
              _TreemapPanel(flexible: flexible),
              const SizedBox(height: 16),
              const _ExposurePanel(),
            ],
          );
        }

        return Row(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Expanded(child: _TreemapPanel(flexible: flexible)),
            const SizedBox(width: 16),
            const SizedBox(
              width: 318,
              child: ClipRect(child: _ExposurePanel()),
            ),
          ],
        );
      },
    );
  }
}

class _TreemapPanel extends StatelessWidget {
  const _TreemapPanel({this.flexible = false});

  final bool flexible;

  @override
  Widget build(BuildContext context) {
    return TraioCard(
      padding: const EdgeInsets.fromLTRB(18, 16, 18, 18),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SectionTitle(title: '资产分布', hint: '面积 = 仓位权重'),
          const SizedBox(height: 16),
          if (flexible)
            const Expanded(child: _Treemap(flexible: true))
          else
            const _Treemap(),
        ],
      ),
    );
  }
}

class _Treemap extends StatelessWidget {
  const _Treemap({this.flexible = false});

  final bool flexible;

  @override
  Widget build(BuildContext context) {
    final bySym = {for (final h in holdings) h.sym: h};
    final row = Row(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
            flex: 22,
            child: _TreemapCell(holding: bySym['NVDA']!, large: true)),
        const SizedBox(width: 6),
        Expanded(
          flex: 23,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Expanded(
                  flex: 16, child: _TreemapCell(holding: bySym['AAPL']!)),
              const SizedBox(height: 6),
              Expanded(
                  flex: 10,
                  child: _TreemapCell(holding: bySym['MSFT']!, small: true)),
            ],
          ),
        ),
        const SizedBox(width: 6),
        Expanded(
          flex: 21,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Expanded(flex: 14, child: _TreemapCell(holding: bySym['VOO']!)),
              const SizedBox(height: 6),
              Expanded(
                  flex: 10,
                  child: _TreemapCell(holding: bySym['TSLA']!, small: true)),
            ],
          ),
        ),
        const SizedBox(width: 6),
        Expanded(
          flex: 31,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Expanded(
                  flex: 11,
                  child:
                      _TreemapCell(holding: bySym['0700.HK']!, small: true)),
              const SizedBox(height: 6),
              Expanded(
                  flex: 11,
                  child: _TreemapCell(holding: bySym['BTC']!, small: true)),
              const SizedBox(height: 6),
              const Expanded(flex: 10, child: _CashCell()),
            ],
          ),
        ),
      ],
    );
    if (flexible) return row;
    return SizedBox(height: 220, child: row);
  }
}

class _TreemapCell extends StatelessWidget {
  const _TreemapCell(
      {required this.holding, this.large = false, this.small = false});

  final Holding holding;
  final bool large;
  final bool small;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final h = constraints.maxHeight;
        final pad = small ? 9.0 : 16.0;
        // Only show the change row when there's enough vertical room.
        final showChange = h >= (small ? 56 : 72);
        return ClipRRect(
          borderRadius: BorderRadius.circular(TraioTokens.r),
          child: Container(
            width: double.infinity,
            padding: EdgeInsets.all(pad),
            color: holding.color,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(holding.sym,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TraioTokens.mono(
                        size: small ? 12 : (large ? 17 : 14),
                        color: Colors.white)),
                SizedBox(height: small ? 3 : 5),
                Text('${holding.weight.g}%',
                    style: TraioTokens.mono(
                        size: small ? 11 : 13,
                        color: Colors.white.withValues(alpha: 0.78),
                        weight: FontWeight.w500)),
                if (showChange) ...[
                  const Spacer(),
                  Text(fmtPct(holding.change),
                      style: TraioTokens.mono(
                          size: small ? 11 : 13, color: Colors.white)),
                ],
              ],
            ),
          ),
        );
      },
    );
  }
}

class _CashCell extends StatelessWidget {
  const _CashCell();

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: const Color(0xFF8E9298),
        borderRadius: BorderRadius.circular(TraioTokens.r),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('现金 & 其他',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TraioTokens.mono(size: 12, color: Colors.white)),
          const SizedBox(height: 4),
          Text('12.2%',
              style: TraioTokens.mono(
                  size: 11,
                  color: Colors.white.withValues(alpha: 0.78),
                  weight: FontWeight.w500)),
        ],
      ),
    );
  }
}

class _ExposurePanel extends StatelessWidget {
  const _ExposurePanel();

  @override
  Widget build(BuildContext context) {
    final top = holdings.first;
    return Container(
      decoration: BoxDecoration(
        color: TraioTokens.surface,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(TraioTokens.rLg),
      ),
      clipBehavior: Clip.hardEdge,
      child: SingleChildScrollView(
        physics: const NeverScrollableScrollPhysics(),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 15, 16, 16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const SectionTitle(title: '集中度'),
              const SizedBox(height: 16),
              _ExposureRow(
                  label: '最大持仓', value: top.sym, detail: '${top.weight.g}%'),
              const SizedBox(height: 13),
              _ExposureRow(
                  label: '现金仓位',
                  value: '${cashWeight.g}%',
                  detail: fmtUsd(41300, dp: 0)),
              const SizedBox(height: 13),
              const _ExposureRow(label: '保证金使用', value: '21%', detail: '低风险'),
              const SizedBox(height: 16),
              const _MarketMix(),
            ],
          ),
        ),
      ),
    );
  }
}

class _MarketMix extends StatelessWidget {
  const _MarketMix();

  @override
  Widget build(BuildContext context) {
    final mix = <String, double>{};
    for (final h in holdings) {
      mix[h.market] = (mix[h.market] ?? 0) + h.weight;
    }
    return Column(
      children: mix.entries
          .map((entry) => Padding(
                padding: const EdgeInsets.only(bottom: 9),
                child: Row(
                  children: [
                    MarketTag(label: entry.key),
                    const SizedBox(width: 10),
                    Expanded(
                      child: ClipRRect(
                        borderRadius: BorderRadius.circular(999),
                        child: LinearProgressIndicator(
                          minHeight: 5,
                          value: entry.value / 100,
                          color: _marketColor(entry.key),
                          backgroundColor: TraioTokens.surfaceSunk,
                        ),
                      ),
                    ),
                    const SizedBox(width: 10),
                    Text('${entry.value.g}%',
                        style: TraioTokens.mono(
                            size: 12,
                            color: TraioTokens.text2,
                            weight: FontWeight.w600)),
                  ],
                ),
              ))
          .toList(),
    );
  }
}

class _ExposureRow extends StatelessWidget {
  const _ExposureRow(
      {required this.label, required this.value, required this.detail});

  final String label;
  final String value;
  final String detail;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Text(label,
            style: TraioTokens.ui(
                size: 13, color: TraioTokens.text3, weight: FontWeight.w700)),
        const Spacer(),
        Text(value, style: TraioTokens.mono(size: 16)),
        const SizedBox(width: 9),
        Text(detail,
            style: TraioTokens.ui(
                size: 12, color: TraioTokens.text3, weight: FontWeight.w700)),
      ],
    );
  }
}

class _HeaderAction extends StatelessWidget {
  const _HeaderAction({required this.icon});

  final IconData icon;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 38,
      height: 38,
      decoration: BoxDecoration(
        color: TraioTokens.surface,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(TraioTokens.rSm),
      ),
      child: Icon(icon, size: 18, color: TraioTokens.text2),
    );
  }
}

Color _marketColor(String label) {
  return switch (label) {
    '港股' => const Color(0xFFC0786E),
    'ETF' => const Color(0xFF4F987F),
    'Crypto' => const Color(0xFFC6963F),
    _ => const Color(0xFF5795D2),
  };
}

extension _CompactNum on num {
  String get g {
    if (this % 1 == 0) return toInt().toString();
    return toString();
  }
}
