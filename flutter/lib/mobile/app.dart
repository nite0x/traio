import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/theme.dart';
import 'home.dart';

class TraioMobileApp extends StatelessWidget {
  const TraioMobileApp({super.key});

  @override
  Widget build(BuildContext context) {
    return ProviderScope(
      child: MaterialApp(
        title: 'Traio',
        debugShowCheckedModeBanner: false,
        theme: TraioTheme.dark(),
        home: const MobileHome(),
      ),
    );
  }
}
