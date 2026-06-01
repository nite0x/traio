import 'package:flutter/material.dart';

// ignore_for_file: prefer_const_constructors

import 'mock_data.dart';
import 'traio_tokens.dart';

class OverviewDeskPage extends StatelessWidget {
  const OverviewDeskPage({super.key});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(0, 22, 0, 22),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: const [
          _OverviewHeader(),
          SizedBox(height: 24),
          _KpiGrid(),
          SizedBox(height: 16),
          _AlertBanner(),
          SizedBox(height: 24),
          Expanded(child: _MainContentRow()),
        ],
      ),
    );
  }
}

// ─── Header ──────────────────────────────────────────────────────────────────

class _OverviewHeader extends StatelessWidget {
  const _OverviewHeader();

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('概览', style: TraioTokens.display(size: 30)),
            const SizedBox(height: 5),
            Row(
              children: [
                Text('2026年6月1日 · 周日',
                    style: TraioTokens.ui(
                        size: 13,
                        color: TraioTokens.text3,
                        weight: FontWeight.w600)),
                const SizedBox(width: 10),
                _MarketStatusChip(open: false),
              ],
            ),
          ],
        ),
        const Spacer(),
        const _MarketPulse(),
      ],
    );
  }
}

class _MarketStatusChip extends StatelessWidget {
  const _MarketStatusChip({required this.open});
  final bool open;

  @override
  Widget build(BuildContext context) {
    final color = open ? TraioTokens.up : TraioTokens.text3;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: color.withValues(alpha: 0.28)),
      ),
      child: Text(open ? '盘中' : '休市',
          style:
              TraioTokens.ui(size: 11, color: color, weight: FontWeight.w800)),
    );
  }
}

class _MarketPulse extends StatelessWidget {
  const _MarketPulse();

  static const _items = [
    _PulseItem('SPY', 530.24, 0.62),
    _PulseItem('QQQ', 458.17, 0.91),
    _PulseItem('VIX', 13.84, -4.21),
    _PulseItem('BTC', 68420, 3.84),
  ];

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 10),
      decoration: BoxDecoration(
        color: TraioTokens.surface,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(TraioTokens.rLg),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (var i = 0; i < _items.length; i++) ...[
            if (i > 0)
              Container(
                  width: 1,
                  height: 28,
                  margin: const EdgeInsets.symmetric(horizontal: 14),
                  color: TraioTokens.border),
            _PulseCell(item: _items[i]),
          ],
        ],
      ),
    );
  }
}

class _PulseItem {
  const _PulseItem(this.ticker, this.price, this.change);
  final String ticker;
  final double price;
  final double change;
}

class _PulseCell extends StatelessWidget {
  const _PulseCell({required this.item});
  final _PulseItem item;

  @override
  Widget build(BuildContext context) {
    final isUp = item.change >= 0;
    final color = isUp ? TraioTokens.up : TraioTokens.down;
    final priceStr = item.ticker == 'BTC'
        ? '\$${_commaFmt(item.price.toInt())}'
        : '\$${item.price.toStringAsFixed(2)}';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Text(item.ticker,
            style: TraioTokens.ui(
                size: 11, color: TraioTokens.text3, weight: FontWeight.w800)),
        const SizedBox(height: 2),
        Text(priceStr,
            style: TraioTokens.mono(size: 13, color: TraioTokens.text)),
        Text('${isUp ? '+' : ''}${item.change.toStringAsFixed(2)}%',
            style: TraioTokens.mono(
                size: 11, color: color, weight: FontWeight.w500)),
      ],
    );
  }

  static String _commaFmt(int v) => v
      .toString()
      .replaceAllMapped(RegExp(r'\B(?=(\d{3})+(?!\d))'), (_) => ',');
}

// ─── KPI Grid ────────────────────────────────────────────────────────────────

