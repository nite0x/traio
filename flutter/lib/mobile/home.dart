import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/api_client.dart';
import '../core/theme.dart';

/// Mobile: Schwab-only, compact watchlist + quick actions.
class MobileHome extends ConsumerWidget {
  const MobileHome({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final groups = ref.watch(watchlistGroupsProvider);
    return Scaffold(
      appBar: AppBar(
        title: const Text('Traio'),
        backgroundColor: TraioTheme.surface,
      ),
      body: groups.when(
        data: (list) => ListView(
          children: [
            ListTile(
              title: Text('自选', style: TraioTheme.mono(context)),
              subtitle: Text('${list.length} 个分组',
                  style: TraioTheme.mono(context, color: TraioTheme.textMuted)),
            ),
            if (list.isNotEmpty) _MobileWatchlistItems(groupId: list.first.id),
            const Divider(height: 1, color: TraioTheme.border),
            ListTile(
              title: Text('持仓', style: TraioTheme.mono(context)),
              trailing:
                  const Icon(Icons.chevron_right, color: TraioTheme.textMuted),
              onTap: () {},
            ),
            ListTile(
              title: Text('快速下单', style: TraioTheme.mono(context)),
              trailing:
                  const Icon(Icons.chevron_right, color: TraioTheme.textMuted),
              onTap: () {},
            ),
          ],
        ),
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: Text('$e')),
      ),
      floatingActionButton: FloatingActionButton(
        onPressed: () {},
        child: const Icon(Icons.show_chart),
      ),
    );
  }
}

class _MobileWatchlistItems extends ConsumerWidget {
  const _MobileWatchlistItems({required this.groupId});

  final int groupId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final items = ref.watch(watchlistItemsProvider(groupId));
    return items.when(
      data: (rows) {
        if (rows.isEmpty) {
          return ListTile(
            dense: true,
            title: Text('暂无自选',
                style: TraioTheme.mono(context, color: TraioTheme.textMuted)),
          );
        }
        return Column(
          children: rows
              .map((item) => ListTile(
                    dense: true,
                    title: Text(item.symbol, style: TraioTheme.mono(context)),
                    subtitle: item.name.isEmpty
                        ? null
                        : Text(item.name,
                            style: TraioTheme.mono(context,
                                color: TraioTheme.textMuted)),
                  ))
              .toList(),
        );
      },
      loading: () => const Padding(
        padding: EdgeInsets.all(16),
        child: Center(child: CircularProgressIndicator(strokeWidth: 2)),
      ),
      error: (e, _) => ListTile(
        dense: true,
        title: Text('$e',
            style: const TextStyle(color: TraioTheme.down, fontSize: 12)),
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
