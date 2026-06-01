import 'package:flutter/material.dart';

import 'mock_data.dart';
import 'shared_widgets.dart';
import 'traio_tokens.dart';

class WatchDeskPage extends StatefulWidget {
  const WatchDeskPage({super.key});

  @override
  State<WatchDeskPage> createState() => _WatchDeskPageState();
}

class _WatchDeskPageState extends State<WatchDeskPage> {
  var selected = '我的清单';

  @override
  Widget build(BuildContext context) {
    final group = watchGroups.firstWhere((g) => g.name == selected,
        orElse: () => watchGroups.first);
    return ListView(
      padding: const EdgeInsets.fromLTRB(0, 50, 0, 110),
      children: [
        const PageHeader(kicker: '分组盯盘与价格预警', title: '自选'),
        const SizedBox(height: 50),
        Row(
          children: [
            Expanded(
              child: Container(
                height: 46,
                padding: const EdgeInsets.symmetric(horizontal: 16),
                decoration: BoxDecoration(
                  color: TraioTokens.surface,
                  border: Border.all(color: TraioTokens.border),
                  borderRadius: BorderRadius.circular(TraioTokens.r),
                ),
                child: Row(
                  children: [
                    const Icon(Icons.search_rounded,
                        size: 18, color: TraioTokens.text3),
                    const SizedBox(width: 10),
                    Text('添加代码、名称或 ISIN',
                        style: TraioTokens.ui(
                            size: 14,
                            color: TraioTokens.text3,
                            weight: FontWeight.w600)),
                  ],
                ),
              ),
            ),
            const SizedBox(width: 16),
            SegmentedFilter(
              items: watchGroups.map((g) => g.name).toList(),
              selected: selected,
              onSelected: (v) => setState(() => selected = v),
            ),
          ],
        ),
        const SizedBox(height: 34),
        SectionTitle(
            title: group.name, hint: '${group.items.length} 个标的 · 管理预警'),
        const SizedBox(height: 16),
        TraioCard(
          padding: EdgeInsets.zero,
          child: Column(children: [
            for (final item in group.items) _WatchRow(item: item)
          ]),
        ),
      ],
    );
  }
}

class _WatchRow extends StatelessWidget {
  const _WatchRow({required this.item});

  final WatchItem item;

  @override
  Widget build(BuildContext context) {
    final color = toneColor(item.change);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 14),
      decoration: const BoxDecoration(
          border: Border(bottom: BorderSide(color: TraioTokens.border))),
      child: Row(
        children: [
          SizedBox(
            width: 185,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(children: [
                  Text(item.symbol, style: TraioTokens.mono(size: 15)),
                  const SizedBox(width: 7),
                  MarketTag(label: item.market)
                ]),
                const SizedBox(height: 4),
                Text(item.name,
                    style: TraioTokens.ui(
                        size: 12,
                        color: TraioTokens.text3,
                        weight: FontWeight.w600)),
                if (item.alert != null) ...[
                  const SizedBox(height: 6),
                  StatusPill(label: item.alert!, color: TraioTokens.accent),
                ],
              ],
            ),
          ),
          Expanded(
              child: Sparkline(values: item.spark, color: color, height: 38)),
          const SizedBox(width: 24),
          SizedBox(
              width: 108,
              child: Text(fmtUsd(item.last),
                  textAlign: TextAlign.right,
                  style: TraioTokens.mono(size: 15))),
          const SizedBox(width: 18),
          Container(
            width: 86,
            padding: const EdgeInsets.symmetric(vertical: 6),
            decoration: BoxDecoration(
                color: color.withValues(alpha: 0.12),
                borderRadius: BorderRadius.circular(8)),
            alignment: Alignment.center,
            child: Text(fmtPct(item.change),
                style: TraioTokens.mono(size: 13, color: color)),
          ),
          const SizedBox(width: 16),
          Icon(Icons.notifications_none_rounded,
              size: 20,
              color: item.alert == null
                  ? TraioTokens.text3.withValues(alpha: 0.55)
                  : TraioTokens.accent),
        ],
      ),
    );
  }
}