class _KpiGrid extends StatelessWidget {
  const _KpiGrid();

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // 总资产 — clickable, opens broker accounts sheet
        Expanded(
          child: _TotalAssetsKpiCard(),
        ),
        const SizedBox(width: 12),
        const Expanded(
            child: _KpiCard(
          label: '今日盈亏',
          value: '+\$3,847',
          valueColor: TraioTokens.up,
          caption: '未实现 +\$3,412  ·  已实现 +\$435',
        )),
        const SizedBox(width: 12),
        const Expanded(
            child: _KpiCard(
          label: '可用现金',
          value: '\$34,940',
          valueColor: TraioTokens.text,
          caption: '现金仓位 12.3%',
        )),
        const SizedBox(width: 12),
        const Expanded(
            child: _KpiCard(
          label: '总购买力',
          value: '\$92,060',
          valueColor: TraioTokens.text,
          caption: '含保证金放大',
        )),
      ],
    );
  }
}

/// 总资产卡片：点击弹出券商账户面板
class _TotalAssetsKpiCard extends StatelessWidget {
  const _TotalAssetsKpiCard();

  @override
  Widget build(BuildContext context) {
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      child: GestureDetector(
        onTap: () => _showBrokerSheet(context),
        child: Container(
          padding: const EdgeInsets.fromLTRB(20, 18, 20, 16),
          decoration: BoxDecoration(
            color: TraioTokens.surface,
            border: Border.all(color: TraioTokens.border),
            borderRadius: BorderRadius.circular(TraioTokens.rLg),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Text('总资产',
                      style: TraioTokens.ui(
                          size: 12,
                          color: TraioTokens.text3,
                          weight: FontWeight.w700)),
                  const Spacer(),
                  Icon(Icons.chevron_right_rounded,
                      size: 15, color: TraioTokens.text3),
                ],
              ),
              const SizedBox(height: 10),
              Text('\$284,920', style: TraioTokens.mono(size: 24)),
              const SizedBox(height: 7),
              Text('持仓 \$249,980  ·  现金 \$34,940',
                  style: TraioTokens.mono(
                      size: 12,
                      color: TraioTokens.text3,
                      weight: FontWeight.w500)),
            ],
          ),
        ),
      ),
    );
  }

  void _showBrokerSheet(BuildContext context) {
    showDialog<void>(
      context: context,
      barrierColor: Colors.black.withValues(alpha: 0.28),
      builder: (_) => const _BrokerAccountsDialog(),
    );
  }
}

class _KpiCard extends StatelessWidget {
  const _KpiCard({
    required this.label,
    required this.value,
    required this.caption,
    this.valueColor = TraioTokens.text,
  });

  final String label;
  final String value;
  final String caption;
  final Color valueColor;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.fromLTRB(20, 18, 20, 16),
      decoration: BoxDecoration(
        color: TraioTokens.surface,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(TraioTokens.rLg),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label,
              style: TraioTokens.ui(
                  size: 12, color: TraioTokens.text3, weight: FontWeight.w700)),
          const SizedBox(height: 10),
          Text(value, style: TraioTokens.mono(size: 24, color: valueColor)),
          const SizedBox(height: 7),
          Text(caption,
              style: TraioTokens.mono(
                  size: 12, color: TraioTokens.text3, weight: FontWeight.w500)),
        ],
      ),
    );
  }
}

// ─── Broker Accounts Dialog ───────────────────────────────────────────────────

class _BrokerAccountsDialog extends StatelessWidget {
  const _BrokerAccountsDialog();

