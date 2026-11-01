package httpapi

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
)

func (s *Server) managementOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r, "ADMIN"); !ok {
		return
	}

	page, size, offset := pageParams(r)
	docType := r.URL.Query().Get("type")
	if !validEnum(docType, "SUPPORTING", "DOCUMENTATION", "MINUTES", "ATTENDANCE_LIST") {
		docType = ""
	}
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	where := []string{"1=1"}
	args := []any{}
	if docType != "" {
		where = append(where, "doc.document_type=?")
		args = append(args, docType)
	}
	if search != "" {
		where = append(where, "lower(m.title) LIKE ?")
		args = append(args, "%"+strings.ToLower(search)+"%")
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM documents doc JOIN meetings m ON m.id=doc.meeting_id WHERE `+clause), args...).Scan(&total); err != nil {
		writeError(w, 500, "Gagal memuat arsip dokumen", nil)
		return
	}
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT doc.id,doc.filename,doc.document_type,doc.created_at,doc.verified_at,doc.uploaded_by_id,m.id,m.title,m.start_at,d.name,u.name FROM documents doc JOIN meetings m ON m.id=doc.meeting_id JOIN departments d ON d.id=m.department_id LEFT JOIN users u ON u.id=doc.uploaded_by_id WHERE `+clause+` ORDER BY doc.created_at DESC LIMIT ? OFFSET ?`), append(args, size, offset)...)
	if err != nil {
		writeError(w, 500, "Gagal memuat arsip dokumen", nil)
		return
	}
	documents := []any{}
	for rows.Next() {
		var id, filename, typ, created, meetingID, title, start, department string
		var verifiedAt, uploadedID, uploadedName sql.NullString
		if rows.Scan(&id, &filename, &typ, &created, &verifiedAt, &uploadedID, &meetingID, &title, &start, &department, &uploadedName) != nil {
			rows.Close()
			writeError(w, 500, "Gagal membaca arsip dokumen", nil)
			return
		}
		var uploaded any
		if uploadedName.Valid {
			uploaded = map[string]string{"name": uploadedName.String}
		}
		documents = append(documents, map[string]any{
			"id": id, "filename": filename, "documentType": typ, "createdAt": created, "verifiedAt": nullableOut(verifiedAt),
			"uploadedBy": uploaded,
			"meeting":    map[string]any{"id": meetingID, "title": title, "startAt": start, "department": map[string]string{"name": department}},
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(w, 500, "Gagal membaca arsip dokumen", nil)
		return
	}
	rows.Close()

	counts := map[string]int{"SUPPORTING": 0, "DOCUMENTATION": 0, "MINUTES": 0, "ATTENDANCE_LIST": 0}
	countRows, err := s.store.DB().QueryContext(r.Context(), `SELECT document_type,count(*) FROM documents GROUP BY document_type`)
	if err != nil {
		writeError(w, 500, "Gagal menghitung dokumen", nil)
		return
	}
	for countRows.Next() {
		var typ string
		var count int
		if countRows.Scan(&typ, &count) == nil {
			counts[typ] = count
		}
	}
	if err := countRows.Err(); err != nil {
		countRows.Close()
		writeError(w, 500, "Gagal menghitung dokumen", nil)
		return
	}
	countRows.Close()

	statusCounts := map[string]int{}
	for _, status := range []string{"SUBMITTED", "APPROVED", "COMPLETED"} {
		var count int
		if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM meetings WHERE status=?`), status).Scan(&count); err != nil {
			writeError(w, 500, "Gagal menghitung rapat", nil)
			return
		}
		statusCounts[status] = count
	}
	nowValue := time.Now().UTC().Format(time.RFC3339Nano)
	missingPredicate := `status='APPROVED' AND end_at<=? AND (NOT EXISTS(SELECT 1 FROM documents d WHERE d.meeting_id=meetings.id AND d.document_type='DOCUMENTATION' AND d.verified_at IS NOT NULL) OR NOT EXISTS(SELECT 1 FROM documents d WHERE d.meeting_id=meetings.id AND d.document_type='ATTENDANCE_LIST' AND d.verified_at IS NOT NULL))`
	var missingCount int
	if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM meetings WHERE `+missingPredicate), nowValue).Scan(&missingCount); err != nil {
		writeError(w, 500, "Gagal menghitung evidence yang belum lengkap", nil)
		return
	}
	missingRows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT m.id,m.title,m.end_at,d.name,u.name FROM meetings m JOIN departments d ON d.id=m.department_id JOIN users u ON u.id=m.organizer_id WHERE `+strings.ReplaceAll(missingPredicate, "meetings.", "m.")+` ORDER BY m.end_at LIMIT 20`), nowValue)
	if err != nil {
		writeError(w, 500, "Gagal memuat rapat yang memerlukan evidence", nil)
		return
	}
	type incompleteMeeting struct {
		id, title, ended, department, organizer string
	}
	incomplete := []incompleteMeeting{}
	for missingRows.Next() {
		var item incompleteMeeting
		if missingRows.Scan(&item.id, &item.title, &item.ended, &item.department, &item.organizer) != nil {
			missingRows.Close()
			writeError(w, 500, "Gagal membaca rapat yang memerlukan evidence", nil)
			return
		}
		incomplete = append(incomplete, item)
	}
	if err := missingRows.Err(); err != nil {
		missingRows.Close()
		writeError(w, 500, "Gagal membaca rapat yang memerlukan evidence", nil)
		return
	}
	missingRows.Close()
	missing := []any{}
	for _, meeting := range incomplete {
		typeRows, queryErr := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT document_type,verified_at FROM documents WHERE meeting_id=?`), meeting.id)
		if queryErr != nil {
			writeError(w, 500, "Gagal memeriksa evidence rapat", nil)
			return
		}
		type evidenceType struct {
			documentType string
			verified     bool
		}
		types := []evidenceType{}
		for typeRows.Next() {
			var typ string
			var verifiedAt sql.NullString
			if typeRows.Scan(&typ, &verifiedAt) == nil {
				types = append(types, evidenceType{documentType: typ, verified: verifiedAt.Valid})
			}
		}
		typeErr := typeRows.Err()
		typeRows.Close()
		if typeErr != nil {
			writeError(w, 500, "Gagal memeriksa evidence rapat", nil)
			return
		}
		docs := []any{}
		for _, typ := range types {
			docs = append(docs, map[string]any{"documentType": typ.documentType, "verifiedAt": typ.verified})
		}
		missing = append(missing, map[string]any{
			"id": meeting.id, "title": meeting.title, "endAt": meeting.ended,
			"department": map[string]string{"name": meeting.department},
			"organizer":  map[string]string{"name": meeting.organizer},
			"documents":  docs,
		})
	}

	writeJSON(w, 200, map[string]any{
		"documents": documents, "total": total, "page": page, "pageSize": size,
		"documentCounts": counts, "meetingCounts": statusCounts,
		"missingCount": missingCount, "missing": missing,
	})
}
