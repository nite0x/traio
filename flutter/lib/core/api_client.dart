import 'dart:io';

import 'package:dio/dio.dart';
import 'package:dio/io.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'backend_launcher.dart';
import 'config.dart';
import 'ibkr_browser.dart';

final traioConfigProvider = Provider<TraioConfig>((ref) {
  return TraioConfig(apiBaseUrl: BackendLauncher.apiBaseUrl);
});

/// Bumps when backend endpoint changes so Dio picks up the new base URL.
final backendEndpointProvider = StateProvider<int>((ref) => 0);

void refreshBackendEndpoint(WidgetRef ref) {
  ref.read(backendEndpointProvider.notifier).state++;
}

final dioProvider = Provider<Dio>((ref) {
  ref.watch(backendEndpointProvider);
  final cfg = TraioConfig(apiBaseUrl: BackendLauncher.apiBaseUrl);
  final dio = Dio(BaseOptions(
    baseUrl: cfg.apiV1,
    connectTimeout: const Duration(seconds: 10),
    receiveTimeout: const Duration(seconds: 30),
    headers: {'Accept': 'application/json'},
  ));
  (dio.httpClientAdapter as IOHttpClientAdapter).createHttpClient = () {
    return HttpClient()..findProxy = (uri) => 'DIRECT';
  };
  return dio;
});

class TraioApiClient {
  TraioApiClient(this._dio);

  final Dio _dio;

  Future<Map<String, dynamic>> health() async {
    final root = _dio.options.baseUrl.replaceAll('/api/v1', '');
    final dio = Dio(BaseOptions(baseUrl: root));
    (dio.httpClientAdapter as IOHttpClientAdapter).createHttpClient = () {
      return HttpClient()..findProxy = (uri) => 'DIRECT';
    };
    final res = await dio.get('/health');
    return Map<String, dynamic>.from(res.data as Map);
  }

