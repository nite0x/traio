import Flutter
import UIKit
import Traio // gomobile-bound Go backend (Traio.xcframework)

@main
@objc class AppDelegate: FlutterAppDelegate {
  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    GeneratedPluginRegistrant.register(with: self)

    let controller = window?.rootViewController as! FlutterViewController
    let channel = FlutterMethodChannel(
      name: "traio/backend",
      binaryMessenger: controller.binaryMessenger
    )
    channel.setMethodCallHandler { call, result in
      switch call.method {
      case "start":
        let args = call.arguments as? [String: Any]
        let dataDir = args?["dataDir"] as? String ?? NSSearchPathForDirectoriesInDomains(
          .documentDirectory, .userDomainMask, true
        ).first ?? NSTemporaryDirectory()

        // Run the Go HTTP server in-process and return the chosen loopback port.
        // MobileStartServer is exported by the gomobile bind of ./mobile; gomobile
        // maps the Go (int, error) return to a throwing call with an inout port.
        do {
          var port: Int = 0
          try MobileStartServer(dataDir, &port)
          result(port)
        } catch let err {
          result(FlutterError(
            code: "backend_start_failed",
            message: err.localizedDescription,
            details: nil
          ))
        }
      default:
        result(FlutterMethodNotImplemented)
      }
    }

    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }
}