  @override
  Widget build(BuildContext context) {
    return Dialog(
      backgroundColor: Colors.transparent,
      insetPadding: const EdgeInsets.symmetric(horizontal: 40, vertical: 60),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 780),
        child: Container(
          decoration: BoxDecoration(
            color: TraioTokens.surface,
            border: Border.all(color: TraioTokens.border),
            borderRadius: BorderRadius.circular(TraioTokens.rXl),
            boxShadow: TraioTokens.shadowLg,
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // Header
              Padding(
                padding: const EdgeInsets.fromLTRB(24, 22, 16, 0),
                child: Row(
                  children: [
                    Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text('券商账户',
                            style: TraioTokens.display(size: 20)),
                        const SizedBox(height: 2),
                        Text('净值 · 持仓市值 · 可用现金 · 购买力 · 保证金',
                            style: TraioTokens.ui(
                                size: 12,
                                color: TraioTokens.text3,
                                weight: FontWeight.w600)),
                      ],
                    ),
                    const Spacer(),
                    IconButton(
                      onPressed: () => Navigator.of(context).pop(),
                      icon: const Icon(Icons.close_rounded,
                          size: 20, color: TraioTokens.text2),
                      splashRadius: 18,
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 16),
              const Divider(height: 1, color: TraioTokens.border),
              Padding(
                padding: const EdgeInsets.fromLTRB(24, 12, 24, 0),
                child: _BrokerTableHeader(),
              ),
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 24),
                child: Divider(height: 8, color: TraioTokens.border),
              ),
              for (final acct in accounts)
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 24),
                  child: Column(
                    children: [
                      _BrokerAccountRow(account: acct),
                      const Divider(height: 1, color: TraioTokens.border),
                    ],
                  ),
                ),
              const SizedBox(height: 16),
            ],
          ),
        ),
      ),
    );
  }
}

class _BrokerTableHeader extends StatelessWidget {
  const _BrokerTableHeader();

  @override
  Widget build(BuildContext context) {
    const style = TextStyle(
      fontFamily: TraioTokens.uiFont,
      fontSize: 11,
      fontWeight: FontWeight.w700,
      color: TraioTokens.text3,
      letterSpacing: 0.4,
    );
    return Row(
      children: const [
        Expanded(flex: 30, child: Text('账户', style: style)),
        Expanded(flex: 18, child: _RightText('净值', style)),
        Expanded(flex: 18, child: _RightText('持仓市值', style)),
        Expanded(flex: 16, child: _RightText('可用现金', style)),
        Expanded(flex: 16, child: _RightText('购买力', style)),
        Expanded(flex: 18, child: _RightText('保证金', style)),
      ],
    );
  }
}

