/// Backend base URL — all business logic lives in Go.
class TraioConfig {
  TraioConfig({this.apiBaseUrl = 'http://127.0.0.1:38180'});

  final String apiBaseUrl;
  String get apiV1 => '$apiBaseUrl/api/v1';
  String get wsUrl => apiBaseUrl.replaceFirst('http', 'ws') + '/api/v1/ws';
}
