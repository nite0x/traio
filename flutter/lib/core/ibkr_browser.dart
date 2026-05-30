import 'dart:io';

/// Opens and closes the system browser for IBKR Gateway manual login.
class IbkrBrowser {
  static const gatewayHost = 'localhost:5680';

  static Future<void> open(String url) async {
    if (Platform.isMacOS) {
      await Process.run('open', [url]);
    } else if (Platform.isWindows) {
      await Process.run('cmd', ['/c', 'start', url]);
    } else if (Platform.isLinux) {
      await Process.run('xdg-open', [url]);
    }
  }

  /// Closes browser tabs pointing at the local IBKR Gateway.
  static Future<void> closeGatewayTabs() async {
    if (!Platform.isMacOS) return;
    await Process.run('osascript', ['-e', _macCloseScript]);
  }

  static final _macCloseScript = '''
set gatewayHost to "$gatewayHost"
repeat with browserName in {"Google Chrome", "Chromium", "Arc", "Microsoft Edge", "Brave Browser"}
  try
    tell application browserName
      repeat with w in windows
        repeat with t in tabs of w
          if (URL of t) contains gatewayHost then close t
        end repeat
      end repeat
    end tell
  end try
end repeat
try
  tell application "Safari"
    repeat with w in windows
      repeat with t in tabs of w
        if URL of t contains gatewayHost then close t
      end repeat
    end repeat
  end tell
end try
''';
}
