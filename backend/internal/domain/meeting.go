package domain

import "time"

func MeetingWindow(start, end, now time.Time) string {
	if !end.After(start) {
		return "Waktu selesai harus sesudah waktu mulai"
	}
	if !start.After(now) {
		return "Rapat harus dijadwalkan pada waktu mendatang"
	}
	if end.Sub(start) > 24*time.Hour {
		return "Durasi rapat maksimal 24 jam"
	}
	return ""
}
