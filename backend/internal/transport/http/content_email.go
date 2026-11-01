package httpapi

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *Server) announcementsList(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	query := `SELECT id,type,title,body,image_storage_key,image_mime_type,published_at,expires_at FROM announcements WHERE (expires_at IS NULL OR expires_at>?)`
	args := []any{now()}
	query += ` AND (target_roles='[]' OR target_roles LIKE ?)`
	args = append(args, `%"`+u.Role+`"%`)
	departmentID := ""
	if u.DepartmentID != nil {
		departmentID = *u.DepartmentID
	}
	query += ` AND (target_department_ids='[]' OR (?<>'' AND target_department_ids LIKE ?))`
	args = append(args, departmentID, `%"`+departmentID+`"%`)
	if typ := r.URL.Query().Get("type"); validEnum(typ, "GENERAL", "MANAGEMENT_TALK", "UIP_NEWS") {
		query += ` AND type=?`
		args = append(args, typ)
	}
	query += ` ORDER BY published_at DESC LIMIT 20`
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(query), args...)
	if err != nil {
		writeError(w, 500, "Gagal memuat pengumuman", nil)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, typ, title, body, published string
		var key, mime, expiry sql.NullString
		if err := rows.Scan(&id, &typ, &title, &body, &key, &mime, &published, &expiry); err != nil {
			writeError(w, 500, "Gagal membaca pengumuman", nil)
			return
		}
		items = append(items, announcementJSON(id, typ, title, body, key, mime, published, expiry))
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, "Gagal membaca pengumuman", nil)
		return
	}
	writeJSON(w, 200, items)
}

func announcementJSON(id, typ, title, body string, key, mime sql.NullString, published string, expiry sql.NullString) map[string]any {
	var imageURL any
	if key.Valid {
		imageURL = "/api/announcements/" + id + "/image"
	}
	return map[string]any{"id": id, "type": typ, "title": title, "body": body, "publishedAt": published, "expiresAt": nullableOut(expiry), "imageUrl": imageURL}
}

func announcementAudience(value any) ([]string, []string, error) {
	roles, err := stringArrayValue(valueMap(value, "targetRoles"))
	if err != nil {
		return nil, nil, err
	}
	departments, err := stringArrayValue(valueMap(value, "targetDepartmentIds"))
	if err != nil {
		return nil, nil, err
	}
	if len(roles) > 4 || len(departments) > 100 || hasDuplicates(roles) || hasDuplicates(departments) {
		return nil, nil, fmt.Errorf("invalid audience")
	}
	for _, role := range roles {
		if !validEnum(role, "ADMIN", "APPROVER", "ORGANIZER", "PARTICIPANT") {
			return nil, nil, fmt.Errorf("invalid audience")
		}
	}
	for _, department := range departments {
		if strings.TrimSpace(department) == "" {
			return nil, nil, fmt.Errorf("invalid audience")
		}
	}
	return roles, departments, nil
}

func valueMap(value any, key string) any {
	if values, ok := value.(map[string]any); ok {
		return values[key]
	}
	return nil
}

func stringArrayValue(value any) ([]string, error) {
	if value == nil || value == "" {
		return []string{}, nil
	}
	if items, ok := value.([]any); ok {
		result := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("invalid array")
			}
			result = append(result, text)
		}
		return result, nil
	}
	raw, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("invalid array")
	}
	var result []string
	if err := json.Unmarshal([]byte(raw), &result); err != nil || result == nil {
		return nil, fmt.Errorf("invalid array")
	}
	return result, nil
}

func announcementTargetsMatch(role, departmentID string, roles, departments []string) bool {
	roleMatch, departmentMatch := len(roles) == 0, len(departments) == 0
	for _, target := range roles {
		if target == role {
			roleMatch = true
			break
		}
	}
	for _, target := range departments {
		if target == departmentID {
			departmentMatch = true
			break
		}
	}
	return roleMatch && departmentMatch
}