class _BrokerAccountRow extends StatelessWidget {
  const _BrokerAccountRow({required this.account});
  final BrokerAccount account;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Row(
        children: [
          Expanded(
            flex: 30,
            child: Row(
              children: [
                Container(
                  width: 32,
                  height: 32,
                  decoration: BoxDecoration(
                    color: account.color.withValues(alpha: 0.12),
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(
                        color: account.color.withValues(alpha: 0.28)),
                  ),
                  alignment: Alignment.center,
                  child: Text(account.short,
                      style: TraioTokens.ui(
                          size: 11,
                          color: account.color,
                          weight: FontWeight.w900)),
                ),
                const SizedBox(width: 10),
                Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(account.name,
                        style:
                            TraioTokens.ui(size: 13, weight: FontWeight.w700)),
                    Text(account.kind,
                        style: TraioTokens.ui(
                            size: 11,
                            color: TraioTokens.text3,
                            weight: FontWeight.w500)),
                  ],
                ),
              ],
            ),
          ),
          Expanded(
            flex: 18,
            child: Text(fmtUsd(account.netValue, dp: 0),
                style: TraioTokens.mono(size: 13),
                textAlign: TextAlign.right),
          ),
          Expanded(
            flex: 18,
            child: Text(fmtUsd(account.netValue - account.cash, dp: 0),
                style: TraioTokens.mono(size: 13),
                textAlign: TextAlign.right),
          ),
          Expanded(
            flex: 16,
            child: Text(fmtUsd(account.cash, dp: 0),
                style: TraioTokens.mono(size: 13),
                textAlign: TextAlign.right),
          ),
          Expanded(
            flex: 16,
            child: Text(fmtUsd(account.buyingPower, dp: 0),
                style: TraioTokens.mono(size: 13),
                textAlign: TextAlign.right),
          ),
          Expanded(
            flex: 18,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Text('${account.marginUsed}%',
                    style: TraioTokens.mono(
                        size: 12,
                        color: account.marginUsed > 50
                            ? TraioTokens.down
                            : account.marginUsed > 25
                                ? TraioTokens.warn
                                : TraioTokens.text2)),
                const SizedBox(height: 4),
                _MiniBar(pct: account.marginUsed / 100),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

// ─── Alert Banner ─────────────────────────────────────────────────────────────

class _AlertBanner extends StatelessWidget {
  const _AlertBanner();

  static const bool _hasAlert = true;

  @override
  Widget build(BuildContext context) {
    if (!_hasAlert) return const SizedBox.shrink();
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 12),
      decoration: BoxDecoration(
        color: TraioTokens.warn.withValues(alpha: 0.10),
        border: Border.all(color: TraioTokens.warn.withValues(alpha: 0.35)),
        borderRadius: BorderRadius.circular(TraioTokens.r),
      ),
      child: Row(
        children: [
          Icon(Icons.notifications_outlined, size: 18, color: TraioTokens.warn),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              'moomoo CSV 待映射 — 72 行需确认字段后方可入库',
              style: TraioTokens.ui(
                  size: 13, color: TraioTokens.warn, weight: FontWeight.w600),
            ),
          ),
          const SizedBox(width: 10),
          Text('查看订单 →',
              style: TraioTokens.ui(
                  size: 13, color: TraioTokens.warn, weight: FontWeight.w800)),
        ],
      ),
    );
  }
}

// ─── Main Content Row ─────────────────────────────────────────────────────────

class _MainContentRow extends StatelessWidget {
  const _MainContentRow();

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: const [
        Expanded(flex: 7, child: _HoldingsPerformanceCard()),
        SizedBox(width: 16),
        Expanded(flex: 3, child: _ConcentrationRiskCard()),
      ],
    );
  }
}

// ─── Holdings Performance Table ───────────────────────────────────────────────

class _HoldingsPerformanceCard extends StatelessWidget {
  const _HoldingsPerformanceCard();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: TraioTokens.surface,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(TraioTokens.rLg),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('持仓今日表现', style: TraioTokens.display(size: 18)),
          const SizedBox(height: 3),
          Text('各标的今日涨跌 · 盈亏金额',
              style: TraioTokens.ui(
                  size: 12,
                  color: TraioTokens.text3,
                  weight: FontWeight.w600)),
          const SizedBox(height: 18),
          _HoldingsTableHeader(),
          const SizedBox(height: 8),
          const Divider(height: 1, color: TraioTokens.border),
          for (final h in holdings) ...[
            _HoldingRow(holding: h),
            const Divider(height: 1, color: TraioTokens.border),
          ],
        ],
      ),
    );
  }
}

class _HoldingsTableHeader extends StatelessWidget {
  const _HoldingsTableHeader();

  @override
  Widget build(BuildContext context) {
    const style = TextStyle(
      fontFamily: TraioTokens.uiFont,
      fontSize: 11,
      fontWeight: FontWeight.w700,
      color: TraioTokens.text3,
      letterSpacing: 0.4,
    );
    return Row(
      children: const [
        Expanded(flex: 22, child: Text('标的', style: style)),
        Expanded(flex: 18, child: _RightText('持仓市值', style)),
        Expanded(flex: 16, child: _RightText('今日涨跌', style)),
        Expanded(flex: 18, child: _RightText('今日盈亏', style)),
        Expanded(flex: 18, child: _RightText('总盈亏', style)),
        Expanded(flex: 12, child: _RightText('占比', style)),
      ],
    );
  }
}

