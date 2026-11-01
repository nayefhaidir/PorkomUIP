package httpapi

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) documentVerify(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	id := r.PathValue("id")
	var meetingID, organizerID, title, number, documentType, meetingEnd string
	var verifiedAt sql.NullString
	err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT d.meeting_id,m.organizer_id,m.title,m.meeting_number,d.document_type,d.verified_at,m.end_at FROM documents d JOIN meetings m ON m.id=d.meeting_id WHERE d.id=? AND m.status='APPROVED'`), id).Scan(&meetingID, &organizerID, &title, &number, &documentType, &verifiedAt, &meetingEnd)
	if err != nil {
		writeError(w, 404, "Evidence rapat tidak ditemukan atau tidak dapat diverifikasi", nil)
		return
	}
	if !validEnum(documentType, "DOCUMENTATION", "ATTENDANCE_LIST") {
		writeError(w, 400, "Jenis dokumen ini tidak memerlukan verifikasi evidence", nil)
		return
	}
	endedAt, err := time.Parse(time.RFC3339Nano, meetingEnd)
	if err != nil {
		writeError(w, 500, "Waktu rapat tidak valid untuk verifikasi evidence", nil)
		return
	}
	if endedAt.After(time.Now()) {
		writeError(w, http.StatusConflict, "Evidence hanya dapat diverifikasi setelah waktu rapat selesai", nil)
		return
	}
	if verifiedAt.Valid {
		writeJSON(w, 200, map[string]any{"ok": true, "verifiedAt": verifiedAt.String, "alreadyVerified": true})
		return
	}
	cfg, err := s.loadMailConfig(r)
	if err != nil {
		writeError(w, 500, "Gagal memuat pengaturan email", nil)
		return
	}
	stamp := now()
	completed := false
	alreadyVerified := false
	err = s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(r.Context(), s.store.Q(`UPDATE documents SET verified_at=?,verified_by_id=? WHERE id=? AND verified_at IS NULL AND EXISTS(SELECT 1 FROM meetings WHERE meetings.id=documents.meeting_id AND meetings.status='APPROVED')`), stamp, u.ID, id)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			var current sql.NullString
			var status string
			if err := tx.QueryRowContext(r.Context(), s.store.Q(`SELECT d.verified_at,m.status FROM documents d JOIN meetings m ON m.id=d.meeting_id WHERE d.id=?`), id).Scan(&current, &status); err != nil {
				return err
			}
			if status != "APPROVED" {
				return sql.ErrNoRows
			}
			if current.Valid {
				stamp = current.String
				alreadyVerified = true
				return nil
			}
			return fmt.Errorf("evidence verification was not applied")
		}
		var verifiedDocumentation, verifiedAttendance int
		if err := tx.QueryRowContext(r.Context(), s.store.Q(`SELECT coalesce(sum(CASE WHEN document_type='DOCUMENTATION' AND verified_at IS NOT NULL THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN document_type='ATTENDANCE_LIST' AND verified_at IS NOT NULL THEN 1 ELSE 0 END),0) FROM documents WHERE meeting_id=?`), meetingID).Scan(&verifiedDocumentation, &verifiedAttendance); err != nil {
			return err
		}
		completed = verifiedDocumentation > 0 && verifiedAttendance > 0
		if completed {
			if _, err := tx.ExecContext(r.Context(), s.store.Q(`UPDATE meetings SET status='COMPLETED',updated_at=? WHERE id=? AND status='APPROVED'`), stamp, meetingID); err != nil {
				return err
			}
		}
		var recipient string
		if err := tx.QueryRowContext(r.Context(), s.store.Q(`SELECT email FROM users WHERE id=? AND active=1`), organizerID).Scan(&recipient); err != nil && err != sql.ErrNoRows {
			return err
		}
		if recipient != "" {
			if _, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO notifications(id,user_id,title,body,created_at) VALUES(?,?,?,?,?)`), newID(), organizerID, "Evidence rapat diverifikasi", fmt.Sprintf("%s · %s", number, title), stamp); err != nil {
				return err
			}
			if err := s.enqueueEventTx(r.Context(), tx, cfg, "verification", meetingID+":"+id, title, number, []string{recipient}); err != nil {
				return err
			}
		}
		return txAudit(s.store, r, tx, u, "VERIFY", "Document", id, map[string]any{"meetingId": meetingID, "documentType": documentType})
	})
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, 404, "Evidence rapat tidak ditemukan atau tidak dapat diverifikasi", nil)
			return
		}
		writeError(w, 500, "Gagal memverifikasi evidence", nil)
		return
	}
	if alreadyVerified {
		writeJSON(w, 200, map[string]any{"ok": true, "verifiedAt": stamp, "alreadyVerified": true})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "verifiedAt": stamp, "meetingCompleted": completed})
}