func (s *Server) announcementsCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	var b map[string]any
	var imageBytes []byte
	var mime, filename string
	if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
		if err := r.ParseMultipartForm(6 << 20); err != nil {
			writeError(w, 413, "Gambar harus PNG/JPG dan maksimal 5 MiB", nil)
			return
		}
		b = map[string]any{"type": r.FormValue("type"), "title": r.FormValue("title"), "body": r.FormValue("body"), "expiresAt": r.FormValue("expiresAt"), "targetRoles": r.FormValue("targetRoles"), "targetDepartmentIds": r.FormValue("targetDepartmentIds")}
		file, header, err := r.FormFile("image")
		if err == nil {
			defer file.Close()
			mime, filename = header.Header.Get("Content-Type"), header.Filename
			imageBytes, err = io.ReadAll(io.LimitReader(file, 5<<20+1))
			if err != nil {
				writeError(w, 400, "Berkas gambar tidak valid", nil)
				return
			}
		}
	} else {
		b, ok = decodeMap(w, r)
		if !ok {
			return
		}
	}
	typ := asString(b, "type")
	if typ == "" {
		typ = "GENERAL"
	}
	title, body := strings.TrimSpace(asString(b, "title")), strings.TrimSpace(asString(b, "body"))
	targetRoles, targetDepartments, audienceErr := announcementAudience(b)
	if !validEnum(typ, "GENERAL", "MANAGEMENT_TALK", "UIP_NEWS") || len(title) < 3 || len(title) > 160 || len(body) < 3 || len(body) > 3000 {
		writeError(w, 400, "Pengumuman tidak valid", nil)
		return
	}
	if audienceErr != nil {
		writeError(w, 400, "Sasaran pengumuman tidak valid", nil)
		return
	}
	if typ == "GENERAL" && u.Role != "ADMIN" {
		writeError(w, 403, "Hanya Super User yang dapat menerbitkan pengumuman umum", nil)
		return
	}
	if typ == "MANAGEMENT_TALK" && !containsPage(u, "management-talk", u.Role == "ADMIN" || u.Role == "APPROVER" && (strings.Contains(strings.ToUpper(valueOrEmpty(u.JobTitle)), "GM") || strings.Contains(strings.ToUpper(valueOrEmpty(u.JobTitle)), "SRM"))) {
		writeError(w, 403, "Management Talk hanya dapat diterbitkan oleh GM atau SRM yang ditetapkan", nil)
		return
	}
	if typ == "UIP_NEWS" && !containsPage(u, "berita-uip", u.Role == "ADMIN" || u.Role == "APPROVER" && departmentShortContainsCSR(r, s, u)) {
		writeError(w, 403, "Berita UIP hanya dapat diterbitkan oleh Super User atau bidang CSR & Umum", nil)
		return
	}
	if (typ == "UIP_NEWS" && len(imageBytes) == 0) || (typ != "UIP_NEWS" && len(imageBytes) > 0) {
		writeError(w, 400, "Gambar hanya wajib dan dapat ditambahkan pada Berita UIP", nil)
		return
	}
	key := ""
	if len(imageBytes) > 0 {
		ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg"}[mime]
		if ext == "" || len(imageBytes) == 0 || len(imageBytes) > 5<<20 || filepath.Ext(strings.ToLower(filename)) != ext || !validSignature(mime, imageBytes) {
			writeError(w, 415, "Gambar harus PNG/JPG dan maksimal 5 MiB", nil)
			return
		}
		key = newID() + ext
		if err := os.WriteFile(filepath.Join(storageDir(), key), imageBytes, 0600); err != nil {
			writeError(w, 500, "Gagal menyimpan gambar", nil)
			return
		}
	}
	for _, departmentID := range targetDepartments {
		var count int
		if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM departments WHERE id=?`), departmentID).Scan(&count); err != nil || count == 0 {
			if key != "" {
				_ = os.Remove(filepath.Join(storageDir(), key))
			}
			writeError(w, 400, "Salah satu bidang sasaran tidak ditemukan", nil)
			return
		}
	}
	targetRolesJSON, _ := json.Marshal(targetRoles)
	targetDepartmentsJSON, _ := json.Marshal(targetDepartments)
	id, stamp := newID(), now()
	expiresText := strings.TrimSpace(asString(b, "expiresAt"))
	if expiresText != "" {
		if _, err := time.Parse(time.RFC3339, expiresText); err != nil {
			if key != "" {
				_ = os.Remove(filepath.Join(storageDir(), key))
			}
			writeError(w, 400, "Waktu berakhir pengumuman tidak valid", nil)
			return
		}
	}
	expiry := nullable(expiresText)
	mailConfig, err := s.loadMailConfig(r)
	if err != nil {
		if key != "" {
			_ = os.Remove(filepath.Join(storageDir(), key))
		}
		writeError(w, 500, "Gagal memuat pengaturan email", nil)
		return
	}
	err = s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO announcements(id,type,title,body,image_storage_key,image_mime_type,target_roles,target_department_ids,published_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`), id, typ, title, body, nullable(key), nullable(mime), string(targetRolesJSON), string(targetDepartmentsJSON), stamp, expiry); err != nil {
			return err
		}
		notificationTitle := title
		if strings.Contains(strings.ToLower(title), "penting") || strings.Contains(strings.ToLower(title), "urgent") || strings.Contains(strings.ToLower(title), "darurat") {
			notificationTitle = "PENTING: " + title
		}
		users, err := tx.QueryContext(r.Context(), s.store.Q(`SELECT id,email,role,department_id FROM users WHERE active=1 AND id<>?`), u.ID)
		if err != nil {
			return err
		}
		recipients := []string{}
		for users.Next() {
			var userID, email, role string
			var departmentID sql.NullString
			if err := users.Scan(&userID, &email, &role, &departmentID); err != nil {
				users.Close()
				return err
			}
			if !announcementTargetsMatch(role, departmentID.String, targetRoles, targetDepartments) {
				continue
			}
			recipients = append(recipients, email)
			if _, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO notifications(id,user_id,title,body,created_at) VALUES(?,?,?,?,?)`), newID(), userID, notificationTitle, body, stamp); err != nil {
				users.Close()
				return err
			}
		}
		if err := users.Err(); err != nil {
			users.Close()
			return err
		}
		users.Close()
		if err := s.enqueueTemplateTx(r.Context(), tx, mailConfig, "announcement", id, title, "", recipients, map[string]string{"meetingDetails": body}); err != nil {
			return err
		}
		return txAudit(s.store, r, tx, u, "CREATE", "Announcement", id, map[string]any{"type": typ, "targetRoles": targetRoles, "targetDepartmentCount": len(targetDepartments)})
	})
	if err != nil {
		if key != "" {
			_ = os.Remove(filepath.Join(storageDir(), key))
		}
		writeError(w, 500, "Gagal menyimpan pengumuman", nil)
		return
	}
	writeJSON(w, 201, announcementJSON(id, typ, title, body, sql.NullString{String: key, Valid: key != ""}, sql.NullString{String: mime, Valid: mime != ""}, stamp, sql.NullString{String: expiresText, Valid: expiresText != ""}))
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func (s *Server) announcementPatch(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	var b map[string]any
	var imageBytes []byte
	var imageMime, imageName string
	if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
		if err := r.ParseMultipartForm(6 << 20); err != nil {
			writeError(w, 413, "Gambar harus PNG/JPG dan maksimal 5 MiB", nil)
			return
		}
		b = map[string]any{"title": r.FormValue("title"), "body": r.FormValue("body"), "expiresAt": r.FormValue("expiresAt")}
		file, header, err := r.FormFile("image")
		if err == nil {
			defer file.Close()
			imageMime, imageName = header.Header.Get("Content-Type"), header.Filename
			imageBytes, err = io.ReadAll(io.LimitReader(file, 5<<20+1))
			if err != nil {
				writeError(w, 400, "Berkas gambar tidak valid", nil)
				return
			}
		}
	} else {
		var valid bool
		b, valid = decodeMap(w, r)
		if !valid {
			return
		}
	}
	id := r.PathValue("id")
	var typ, title, body string
	var key, mime, expiry sql.NullString
	var published string
	err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT type,title,body,image_storage_key,image_mime_type,published_at,expires_at FROM announcements WHERE id=?`), id).Scan(&typ, &title, &body, &key, &mime, &published, &expiry)
	if err != nil {
		writeError(w, 404, "Pengumuman tidak ditemukan", nil)
		return
	}
	if typ == "GENERAL" && u.Role != "ADMIN" ||
		typ == "MANAGEMENT_TALK" && !containsPage(u, "management-talk", u.Role == "ADMIN" || u.Role == "APPROVER" && (strings.Contains(strings.ToUpper(valueOrEmpty(u.JobTitle)), "GM") || strings.Contains(strings.ToUpper(valueOrEmpty(u.JobTitle)), "SRM"))) ||
		typ == "UIP_NEWS" && !containsPage(u, "berita-uip", u.Role == "ADMIN" || u.Role == "APPROVER" && departmentShortContainsCSR(r, s, u)) {
		writeError(w, 403, "Akses untuk mengubah bagian ini tidak tersedia", nil)
		return
	}
	if _, hasTitle := b["title"]; !hasTitle {
		writeError(w, 400, "Pengumuman tidak valid", nil)
		return
	}
	if _, hasBody := b["body"]; !hasBody {
		writeError(w, 400, "Pengumuman tidak valid", nil)
		return
	}
	if v := asString(b, "title"); v != "" {
		title = strings.TrimSpace(v)
	}
	if v := asString(b, "body"); v != "" {
		body = strings.TrimSpace(v)
	}
	if len(title) < 3 || len(title) > 160 || len(body) < 3 || len(body) > 3000 {
		writeError(w, 400, "Pengumuman tidak valid", nil)
		return
	}
	if v, exists := b["expiresAt"]; exists {
		expiry = sql.NullString{String: jsonString(v), Valid: v != nil && jsonString(v) != ""}
	}
	oldKey := key
	if len(imageBytes) > 0 {
		ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg"}[imageMime]
		if typ != "UIP_NEWS" || ext == "" || filepath.Ext(strings.ToLower(imageName)) != ext || len(imageBytes) > 5<<20 || !validSignature(imageMime, imageBytes) {
			writeError(w, 415, "Gambar harus PNG/JPG dan maksimal 5 MiB", nil)
			return
		}
		key = sql.NullString{String: newID() + ext, Valid: true}
		mime = sql.NullString{String: imageMime, Valid: true}
		if err := os.WriteFile(filepath.Join(storageDir(), key.String), imageBytes, 0600); err != nil {
			writeError(w, 500, "Gagal menyimpan gambar", nil)
			return
		}
	}
	_, err = s.store.Exec(r.Context(), `UPDATE announcements SET title=?,body=?,expires_at=?,image_storage_key=?,image_mime_type=? WHERE id=?`, title, body, nullStringArg(expiry), nullStringArg(key), nullStringArg(mime), id)
	if err != nil {
		if len(imageBytes) > 0 {
			_ = os.Remove(filepath.Join(storageDir(), key.String))
		}
		writeError(w, 500, "Gagal memperbarui pengumuman", nil)
		return
	}
	if len(imageBytes) > 0 && oldKey.Valid {
		// Old image cleanup is best effort after the replacement is committed.
		if oldKey.String != key.String {
			_ = os.Remove(filepath.Join(storageDir(), filepath.Base(oldKey.String)))
		}
	}
	s.audit(r, u, "UPDATE", "Announcement", id, nil)
	s.notifyAllActive(r, u.ID, "Konten portal diperbarui", title+" telah diperbarui.")
	writeJSON(w, 200, announcementJSON(id, typ, title, body, key, mime, published, expiry))
}

