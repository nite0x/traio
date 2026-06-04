export const fmt = {
  money: (v?: number) => {
    if (v == null) return "—";
    return new Intl.NumberFormat("zh-CN", {
      style: "currency",
      currency: "USD",
      minimumFractionDigits: 2,
    }).format(v);
  },

  price: (v?: number) => {
    if (v == null) return "—";
    return new Intl.NumberFormat("zh-CN", {
      minimumFractionDigits: 2,
      maximumFractionDigits: 4,
    }).format(v);
  },

  pct: (v?: number) => {
    if (v == null) return "—";
    const sign = v >= 0 ? "+" : "";
    return `${sign}${v.toFixed(2)}%`;
  },

  compact: (v: number) => {
    if (Math.abs(v) >= 1_000_000)
      return `${(v / 1_000_000).toFixed(1)}M`;
    if (Math.abs(v) >= 1_000)
      return `${(v / 1_000).toFixed(0)}K`;
    return String(v);
  },
};
