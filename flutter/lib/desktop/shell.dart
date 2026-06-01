import 'dart:ui';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

// ignore_for_file: prefer_const_constructors

import 'redesign/import_export_page.dart';
import 'redesign/overview_page.dart';
import 'redesign/settings_redesign_page.dart';
import 'redesign/traio_tokens.dart';
import 'redesign/watch_page.dart';

final _deskNavProvider = StateProvider<int>((ref) => 0);

class DesktopShell extends ConsumerStatefulWidget {
  const DesktopShell({super.key});

  @override
  ConsumerState<DesktopShell> createState() => _DesktopShellState();
}

class _DesktopShellState extends ConsumerState<DesktopShell> {
  var _bloomOpen = false;

  static const _items = [
    _NavItem('概览', Icons.grid_view_rounded, OverviewDeskPage()),
    _NavItem('自选', Icons.star_border_rounded, WatchDeskPage()),
    _NavItem('导入 / 导出', Icons.import_export_rounded, ImportExportDeskPage()),
    _NavItem('设置', Icons.settings_outlined, SettingsRedesignDeskPage()),
  ];

  @override
  Widget build(BuildContext context) {
    final idx = ref.watch(_deskNavProvider);
    final current = _items[idx];

    return Scaffold(
      backgroundColor: TraioTokens.bg,
      body: ScrollConfiguration(
        behavior: const _NoScrollbarBehavior(),
        child: Stack(
          children: [
            Column(
              children: [
                Expanded(
                  child: LayoutBuilder(
                    builder: (context, constraints) {
                      final horizontalPadding = switch (constraints.maxWidth) {
                        >= 1600 => 36.0,
                        >= 1200 => 28.0,
                        _ => 20.0,
                      };

                      return SizedBox(
                        width: double.infinity,
                        height: constraints.maxHeight,
                        child: Padding(
                          padding: EdgeInsets.fromLTRB(
                            horizontalPadding,
                            0,
                            horizontalPadding,
                            82,
                          ),
                          child: current.page,
                        ),
                      );
                    },
                  ),
                ),
              ],
            ),
            if (_bloomOpen)
              Positioned(
                left: 0,
                right: 0,
                bottom: 68,
                child: Center(
                  child: MouseRegion(
                    onExit: (_) => setState(() => _bloomOpen = false),
                    child: AnimatedSlide(
                      duration: const Duration(milliseconds: 160),
                      offset: _bloomOpen ? Offset.zero : const Offset(0, 0.04),
                      child: _BloomMenu(
                        items: _items,
                        selected: idx,
                        onSelected: (i) {
                          ref.read(_deskNavProvider.notifier).state = i;
                          setState(() => _bloomOpen = false);
                        },
                      ),
                    ),
                  ),
                ),
              ),
            Positioned(
              left: 0,
              right: 0,
              bottom: 14,
              child: Center(
                child: MouseRegion(
                  onEnter: (_) => setState(() => _bloomOpen = true),
                  child: _Orb(
                    label: current.label,
                    onTap: () => setState(() => _bloomOpen = !_bloomOpen),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _NoScrollbarBehavior extends MaterialScrollBehavior {
  const _NoScrollbarBehavior();

  @override
  Widget buildScrollbar(
    BuildContext context,
    Widget child,
    ScrollableDetails details,
  ) {
    return child;
  }
}

class _BloomMenu extends StatelessWidget {
  const _BloomMenu(
      {required this.items, required this.selected, required this.onSelected});

  final List<_NavItem> items;
  final int selected;
  final ValueChanged<int> onSelected;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(22),
      child: BackdropFilter(
        filter: ImageFilter.blur(sigmaX: 22, sigmaY: 22),
        child: Container(
          width: 390,
          padding: const EdgeInsets.fromLTRB(22, 22, 22, 18),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: 0.78),
            border: Border.all(color: Colors.white.withValues(alpha: 0.72)),
            borderRadius: BorderRadius.circular(22),
            boxShadow: TraioTokens.shadowLg,
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              GridView.builder(
                shrinkWrap: true,
                physics: const NeverScrollableScrollPhysics(),
                gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                  crossAxisCount: 2,
                  mainAxisExtent: 48,
                  crossAxisSpacing: 16,
                  mainAxisSpacing: 7,
                ),
                itemCount: items.length,
                itemBuilder: (context, i) {
                  final item = items[i];
                  final active = i == selected;
                  return InkWell(
                    borderRadius: BorderRadius.circular(TraioTokens.rSm),
                    onTap: () => onSelected(i),
                    child: Row(
                      children: [
                        Container(
                          width: 8,
                          height: 8,
                          decoration: BoxDecoration(
                              color: active
                                  ? TraioTokens.accent
                                  : TraioTokens.text3,
                              shape: BoxShape.circle),
                        ),
                        const SizedBox(width: 16),
                        Icon(item.icon,
                            size: 20,
                            color:
                                active ? TraioTokens.text : TraioTokens.text2),
                        const SizedBox(width: 13),
                        Text(item.label,
                            style: TraioTokens.ui(
                                size: 15,
                                color: active
                                    ? TraioTokens.text
                                    : TraioTokens.text2,
                                weight: FontWeight.w800)),
                      ],
                    ),
                  );
                },
              ),
              const Divider(height: 26, color: TraioTokens.border),
              Row(
                children: [
                  const Icon(Icons.search_rounded,
                      size: 20, color: TraioTokens.text2),
                  const SizedBox(width: 13),
                  Text('搜索 ⌘K',
                      style: TraioTokens.ui(
                          size: 15,
                          color: TraioTokens.text2,
                          weight: FontWeight.w800)),
                  const Spacer(),
                  const Icon(Icons.tune_rounded,
                      size: 20, color: TraioTokens.text2),
                  const SizedBox(width: 13),
                  Text('命令',
                      style: TraioTokens.ui(
                          size: 15,
                          color: TraioTokens.text2,
                          weight: FontWeight.w800)),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _Orb extends StatelessWidget {
  const _Orb({required this.label, required this.onTap});

  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.fromLTRB(18, 10, 10, 10),
        decoration: BoxDecoration(
          color: TraioTokens.surface.withValues(alpha: 0.94),
          border: Border.all(color: TraioTokens.border),
          borderRadius: BorderRadius.circular(999),
          boxShadow: TraioTokens.shadowLg,
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Container(
                width: 8,
                height: 8,
                decoration: const BoxDecoration(
                    color: TraioTokens.accent, shape: BoxShape.circle)),
            const SizedBox(width: 11),
            Text(label,
                style: TraioTokens.ui(size: 14, weight: FontWeight.w900)),
            const SizedBox(width: 13),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              decoration: BoxDecoration(
                  border: Border.all(color: TraioTokens.borderStrong),
                  borderRadius: BorderRadius.circular(8)),
              child: Text('⌘K',
                  style: TraioTokens.mono(size: 11, color: TraioTokens.text3)),
            ),
            const SizedBox(width: 9),
            Container(
              width: 34,
              height: 34,
              decoration: const BoxDecoration(
                  color: TraioTokens.accent, shape: BoxShape.circle),
              child:
                  const Icon(Icons.bolt_rounded, size: 19, color: Colors.white),
            ),
          ],
        ),
      ),
    );
  }
}

class _NavItem {
  const _NavItem(this.label, this.icon, this.page);

  final String label;
  final IconData icon;
  final Widget page;
}
