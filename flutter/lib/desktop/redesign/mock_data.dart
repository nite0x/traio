import 'package:flutter/material.dart';

class Holding {
  const Holding({
    required this.sym,
    required this.name,
    required this.market,
    required this.weight,
    required this.change,
    required this.value,
    required this.unrealized,
    required this.unrealizedPct,
    required this.account,
    required this.qty,
    required this.avg,
    required this.color,
    required this.spark,
  });

  final String sym;
  final String name;
  final String market;
  final double weight;
  final double change;
  final double value;
  final double unrealized;
  final double unrealizedPct;
  final String account;
  final double qty;
  final double avg;
  final Color color;
  final List<double> spark;
}

class BrokerAccount {
  const BrokerAccount({
    required this.id,
    required this.name,
    required this.short,
    required this.last4,
    required this.color,
    required this.netValue,
    required this.pnl,
    required this.change,
    required this.alloc,
    required this.cash,
    required this.buyingPower,
    required this.marginUsed,
    required this.kind,
  });

  final String id;
  final String name;
  final String short;
  final String last4;
  final Color color;
  final double netValue;
  final double pnl;
  final double change;
  final double alloc;
  final double cash;
  final double buyingPower;
  final double marginUsed;
  final String kind;
}

class WatchItem {
  const WatchItem({
    required this.symbol,
    required this.name,
    required this.market,
    required this.last,
    required this.change,
    required this.spark,
    this.alert,
  });

  final String symbol;
  final String name;
  final String market;
  final double last;
  final double change;
  final List<double> spark;
  final String? alert;
}

class WatchGroup {
  const WatchGroup({required this.name, required this.items});

  final String name;
  final List<WatchItem> items;
}

class TransactionItem {
  const TransactionItem({
    required this.kind,
    required this.label,
    required this.account,
    required this.date,
    required this.amount,
  });

  final String kind;
  final String label;
  final String account;
  final String date;
  final double amount;
}

class ImportTask {
  const ImportTask({
    required this.title,
    required this.source,
    required this.status,
    required this.rows,
    required this.time,
    required this.tone,
  });

  final String title;
  final String source;
  final String status;
  final int rows;
  final String time;
  final Color tone;
}

const holdings = [
  Holding(
    sym: 'NVDA',
    name: '英伟达',
    market: '美股',
    weight: 22,
    change: 2.41,
    value: 62810,
    unrealized: 15674,
    unrealizedPct: 33.25,
    account: 'IBKR',
    qty: 480,
    avg: 98.20,
    color: Color(0xFF7B70D7),
    spark: [60, 61, 60.5, 62, 61.5, 63, 62.5, 64, 63.5, 65],
  ),
  Holding(
    sym: 'AAPL',
    name: '苹果',
    market: '美股',
    weight: 15.9,
    change: -0.62,
    value: 45200,
    unrealized: 3217,
    unrealizedPct: 7.66,
    account: 'IBKR',
    qty: 238,
    avg: 176.40,
    color: Color(0xFF5795D2),
    spark: [46, 45.8, 46.2, 45.5, 45.7, 45.2, 45.4, 45.0, 45.2, 45.0],
  ),
  Holding(
    sym: 'VOO',
    name: '标普500 ETF',
    market: 'ETF',
    weight: 14,
    change: 0.34,
    value: 39900,
    unrealized: 3240,
    unrealizedPct: 8.84,
    account: '嘉信',
    qty: 73.6,
    avg: 498.10,
    color: Color(0xFF4F987F),
    spark: [39.4, 39.5, 39.4, 39.6, 39.7, 39.6, 39.8, 39.7, 39.85, 39.9],
  ),
  Holding(
    sym: '0700.HK',
    name: '腾讯控股',
    market: '港股',
    weight: 12,
    change: 1.12,
    value: 34180,
    unrealized: 4300,
    unrealizedPct: 14.39,
    account: 'moomoo',
    qty: 830,
    avg: 36,
    color: Color(0xFFA86CBE),
    spark: [33.5, 33.7, 33.6, 33.9, 34, 33.8, 34.1, 34, 34.2, 34.18],
  ),
  Holding(
    sym: 'BTC',
    name: '比特币',
    market: 'Crypto',
    weight: 9,
    change: 3.84,
    value: 25640,
    unrealized: 5412,
    unrealizedPct: 26.75,
    account: 'moomoo',
    qty: 0.3746,
    avg: 54000,
    color: Color(0xFFC6963F),
    spark: [24.4, 24.6, 24.5, 25, 24.8, 25.2, 25.1, 25.5, 25.4, 25.64],
  ),
  Holding(
    sym: 'MSFT',
    name: '微软',
    market: '美股',
    weight: 8,
    change: 0.91,
    value: 22800,
    unrealized: 3084,
    unrealizedPct: 15.64,
    account: 'IBKR',
    qty: 53,
    avg: 372,
    color: Color(0xFF6A89CF),
    spark: [22.4, 22.5, 22.4, 22.6, 22.55, 22.7, 22.65, 22.75, 22.7, 22.8],
  ),
  Holding(
    sym: 'TSLA',
    name: '特斯拉',
    market: '美股',
    weight: 6.9,
    change: -1.93,
    value: 19690,
    unrealized: -2860,
    unrealizedPct: -12.68,
    account: '嘉信',
    qty: 110,
    avg: 205,
    color: Color(0xFF43A6B3),
    spark: [20.4, 20.2, 20.3, 20, 20.1, 19.8, 19.9, 19.7, 19.75, 19.69],
  ),
];

