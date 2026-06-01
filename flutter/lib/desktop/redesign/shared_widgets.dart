import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'traio_tokens.dart';

class TraioCard extends StatelessWidget {
  const TraioCard({
    required this.child,
    super.key,
    this.padding = const EdgeInsets.all(18),
    this.radius = TraioTokens.rLg,
  });

  final Widget child;
  final EdgeInsetsGeometry padding;
  final double radius;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: padding,
      decoration: BoxDecoration(
        color: TraioTokens.surface,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(radius),
      ),
      child: child,
    );
  }
}

class PageHeader extends StatelessWidget {
  const PageHeader({
    required this.kicker,
    required this.title,
    super.key,
    this.trailing,
  });

  final String kicker;
  final String title;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(kicker,
                  style: TraioTokens.ui(
                      size: 13,
                      color: TraioTokens.text3,
                      weight: FontWeight.w700)),
              const SizedBox(height: 6),
              Row(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  Text(title, style: TraioTokens.display(size: 30)),
                  const SizedBox(width: 52),
                  Padding(
                    padding: const EdgeInsets.only(bottom: 3),
                    child: Text('2026年5月31日 · 周日',
                        style: TraioTokens.ui(
                            size: 15,
                            color: TraioTokens.text3,
                            weight: FontWeight.w600)),
                  ),
                ],
              ),
            ],
          ),
        ),
        if (trailing != null) trailing!,
        const SizedBox(width: 22),
        const AvatarBadge(),
      ],
    );
  }
}

class AvatarBadge extends StatelessWidget {
  const AvatarBadge({super.key});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 42,
      height: 42,
      decoration: const BoxDecoration(
          color: TraioTokens.accent, shape: BoxShape.circle),
      alignment: Alignment.center,
      child: Text('AC',
          style: TraioTokens.ui(
              size: 14, color: Colors.white, weight: FontWeight.w800)),
    );
  }
}

class SummaryStrip extends StatelessWidget {
  const SummaryStrip({required this.cells, super.key});

  final List<SummaryCellData> cells;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: TraioTokens.surface,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(TraioTokens.rLg),
      ),
      clipBehavior: Clip.antiAlias,
      child: Row(
        children: [
          for (var i = 0; i < cells.length; i++)
            Expanded(
              child: Container(
                padding: const EdgeInsets.fromLTRB(24, 20, 24, 18),
                decoration: BoxDecoration(
                  border: i == cells.length - 1
                      ? null
                      : const Border(
                          right: BorderSide(color: TraioTokens.border)),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(cells[i].label,
                        style: TraioTokens.ui(
                            size: 13,
                            color: TraioTokens.text3,
                            weight: FontWeight.w700)),
                    const SizedBox(height: 13),
                    Text(cells[i].value,
                        style: TraioTokens.mono(
                            size: 26,
                            color: cells[i].valueColor ?? TraioTokens.text)),
                    const SizedBox(height: 9),
                    Text(cells[i].caption,
                        style: TraioTokens.mono(
                            size: 13,
                            color: TraioTokens.text3,
                            weight: FontWeight.w500)),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class SummaryCellData {
  const SummaryCellData(
      {required this.label,
      required this.value,
      required this.caption,
      this.valueColor});

  final String label;
  final String value;
  final String caption;
  final Color? valueColor;
}

class SectionTitle extends StatelessWidget {
  const SectionTitle(
      {required this.title, super.key, this.hint, this.trailing});

  final String title;
  final String? hint;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Text(title, style: TraioTokens.display(size: 20)),
        if (hint != null) ...[
          const SizedBox(width: 12),
          Text(hint!,
              style: TraioTokens.ui(
                  size: 14, color: TraioTokens.text3, weight: FontWeight.w600)),
        ],
        const Spacer(),
        if (trailing != null) trailing!,
      ],
    );
  }
}

class StatusPill extends StatelessWidget {
  const StatusPill({required this.label, required this.color, super.key});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
              width: 6,
              height: 6,
              decoration: BoxDecoration(color: color, shape: BoxShape.circle)),
          const SizedBox(width: 6),
          Text(label,
              style: TraioTokens.ui(
                  size: 12, color: color, weight: FontWeight.w800)),
        ],
      ),
    );
  }
}

class MarketTag extends StatelessWidget {
  const MarketTag({required this.label, super.key});

  final String label;

  @override
  Widget build(BuildContext context) {
    final color = switch (label) {
      '港股' => const Color(0xFFC0786E),
      'ETF' => const Color(0xFF4F987F),
      'Crypto' => const Color(0xFFC6963F),
      _ => const Color(0xFF5795D2),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.06),
        border: Border.all(color: color.withValues(alpha: 0.55)),
        borderRadius: BorderRadius.circular(7),
      ),
      child: Text(label,
          style:
              TraioTokens.ui(size: 11, color: color, weight: FontWeight.w800)),
    );
  }
}

class Sparkline extends StatelessWidget {
  const Sparkline(
      {required this.values, required this.color, super.key, this.height = 36});

  final List<double> values;
  final Color color;
  final double height;

  @override
  Widget build(BuildContext context) {
    return CustomPaint(
      size: Size(double.infinity, height),
      painter: _SparkPainter(values: values, color: color),
    );
  }
}

class _SparkPainter extends CustomPainter {
  const _SparkPainter({required this.values, required this.color});

  final List<double> values;
  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    if (values.length < 2 || size.width <= 0) return;
    final minV = values.reduce(math.min);
    final maxV = values.reduce(math.max);
    final range = maxV - minV == 0 ? 1 : maxV - minV;
    final path = Path();
    for (var i = 0; i < values.length; i++) {
      final x = i / (values.length - 1) * size.width;
      final y = 4 + (1 - (values[i] - minV) / range) * (size.height - 8);
      if (i == 0) {
        path.moveTo(x, y);
      } else {
        path.lineTo(x, y);
      }
    }
    canvas.drawPath(
      path,
      Paint()
        ..color = color
        ..strokeWidth = 2
        ..style = PaintingStyle.stroke
        ..strokeCap = StrokeCap.round
        ..strokeJoin = StrokeJoin.round,
    );
  }

  @override
  bool shouldRepaint(covariant _SparkPainter oldDelegate) {
    return oldDelegate.values != values || oldDelegate.color != color;
  }
}

class SegmentedFilter extends StatelessWidget {
  const SegmentedFilter(
      {required this.items,
      required this.selected,
      required this.onSelected,
      super.key});

  final List<String> items;
  final String selected;
  final ValueChanged<String> onSelected;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(3),
      decoration: BoxDecoration(
        color: TraioTokens.surface2,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final item in items)
            GestureDetector(
              onTap: () => onSelected(item),
              child: AnimatedContainer(
                duration: const Duration(milliseconds: 140),
                padding:
                    const EdgeInsets.symmetric(horizontal: 17, vertical: 8),
                decoration: BoxDecoration(
                  color: selected == item
                      ? TraioTokens.surface
                      : Colors.transparent,
                  borderRadius: BorderRadius.circular(999),
                  boxShadow: selected == item ? TraioTokens.shadow : null,
                ),
                child: Text(item,
                    style: TraioTokens.ui(
                        size: 13,
                        color: selected == item
                            ? TraioTokens.text
                            : TraioTokens.text3,
                        weight: FontWeight.w800)),
              ),
            ),
        ],
      ),
    );
  }
}
