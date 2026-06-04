import React from "react";
import "./ui.css";

// ── Card ─────────────────────────────────────────────────────────────────────

interface CardProps {
  children: React.ReactNode;
  className?: string;
  style?: React.CSSProperties;
  onClick?: () => void;
  hoverable?: boolean;
}

export function Card({ children, className = "", style, onClick, hoverable }: CardProps) {
  return (
    <div
      className={`ui-card${hoverable ? " ui-card--hover" : ""}${onClick ? " ui-card--click" : ""} ${className}`}
      style={style}
      onClick={onClick}
    >
      {children}
    </div>
  );
}

// ── KPI Card ─────────────────────────────────────────────────────────────────

interface KpiCardProps {
  label: string;
  value: string;
  sub?: string;
  valueClass?: string;
  onClick?: () => void;
  accent?: boolean;
}

export function KpiCard({ label, value, sub, valueClass = "", onClick, accent }: KpiCardProps) {
  return (
    <Card
      className={`kpi-card${accent ? " kpi-card--accent" : ""}`}
      onClick={onClick}
      hoverable={!!onClick}
    >
      <div className="kpi-label">{label}</div>
      <div className={`kpi-value mono ${valueClass}`}>{value}</div>
      {sub && <div className="kpi-sub mono text-3">{sub}</div>}
    </Card>
  );
}

// ── Section Title ─────────────────────────────────────────────────────────────

interface SectionTitleProps {
  title: string;
  hint?: string;
  trailing?: React.ReactNode;
}

export function SectionTitle({ title, hint, trailing }: SectionTitleProps) {
  return (
    <div className="section-title">
      <span className="section-title__text">{title}</span>
      {hint && <span className="section-title__hint text-3">{hint}</span>}
      {trailing && <div className="section-title__trailing">{trailing}</div>}
    </div>
  );
}

// ── StatusPill ───────────────────────────────────────────────────────────────

interface StatusPillProps {
  label: string;
  color?: string;
  variant?: "up" | "down" | "warn" | "accent" | "muted";
}

export function StatusPill({ label, variant = "muted" }: StatusPillProps) {
  return (
    <span className={`status-pill status-pill--${variant}`}>
      <span className="status-pill__dot" />
      {label}
    </span>
  );
}

// ── Badge ────────────────────────────────────────────────────────────────────

interface BadgeProps {
  label: string;
  variant?: "blue" | "teal" | "gold" | "rust" | "default";
}

export function Badge({ label, variant = "default" }: BadgeProps) {
  return <span className={`badge badge--${variant}`}>{label}</span>;
}

// ── Button ───────────────────────────────────────────────────────────────────

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "default" | "primary" | "danger" | "ghost";
  size?: "sm" | "md";
  loading?: boolean;
  icon?: React.ReactNode;
}

export function Button({
  children,
  variant = "default",
  size = "md",
  loading,
  icon,
  className = "",
  disabled,
  ...rest
}: ButtonProps) {
  return (
    <button
      className={`btn btn--${variant} btn--${size} ${className}`}
      disabled={disabled || loading}
      {...rest}
    >
      {loading ? <span className="btn__spinner" /> : icon && <span className="btn__icon">{icon}</span>}
      {children}
    </button>
  );
}

// ── Segmented control ────────────────────────────────────────────────────────

interface SegmentedProps<T extends string> {
  items: { value: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
}

export function Segmented<T extends string>({ items, value, onChange }: SegmentedProps<T>) {
  return (
    <div className="segmented">
      {items.map((item) => (
        <button
          key={item.value}
          className={`segmented__item${value === item.value ? " segmented__item--active" : ""}`}
          onClick={() => onChange(item.value)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}

// ── Spinner / loading states ─────────────────────────────────────────────────

export function Spinner() {
  return (
    <div className="spinner-wrap">
      <div className="spinner" />
    </div>
  );
}

export function EmptyState({ message }: { message: string }) {
  return <div className="empty-state text-3">{message}</div>;
}

// ── Toast ────────────────────────────────────────────────────────────────────

interface ToastProps {
  message: string;
  type?: "info" | "error" | "success";
}

export function Toast({ message, type = "info" }: ToastProps) {
  return <div className={`toast toast--${type}`}>{message}</div>;
}

// ── Input ────────────────────────────────────────────────────────────────────

interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  icon?: React.ReactNode;
}

export function Input({ icon, className = "", ...rest }: InputProps) {
  if (icon) {
    return (
      <div className={`input-wrap ${className}`}>
        <span className="input-wrap__icon">{icon}</span>
        <input className="input input--with-icon" {...rest} />
      </div>
    );
  }
  return <input className={`input ${className}`} {...rest} />;
}

// ── Table helpers ─────────────────────────────────────────────────────────────

export function Table({ children }: { children: React.ReactNode }) {
  return (
    <div className="table-wrap">
      <table className="table">{children}</table>
    </div>
  );
}

export function Th({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return <th className={`table__th${right ? " table__th--right" : ""}`}>{children}</th>;
}

export function Td({ children, right, mono, className = "" }: {
  children: React.ReactNode;
  right?: boolean;
  mono?: boolean;
  className?: string;
}) {
  return (
    <td className={`table__td${right ? " table__td--right" : ""}${mono ? " mono" : ""} ${className}`}>
      {children}
    </td>
  );
}
