import 'package:flutter/material.dart';

// ignore_for_file: prefer_const_constructors

import 'mock_data.dart';
import 'shared_widgets.dart';
import 'traio_tokens.dart';

class ImportExportDeskPage extends StatelessWidget {
  const ImportExportDeskPage({super.key});

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(0, 50, 0, 110),
      children: [
        const PageHeader(kicker: '交易记录、持仓快照与税务辅助', title: '导入 / 导出'),
        const SizedBox(height: 50),
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Expanded(flex: 6, child: _ImportPanel()),
            const SizedBox(width: 18),
            Expanded(
              flex: 4,
              child: Column(
                children: const [
                  _ExportPanel(),
                  SizedBox(height: 18),
                  _MappingPanel(),
                ],
              ),
            ),
          ],
        ),
        const SizedBox(height: 36),
        const SectionTitle(title: '最近任务', hint: '导入校验、字段映射与导出历史'),
        const SizedBox(height: 16),
        TraioCard(
          padding: EdgeInsets.zero,
          child: Column(
              children: [for (final task in importTasks) _TaskRow(task: task)]),
        ),
      ],
    );
  }
}

class _ImportPanel extends StatelessWidget {
  const _ImportPanel();

  @override
  Widget build(BuildContext context) {
    return TraioCard(
      padding: const EdgeInsets.all(22),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('导入数据', style: TraioTokens.display(size: 20)),
          const SizedBox(height: 8),
          Text('支持券商导出的 CSV、IBKR Flex Query、持仓快照和资金流水。',
              style: TraioTokens.ui(
                  size: 13, color: TraioTokens.text3, weight: FontWeight.w600)),
          const SizedBox(height: 20),
          Row(
            children: const [
              Expanded(
                  child: _ImportTile(
                      icon: Icons.upload_file_rounded,
                      title: '交易记录',
                      desc: '成交、费用、分红、划转')),
              SizedBox(width: 12),
              Expanded(
                  child: _ImportTile(
                      icon: Icons.account_balance_wallet_outlined,
                      title: '持仓快照',
                      desc: '数量、成本、币种、市值')),
            ],
          ),
          const SizedBox(height: 12),
          Row(
            children: const [
              Expanded(
                  child: _ImportTile(
                      icon: Icons.sync_alt_rounded,
                      title: 'IBKR Flex',
                      desc: '自动解析 Activity Statement')),
              SizedBox(width: 12),
              Expanded(
                  child: _ImportTile(
                      icon: Icons.table_chart_outlined,
                      title: '字段映射',
                      desc: '手动匹配未知模板')),
            ],
          ),
        ],
      ),
    );
  }
}

class _ImportTile extends StatelessWidget {
  const _ImportTile(
      {required this.icon, required this.title, required this.desc});

  final IconData icon;
  final String title;
  final String desc;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: TraioTokens.surface2,
        border: Border.all(color: TraioTokens.border),
        borderRadius: BorderRadius.circular(TraioTokens.r),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, size: 24, color: TraioTokens.accent),
          const SizedBox(height: 18),
          Text(title, style: TraioTokens.ui(size: 15, weight: FontWeight.w800)),
          const SizedBox(height: 6),
          Text(desc,
              style: TraioTokens.ui(
                  size: 12, color: TraioTokens.text3, weight: FontWeight.w600)),
        ],
      ),
    );
  }
}

class _ExportPanel extends StatelessWidget {
  const _ExportPanel();

  @override
  Widget build(BuildContext context) {
    return TraioCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('导出', style: TraioTokens.display(size: 18)),
          const SizedBox(height: 14),
          _exportRow('持仓汇总 CSV', '按账户与标的展开'),
          _exportRow('交易流水 CSV', '含手续费与币种'),
          _exportRow('税务辅助包', '已实现盈亏与分红'),
        ],
      ),
    );
  }

  Widget _exportRow(String title, String desc) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Row(
        children: [
          const Icon(Icons.download_rounded,
              size: 18, color: TraioTokens.accent),
          const SizedBox(width: 10),
          Expanded(
            child:
                Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(title,
                  style: TraioTokens.ui(size: 13, weight: FontWeight.w800)),
              Text(desc,
                  style: TraioTokens.ui(
                      size: 11,
                      color: TraioTokens.text3,
                      weight: FontWeight.w600)),
            ]),
          ),
        ],
      ),
    );
  }
}

class _MappingPanel extends StatelessWidget {
  const _MappingPanel();

  @override
  Widget build(BuildContext context) {
    const fields = ['代码', '市场', '数量', '价格', '手续费', '币种', '日期'];
    return TraioCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('字段映射预览', style: TraioTokens.display(size: 18)),
          const SizedBox(height: 14),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: fields
                .map((f) => StatusPill(label: f, color: TraioTokens.text3))
                .toList(),
          ),
          const SizedBox(height: 16),
          Text('重复记录、未知代码和币种转换会在提交前停留确认。',
              style: TraioTokens.ui(
                  size: 12,
                  color: TraioTokens.text3,
                  weight: FontWeight.w600,
                  height: 1.45)),
        ],
      ),
    );
  }
}

class _TaskRow extends StatelessWidget {
  const _TaskRow({required this.task});

  final ImportTask task;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 14),
      decoration: const BoxDecoration(
          border: Border(bottom: BorderSide(color: TraioTokens.border))),
      child: Row(
        children: [
          StatusPill(label: task.status, color: task.tone),
          const SizedBox(width: 16),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(task.title,
                    style: TraioTokens.ui(size: 14, weight: FontWeight.w800)),
                const SizedBox(height: 4),
                Text(task.source,
                    style: TraioTokens.ui(
                        size: 12,
                        color: TraioTokens.text3,
                        weight: FontWeight.w600)),
              ],
            ),
          ),
          Text('${task.rows} 行', style: TraioTokens.mono(size: 13)),
          const SizedBox(width: 28),
          SizedBox(
              width: 80,
              child: Text(task.time,
                  textAlign: TextAlign.right,
                  style: TraioTokens.ui(
                      size: 12,
                      color: TraioTokens.text3,
                      weight: FontWeight.w700))),
        ],
      ),
    );
  }
}