func (s *Server) announcementDelete(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var typ, title string
	var key sql.NullString
	if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT type,title,image_storage_key FROM announcements WHERE id=?`), id).Scan(&typ, &title, &key); err != nil {
		writeError(w, 404, "Pengumuman tidak ditemukan", nil)
		return
	}
	if typ == "GENERAL" && u.Role != "ADMIN" ||
		typ == "MANAGEMENT_TALK" && !containsPage(u, "management-talk", u.Role == "ADMIN" || u.Role == "APPROVER" && (strings.Contains(strings.ToUpper(valueOrEmpty(u.JobTitle)), "GM") || strings.Contains(strings.ToUpper(valueOrEmpty(u.JobTitle)), "SRM"))) ||
		typ == "UIP_NEWS" && !containsPage(u, "berita-uip", u.Role == "ADMIN" || u.Role == "APPROVER" && departmentShortContainsCSR(r, s, u)) {
		writeError(w, 403, "Akses untuk menghapus bagian ini tidak tersedia", nil)
		return
	}
	_, _ = s.store.Exec(r.Context(), `DELETE FROM announcements WHERE id=?`, id)
	if key.Valid {
		_ = os.Remove(filepath.Join(storageDir(), filepath.Base(key.String)))
	}
	s.audit(r, u, "DELETE", "Announcement", id, nil)
	s.notifyAllActive(r, u.ID, "Konten portal dihapus", title+" telah dihapus.")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) announcementImage(w http.ResponseWriter, r *http.Request) {
	var key, mime sql.NullString
	var targetRoles, targetDepartments string
	err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT image_storage_key,image_mime_type,target_roles,target_department_ids FROM announcements WHERE id=? AND type='UIP_NEWS' AND (expires_at IS NULL OR expires_at>?)`), r.PathValue("id"), now()).Scan(&key, &mime, &targetRoles, &targetDepartments)
	if err != nil || !key.Valid || !mime.Valid {
		writeError(w, 404, "Gambar berita tidak ditemukan", nil)
		return
	}
	publicAudience := (targetRoles == "[]" || targetRoles == `["ADMIN","APPROVER","ORGANIZER","PARTICIPANT"]`) && targetDepartments == "[]"
	if !publicAudience {
		u, ok := userFrom(r)
		departmentID := ""
		if u.DepartmentID != nil {
			departmentID = *u.DepartmentID
		}
		if !ok || !announcementTargetsMatch(u.Role, departmentID, decodeList(targetRoles), decodeList(targetDepartments)) {
			writeError(w, 404, "Gambar berita tidak ditemukan", nil)
			return
		}
	}
	content, err := os.ReadFile(filepath.Join(storageDir(), filepath.Base(key.String)))
	if err != nil {
		writeError(w, 404, "Berkas gambar tidak ditemukan", nil)
		return
	}
	w.Header().Set("Content-Type", mime.String)
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	if publicAudience {
		w.Header().Set("Cache-Control", "public, max-age=300, stale-while-revalidate=600")
	} else {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = w.Write(content)
}

type mailConfig struct {
	Enabled                   bool                    `json:"enabled"`
	Host                      string                  `json:"host"`
	Port                      int                     `json:"port"`
	Secure                    bool                    `json:"secure"`
	Username                  string                  `json:"username"`
	PasswordCipher            string                  `json:"passwordCipher,omitempty"`
	FromName                  string                  `json:"fromName"`
	FromEmail                 string                  `json:"fromEmail"`
	FacilitiesEmail           string                  `json:"facilitiesEmail"`
	AdminEmail                string                  `json:"adminEmail"`
	EvidenceEscalationEmail   string                  `json:"evidenceEscalationEmail"`
	EvidenceReminderHours     []int                   `json:"evidenceReminderHours"`
	PreMeetingReminderMinutes []int                   `json:"preMeetingReminderMinutes"`
	Events                    map[string]bool         `json:"events"`
	Templates                 map[string]mailTemplate `json:"templates"`
}

type mailTemplate struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

var emailEvents = []string{"submission", "status", "invite", "catering", "evidence", "evidenceUploaded", "announcement", "reschedule", "beforeMeeting", "verification", "systemAlert"}

