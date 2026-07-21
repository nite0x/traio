import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/backend_provider.dart';
import '../core/theme.dart';
import 'home.dart';

class TraioMobileApp extends StatelessWidget {
  const TraioMobileApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Traio',
      debugShowCheckedModeBanner: false,
      theme: TraioTheme.dark(),
      home: const _BackendGate(),
    );
  }
}

class _BackendGate extends ConsumerWidget {
  const _BackendGate();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final backend = ref.watch(embeddedBackendProvider);
    return backend.when(
      loading: () => const Scaffold(
        body: Center(child: CircularProgressIndicator()),
      ),
      error: (error, _) => Scaffold(
        body: Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(
              '后端启动失败\n$error',
              textAlign: TextAlign.center,
              style: TraioTheme.mono(context, color: TraioTheme.down),
            ),
          ),
        ),
      ),
      data: (_) => const MobileHome(),
    );
  }
}
