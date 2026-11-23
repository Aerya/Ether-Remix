export type DurationUnit = "s" | "m" | "h" | "d";

const UNIT_SECONDS: Record<DurationUnit, number> = {
  d: 86400,
  h: 3600,
  m: 60,
  s: 1,
};

const DISPLAY_UNITS: DurationUnit[] = ["d", "h", "m", "s"];

export function parseDuration(raw: string): { amount: string; unit: DurationUnit } {
  if (!raw) return { amount: "", unit: "h" };
  const factors: Record<string, number> = {
    ns: 1e-9,
    us: 1e-6,
    µs: 1e-6,
    ms: 0.001,
    s: 1,
    m: 60,
    h: 3600,
  };
  let seconds = 0;
  const parts = [...raw.matchAll(/([+-]?\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g)];
  if (parts.map((p) => p[0]).join("") !== raw) return { amount: raw, unit: "h" };
  for (const p of parts) seconds += Number(p[1]) * factors[p[2]!]!;
  const total = Number(seconds.toPrecision(12));
  if (total === 0) return { amount: "0", unit: "h" };
  for (const unit of DISPLAY_UNITS) {
    const divided = Number((total / UNIT_SECONDS[unit]).toPrecision(12));
    if (Number.isInteger(divided)) {
      return { amount: String(divided), unit };
    }
  }
  return { amount: String(Number(total.toFixed(2))), unit: "s" };
}

export function formatDuration(amount: string, unit: DurationUnit): string {
  if (amount === "") return "";
  const seconds = Number((Number(amount) * UNIT_SECONDS[unit]).toPrecision(12));
  if (!Number.isFinite(seconds)) return amount;
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}