class _HoldingRow extends StatelessWidget {
  const _HoldingRow({required this.holding});
  final Holding holding;

  @override
  Widget build(BuildContext context) {
    final dayPnl = holding.value * holding.change / 100;
    final changeUp = holding.change >= 0;
    final changeColor = changeUp ? TraioTokens.up : TraioTokens.down;
    final unrealizedUp = holding.unrealized >= 0;
    final unrealizedColor = unrealizedUp ? TraioTokens.up : TraioTokens.down;

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 11),
      child: Row(
        children: [
          // Symbol + market tag
          Expanded(
            flex: 22,
            child: Row(
              children: [
                Container(
                  width: 8,
                  height: 28,
                  decoration: BoxDecoration(
                    color: holding.color,
                    borderRadius: BorderRadius.circular(3),
                  ),
                ),
                const SizedBox(width: 10),
                Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(holding.sym,
                        style: TraioTokens.ui(
                            size: 13, weight: FontWeight.w800)),
                    Text(holding.market,
                        style: TraioTokens.ui(
                            size: 11,
                            color: TraioTokens.text3,
                            weight: FontWeight.w500)),
                  ],
                ),
              ],
            ),
          ),
          // Market value
          Expanded(
            flex: 18,
            child: Text(fmtUsd(holding.value, dp: 0),
                style: TraioTokens.mono(size: 13),
                textAlign: TextAlign.right),
          ),
          // Day change % — colored pill
          Expanded(
            flex: 16,
            child: Align(
              alignment: Alignment.centerRight,
              child: Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                decoration: BoxDecoration(
                  color: changeColor.withValues(alpha: 0.10),
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Text(
                  '${changeUp ? '+' : ''}${holding.change.toStringAsFixed(2)}%',
                  style: TraioTokens.mono(
                      size: 12, color: changeColor, weight: FontWeight.w700),
                ),
              ),
            ),
          ),
          // Day P&L
          Expanded(
            flex: 18,
            child: Text(
              '${changeUp ? '+' : '−'}\$${dayPnl.abs().toStringAsFixed(0)}',
              style: TraioTokens.mono(size: 13, color: changeColor),
              textAlign: TextAlign.right,
            ),
          ),
          // Total unrealized P&L
          Expanded(
            flex: 18,
            child: Text(
              '${unrealizedUp ? '+' : '−'}\$${holding.unrealized.abs().toStringAsFixed(0)}',
              style: TraioTokens.mono(size: 13, color: unrealizedColor),
              textAlign: TextAlign.right,
            ),
          ),
          // Weight
          Expanded(
            flex: 12,
            child: Text('${holding.weight.toStringAsFixed(1)}%',
                style: TraioTokens.mono(
                    size: 13, color: TraioTokens.text2),
                textAlign: TextAlign.right),
          ),
        ],
      ),
    );
  }
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

class _RightText extends StatelessWidget {
  const _RightText(this.text, this.style);
  final String text;
  final TextStyle style;

  @override
  Widget build(BuildContext context) =>
      Text(text, style: style, textAlign: TextAlign.right);
}

class _MiniBar extends StatelessWidget {
  const _MiniBar({required this.pct});
  final double pct;

  @override
  Widget build(BuildContext context) {
    final color = pct > 0.5
        ? TraioTokens.down
        : pct > 0.25
            ? TraioTokens.warn
            : TraioTokens.up;
    return LayoutBuilder(builder: (context, constraints) {
      return Container(
        width: constraints.maxWidth,
        height: 4,
        decoration: BoxDecoration(
          color: TraioTokens.surfaceSunk,
          borderRadius: BorderRadius.circular(99),
        ),
        alignment: Alignment.centerLeft,
        child: FractionallySizedBox(
          widthFactor: pct.clamp(0.0, 1.0),
          child: Container(
            decoration: BoxDecoration(
              color: color,
              borderRadius: BorderRadius.circular(99),
            ),
          ),
        ),
      );
    });
  }
}