func defaultMailConfig() mailConfig {
	events := map[string]bool{}
	templates := map[string]mailTemplate{}
	for _, k := range emailEvents {
		events[k] = true
		templates[k] = mailTemplate{Subject: "Informasi UIP Meeting Portal", Body: "Informasi rapat: {{title}}"}
	}
	templates["submission"] = mailTemplate{Subject: "Pengajuan rapat baru: {{meetingTitle}}", Body: "{{meetingTitle}} ({{meetingNumber}}) menunggu persetujuan.\n{{portalUrl}}"}
	templates["status"] = mailTemplate{Subject: "Pembaruan rapat: {{meetingTitle}}", Body: "Status: {{status}}\n{{meetingDetails}}\n{{reason}}\n{{portalUrl}}"}
	templates["invite"] = mailTemplate{Subject: "Undangan rapat: {{meetingTitle}}", Body: "Anda diundang ke rapat berikut:\n{{meetingDetails}}\n{{portalUrl}}"}
	templates["catering"] = mailTemplate{Subject: "Permintaan konsumsi: {{meetingTitle}}", Body: "{{meetingDetails}}\nRincian: {{consumption}}\n{{portalUrl}}"}
	templates["evidence"] = mailTemplate{Subject: "Pengingat evidence rapat: {{meetingTitle}}", Body: "{{meetingTitle}} ({{meetingNumber}})\nBukti yang perlu dilengkapi/diverifikasi: {{missingEvidence}}\n{{portalUrl}}"}
	templates["evidenceUploaded"] = mailTemplate{Subject: "Evidence diunggah: {{meetingTitle}}", Body: "Evidence rapat telah diunggah dan menunggu verifikasi.\n{{meetingTitle}} ({{meetingNumber}})\n{{portalUrl}}"}
	templates["beforeMeeting"] = mailTemplate{Subject: "Pengingat rapat: {{meetingTitle}}", Body: "{{meetingDetails}}\n{{portalUrl}}"}
	templates["verification"] = mailTemplate{Subject: "Evidence diverifikasi: {{meetingTitle}}", Body: "Evidence rapat {{meetingTitle}} ({{meetingNumber}}) telah diverifikasi.\n{{portalUrl}}"}
	templates["reschedule"] = mailTemplate{Subject: "Perubahan jadwal: {{meetingTitle}}", Body: "{{meetingDetails}}\n{{portalUrl}}"}
	templates["announcement"] = mailTemplate{Subject: "Pengumuman UIP Meeting Portal: {{meetingTitle}}", Body: "{{meetingTitle}}\n{{meetingDetails}}\n{{portalUrl}}"}
	templates["systemAlert"] = mailTemplate{Subject: "UIP Meeting Portal: pengiriman email gagal", Body: "Satu pesan email tidak dapat dikirim setelah percobaan ulang. Periksa antrean email pada portal."}
	return mailConfig{Port: 587, FromName: "UIP Meeting Portal", Events: events, Templates: templates, EvidenceReminderHours: []int{1, 3, 7}, PreMeetingReminderMinutes: []int{60}}
}

func validSMTPHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "\r\n/\\ \t") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
	}
	normalized := strings.TrimSuffix(host, ".")
	if strings.EqualFold(normalized, "localhost") || len(normalized) > 253 || !strings.Contains(normalized, ".") {
		return false
	}
	for _, label := range strings.Split(normalized, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
	}
	return true
}

func validSMTPPort(port int) bool {
	return port == 25 || port == 465 || port == 587 || port == 2525
}

var nonPublicSMTPNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

func publicSMTPAddress(address net.IP) bool {
	ip, ok := netip.AddrFromSlice(address)
	if !ok {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	for _, network := range nonPublicSMTPNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func smtpTargetIP(ctx context.Context, host string, allowPrivate bool) (net.IP, error) {
	var addresses []net.IP
	if ip := net.ParseIP(host); ip != nil {
		addresses = []net.IP{ip}
	} else {
		resolved, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, errSMTPDNS
		}
		addresses = resolved
	}
	if len(addresses) == 0 {
		return nil, errSMTPDNS
	}
	for _, address := range addresses {
		if !allowPrivate && !publicSMTPAddress(address) {
			return nil, errSMTPPrivateTarget
		}
	}
	return addresses[0], nil
}

var (
	errSMTPDNS           = errors.New("SMTP DNS resolution failed")
	errSMTPPrivateTarget = errors.New("SMTP target is not public")
)

func intArray(values []any) []int {
	result := make([]int, 0, len(values))
	for _, value := range values {
		number, ok := value.(float64)
		if !ok || number != float64(int(number)) {
			return nil
		}
		result = append(result, int(number))
	}
	return result
}

func validIntervals(values []int, minimum, maximum int) bool {
	if len(values) == 0 || len(values) > 10 {
		return false
	}
	seen := map[int]bool{}
	for _, value := range values {
		if value < minimum || value > maximum || seen[value] {
			return false
		}
		seen[value] = true
	}
	sort.Ints(values)
	return true
}

func (s *Server) loadMailConfig(r *http.Request) (mailConfig, error) {
	return s.loadMailConfigContext(r.Context())
}

func (s *Server) loadMailConfigContext(ctx context.Context) (mailConfig, error) {
	cfg := defaultMailConfig()
	var raw string
	err := s.store.DB().QueryRowContext(ctx, s.store.Q(`SELECT value FROM settings WHERE key='emailConfig'`)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return mailConfig{}, fmt.Errorf("load email settings: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return mailConfig{}, fmt.Errorf("decode email settings: %w", err)
	}
	defaults := defaultMailConfig()
	if cfg.Events == nil {
		cfg.Events = defaults.Events
	} else {
		for _, event := range emailEvents {
			if _, ok := cfg.Events[event]; !ok {
				cfg.Events[event] = true
			}
		}
	}
	if cfg.Templates == nil {
		cfg.Templates = defaults.Templates
	} else {
		for _, event := range emailEvents {
			if _, ok := cfg.Templates[event]; !ok {
				cfg.Templates[event] = defaults.Templates[event]
			}
		}
	}
	if cfg.EvidenceReminderHours == nil {
		cfg.EvidenceReminderHours = defaults.EvidenceReminderHours
	}
	if cfg.PreMeetingReminderMinutes == nil {
		cfg.PreMeetingReminderMinutes = defaults.PreMeetingReminderMinutes
	}
	return cfg, nil
}

func (s *Server) emailConfigGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r, "ADMIN"); !ok {
		return
	}
	cfg, err := s.loadMailConfig(r)
	if err != nil {
		writeError(w, 500, "Gagal memuat pengaturan email", nil)
		return
	}
	pending, failed, sent := 0, 0, 0
	if err := s.store.DB().QueryRowContext(r.Context(), `SELECT coalesce(sum(CASE WHEN status IN ('PENDING','RETRYING','PROCESSING','SENDING') THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN status='FAILED' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN status='SENT' THEN 1 ELSE 0 END),0) FROM email_outbox`).Scan(&pending, &failed, &sent); err != nil {
		writeError(w, 500, "Gagal memuat status antrean email", nil)
		return
	}
	hasPassword := cfg.PasswordCipher != ""
	cfg.PasswordCipher = ""
	_, keyErr := hexKey(os.Getenv("MAIL_ENCRYPTION_KEY"))
	writeJSON(w, 200, map[string]any{"enabled": cfg.Enabled, "host": cfg.Host, "port": cfg.Port, "secure": cfg.Secure, "username": cfg.Username, "fromName": cfg.FromName, "fromEmail": cfg.FromEmail, "facilitiesEmail": cfg.FacilitiesEmail, "adminEmail": cfg.AdminEmail, "evidenceEscalationEmail": cfg.EvidenceEscalationEmail, "evidenceReminderHours": cfg.EvidenceReminderHours, "preMeetingReminderMinutes": cfg.PreMeetingReminderMinutes, "events": cfg.Events, "templates": cfg.Templates, "hasPassword": hasPassword, "encryptionReady": keyErr == nil, "queue": map[string]int{"pending": pending, "failed": failed, "sent": sent}})
}

