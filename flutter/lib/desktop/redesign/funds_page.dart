import 'package:flutter/material.dart';

import 'mock_data.dart';
import 'shared_widgets.dart';
import 'traio_tokens.dart';

class FundsDeskPage extends StatelessWidget {
  const FundsDeskPage({super.key});

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(0, 50, 0, 110),
      children: const [
        PageHeader(kicker: '跨券商净值与现金购买力', title: '账户与资金'),
        SizedBox(height: 50),
        FundsSummaryStrip(),
        SizedBox(height: 42),
        SectionTitle(title: '券商账户', hint: '净值 · 现金 · 购买力 · 保证金占用'),
        SizedBox(height: 18),
        BrokerAccountsSection(),
        SizedBox(height: 42),
        FundingTransactionsSection(),
      ],
    );
  }
}

class OverviewFundsSummaryStrip extends StatelessWidget {
  const OverviewFundsSummaryStrip({super.key});

  @override
  Widget build(BuildContext context) {
    final totalCash = accounts.fold<double>(0, (s, a) => s + a.cash);
    final totalBp = accounts.fold<double>(0, (s, a) => s + a.buyingPower);
    return SummaryStrip(
      cells: [
        const SummaryCellData(
            label: '总资产',
            value: '\$284,920',
            caption: '持仓 \$250,220 · 现金 \$41,300'),
        SummaryCellData(
            label: '今日盈亏',
            value: fmtSignedUsd(3847, dp: 0),
            caption: '+1.37% 今日 · 已实现 +\$40,343',
            valueColor: TraioTokens.up),
        SummaryCellData(
            label: '可用现金',
            value: fmtUsd(totalCash, dp: 0),
            caption: '14.5% 现金仓位'),
        SummaryCellData(
            label: '总购买力', value: fmtUsd(totalBp, dp: 0), caption: '保证金账户合并'),
      ],
    );
  }
}

class FundsSummaryStrip extends StatelessWidget {
  const FundsSummaryStrip({super.key});

  @override
  Widget build(BuildContext context) {
    final totalCash = accounts.fold<double>(0, (s, a) => s + a.cash);
    final totalBp = accounts.fold<double>(0, (s, a) => s + a.buyingPower);
    return SummaryStrip(
      cells: [
        const SummaryCellData(
            label: '总净值', value: '\$284,920', caption: '3 家券商已聚合'),
        SummaryCellData(
            label: '可用现金', value: fmtUsd(totalCash, dp: 0), caption: '美元归一口径'),
        SummaryCellData(
            label: '总购买力', value: fmtUsd(totalBp, dp: 0), caption: '保证金账户合并'),
        const SummaryCellData(
            label: '本月分红',
            value: '\$1,284',
            caption: '含港币折美元',
            valueColor: TraioTokens.up),
      ],
    );
  }
}

class BrokerAccountsSection extends StatelessWidget {
  const BrokerAccountsSection({super.key, this.stacked = false});

  final bool stacked;

  @override
  Widget build(BuildContext context) {
    if (stacked) {
      return Column(
        children: [
          for (final account in accounts) ...[
            _FundCard(account: account),
            if (account != accounts.last) const SizedBox(height: 12),
          ],
        ],
      );
    }

    return LayoutBuilder(
      builder: (context, constraints) {
        if (constraints.maxWidth < 900) {
          return Column(
            children: [
              for (final account in accounts) ...[
                _FundCard(account: account),
                if (account != accounts.last) const SizedBox(height: 12),
              ],
            ],
          );
        }

        return Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            for (final account in accounts) ...[
              Expanded(child: _FundCard(account: account)),
              if (account != accounts.last) const SizedBox(width: 16),
            ],
          ],
        );
      },
    );
  }
}

class FundingTransactionsSection extends StatelessWidget {
  const FundingTransactionsSection({super.key});

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SectionTitle(title: '资金流水', hint: '最近 5 条'),
        const SizedBox(height: 18),
        TraioCard(
          padding: EdgeInsets.zero,
          child: Column(
            children: [for (final txn in transactions) _TxnRow(txn: txn)],
          ),
        ),
      ],
    );
  }
}

