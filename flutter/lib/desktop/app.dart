import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/theme.dart';
import 'shell.dart';

class TraioDesktopApp extends StatelessWidget {
  const TraioDesktopApp({super.key});

  @override
  Widget build(BuildContext context) {
    return ProviderScope(
      child: MaterialApp(
        title: 'Traio',
        debugShowCheckedModeBanner: false,
        theme: TraioTheme.dark(),
        home: const DesktopShell(),
      ),
    );
  }
}