func (s *Server) emailOutboxList(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r, "ADMIN"); !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !validEnum(status, "PENDING", "RETRYING", "PROCESSING", "SENT", "FAILED") {
		writeError(w, 400, "Status antrean tidak valid", nil)
		return
	}
	page, size, offset := pageParams(r)
	query := `SELECT id,event_key,recipient,subject,status,attempts,last_error,available_at,sent_at,created_at FROM email_outbox`
	args := []any{}
	if status != "" {
		query += ` WHERE status=?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, size, offset)
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(query), args...)
	if err != nil {
		writeError(w, 500, "Gagal memuat antrean email", nil)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, event, recipient, subject, state, available, created string
		var attempts int
		var lastError, sent sql.NullString
		if err := rows.Scan(&id, &event, &recipient, &subject, &state, &attempts, &lastError, &available, &sent, &created); err != nil {
			writeError(w, 500, "Gagal membaca antrean email", nil)
			return
		}
		items = append(items, map[string]any{"id": id, "event": event, "recipient": recipient, "subject": subject, "status": state, "attempts": attempts, "lastError": nullableOut(lastError), "availableAt": available, "sentAt": nullableOut(sent), "createdAt": created})
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, "Gagal membaca antrean email", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "page": page, "pageSize": size})
}

func encryptMailPassword(password string) (string, error) {
	keyBytes, err := hexKey(os.Getenv("MAIL_ENCRYPTION_KEY"))
	if err != nil {
		return "", fmt.Errorf("Kunci enkripsi SMTP belum dikonfigurasi")
	}
	block, _ := aes.NewCipher(keyBytes)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(nonce, gcm.Seal(nil, nonce, []byte(password), nil)...)), nil
}

func decryptMailPassword(encrypted string) (string, error) {
	key, err := hexKey(os.Getenv("MAIL_ENCRYPTION_KEY"))
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("invalid encrypted secret")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	return string(plain), err
}

func hexKey(value string) ([]byte, error) {
	if len(value) != 64 {
		return nil, fmt.Errorf("invalid encryption key")
	}
	var out [32]byte
	for i := 0; i < len(out); i++ {
		var b byte
		_, err := fmt.Sscanf(value[i*2:i*2+2], "%02x", &b)
		if err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out[:], nil
}

func (s *Server) emailConfigPatch(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	cfg, err := s.loadMailConfig(r)
	if err != nil {
		writeError(w, 500, "Gagal memuat pengaturan email", nil)
		return
	}
	cfg.Enabled = asBool(b, "enabled", false)
	cfg.Host = strings.TrimSpace(asString(b, "host"))
	cfg.Port = asInt(b, "port", cfg.Port)
	cfg.Secure = asBool(b, "secure", false)
	cfg.Username = strings.TrimSpace(asString(b, "username"))
	cfg.FromName = strings.TrimSpace(asString(b, "fromName"))
	cfg.FromEmail = strings.ToLower(asString(b, "fromEmail"))
	cfg.FacilitiesEmail = strings.ToLower(asString(b, "facilitiesEmail"))
	cfg.AdminEmail = strings.ToLower(strings.TrimSpace(asString(b, "adminEmail")))
	cfg.EvidenceEscalationEmail = strings.ToLower(strings.TrimSpace(asString(b, "evidenceEscalationEmail")))
	if v := asString(b, "password"); v != "" {
		encrypted, err := encryptMailPassword(v)
		if err != nil {
			writeError(w, 400, err.Error(), nil)
			return
		}
		cfg.PasswordCipher = encrypted
	}
	if raw, ok := b["events"].(map[string]any); ok {
		for _, k := range emailEvents {
			cfg.Events[k] = asBool(raw, k, cfg.Events[k])
		}
	}
	if raw, ok := b["templates"].(map[string]any); ok {
		for _, k := range emailEvents {
			if item, ok := raw[k].(map[string]any); ok {
				cfg.Templates[k] = mailTemplate{Subject: strings.TrimSpace(asString(item, "subject")), Body: strings.TrimSpace(asString(item, "body"))}
			}
		}
	}
	if raw, ok := b["evidenceReminderHours"].([]any); ok {
		cfg.EvidenceReminderHours = intArray(raw)
	}
	if raw, ok := b["preMeetingReminderMinutes"].([]any); ok {
		cfg.PreMeetingReminderMinutes = intArray(raw)
	}
	if !validSMTPPort(cfg.Port) || len(cfg.FromName) < 2 || len(cfg.FromName) > 100 || len(cfg.Username) > 254 || !validEmail(cfg.FromEmail) || !validSMTPHost(cfg.Host) ||
		(cfg.FacilitiesEmail != "" && !validEmail(cfg.FacilitiesEmail)) || (cfg.AdminEmail != "" && !validEmail(cfg.AdminEmail)) ||
		(cfg.EvidenceEscalationEmail != "" && !validEmail(cfg.EvidenceEscalationEmail)) ||
		!validIntervals(cfg.EvidenceReminderHours, 1, 24*30) || !validIntervals(cfg.PreMeetingReminderMinutes, 1, 7*24*60) {
		writeError(w, 400, "Pengaturan email tidak valid", nil)
		return
	}
	raw, _ := json.Marshal(cfg)
	_, err = s.store.Exec(r.Context(), `INSERT INTO settings(key,value,updated_at) VALUES('emailConfig',?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, string(raw), now())
	if err != nil {
		writeError(w, 500, "Gagal menyimpan pengaturan email", nil)
		return
	}
	s.audit(r, u, "UPDATE", "SystemSetting", "emailConfig", map[string]any{"enabled": cfg.Enabled, "host": cfg.Host, "fromEmail": cfg.FromEmail, "passwordUpdated": asString(b, "password") != ""})
	writeJSON(w, 200, map[string]any{"ok": true, "hasPassword": cfg.PasswordCipher != ""})
}

func (s *Server) emailTest(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	_, valid := decodeMap(w, r)
	if !valid && r.ContentLength != 0 {
		return
	}
	if err := s.sendMail(r.Context(), u.Email, "Email uji UIP Meeting Portal", "Email uji berhasil dikirim.", ""); err != nil {
		reference := newID()[:10]
		code := emailFailureCode(err)
		log.Printf("SMTP test failed reference=%s code=%s cause=%s", reference, code, emailFailureClass(err))
		writeError(w, 502, emailFailureMessage(code), map[string]string{"code": code, "reference": reference})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "Email uji diterima server SMTP untuk akun Anda."})
}

type mailSendFailure struct {
	code  string
	cause error
}

func (e *mailSendFailure) Error() string { return e.code }
func (e *mailSendFailure) Unwrap() error { return e.cause }

func mailFailure(code string, cause error) error {
	return &mailSendFailure{code: code, cause: cause}
}

func smtpFailure(code string, cause error) error {
	var networkError net.Error
	if errors.As(cause, &networkError) && networkError.Timeout() {
		return mailFailure("MAIL_SMTP_TIMEOUT", cause)
	}
	return mailFailure(code, cause)
}

func emailFailureCode(err error) string {
	var failure *mailSendFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return "MAIL_SEND_FAILED"
}