const cashWeight = 12.2;

const accounts = [
  BrokerAccount(
    id: 'ibkr',
    name: 'IBKR 盈透',
    short: 'IB',
    last4: '4821',
    color: Color(0xFF4779C3),
    netValue: 168520,
    pnl: 2284,
    change: 1.62,
    alloc: 59.1,
    cash: 22840,
    buyingPower: 61400,
    marginUsed: 28,
    kind: '保证金账户',
  ),
  BrokerAccount(
    id: 'moomoo',
    name: 'moomoo',
    short: 'MM',
    last4: '6307',
    color: Color(0xFFD68A31),
    netValue: 74200,
    pnl: 1018,
    change: 0.88,
    alloc: 26,
    cash: 12200,
    buyingPower: 24400,
    marginUsed: 12,
    kind: '保证金账户',
  ),
  BrokerAccount(
    id: 'schwab',
    name: '嘉信 Schwab',
    short: 'CS',
    last4: '9034',
    color: Color(0xFF4F987F),
    netValue: 42200,
    pnl: 545,
    change: 1.31,
    alloc: 14.9,
    cash: 6260,
    buyingPower: 6260,
    marginUsed: 0,
    kind: '现金账户',
  ),
];

const watchGroups = [
  WatchGroup(
    name: '我的清单',
    items: [
      WatchItem(
          symbol: 'NVDA',
          name: '英伟达',
          market: '美股',
          last: 130.85,
          change: 2.41,
          alert: '突破 \$135.00',
          spark: [128, 129, 128.7, 129.8, 130.1, 129.9, 130.4, 130.85]),
      WatchItem(
          symbol: 'AAPL',
          name: '苹果',
          market: '美股',
          last: 189.92,
          change: -0.62,
          spark: [191, 190.4, 190.8, 190.1, 189.8, 190, 189.6, 189.92]),
      WatchItem(
          symbol: '0700.HK',
          name: '腾讯控股',
          market: '港股',
          last: 411.80,
          change: 1.12,
          spark: [405, 407, 406, 409, 410, 408, 411, 411.8]),
      WatchItem(
          symbol: 'BTC',
          name: '比特币',
          market: 'Crypto',
          last: 68420,
          change: 3.84,
          alert: '跌破 \$66,000',
          spark: [66200, 66800, 67100, 67600, 67200, 68100, 67900, 68420]),
      WatchItem(
          symbol: 'VOO',
          name: '标普500 ETF',
          market: 'ETF',
          last: 542.10,
          change: 0.34,
          spark: [539, 540, 540.4, 541, 540.8, 541.4, 541.7, 542.1]),
    ],
  ),
  WatchGroup(
    name: '科技观察',
    items: [
      WatchItem(
          symbol: 'AMD',
          name: '超威半导体',
          market: '美股',
          last: 168.20,
          change: 1.84,
          spark: [163, 164, 165, 164.7, 166, 167, 166.6, 168.2]),
      WatchItem(
          symbol: 'META',
          name: 'Meta',
          market: '美股',
          last: 512.30,
          change: -0.45,
          spark: [516, 514, 515, 513.5, 512.8, 513.1, 512, 512.3]),
      WatchItem(
          symbol: 'GOOGL',
          name: '谷歌 A',
          market: '美股',
          last: 178.90,
          change: 0.92,
          spark: [175, 176, 175.8, 177, 177.2, 178, 178.4, 178.9]),
    ],
  ),
];

const transactions = [
  TransactionItem(
      kind: 'dividend',
      label: 'VOO 现金分红',
      account: '嘉信',
      date: '05-30',
      amount: 162),
  TransactionItem(
      kind: 'deposit',
      label: '银行入金 · ACH',
      account: 'IBKR',
      date: '05-28',
      amount: 20000),
  TransactionItem(
      kind: 'fee',
      label: '行情数据订阅费',
      account: 'IBKR',
      date: '05-27',
      amount: -10),
  TransactionItem(
      kind: 'dividend',
      label: 'AAPL 现金分红',
      account: 'IBKR',
      date: '05-24',
      amount: 24),
  TransactionItem(
      kind: 'withdraw',
      label: '提现至银行',
      account: 'moomoo',
      date: '05-20',
      amount: -5000),
];

const importTasks = [
  ImportTask(
      title: 'IBKR Flex Query',
      source: 'U4821 · 交易记录',
      status: '已完成',
      rows: 384,
      time: '今日 10:24',
      tone: Color(0xFF57BD7D)),
  ImportTask(
      title: 'moomoo CSV',
      source: '港股成交与分红',
      status: '待映射',
      rows: 72,
      time: '昨日 18:42',
      tone: Color(0xFFC7983F)),
  ImportTask(
      title: 'Schwab Positions',
      source: '持仓快照',
      status: '已完成',
      rows: 19,
      time: '05-29',
      tone: Color(0xFF57BD7D)),
  ImportTask(
      title: '税务辅助导出',
      source: '交易流水 CSV',
      status: '已导出',
      rows: 455,
      time: '05-28',
      tone: Color(0xFF6C5DD3)),
];
