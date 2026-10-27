import type { NetWorthPoint } from "./schema";

/** Missing days and changed account coverage split a series instead of inventing a return. */
export function netWorthSeries(points: readonly NetWorthPoint[]) {
  const present = points.filter((point) => point.netWorth !== null);
  if (present.length === 0) return { paths: [] as string[], dots: [] as { x: number; y: number; day: string; value: number }[], min: 0, max: 0 };
  const min = Math.min(...present.map((point) => point.netWorth!));
  const max = Math.max(...present.map((point) => point.netWorth!));
  const spread = max - min || Math.max(Math.abs(max) * 0.01, 1);
  const dots: { x: number; y: number; day: string; value: number }[] = [];
  const paths: string[] = [];
  let current: string[] = [];
  let coverage = "";
  for (const [index, point] of points.entries()) {
    const nextCoverage = [...point.accountHashes].sort().join(",");
    if (point.netWorth === null || nextCoverage !== coverage) {
      if (current.length > 0) paths.push(current.join(" "));
      current = [];
    }
    coverage = nextCoverage;
    if (point.netWorth === null) continue;
    const x = points.length === 1 ? 300 : 12 + index / (points.length - 1) * 576;
    const y = max === min ? 90 : 12 + (max - point.netWorth) / spread * 156;
    dots.push({ x, y, day: point.day, value: point.netWorth });
    current.push(`${current.length === 0 ? "M" : "L"}${x.toFixed(2)},${y.toFixed(2)}`);
  }
  if (current.length > 0) paths.push(current.join(" "));
  return { paths, dots, min, max };
}