func emailFailureMessage(code string) string {
	switch code {
	case "MAIL_CONFIG_UNAVAILABLE":
		return "Pengaturan email tidak dapat dibaca. Coba muat ulang halaman; jika berlanjut, hubungi administrator."
	case "MAIL_CONFIG_INVALID":
		return "Periksa pengaturan host, port, keamanan, dan alamat email SMTP."
	case "MAIL_DISABLED":
		return "Aktifkan pengiriman email terlebih dahulu."
	case "MAIL_CREDENTIALS_UNAVAILABLE":
		return "Kredensial SMTP tidak dapat dibuka. Pastikan kunci enkripsi deployment yang sama tetap terpasang."
	case "MAIL_ENCRYPTION_KEY_MISSING":
		return "Kunci enkripsi SMTP tidak tersedia pada API. Minta administrator memeriksa konfigurasi deployment."
	case "MAIL_SMTP_DNS_FAILED":
		return "Nama host SMTP tidak ditemukan. Periksa host dan DNS pada server aplikasi."
	case "MAIL_SMTP_PRIVATE_TARGET":
		return "Relay SMTP berada di jaringan privat. Minta administrator memeriksa izin relay internal."
	case "MAIL_SMTP_TIMEOUT":
		return "Koneksi ke SMTP melewati batas waktu. Minta administrator memeriksa firewall dan akses keluar ke port SMTP."
	case "MAIL_SMTP_CONNECTION_FAILED":
		return "Server aplikasi tidak dapat tersambung ke SMTP. Periksa host, port, firewall, dan akses jaringan."
	case "MAIL_SMTP_STARTTLS_UNAVAILABLE":
		return "Server SMTP tidak menawarkan STARTTLS. Pastikan mode STARTTLS dan port server sudah sesuai."
	case "MAIL_SMTP_TLS_FAILED":
		return "Negosiasi TLS gagal. Pastikan mode keamanan cocok dengan port dan sertifikat server valid."
	case "MAIL_SMTP_AUTH_FAILED":
		return "Login SMTP ditolak. Periksa username, kata sandi, dan metode autentikasi pada server SMTP."
	case "MAIL_SMTP_SENDER_REJECTED":
		return "Server SMTP menolak alamat pengirim. Periksa izin alamat pengirim pada akun SMTP."
	case "MAIL_SMTP_RECIPIENT_REJECTED":
		return "Server SMTP menolak alamat penerima uji. Periksa kebijakan pengiriman akun SMTP."
	case "MAIL_SMTP_MESSAGE_REJECTED", "MAIL_SMTP_TRANSFER_FAILED", "MAIL_SMTP_ACCEPTANCE_FAILED":
		return "Server SMTP menolak pesan. Periksa kebijakan relay dan batas pengiriman pada server SMTP."
	default:
		return "Pengiriman email gagal. Periksa konfigurasi SMTP dan hubungi administrator jika masalah berlanjut."
	}
}

func emailFailureClass(err error) string {
	var failure *mailSendFailure
	if !errors.As(err, &failure) || failure.cause == nil {
		return "no_cause"
	}
	var networkError net.Error
	if errors.As(failure.cause, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	var dnsError *net.DNSError
	if errors.As(failure.cause, &dnsError) {
		return "dns"
	}
	var smtpError *textproto.Error
	if errors.As(failure.cause, &smtpError) {
		return fmt.Sprintf("smtp_response_%d", smtpError.Code)
	}
	return fmt.Sprintf("cause_type_%T", failure.cause)
}

func (s *Server) sendMail(ctx context.Context, recipient, subject, body, htmlBody string) error {
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cfg, err := s.loadMailConfigContext(ctx)
	if err != nil {
		return mailFailure("MAIL_CONFIG_UNAVAILABLE", err)
	}
	if !cfg.Enabled {
		return mailFailure("MAIL_DISABLED", nil)
	}
	if !validSMTPHost(cfg.Host) || !validSMTPPort(cfg.Port) || !validEmail(cfg.FromEmail) || strings.ContainsAny(cfg.Host+cfg.FromEmail+cfg.Username, "\r\n") {
		return mailFailure("MAIL_CONFIG_INVALID", nil)
	}
	address, err := mail.ParseAddress(recipient)
	if err != nil || address.Address != recipient || !validEmail(address.Address) {
		return mailFailure("MAIL_CONFIG_INVALID", nil)
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	target, err := smtpTargetIP(ctx, cfg.Host, os.Getenv("SMTP_ALLOW_PRIVATE_NETWORK") == "true")
	if err != nil {
		switch {
		case errors.Is(err, errSMTPPrivateTarget):
			return mailFailure("MAIL_SMTP_PRIVATE_TARGET", err)
		case errors.Is(err, errSMTPDNS):
			return mailFailure("MAIL_SMTP_DNS_FAILED", err)
		default:
			return mailFailure("MAIL_CONFIG_INVALID", err)
		}
	}
	hostport := net.JoinHostPort(target.String(), strconv.Itoa(cfg.Port))
	conn, err := dialer.DialContext(ctx, "tcp", hostport)
	if err != nil {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return mailFailure("MAIL_SMTP_TIMEOUT", err)
		}
		return mailFailure("MAIL_SMTP_CONNECTION_FAILED", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			return mailFailure("MAIL_SMTP_CONNECTION_FAILED", err)
		}
	}
	if cfg.Secure {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return smtpFailure("MAIL_SMTP_TLS_FAILED", err)
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return smtpFailure("MAIL_SMTP_CONNECTION_FAILED", err)
	}
	defer client.Close()
	if !cfg.Secure {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return mailFailure("MAIL_SMTP_STARTTLS_UNAVAILABLE", nil)
		}
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return smtpFailure("MAIL_SMTP_TLS_FAILED", err)
		}
	}
	password := ""
	if cfg.PasswordCipher != "" {
		password, err = decryptMailPassword(cfg.PasswordCipher)
		if err != nil {
			if _, keyErr := hexKey(os.Getenv("MAIL_ENCRYPTION_KEY")); keyErr != nil {
				return mailFailure("MAIL_ENCRYPTION_KEY_MISSING", keyErr)
			}
			return mailFailure("MAIL_CREDENTIALS_UNAVAILABLE", err)
		}
	}
	if cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.Username, password, cfg.Host)); err != nil {
			return smtpFailure("MAIL_SMTP_AUTH_FAILED", err)
		}
	}
	message, err := buildEmailMessage(cfg, address.Address, subject, body, htmlBody)
	if err != nil {
		return mailFailure("MAIL_MESSAGE_INVALID", err)
	}
	if err = client.Mail(cfg.FromEmail); err != nil {
		return smtpFailure("MAIL_SMTP_SENDER_REJECTED", err)
	}
	if err = client.Rcpt(address.Address); err != nil {
		return smtpFailure("MAIL_SMTP_RECIPIENT_REJECTED", err)
	}
	writer, err := client.Data()
	if err != nil {
		return smtpFailure("MAIL_SMTP_MESSAGE_REJECTED", err)
	}
	if _, err = writer.Write(message); err != nil {
		_ = writer.Close()
		return smtpFailure("MAIL_SMTP_TRANSFER_FAILED", err)
	}
	if err = writer.Close(); err != nil {
		return smtpFailure("MAIL_SMTP_ACCEPTANCE_FAILED", err)
	}
	return nil
}

