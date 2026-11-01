import test from "node:test";
import assert from "node:assert/strict";
import { meetingWindowError, overlaps } from "../src/lib/scheduling";

test("time ranges that touch at a boundary do not overlap", () => {
  const a = { startAt: new Date("2030-01-01T09:00:00Z"), endAt: new Date("2030-01-01T10:00:00Z") };
  const b = { startAt: new Date("2030-01-01T10:00:00Z"), endAt: new Date("2030-01-01T11:00:00Z") };
  assert.equal(overlaps(a, b), false);
  assert.equal(overlaps({ ...b, startAt: new Date("2030-01-01T09:59:59Z") }, a), true);
});

test("meeting windows reject invalid order, past time, and durations above one day", () => {
  const now = new Date("2030-01-01T00:00:00Z");
  assert.match(meetingWindowError(new Date("2030-01-01T11:00:00Z"), new Date("2030-01-01T10:00:00Z"), now)!, /sesudah/);
  assert.match(meetingWindowError(new Date("2029-12-31T23:00:00Z"), new Date("2030-01-01T01:00:00Z"), now)!, /mendatang/);
  assert.match(meetingWindowError(new Date("2030-01-02T00:00:01Z"), new Date("2030-01-03T00:00:02Z"), now)!, /24 jam/);
  assert.equal(meetingWindowError(new Date("2030-01-01T01:00:00Z"), new Date("2030-01-01T02:00:00Z"), now), null);
});