  Future<List<WatchlistGroup>> watchlistGroups() async {
    final res = await _dio.get('/watchlist/groups');
    return (res.data as List<dynamic>)
        .map(
            (e) => WatchlistGroup.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList();
  }

  Future<List<WatchlistItem>> watchlistItems(int groupId) async {
    final res = await _dio.get('/watchlist/groups/$groupId/items');
    return (res.data as List<dynamic>)
        .map((e) => WatchlistItem.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList();
  }

  Future<WatchlistItem> addWatchlistItem(
      int groupId, Instrument instrument) async {
    final res = await _dio.post('/watchlist/groups/$groupId/items',
        data: instrument.toJson());
    return WatchlistItem.fromJson(Map<String, dynamic>.from(res.data as Map));
  }

  Future<void> removeWatchlistItem(int groupId, String symbol) async {
    await _dio.delete('/watchlist/groups/$groupId/items/$symbol');
  }

  Future<List<Instrument>> searchInstruments(String query) async {
    final res =
        await _dio.get('/instruments/search', queryParameters: {'q': query});
    return (res.data as List<dynamic>)
        .map((e) => Instrument.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList();
  }

  Future<Map<String, dynamic>> quote(String symbol) async {
    final res = await _dio.get('/quotes/$symbol');
    return Map<String, dynamic>.from(res.data as Map);
  }

  Future<List<Quote>> quotesByConids(Iterable<int> conids) async {
    final ids = conids.where((id) => id > 0).toSet().toList();
    if (ids.isEmpty) return const [];
    final res =
        await _dio.get('/quotes', queryParameters: {'conids': ids.join(',')});
    return (res.data as List<dynamic>)
        .map((e) => Quote.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList();
  }

  Future<List<dynamic>> positions() async {
    final res = await _dio.get('/positions');
    return res.data as List<dynamic>;
  }

  Future<AccountEquityResponse> accountEquity() async {
    final res = await _dio.get('/account/equity');
    return AccountEquityResponse.fromJson(
        Map<String, dynamic>.from(res.data as Map));
  }

  Future<Map<String, dynamic>> ibkrGatewayStatus() async {
    final res = await _dio.get('/ibkr/gateway/status');
    return Map<String, dynamic>.from(res.data as Map);
  }

  Future<void> ibkrGatewayReconnect() async {
    await _dio.post('/ibkr/gateway/reconnect');
  }

  Future<void> ibkrGatewayStart() async {
    await _dio.post('/ibkr/gateway/start');
  }

  Future<void> ibkrGatewayStop() async {
    await _dio.post('/ibkr/gateway/stop');
  }

  Future<Map<String, dynamic>?> serverStatus() async {
    try {
      final res = await _dio.get('/server/status');
      return Map<String, dynamic>.from(res.data as Map);
    } catch (_) {
      return null;
    }
  }

  Future<void> serverShutdown() async {
    await _dio.post('/server/shutdown');
  }

  /// Polls until gateway is online (manual login) or timeout.
  Future<String?> waitForIbkrLoginURL(
      {Duration timeout = const Duration(seconds: 45)}) async {
    final deadline = DateTime.now().add(timeout);
    while (DateTime.now().isBefore(deadline)) {
      try {
        final s = await ibkrGatewayStatus();
        if (s['authenticated'] == true) return null;
        final url = s['login_url']?.toString() ?? '';
        if (s['running'] == true && url.isNotEmpty) return url;
      } catch (_) {}
      await Future<void>.delayed(const Duration(seconds: 2));
    }
    return null;
  }

  /// Polls until IBKR session is authenticated or timeout.
  Future<Map<String, dynamic>?> waitForIbkrAuthenticated({
    Duration timeout = const Duration(minutes: 5),
  }) async {
    final deadline = DateTime.now().add(timeout);
    while (DateTime.now().isBefore(deadline)) {
      try {
        final s = await ibkrGatewayStatus();
        if (s['authenticated'] == true) return s;
      } catch (_) {}
      await Future<void>.delayed(const Duration(seconds: 2));
    }
    return null;
  }

  /// Opens login page, waits for auth, closes browser tab, refreshes status.
  Future<Map<String, dynamic>?> openLoginAndWait() async {
    final loginURL = await waitForIbkrLoginURL();
    if (loginURL == null) {
      final s = await ibkrGatewayStatus();
      return s['authenticated'] == true ? s : null;
    }
    await IbkrBrowser.open(loginURL);
    final status = await waitForIbkrAuthenticated();
    if (status != null) {
      await IbkrBrowser.closeGatewayTabs();
    }
    return status;
  }

  Future<Map<String, dynamic>> getSettings() async {
    final res = await _dio.get('/settings');
    return Map<String, dynamic>.from(res.data as Map);
  }

  Future<Map<String, dynamic>> getSettingsDefaults() async {
    final res = await _dio.get('/settings/defaults');
    return Map<String, dynamic>.from(res.data as Map);
  }

  Future<void> putSettings(Map<String, dynamic> settings) async {
    await _dio.put('/settings', data: settings);
  }
}

final ibkrGatewayStatusProvider =
    StreamProvider<Map<String, dynamic>>((ref) async* {
  ref.watch(apiClientProvider);
  while (true) {
    if (!await BackendLauncher.isServerRunning()) {
      yield {
        'running': false,
        'authenticated': false,
        'account': '',
        'session_age_seconds': 0,
      };
      await Future<void>.delayed(const Duration(seconds: 2));
      continue;
    }
    final client = ref.read(apiClientProvider);
    Map<String, dynamic> status;
    try {
      status = await client.ibkrGatewayStatus();
    } catch (_) {
      status = {
        'running': false,
        'authenticated': false,
        'account': '',
        'session_age_seconds': 0
      };
    }
    yield status;
    final pending =
        status['running'] == true && status['authenticated'] != true;
    await Future<void>.delayed(Duration(seconds: pending ? 3 : 15));
  }
});

final apiClientProvider = Provider<TraioApiClient>((ref) {
  return TraioApiClient(ref.watch(dioProvider));
});

class WatchlistGroup {
  const WatchlistGroup({
    required this.id,
    required this.name,
    required this.sortOrder,
  });

  final int id;
  final String name;
  final int sortOrder;

  factory WatchlistGroup.fromJson(Map<String, dynamic> json) {
    return WatchlistGroup(
      id: json['id'] as int,
      name: json['name']?.toString() ?? '',
      sortOrder: json['sort_order'] as int? ?? 0,
    );
  }
}

class AccountEquityResponse {
  const AccountEquityResponse({
    required this.points,
    required this.summary,
    this.warning,
  });

  final List<AccountEquityPoint> points;
  final AccountSummary summary;
  final String? warning;

  factory AccountEquityResponse.fromJson(Map<String, dynamic> json) {
    final rawPoints = json['points'] as List<dynamic>? ?? const [];
    return AccountEquityResponse(
      points: rawPoints
          .map((e) =>
              AccountEquityPoint.fromJson(Map<String, dynamic>.from(e as Map)))
          .toList(),
      summary: AccountSummary.fromJson(
          Map<String, dynamic>.from(json['summary'] as Map? ?? const {})),
      warning: json['warning']?.toString(),
    );
  }
}

class AccountEquityPoint {
  const AccountEquityPoint({
    required this.time,
    required this.value,
    required this.currency,
    required this.source,
  });

  final DateTime time;
  final double value;
  final String currency;
  final String source;

  factory AccountEquityPoint.fromJson(Map<String, dynamic> json) {
    return AccountEquityPoint(
      time: DateTime.tryParse(json['time']?.toString() ?? '') ??
          DateTime.fromMillisecondsSinceEpoch(0),
      value: (json['value'] as num?)?.toDouble() ?? 0,
      currency: json['currency']?.toString() ?? '',
      source: json['source']?.toString() ?? '',
    );
  }
}

class AccountSummary {
  const AccountSummary({
    required this.accountId,
    required this.currency,
    required this.netLiquidation,
    required this.totalCashValue,
    required this.grossPositionValue,
    required this.unrealizedPnl,
    required this.realizedPnl,
    required this.buyingPower,
    required this.broker,
    required this.asOf,
  });

  final String accountId;
  final String currency;
  final double netLiquidation;
  final double totalCashValue;
  final double grossPositionValue;
  final double unrealizedPnl;
  final double realizedPnl;
  final double buyingPower;
  final String broker;
  final DateTime? asOf;

  factory AccountSummary.fromJson(Map<String, dynamic> json) {
    return AccountSummary(
      accountId: json['account_id']?.toString() ?? '',
      currency: json['currency']?.toString() ?? '',
      netLiquidation: (json['net_liquidation'] as num?)?.toDouble() ?? 0,
      totalCashValue: (json['total_cash_value'] as num?)?.toDouble() ?? 0,
      grossPositionValue:
          (json['gross_position_value'] as num?)?.toDouble() ?? 0,
      unrealizedPnl: (json['unrealized_pnl'] as num?)?.toDouble() ?? 0,
      realizedPnl: (json['realized_pnl'] as num?)?.toDouble() ?? 0,
      buyingPower: (json['buying_power'] as num?)?.toDouble() ?? 0,
      broker: json['broker']?.toString() ?? '',
      asOf: DateTime.tryParse(json['as_of']?.toString() ?? ''),
    );
  }
}

class WatchlistItem {
  const WatchlistItem({
    required this.id,
    required this.groupId,
    required this.symbol,
    required this.conid,
    required this.name,
    required this.secType,
    required this.exchange,
    required this.currency,
  });

  final int id;
  final int groupId;
  final String symbol;
  final int conid;
  final String name;
  final String secType;
  final String exchange;
  final String currency;

  factory WatchlistItem.fromJson(Map<String, dynamic> json) {
    return WatchlistItem(
      id: json['id'] as int,
      groupId: json['group_id'] as int,
      symbol: json['symbol']?.toString() ?? '',
      conid: json['conid'] as int? ?? 0,
      name: json['name']?.toString() ?? '',
      secType: json['sec_type']?.toString() ?? '',
      exchange: json['exchange']?.toString() ?? '',
      currency: json['currency']?.toString() ?? '',
    );
  }
}

class Quote {
  const Quote({
    required this.conid,
    required this.symbol,
    required this.last,
    required this.bid,
    required this.ask,
    required this.change,
    required this.changePct,
    required this.volume,
  });

  final int conid;
  final String symbol;
  final double last;
  final double bid;
  final double ask;
  final double change;
  final double changePct;
  final int volume;

  factory Quote.fromJson(Map<String, dynamic> json) {
    return Quote(
      conid: (json['conid'] as num?)?.toInt() ?? 0,
      symbol: json['symbol']?.toString() ?? '',
      last: (json['last'] as num?)?.toDouble() ?? 0,
      bid: (json['bid'] as num?)?.toDouble() ?? 0,
      ask: (json['ask'] as num?)?.toDouble() ?? 0,
      change: (json['change'] as num?)?.toDouble() ?? 0,
      changePct: (json['change_pct'] as num?)?.toDouble() ?? 0,
      volume: (json['volume'] as num?)?.toInt() ?? 0,
    );
  }
}

class Instrument {
  const Instrument({
    required this.conid,
    required this.symbol,
    required this.name,
    required this.secType,
    required this.exchange,
    required this.currency,
  });

  final int conid;
  final String symbol;
  final String name;
  final String secType;
  final String exchange;
  final String currency;

  factory Instrument.fromJson(Map<String, dynamic> json) {
    return Instrument(
      conid: json['conid'] as int? ?? 0,
      symbol: json['symbol']?.toString() ?? '',
      name: json['name']?.toString() ?? '',
      secType: json['sec_type']?.toString() ?? '',
      exchange: json['exchange']?.toString() ?? '',
      currency: json['currency']?.toString() ?? '',
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'conid': conid,
      'symbol': symbol,
      'name': name,
      'sec_type': secType,
      'exchange': exchange,
      'currency': currency,
    };
  }
}