func buildEmailMessage(cfg mailConfig, recipient, subject, textBody, htmlBody string) ([]byte, error) {
	if htmlBody == "" {
		htmlBody = "<!doctype html><html><body><pre>" + htmltemplate.EscapeString(textBody) + "</pre></body></html>"
	}
	var content bytes.Buffer
	writer := multipart.NewWriter(&content)
	textPart, err := writer.CreatePart(map[string][]string{"Content-Type": {"text/plain; charset=UTF-8"}, "Content-Transfer-Encoding": {"8bit"}})
	if err != nil {
		return nil, err
	}
	if _, err = textPart.Write([]byte(textBody)); err != nil {
		return nil, err
	}
	htmlPart, err := writer.CreatePart(map[string][]string{"Content-Type": {"text/html; charset=UTF-8"}, "Content-Transfer-Encoding": {"8bit"}})
	if err != nil {
		return nil, err
	}
	if _, err = htmlPart.Write([]byte(htmlBody)); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	fromName := mime.QEncoding.Encode("UTF-8", sanitizeMailHeader(cfg.FromName))
	headers := "From: " + fromName + " <" + sanitizeMailHeader(cfg.FromEmail) + ">\r\n" +
		"To: " + sanitizeMailHeader(recipient) + "\r\n" +
		"Subject: " + encodeMailSubject(subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=\"" + writer.Boundary() + "\"\r\n\r\n"
	return append([]byte(headers), content.Bytes()...), nil
}

func sanitizeMailHeader(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value))
}

func encodeMailSubject(subject string) string {
	return mime.QEncoding.Encode("UTF-8", sanitizeMailHeader(subject))
}

func (s *Server) emailRetry(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	result, err := s.store.Exec(r.Context(), `UPDATE email_outbox SET status='RETRYING',available_at=?,claim_token=NULL,lease_until=NULL,updated_at=? WHERE status='FAILED' AND id IN (SELECT id FROM email_outbox WHERE status='FAILED' ORDER BY updated_at LIMIT 100)`, now(), now())
	if err != nil {
		writeError(w, 500, "Gagal mengantrekan ulang email", nil)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		writeError(w, 500, "Gagal menghitung email yang diantrekan ulang", nil)
		return
	}
	s.audit(r, u, "RETRY", "EmailOutbox", "failed", map[string]int64{"count": count})
	writeJSON(w, 200, map[string]any{"ok": true, "requeued": count, "message": "Email gagal dijadwalkan ulang; status Terkirim berarti diterima server SMTP, bukan bukti telah diterima kotak masuk."})
}

type queuedMail struct {
	id, claim, eventKey, recipient, subject, textBody, htmlBody string
	attempts                                                    int
}

func (s *Server) claimMail(ctx context.Context, limit int) ([]queuedMail, error) {
	token := newID()
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		stamp := now()
		leaseUntil := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano)
		_, err := tx.ExecContext(ctx, s.store.Q(`UPDATE email_outbox SET status='PROCESSING',claim_token=?,lease_until=?,last_attempt_at=?,attempts=attempts+1,updated_at=? WHERE id IN (SELECT id FROM email_outbox WHERE ((status IN ('PENDING','RETRYING') AND available_at<=?) OR (status IN ('PROCESSING','SENDING') AND (lease_until IS NULL OR lease_until<=?))) ORDER BY available_at,created_at LIMIT ?) AND ((status IN ('PENDING','RETRYING') AND available_at<=?) OR (status IN ('PROCESSING','SENDING') AND (lease_until IS NULL OR lease_until<=?)))`), token, leaseUntil, stamp, stamp, stamp, stamp, limit, stamp, stamp)
		return err
	})
	if err != nil {
		return nil, err
	}
	rows, err := s.store.DB().QueryContext(ctx, s.store.Q(`SELECT id,claim_token,event_key,recipient,subject,text_body,html_body,attempts FROM email_outbox WHERE claim_token=? AND status='PROCESSING' ORDER BY created_at`), token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []queuedMail{}
	for rows.Next() {
		var item queuedMail
		if err := rows.Scan(&item.id, &item.claim, &item.eventKey, &item.recipient, &item.subject, &item.textBody, &item.htmlBody, &item.attempts); err != nil {
			return nil, err
		}
		messages = append(messages, item)
	}
	return messages, rows.Err()
}

func (s *Server) dispatchOutbox(ctx context.Context, limit int) (map[string]int, error) {
	messages, err := s.claimMail(ctx, limit)
	if err != nil {
		return nil, err
	}
	sent, failed := 0, 0
	for _, item := range messages {
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.sendMail(sendCtx, item.recipient, item.subject, item.textBody, item.htmlBody)
		cancel()
		if err != nil {
			status, available := "RETRYING", time.Now().UTC().Add(time.Duration(1<<min(item.attempts-1, 8))*time.Minute).Format(time.RFC3339Nano)
			if item.attempts >= 8 {
				status = "FAILED"
			}
			_, updateErr := s.store.Exec(ctx, `UPDATE email_outbox SET status=?,available_at=?,last_error=?,claim_token=NULL,lease_until=NULL,updated_at=? WHERE id=? AND claim_token=?`, status, available, truncate(err.Error(), 120), now(), item.id, item.claim)
			if updateErr != nil {
				return map[string]int{"sent": sent, "failed": failed}, updateErr
			}
			if status == "FAILED" && !strings.HasPrefix(item.eventKey, "systemAlert:") {
				if err := s.enqueueSystemFailureAlert(ctx, item.id); err != nil {
					log.Printf("failed to enqueue email failure alert: %v", err)
				}
			}
			failed++
		} else {
			_, err = s.store.Exec(ctx, `UPDATE email_outbox SET status='SENT',sent_at=?,last_error=NULL,claim_token=NULL,lease_until=NULL,updated_at=? WHERE id=? AND claim_token=?`, now(), now(), item.id, item.claim)
			if err != nil {
				return map[string]int{"sent": sent, "failed": failed}, err
			}
			sent++
		}
	}
	return map[string]int{"sent": sent, "failed": failed}, nil
}

func (s *Server) enqueueSystemFailureAlert(ctx context.Context, failedID string) error {
	cfg, err := s.loadMailConfigContext(ctx)
	if err != nil {
		return err
	}
	if cfg.AdminEmail == "" {
		return nil
	}
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		return s.enqueueEventTx(ctx, tx, cfg, "systemAlert", "failed:"+failedID, "Email gagal", failedID, []string{cfg.AdminEmail})
	})
}

func queueEmailTx(ctx context.Context, tx *sql.Tx, s *Server, event, instanceID, recipient, subject, body string) error {
	address, err := mail.ParseAddress(recipient)
	if err != nil || address.Address != recipient || !validEmail(recipient) {
		return fmt.Errorf("invalid notification recipient")
	}
	htmlBody := "<!doctype html><html><body><pre>" + htmltemplate.EscapeString(body) + "</pre></body></html>"
	eventKey := event + ":" + instanceID + ":" + strings.ToLower(recipient)
	_, err = tx.ExecContext(ctx, s.store.Q(`INSERT INTO email_outbox(id,event_key,recipient,subject,text_body,html_body,status,available_at,created_at,updated_at) VALUES(?,?,?,?,?,?, 'PENDING',?,?,?) ON CONFLICT(event_key) DO NOTHING`), newID(), eventKey, recipient, subject, body, htmlBody, now(), now(), now())
	return err
}