// ─── Concentration Risk Card ──────────────────────────────────────────────────

class _ConcentrationRiskCard extends StatelessWidget {
  const _ConcentrationRiskCard();

  static const _items = [
    _RiskItem('单股集中度', '偏集中', _RiskLevel.medium, 22.0, 20.0, 'NVDA 占比 22%'),
    _RiskItem('行业集中度', '过高', _RiskLevel.high, 58.0, 40.0, '科技股 58%'),
    _RiskItem('现金比例', '正常', _RiskLevel.ok, 12.3, 10.0, '低于 10% 预警'),
    _RiskItem('杠杆率', '正常', _RiskLevel.ok, 18.0, 50.0, '总保证金 18%'),
  ];

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: TraioTokens.surface,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(TraioTokens.rLg),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('集中度风险', style: TraioTokens.display(size: 18)),
          const SizedBox(height: 3),
          Text('持仓分布 · 暴露度检测',
              style: TraioTokens.ui(
                  size: 12,
                  color: TraioTokens.text3,
                  weight: FontWeight.w600)),
          const SizedBox(height: 18),
          for (var i = 0; i < _items.length; i++) ...[
            if (i > 0) const Divider(height: 20, color: TraioTokens.border),
            _RiskRow(item: _items[i]),
          ],
        ],
      ),
    );
  }
}

enum _RiskLevel { ok, medium, high }

class _RiskItem {
  const _RiskItem(
      this.name, this.badge, this.level, this.value, this.threshold, this.hint);
  final String name;
  final String badge;
  final _RiskLevel level;
  final double value;
  final double threshold;
  final String hint;
}

class _RiskRow extends StatelessWidget {
  const _RiskRow({required this.item});
  final _RiskItem item;

  Color get _color => switch (item.level) {
        _RiskLevel.ok => TraioTokens.up,
        _RiskLevel.medium => TraioTokens.warn,
        _RiskLevel.high => TraioTokens.down,
      };

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              child: Text(item.name,
                  style: TraioTokens.ui(size: 13, weight: FontWeight.w700)),
            ),
            _RiskBadge(label: item.badge, color: _color),
          ],
        ),
        const SizedBox(height: 6),
        Row(
          children: [
            Text('${item.value.toStringAsFixed(1)}%',
                style: TraioTokens.mono(size: 13, color: _color)),
            const SizedBox(width: 6),
            Text('建议 <${item.threshold.toStringAsFixed(0)}%',
                style: TraioTokens.ui(
                    size: 11,
                    color: TraioTokens.text3,
                    weight: FontWeight.w500)),
          ],
        ),
        const SizedBox(height: 7),
        _ThinBar(
            pct: (item.value / (item.threshold * 1.8)).clamp(0.0, 1.0),
            color: _color),
        const SizedBox(height: 4),
        Text(item.hint,
            style: TraioTokens.ui(
                size: 11,
                color: TraioTokens.text3,
                weight: FontWeight.w500)),
      ],
    );
  }
}

class _RiskBadge extends StatelessWidget {
  const _RiskBadge({required this.label, required this.color});
  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.10),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(label,
          style:
              TraioTokens.ui(size: 11, color: color, weight: FontWeight.w800)),
    );
  }
}

class _ThinBar extends StatelessWidget {
  const _ThinBar({required this.pct, required this.color});
  final double pct;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(builder: (context, constraints) {
      return Container(
        width: constraints.maxWidth,
        height: 3,
        decoration: BoxDecoration(
          color: TraioTokens.surfaceSunk,
          borderRadius: BorderRadius.circular(99),
        ),
        alignment: Alignment.centerLeft,
        child: FractionallySizedBox(
          widthFactor: pct,
          child: Container(
            decoration: BoxDecoration(
              color: color,
              borderRadius: BorderRadius.circular(99),
            ),
          ),
        ),
      );
    });
  }
}
