import 'package:flutter/material.dart';

class TraioTokens {
  const TraioTokens._();

  static const canvas = Color(0xFFEFF0F3);
  static const bg = Color(0xFFFCFCFD);
  static const surface = Color(0xFFFFFFFF);
  static const surface2 = Color(0xFFF6F6F8);
  static const surfaceSunk = Color(0xFFEFF0F3);
  static const border = Color(0xFFE1E2E6);
  static const borderStrong = Color(0xFFD0D2D8);

  static const text = Color(0xFF20212B);
  static const text2 = Color(0xFF6A6D78);
  static const text3 = Color(0xFF969AA4);

  static const accent = Color(0xFF6C5DD3);
  static const accentSoft = Color(0xFFF0EEFF);
  static const up = Color(0xFF57BD7D);
  static const down = Color(0xFFE55B5B);
  static const warn = Color(0xFFC7983F);

  static const displayFont = 'Space Grotesk';
  static const uiFont = 'Hanken Grotesk';
  static const monoFont = 'JetBrains Mono';

  static const rSm = 9.0;
  static const r = 13.0;
  static const rLg = 18.0;
  static const rXl = 26.0;

  static List<BoxShadow> get shadow => [
        BoxShadow(
          color: const Color(0xFF141628).withValues(alpha: 0.05),
          blurRadius: 2,
          offset: const Offset(0, 1),
        ),
        BoxShadow(
          color: const Color(0xFF141628).withValues(alpha: 0.18),
          blurRadius: 32,
          spreadRadius: -16,
          offset: const Offset(0, 12),
        ),
      ];

  static List<BoxShadow> get shadowLg => [
        BoxShadow(
          color: const Color(0xFF141628).withValues(alpha: 0.06),
          blurRadius: 6,
          offset: const Offset(0, 2),
        ),
        BoxShadow(
          color: const Color(0xFF141628).withValues(alpha: 0.24),
          blurRadius: 90,
          spreadRadius: -40,
          offset: const Offset(0, 40),
        ),
      ];

  static TextStyle ui({
    double size = 14,
    FontWeight weight = FontWeight.w500,
    Color color = text,
    double height = 1.25,
  }) {
    return TextStyle(
      fontFamily: uiFont,
      fontSize: size,
      fontWeight: weight,
      color: color,
      height: height,
    );
  }

  static TextStyle display({
    double size = 28,
    FontWeight weight = FontWeight.w600,
    Color color = text,
    double height = 1.05,
  }) {
    return TextStyle(
      fontFamily: displayFont,
      fontSize: size,
      fontWeight: weight,
      color: color,
      height: height,
    );
  }

  static TextStyle mono({
    double size = 14,
    FontWeight weight = FontWeight.w600,
    Color color = text,
    double height = 1.2,
  }) {
    return TextStyle(
      fontFamily: monoFont,
      fontSize: size,
      fontWeight: weight,
      color: color,
      height: height,
      fontFeatures: const [FontFeature.tabularFigures()],
    );
  }
}

String fmtUsd(num value, {int dp = 2}) {
  final abs = value.abs();
  final fixed = abs.toStringAsFixed(dp);
  final parts = fixed.split('.');
  final whole = parts.first.replaceAllMapped(
    RegExp(r'\B(?=(\d{3})+(?!\d))'),
    (_) => ',',
  );
  final cents = dp == 0 ? '' : '.${parts.last}';
  return '${value < 0 ? '-' : ''}\$$whole$cents';
}

String fmtSignedUsd(num value, {int dp = 2}) {
  return '${value >= 0 ? '+' : '−'}${fmtUsd(value.abs(), dp: dp)}';
}

String fmtPct(num value, {int dp = 2}) {
  return '${value >= 0 ? '+' : '−'}${value.abs().toStringAsFixed(dp)}%';
}

Color toneColor(num value) => value >= 0 ? TraioTokens.up : TraioTokens.down;
