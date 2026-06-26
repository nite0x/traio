use std::net::{SocketAddr, TcpStream};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use tauri::AppHandle;
use tauri::Manager;
use tauri_plugin_shell::process::CommandChild;
use tauri_plugin_shell::ShellExt;

const PROD_SERVER_PORT: u16 = 38180;
const DEV_SERVER_PORT: u16 = 38181;
const STARTUP_TIMEOUT: Duration = Duration::from_secs(20);

pub struct BackendChild(pub Mutex<Option<CommandChild>>);
pub struct BackendOwned(pub Mutex<bool>);

pub fn ensure_started(app: &AppHandle) -> Result<(), String> {
    if server_healthy() {
        return Ok(());
    }

    let runtime_dir = runtime_dir();
    std::fs::create_dir_all(&runtime_dir).map_err(|e| e.to_string())?;
    let port = server_port();

    let sidecar = app
        .shell()
        .sidecar("traio-server")
        .map_err(|e| format!("resolve traio-server sidecar: {e}"))?
        .env("TRAIO_RUNTIME_DIR", &runtime_dir)
        .env("TRAIO_SERVER_PORT", port.to_string());

    let (_rx, child) = sidecar
        .spawn()
        .map_err(|e| format!("spawn traio-server: {e}"))?;

    if !wait_for_healthy(STARTUP_TIMEOUT) {
        let _ = child.kill();
        return Err(format!(
            "traio-server did not become ready on 127.0.0.1:{port} in time"
        ));
    }

    *app.state::<BackendChild>().0.lock().unwrap() = Some(child);
    *app.state::<BackendOwned>().0.lock().unwrap() = true;
    Ok(())
}

pub fn shutdown_owned(app: &AppHandle) {
    let owned = *app.state::<BackendOwned>().0.lock().unwrap();
    if !owned {
        return;
    }

    if let Some(child) = app.state::<BackendChild>().0.lock().unwrap().take() {
        let _ = child.kill();
    }
    *app.state::<BackendOwned>().0.lock().unwrap() = false;
}

fn server_port() -> u16 {
    if let Ok(value) = std::env::var("TRAIO_SERVER_PORT") {
        if let Ok(port) = value.parse::<u16>() {
            if port > 0 {
                return port;
            }
        }
    }
    if cfg!(debug_assertions) {
        DEV_SERVER_PORT
    } else {
        PROD_SERVER_PORT
    }
}

fn server_addr() -> SocketAddr {
    format!("127.0.0.1:{}", server_port())
        .parse()
        .expect("valid server addr")
}

fn server_healthy() -> bool {
    TcpStream::connect_timeout(&server_addr(), Duration::from_millis(500)).is_ok()
}

fn wait_for_healthy(timeout: Duration) -> bool {
    let deadline = Instant::now() + timeout;
    while Instant::now() < deadline {
        if server_healthy() {
            return true;
        }
        std::thread::sleep(Duration::from_millis(200));
    }
    false
}

fn runtime_dir() -> String {
    if let Ok(dir) = std::env::var("TRAIO_RUNTIME_DIR") {
        if !dir.is_empty() {
            return dir;
        }
    }

    #[cfg(target_os = "macos")]
    {
        if let Ok(home) = std::env::var("HOME") {
            return format!("{home}/Library/Application Support/Traio");
        }
    }

    #[cfg(target_os = "windows")]
    {
        if let Ok(app_data) = std::env::var("APPDATA") {
            return format!("{app_data}\\Traio");
        }
    }

    #[cfg(not(any(target_os = "macos", target_os = "windows")))]
    {
        if let Ok(home) = std::env::var("HOME") {
            return format!("{home}/.local/share/Traio");
        }
    }

    "traio-data".to_string()
}
