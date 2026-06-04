import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'core/theme.dart';
import 'mobile/app.dart';

void main() {
  runApp(const ProviderScope(child: TraioMobileApp()));
}
