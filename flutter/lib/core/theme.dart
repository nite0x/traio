import 'package:flutter/material.dart';

/// Clean light theme — high density, monospace numbers.
class TraioTheme {
  // ── Surfaces ──────────────────────────────────────────────────────────────
  static const Color bg      = Color(0xFFF5F5F7); // page background
  static const Color surface = Color(0xFFFFFFFF); // cards / panels
  static const Color surfaceAlt = Color(0xFFFAFAFC); // subtle alternate rows
  static const Color border  = Color(0xFFE4E4E9); // dividers

  // ── Text ─────────────────────────────────────────────────────────────────
  static const Color textPrimary = Color(0xFF111118);
  static const Color textSecondary = Color(0xFF4B4B5A);
  static const Color textMuted  = Color(0xFF9696A6);

  // ── Semantic ──────────────────────────────────────────────────────────────
  static const Color up       = Color(0xFF0B7F4E); // gain green
  static const Color upBg     = Color(0xFFEBF8F2);
  static const Color down     = Color(0xFFCC2B2B); // loss red
  static const Color downBg   = Color(0xFFFDF0F0);
  static const Color accent   = Color(0xFF4F46E5); // indigo accent
  static const Color warn     = Color(0xFFB45309);

  static const String monoFont = 'JetBrains Mono';

  static ThemeData light() {
    return ThemeData(
      useMaterial3: true,
      brightness: Brightness.light,
      scaffoldBackgroundColor: bg,
      colorScheme: const ColorScheme.light(
        surface: surface,
        primary: accent,
        onSurface: textPrimary,
        outline: border,
      ),
      dividerColor: border,
      fontFamily: 'Inter',
      appBarTheme: const AppBarTheme(
        backgroundColor: surface,
        foregroundColor: textPrimary,
        elevation: 0,
        surfaceTintColor: Colors.transparent,
      ),
      textTheme: const TextTheme(
        bodyMedium: TextStyle(color: textPrimary, fontSize: 13),
        labelSmall: TextStyle(color: textMuted, fontSize: 11),
      ).apply(
        bodyColor: textPrimary,
        displayColor: textPrimary,
      ),
    );
  }

  // Keep dark() for backward compat, points to light now.
  static ThemeData dark() => light();

  static TextStyle mono(BuildContext context, {Color? color, double? size}) {
    return TextStyle(
      fontFamily: monoFont,
      fontSize: size ?? 12,
      color: color ?? textPrimary,
      fontFeatures: const [FontFeature.tabularFigures()],
    );
  }
}
