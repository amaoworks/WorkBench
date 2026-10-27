import assert from "node:assert/strict";
import { test } from "node:test";
import { netWorthSeries } from "../src/modules/investment/netWorthSeries.ts";

const point = (day, value, accounts = ["a"]) => ({ day, netWorth: value, accountHashes: accounts, cash: null, invested: null, dayPnl: null });

test("net worth chart splits missing days and changing account coverage", () => {
  const chart = netWorthSeries([
    point("2026-10-01", 100), point("2026-10-02", 110), point("2026-10-05", null, []),
    point("2026-10-06", 120), point("2026-10-07", 200, ["a", "b"]), point("2026-10-08", 210, ["b", "a"]),
  ]);
  assert.equal(chart.paths.length, 3);
  assert.equal(chart.dots.length, 5);
  assert.equal(chart.min, 100);
  assert.equal(chart.max, 210);
  assert.equal(chart.paths[0].split("L").length, 2);
  assert.equal(chart.paths[2].split("L").length, 2);
});

test("empty, zero, and flat net worth series remain finite and truthful", () => {
  assert.equal(netWorthSeries([point("2026-10-01", null)]).dots.length, 0);
  const chart = netWorthSeries([point("2026-10-01", 0), point("2026-10-02", 0)]);
  assert.equal(chart.dots.length, 2);
  assert.equal(chart.min, 0);
  assert.equal(chart.max, 0);
  assert(chart.dots.every((dot) => Number.isFinite(dot.x) && Number.isFinite(dot.y)));
});
