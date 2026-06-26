mod backend;

use tauri::RunEvent;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .manage(backend::BackendChild(std::sync::Mutex::new(None)))
        .manage(backend::BackendOwned(std::sync::Mutex::new(false)))
        .setup(|app| {
            if let Err(err) = backend::ensure_started(app.handle()) {
                eprintln!("[traio] backend: {err}");
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application");

    app.run(|app, event| {
        if matches!(event, RunEvent::Exit) && cfg!(debug_assertions) {
            backend::shutdown_owned(app);
        }
    });
}
