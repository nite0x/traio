/// Backend base URL — supplied by [embeddedBackendProvider] after boot.
class TraioConfig {
  const TraioConfig({required this.apiBaseUrl});

  final String apiBaseUrl;
  String get apiV1 => '$apiBaseUrl/api/v1';
  String get wsUrl => '${apiBaseUrl.replaceFirst('http', 'ws')}/api/v1/ws';
}
