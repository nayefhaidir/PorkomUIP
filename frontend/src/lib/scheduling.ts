export type TimeRange = { startAt: Date; endAt: Date };

/** Meetings use half-open intervals, so one may start exactly when another ends. */
export function overlaps(left: TimeRange, right: TimeRange): boolean {
  return left.startAt < right.endAt && left.endAt > right.startAt;
}

export function meetingWindowError(startAt: Date, endAt: Date, now = new Date()): string | null {
  if (!Number.isFinite(startAt.getTime()) || !Number.isFinite(endAt.getTime())) return "Waktu rapat tidak valid";
  if (endAt <= startAt) return "Waktu selesai harus sesudah waktu mulai";
  if (startAt <= now) return "Rapat harus dijadwalkan pada waktu mendatang";
  if (endAt.getTime() - startAt.getTime() > 24 * 60 * 60 * 1000) return "Durasi rapat maksimal 24 jam";
  return null;
}