func (s *Server) enqueueEventTx(ctx context.Context, tx *sql.Tx, cfg mailConfig, event, instanceID, title, meetingNumber string, recipients []string) error {
	return s.enqueueTemplateTx(ctx, tx, cfg, event, instanceID, title, meetingNumber, recipients, nil)
}

func (s *Server) enqueueTemplateTx(ctx context.Context, tx *sql.Tx, cfg mailConfig, event, instanceID, title, meetingNumber string, recipients []string, values map[string]string) error {
	if !cfg.Enabled || !cfg.Events[event] {
		return nil
	}
	template := cfg.Templates[event]
	subject := strings.ReplaceAll(strings.ReplaceAll(template.Subject, "{{title}}", title), "{{meetingTitle}}", title)
	body := strings.ReplaceAll(strings.ReplaceAll(template.Body, "{{title}}", title), "{{meetingTitle}}", title)
	subject = strings.ReplaceAll(subject, "{{meetingNumber}}", meetingNumber)
	body = strings.ReplaceAll(body, "{{meetingNumber}}", meetingNumber)
	if values == nil {
		values = map[string]string{}
	}
	if _, exists := values["portalUrl"]; !exists {
		values["portalUrl"] = publicPortalURL()
	}
	for key, value := range values {
		subject = strings.ReplaceAll(subject, "{{"+key+"}}", value)
		body = strings.ReplaceAll(body, "{{"+key+"}}", value)
	}
	subject = strings.ReplaceAll(subject, "{{status}}", "")
	body = strings.ReplaceAll(body, "{{status}}", "")
	for _, key := range []string{"reason", "meetingDetails", "consumption", "missingEvidence", "cancellationNotice", "portalUrl"} {
		subject = strings.ReplaceAll(subject, "{{"+key+"}}", "")
		body = strings.ReplaceAll(body, "{{"+key+"}}", "")
	}
	seen := map[string]bool{}
	for _, recipient := range recipients {
		recipient = strings.ToLower(strings.TrimSpace(recipient))
		if !validEmail(recipient) || seen[recipient] {
			continue
		}
		seen[recipient] = true
		if err := queueEmailTx(ctx, tx, s, event, instanceID, recipient, subject, body); err != nil {
			return err
		}
	}
	return nil
}

func publicPortalURL() string {
	raw := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_APP_URL")), "/")
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	local := parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1"
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && local) {
		return ""
	}
	return raw
}

func renderTemplate(template, title, meetingNumber string, missing ...string) string {
	template = strings.ReplaceAll(template, "{{title}}", title)
	template = strings.ReplaceAll(template, "{{meetingTitle}}", title)
	template = strings.ReplaceAll(template, "{{meetingNumber}}", meetingNumber)
	template = strings.ReplaceAll(template, "{{status}}", "")
	if len(missing) > 0 {
		template = strings.ReplaceAll(template, "{{missingEvidence}}", missing[0])
	}
	return template
}

func (s *Server) reportsQuery(r *http.Request) ([]map[string]any, string, error) {
	month := r.URL.Query().Get("month")
	from, to, err := parseMonth(month)
	if err != nil {
		return nil, month, err
	}
	query := `SELECT m.id,m.meeting_number,m.title,m.description,m.start_at,m.end_at,m.status,m.method,m.guest_category,m.expected_participants,m.notes,d.name,coalesce(r.name,oroom.name,''),m.department_id,m.confidential FROM meetings m JOIN departments d ON d.id=m.department_id LEFT JOIN rooms r ON r.id=m.room_id LEFT JOIN rooms oroom ON oroom.id=m.online_resource_id WHERE m.status IN ('SUBMITTED','APPROVED','COMPLETED') AND m.start_at>=? AND m.start_at<?`
	args := []any{from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano)}
	if dep := r.URL.Query().Get("departmentId"); dep != "" {
		query += ` AND m.department_id=?`
		args = append(args, dep)
	}
	if method := r.URL.Query().Get("method"); method != "" {
		if !validEnum(method, "OFFLINE", "ONLINE", "HYBRID") {
			return nil, month, fmt.Errorf("invalid method")
		}
		query += ` AND m.method=?`
		args = append(args, method)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		query += ` AND lower(m.title) LIKE ?`
		args = append(args, "%"+strings.ToLower(q)+"%")
	}
	if u, ok := userFrom(r); ok && !s.meetingManager(r, u) {
		query += ` AND (m.confidential=0 OR m.organizer_id=? OR EXISTS(SELECT 1 FROM attendance a WHERE a.meeting_id=m.id AND a.user_id=?))`
		args = append(args, u.ID, u.ID)
	}
	query += ` ORDER BY m.start_at LIMIT 10000`
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(query), args...)
	if err != nil {
		return nil, month, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, number, title, start, end, status, method, guest, deptName, location, deptID string
		var description, notes sql.NullString
		var participants, confidential int
		if err := rows.Scan(&id, &number, &title, &description, &start, &end, &status, &method, &guest, &participants, &notes, &deptName, &location, &deptID, &confidential); err != nil {
			return nil, month, err
		}
		_ = confidential
		out = append(out, map[string]any{"id": id, "meetingNumber": number, "title": title, "description": nullableOut(description), "startAt": start, "endAt": end, "status": status, "method": method, "guestCategory": guest, "expectedParticipants": participants, "notes": nullableOut(notes), "department": map[string]string{"id": deptID, "name": deptName}, "locationName": location, "confidential": confidential != 0})
	}
	if err := rows.Err(); err != nil {
		return nil, month, err
	}
	return out, month, nil
}

func (s *Server) reportsRows(r *http.Request) ([]map[string]any, string, error) {
	rows, month, err := s.reportsQuery(r)
	if err != nil {
		return nil, month, err
	}
	for _, row := range rows {
		id := row["id"].(string)
		docs := []string{}
		docRows, e := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT document_type FROM documents WHERE meeting_id=?`), id)
		if e != nil {
			return nil, month, e
		}
		for docRows.Next() {
			var typ string
			if err := docRows.Scan(&typ); err != nil {
				docRows.Close()
				return nil, month, err
			}
			docs = append(docs, typ)
		}
		if err := docRows.Err(); err != nil {
			docRows.Close()
			return nil, month, err
		}
		docRows.Close()
		foodRows, e := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT item_type,quantity FROM meeting_consumptions WHERE meeting_id=?`), id)
		if e != nil {
			return nil, month, e
		}
		food := []map[string]any{}
		for foodRows.Next() {
			var typ string
			var qty int
			if err := foodRows.Scan(&typ, &qty); err != nil {
				foodRows.Close()
				return nil, month, err
			}
			food = append(food, map[string]any{"itemType": typ, "quantity": qty})
		}
		if err := foodRows.Err(); err != nil {
			foodRows.Close()
			return nil, month, err
		}
		foodRows.Close()
		row["documents"] = docs
		row["consumptions"] = food
	}
	return rows, month, nil
}

func sortedCounts(counts map[string]int) map[string]int {
	keys := []string{}
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]int{}
	for _, k := range keys {
		out[k] = counts[k]
	}
	return out
}
