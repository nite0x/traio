import 'dart:convert';
import 'dart:io';

import 'package:dio/dio.dart';
import 'package:dio/io.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'embedded_backend.dart';

final dioProvider = Provider<Dio>((ref) {
  final baseUrl = EmbeddedBackend.isStarted
      ? EmbeddedBackend.apiBaseUrl
      : 'http://127.0.0.1:38180';
  final dio = Dio(BaseOptions(
    baseUrl: '$baseUrl/api/v1',
    connectTimeout: const Duration(seconds: 10),
    receiveTimeout: const Duration(seconds: 30),
    headers: {'Accept': 'application/json'},
  ));
  (dio.httpClientAdapter as IOHttpClientAdapter).createHttpClient = () {
    return HttpClient()..findProxy = (uri) => 'DIRECT';
  };
  return dio;
});

final apiClientProvider = Provider<TraioApiClient>((ref) {
  return TraioApiClient(ref.watch(dioProvider));
});

class TraioApiClient {
  TraioApiClient(this._dio);

  final Dio _dio;

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

  Future<List<Quote>> quotesByConids(Iterable<int> conids) async {
    final ids = conids.where((id) => id > 0).toSet().toList();
    if (ids.isEmpty) return const [];
    final res =
        await _dio.get('/quotes', queryParameters: {'conids': ids.join(',')});
    return (res.data as List<dynamic>)
        .map((e) => Quote.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList();
  }

  Stream<Quote> streamQuotes(Iterable<String> symbols) async* {
    final keys = symbols
        .map((symbol) => symbol.trim().toUpperCase())
        .where((symbol) => symbol.isNotEmpty)
        .toSet()
        .toList()
      ..sort();
    if (keys.isEmpty) return;

    final httpBase = _dio.options.baseUrl.replaceFirst(RegExp(r'/api/v1$'), '');
    final wsBase = httpBase.replaceFirst(RegExp(r'^http'), 'ws');
    final uri = Uri.parse('$wsBase/api/v1/ws')
        .replace(queryParameters: {'symbols': keys.join(',')});

    while (true) {
      WebSocket? socket;
      try {
        socket = await WebSocket.connect(uri.toString());
        await for (final raw in socket) {
          final message = jsonDecode(raw.toString()) as Map<String, dynamic>;
          if (message['type'] != 'quote') continue;
          yield Quote.fromJson(
              Map<String, dynamic>.from(message['quote'] as Map));
        }
      } catch (_) {
        // Reconnect below after a short delay.
      } finally {
        if (socket != null) {
          await socket.close();
        }
      }
      await Future<void>.delayed(const Duration(seconds: 2));
    }
  }

  Future<Map<String, dynamic>> getSettings() async {
    final res = await _dio.get('/settings');
    return Map<String, dynamic>.from(res.data as Map);
  }

  Future<void> putSettings(Map<String, dynamic> settings) async {
    await _dio.put('/settings', data: settings);
  }
}

// ─── Models ──────────────────────────────────────────────────────────────────

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
