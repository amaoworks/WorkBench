import assert from "node:assert/strict";
import { test } from "node:test";
import { millisecondsUntilNextLocalDay, notificationDayGroup } from "../src/features/notifications/notificationTime.ts";

process.env.TZ = "America/New_York";

test("notification day groups use local calendar boundaries across daylight saving changes", () => {
  const now = new Date(2024, 2, 11, 12);

  assert.equal(notificationDayGroup(new Date(2024, 2, 11, 0, 1).toISOString(), now), "今天");
  assert.equal(notificationDayGroup(new Date(2024, 2, 10, 0, 1).toISOString(), now), "昨天");
  assert.equal(notificationDayGroup(new Date(2024, 2, 9, 23, 59).toISOString(), now), "更早");
});

test("next local day delay follows a short daylight saving day", () => {
  const start = new Date(2024, 2, 10, 0);
  assert.equal(millisecondsUntilNextLocalDay(start), 23 * 60 * 60 * 1000);
});
