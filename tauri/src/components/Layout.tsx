import { useEffect, useRef } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { getCurrentWindow } from "@tauri-apps/api/window";
import {
  LayoutDashboard,
  Sun,
  Eye,
  Briefcase,
  BarChart2,
  Activity,
  Settings,
  Wifi,
  WifiOff,
  Loader2,
} from "lucide-react";
import { useBackendStatus } from "../hooks/useBackend";
import "./Layout.css";

const NAV = [
  { to: "/",          icon: LayoutDashboard, label: "概览" },
  { to: "/today",     icon: Sun,             label: "今日" },
  { to: "/watch",     icon: Eye,             label: "自选" },
  { to: "/holdings",  icon: Briefcase,       label: "持仓" },
  { to: "/analysis",  icon: BarChart2,       label: "分析" },
  { to: "/broker",    icon: Activity,        label: "券商" },
];

function BackendIndicator() {
  const status = useBackendStatus();
  const label = status === "online" ? "在线" : status === "offline" ? "离线" : "连接中";
  return (
    <div className={`backend-indicator backend-indicator--${status}`}>
      {status === "online"     && <Wifi size={12} />}
      {status === "offline"    && <WifiOff size={12} />}
      {status === "connecting" && <Loader2 size={12} className="spin" />}
      <span>{label}</span>
    </div>
  );
}

export default function Layout() {
  const dragRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = dragRef.current;
    if (!el) return;
    const onMouseDown = (e: MouseEvent) => {
      if (e.buttons === 1) getCurrentWindow().startDragging();
    };
    el.addEventListener("mousedown", onMouseDown);
    return () => el.removeEventListener("mousedown", onMouseDown);
  }, []);

  return (
    <div className="layout">
      {/* Full-width drag bar pinned to top of window */}
      <div className="titlebar-drag" ref={dragRef} />
      <nav className="sidebar">
        <div className="sidebar__header">
          <span className="sidebar__brand">Traio</span>
        </div>

        <ul className="sidebar__nav">
          {NAV.map(({ to, icon: Icon, label }) => (
            <li key={to}>
              <NavLink
                to={to}
                end={to === "/"}
                className={({ isActive }) => `nav-item${isActive ? " nav-item--active" : ""}`}
              >
                <Icon size={16} />
                <span>{label}</span>
              </NavLink>
            </li>
          ))}
        </ul>

        <div className="sidebar__bottom">
          <BackendIndicator />
          <NavLink
            to="/settings"
            className={({ isActive }) => `nav-item${isActive ? " nav-item--active" : ""}`}
          >
            <Settings size={16} />
            <span>设置</span>
          </NavLink>
        </div>
      </nav>

      {/* Main content */}
      <main className="content">
        <Outlet />
      </main>
    </div>
  );
}
