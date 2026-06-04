import 'package:flutter/material.dart';

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
      home: const MobileHome(),
    );
  }
}