class _FundCard extends StatelessWidget {
  const _FundCard({required this.account});

  final BrokerAccount account;

  @override
  Widget build(BuildContext context) {
    final marginColor = account.marginUsed < 25
        ? TraioTokens.up
        : account.marginUsed < 50
            ? TraioTokens.warn
            : TraioTokens.down;
    return TraioCard(
      padding: const EdgeInsets.fromLTRB(18, 16, 18, 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                width: 32,
                height: 32,
                decoration: BoxDecoration(
                  color: account.color,
                  borderRadius: BorderRadius.circular(9),
                ),
                alignment: Alignment.center,
                child: Text(account.short,
                    style: TraioTokens.mono(size: 12, color: Colors.white)),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(account.name,
                        style:
                            TraioTokens.ui(size: 15, weight: FontWeight.w800)),
                    Text('${account.kind} · ${account.last4}',
                        style: TraioTokens.ui(
                            size: 12,
                            color: TraioTokens.text3,
                            weight: FontWeight.w600)),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 16),
          Text(fmtUsd(account.netValue, dp: 0),
              style: TraioTokens.mono(size: 24)),
          const SizedBox(height: 14),
          _kv('持仓市值', fmtUsd(account.netValue - account.cash, dp: 0)),
          _kv('可用现金', fmtUsd(account.cash, dp: 0)),
          _kv('购买力', fmtUsd(account.buyingPower, dp: 0)),
          const SizedBox(height: 12),
          Text('保证金占用 ${account.marginUsed.toStringAsFixed(0)}%',
              style: TraioTokens.ui(
                  size: 12, color: TraioTokens.text3, weight: FontWeight.w700)),
          const SizedBox(height: 8),
          ClipRRect(
            borderRadius: BorderRadius.circular(99),
            child: LinearProgressIndicator(
              minHeight: 7,
              value: account.marginUsed / 100,
              backgroundColor: TraioTokens.surfaceSunk,
              valueColor: AlwaysStoppedAnimation(marginColor),
            ),
          ),
        ],
      ),
    );
  }

  Widget _kv(String k, String v) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 7),
      child: Row(
        children: [
          Text(k,
              style: TraioTokens.ui(
                  size: 13, color: TraioTokens.text3, weight: FontWeight.w600)),
          const Spacer(),
          SizedBox(
            width: 92,
            child: Text(
              v,
              textAlign: TextAlign.end,
              style: TraioTokens.mono(size: 13),
            ),
          ),
        ],
      ),
    );
  }
}

class _TxnRow extends StatelessWidget {
  const _TxnRow({required this.txn});

  final TransactionItem txn;

  @override
  Widget build(BuildContext context) {
    final color = txn.amount > 0
        ? TraioTokens.up
        : txn.amount < 0
            ? TraioTokens.down
            : TraioTokens.text3;
    final icon = switch (txn.kind) {
      'deposit' => Icons.arrow_downward_rounded,
      'withdraw' => Icons.arrow_upward_rounded,
      'fee' => Icons.receipt_long_outlined,
      'dividend' => Icons.payments_outlined,
      _ => Icons.swap_horiz_rounded,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 14),
      decoration: const BoxDecoration(
        border: Border(bottom: BorderSide(color: TraioTokens.border)),
      ),
      child: Row(
        children: [
          Container(
            width: 34,
            height: 34,
            decoration: BoxDecoration(
              color: color.withValues(alpha: 0.12),
              borderRadius: BorderRadius.circular(10),
            ),
            child: Icon(icon, size: 17, color: color),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(txn.label,
                    style: TraioTokens.ui(size: 14, weight: FontWeight.w800)),
                const SizedBox(height: 4),
                Text('${txn.account} · ${txn.date}',
                    style: TraioTokens.ui(
                        size: 12,
                        color: TraioTokens.text3,
                        weight: FontWeight.w600)),
              ],
            ),
          ),
          Text(fmtSignedUsd(txn.amount),
              style: TraioTokens.mono(size: 14, color: color)),
        ],
      ),
    );
  }
}
