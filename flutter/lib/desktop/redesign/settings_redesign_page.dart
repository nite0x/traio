import 'package:flutter/material.dart';

// ignore_for_file: prefer_const_constructors

import 'mock_data.dart';
import 'shared_widgets.dart';
import 'traio_tokens.dart';

class SettingsRedesignDeskPage extends StatelessWidget {
  const SettingsRedesignDeskPage({super.key});

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(0, 50, 0, 110),
      children: [
        const PageHeader(kicker: '券商连接、外观、通知与安全', title: '设置'),
        const SizedBox(height: 50),
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 185,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: const [
                  _SideNav(label: '账户与券商', active: true),
                  _SideNav(label: '外观'),
                  _SideNav(label: '交易偏好'),
                  _SideNav(label: '通知'),
                  _SideNav(label: '安全'),
                ],
              ),
            ),
            const SizedBox(width: 24),
            Expanded(
              child: Column(
                children: const [
                  _BrokerConnections(),
                  SizedBox(height: 18),
                  _AppearanceCard(),
                  SizedBox(height: 18),
                  _PreferenceCard(),
                ],
              ),
            ),
          ],
        ),
      ],
    );
  }
}

class _SideNav extends StatelessWidget {
  const _SideNav({required this.label, this.active = false});

  final String label;
  final bool active;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.symmetric(horizontal: 13, vertical: 11),
      decoration: BoxDecoration(
        color: active ? TraioTokens.accentSoft : Colors.transparent,
        borderRadius: BorderRadius.circular(TraioTokens.rSm),
      ),
      child: Text(label,
          style: TraioTokens.ui(
              size: 13,
              color: active ? TraioTokens.accent : TraioTokens.text3,
              weight: FontWeight.w800)),
    );
  }
}

class _BrokerConnections extends StatelessWidget {
  const _BrokerConnections();

  @override
  Widget build(BuildContext context) {
    return TraioCard(
      padding: EdgeInsets.zero,
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(18),
            child: Row(
              children: [
                Text('券商连接', style: TraioTokens.display(size: 20)),
                const SizedBox(width: 12),
                Text('只读授权，绝不触碰资金权限',
                    style: TraioTokens.ui(
                        size: 13,
                        color: TraioTokens.text3,
                        weight: FontWeight.w700)),
              ],
            ),
          ),
          const Divider(height: 1, color: TraioTokens.border),
          for (final account in accounts)
            _BrokerRow(account: account, connected: true),
          const _BrokerRow.manual(
              name: '富途 Futubull', short: 'FT', connected: false),
        ],
      ),
    );
  }
}

class _BrokerRow extends StatelessWidget {
  const _BrokerRow({required this.account, required this.connected})
      : name = null,
        short = null;

  const _BrokerRow.manual(
      {required this.name, required this.short, required this.connected})
      : account = null;

  final BrokerAccount? account;
  final String? name;
  final String? short;
  final bool connected;

  @override
  Widget build(BuildContext context) {
    final color = account?.color ?? TraioTokens.text3;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 14),
      decoration: const BoxDecoration(
          border: Border(bottom: BorderSide(color: TraioTokens.border))),
      child: Row(
        children: [
          Container(
            width: 32,
            height: 32,
            decoration: BoxDecoration(
                color: color, borderRadius: BorderRadius.circular(9)),
            alignment: Alignment.center,
            child: Text(account?.short ?? short!,
                style: TraioTokens.mono(size: 12, color: Colors.white)),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(account?.name ?? name!,
                    style: TraioTokens.ui(size: 14, weight: FontWeight.w800)),
                const SizedBox(height: 4),
                Text(connected ? '只读授权 · 今日 10:42 同步' : '未连接 · 可导入 CSV',
                    style: TraioTokens.ui(
                        size: 12,
                        color: TraioTokens.text3,
                        weight: FontWeight.w600)),
              ],
            ),
          ),
          StatusPill(
              label: connected ? '已连接' : '未连接',
              color: connected ? TraioTokens.up : TraioTokens.text3),
        ],
      ),
    );
  }
}

class _AppearanceCard extends StatelessWidget {
  const _AppearanceCard();

  @override
  Widget build(BuildContext context) {
    return TraioCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('外观', style: TraioTokens.display(size: 20)),
          const SizedBox(height: 18),
          Row(
            children: const [
              Expanded(
                  child: _SettingLine(label: '主题', value: '浅色 / 深色 / 跟随系统')),
              SizedBox(width: 18),
              Expanded(
                  child: _SettingLine(label: '强调色', value: 'Violet / Sage')),
              SizedBox(width: 18),
              Expanded(child: _SettingLine(label: '涨跌色', value: '绿涨红跌 / 红涨绿跌')),
            ],
          ),
        ],
      ),
    );
  }
}

class _PreferenceCard extends StatelessWidget {
  const _PreferenceCard();

  @override
  Widget build(BuildContext context) {
    return TraioCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('通知与安全', style: TraioTokens.display(size: 20)),
          const SizedBox(height: 18),
          const _ToggleLine(label: '价格预警', enabled: true),
          const _ToggleLine(label: '分红与公司事件', enabled: true),
          const _ToggleLine(label: '每日收盘摘要', enabled: false),
          const _ToggleLine(label: '生物识别解锁', enabled: true),
        ],
      ),
    );
  }
}

class _SettingLine extends StatelessWidget {
  const _SettingLine({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: TraioTokens.surface2,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(TraioTokens.r),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label,
              style: TraioTokens.ui(
                  size: 12, color: TraioTokens.text3, weight: FontWeight.w700)),
          const SizedBox(height: 7),
          Text(value, style: TraioTokens.ui(size: 13, weight: FontWeight.w800)),
        ],
      ),
    );
  }
}

class _ToggleLine extends StatelessWidget {
  const _ToggleLine({required this.label, required this.enabled});

  final String label;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 14),
      child: Row(
        children: [
          Text(label, style: TraioTokens.ui(size: 14, weight: FontWeight.w800)),
          const Spacer(),
          Container(
            width: 46,
            height: 27,
            padding: const EdgeInsets.all(3),
            decoration: BoxDecoration(
                color: enabled ? TraioTokens.accent : TraioTokens.surfaceSunk,
                borderRadius: BorderRadius.circular(999)),
            child: Align(
              alignment: enabled ? Alignment.centerRight : Alignment.centerLeft,
              child: Container(
                  width: 21,
                  height: 21,
                  decoration: const BoxDecoration(
                      color: Colors.white, shape: BoxShape.circle)),
            ),
          ),
        ],
      ),
    );
  }
}
