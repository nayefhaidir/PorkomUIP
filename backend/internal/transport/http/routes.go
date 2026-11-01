package httpapi

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func (s *Server) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", s.health)
	m.HandleFunc("POST /api/auth/login", s.login)
	m.HandleFunc("POST /api/auth/logout", s.logout)
	m.HandleFunc("GET /api/auth/me", s.me)
	m.HandleFunc("PATCH /api/auth/me", s.patchMe)
	m.HandleFunc("GET /api/meetings", s.meetingsList)
	m.HandleFunc("GET /api/management", s.managementOverview)
	m.HandleFunc("POST /api/meetings", s.meetingsCreate)
	m.HandleFunc("GET /api/meetings/{id}", s.meetingGet)
	m.HandleFunc("PATCH /api/meetings/{id}", s.meetingPatch)
	m.HandleFunc("DELETE /api/meetings/{id}", s.meetingDelete)
	m.HandleFunc("POST /api/meetings/{id}/workflow", s.meetingWorkflow)
	m.HandleFunc("POST /api/meetings/{id}/documents", s.meetingDocumentUpload)
	m.HandleFunc("GET /api/documents/{id}", s.documentDownload)
	m.HandleFunc("PATCH /api/documents/{id}/verification", s.documentVerify)
	m.HandleFunc("GET /api/availability", s.availability)
	m.HandleFunc("GET /api/public/schedule", s.publicSchedule)
	m.HandleFunc("GET /api/departments", s.departmentsList)
	m.HandleFunc("POST /api/departments", s.departmentsCreate)
	m.HandleFunc("PATCH /api/departments/{id}", s.departmentPatch)
	m.HandleFunc("DELETE /api/departments/{id}", s.departmentDelete)
	m.HandleFunc("GET /api/rooms", s.roomsList)
	m.HandleFunc("POST /api/rooms", s.roomsCreate)
	m.HandleFunc("PATCH /api/rooms/{id}", s.roomPatch)
	m.HandleFunc("DELETE /api/rooms/{id}", s.roomDelete)
	m.HandleFunc("GET /api/users", s.usersList)
	m.HandleFunc("POST /api/users", s.usersCreate)
	m.HandleFunc("PATCH /api/users/{id}", s.userPatch)
	m.HandleFunc("DELETE /api/users/{id}", s.userDelete)
	m.HandleFunc("GET /api/announcements", s.announcementsList)
	m.HandleFunc("POST /api/announcements", s.announcementsCreate)
	m.HandleFunc("PATCH /api/announcements/{id}", s.announcementPatch)
	m.HandleFunc("DELETE /api/announcements/{id}", s.announcementDelete)
	m.HandleFunc("GET /api/announcements/{id}/image", s.announcementImage)
	m.HandleFunc("GET /api/notifications", s.notificationsList)
	m.HandleFunc("PATCH /api/notifications", s.notificationsMark)
	m.HandleFunc("DELETE /api/notifications", s.notificationsDelete)
	m.HandleFunc("GET /api/audit-logs", s.auditLogs)
	m.HandleFunc("DELETE /api/audit-logs", s.auditDelete)
	m.HandleFunc("GET /api/settings", s.settingsGet)
	m.HandleFunc("PATCH /api/settings", s.settingsPatch)
	m.HandleFunc("GET /api/email/config", s.emailConfigGet)
	m.HandleFunc("PATCH /api/email/config", s.emailConfigPatch)
	m.HandleFunc("POST /api/email/test", s.emailTest)
	m.HandleFunc("POST /api/email/retry", s.emailRetry)
	m.HandleFunc("GET /api/email/outbox", s.emailOutboxList)
	m.HandleFunc("GET /api/reports/monthly", s.monthlyReport)
	m.HandleFunc("GET /api/reports/monthly/export", s.monthlyExport)
	m.HandleFunc("GET /api/backup", s.backupDownload)
	m.HandleFunc("POST /api/backup", s.backupRestore)
	m.HandleFunc("OPTIONS /{path...}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return m
}

func storageDir() string {
	if v := os.Getenv("UPLOAD_DIR"); v != "" {
		return v
	}
	return "uploads"
}

func secureSessionCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]))
	if proto == "http" {
		return false
	}
	return proto == "https" || os.Getenv("NODE_ENV") == "production"
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB().PingContext(r.Context()); err != nil {
		writeError(w, 503, "database unavailable", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "database": s.store.Driver()})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if decodeJSON(w, r, &body) != nil || body.Email == "" || body.Password == "" {
		writeError(w, 400, "Data login tidak valid", nil)
		return
	}
	var user User
	var hash string
	ip := clientAddress(r.RemoteAddr)
	lookupErr := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT id,name,email,role,job_title,department_id,page_access,active,password_hash FROM users WHERE lower(email)=lower(?)`), strings.TrimSpace(body.Email)).Scan(&user.ID, &user.Name, &user.Email, &user.Role, &user.JobTitle, &user.DepartmentID, newJSONList(&user.PageAccess), &user.Active, &hash)
	if lookupErr != nil && lookupErr != sql.ErrNoRows {
		writeError(w, 500, "Gagal memeriksa akun", nil)
		return
	}
	throttleKey := "ip:" + ip
	if lookupErr == nil && user.Active {
		throttleKey = "account:" + user.ID
	}
	s.loginMu.Lock()
	if s.loginFailed == nil {
		s.loginFailed = map[string]loginFailure{}
	}
	for address, entry := range s.loginFailed {
		if !entry.resetAt.After(time.Now()) {
			delete(s.loginFailed, address)
		}
	}
	failure := s.loginFailed[throttleKey]
	if failure.resetAt.After(time.Now()) && failure.count >= 10 {
		retry := int(time.Until(failure.resetAt).Seconds())
		s.loginMu.Unlock()
		w.Header().Set("Retry-After", fmt.Sprint(retry))
		writeError(w, 429, "Terlalu banyak percobaan masuk. Silakan coba lagi dalam 15 menit.", nil)
		return
	}
	s.loginMu.Unlock()
	if lookupErr != nil || !user.Active || !checkPassword(hash, body.Password) {
		s.loginMu.Lock()
		failure = s.loginFailed[throttleKey]
		if !failure.resetAt.After(time.Now()) {
			failure = loginFailure{resetAt: time.Now().Add(15 * time.Minute)}
		}
		failure.count++
		if len(s.loginFailed) >= 10000 {
			oldestAddress := ""
			var oldest time.Time
			for address, entry := range s.loginFailed {
				if oldestAddress == "" || entry.resetAt.Before(oldest) {
					oldestAddress, oldest = address, entry.resetAt
				}
			}
			if oldestAddress != "" && oldestAddress != throttleKey {
				delete(s.loginFailed, oldestAddress)
			}
		}
		s.loginFailed[throttleKey] = failure
		s.loginMu.Unlock()
		writeError(w, 401, "Email atau password salah atau akun nonaktif", nil)
		return
	}
	s.loginMu.Lock()
	delete(s.loginFailed, throttleKey)
	s.loginMu.Unlock()
	sid := newID()
	expires := time.Now().Add(12 * time.Hour)
	token, err := issueJWT(sid, user.ID, expires)
	if err != nil {
		writeError(w, 500, err.Error(), nil)
		return
	}
	_, err = s.store.Exec(r.Context(), `INSERT INTO sessions(id,user_id,token_hash,expires_at,created_at) VALUES(?,?,?,?,?)`, sid, user.ID, tokenHash(token), expires.UTC().Format(time.RFC3339Nano), now())
	if err != nil {
		writeError(w, 500, "Gagal membuat sesi", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: secureSessionCookie(r), SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds())})
	writeJSON(w, 200, map[string]any{"id": user.ID, "name": user.Name, "email": user.Email, "role": user.Role})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		sid, _, _ := parseJWT(c.Value)
		_, _ = s.store.Exec(r.Context(), `UPDATE sessions SET revoked_at=? WHERE id=?`, now(), sid)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: secureSessionCookie(r), SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func clientAddress(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok {
		writeError(w, 401, "Unauthenticated", nil)
		return
	}
	if u.DepartmentID != nil {
		var id, name, short, color string
		err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT id,name,short_name,color FROM departments WHERE id=?`), *u.DepartmentID).Scan(&id, &name, &short, &color)
		if err == nil {
			u.Department = map[string]any{"id": id, "name": name, "shortName": short, "color": color}
		}
	}
	writeJSON(w, 200, u)
}

func (s *Server) patchMe(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	body, valid := decodeMap(w, r)
	if !valid {
		return
	}
	name, hasName := body["name"].(string)
	if hasName {
		name = strings.TrimSpace(name)
		if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 100 {
			writeError(w, 400, "Perubahan profil tidak valid", nil)
			return
		}
	}
	currentPassword, _ := body["currentPassword"].(string)
	newPassword, _ := body["newPassword"].(string)
	if len(newPassword) > 128 || (newPassword != "" && (len(newPassword) < 12 || currentPassword == "")) || (!hasName && newPassword == "") {
		writeError(w, 400, "Perubahan profil tidak valid", nil)
		return
	}

	var passwordHash string
	if newPassword != "" {
		if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT password_hash FROM users WHERE id=?`), u.ID).Scan(&passwordHash); err != nil {
			writeError(w, 500, "Gagal memuat profil", nil)
			return
		}
		if !checkPassword(passwordHash, currentPassword) {
			writeError(w, 403, "Kata sandi saat ini salah", nil)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
		if err != nil {
			writeError(w, 500, "Gagal memperbarui kata sandi", nil)
			return
		}
		passwordHash = string(hash)
	}

	err := s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		switch {
		case hasName && newPassword != "":
			_, err := tx.ExecContext(r.Context(), s.store.Q(`UPDATE users SET name=?,password_hash=?,updated_at=? WHERE id=?`), name, passwordHash, now(), u.ID)
			if err != nil {
				return err
			}
		case hasName:
			_, err := tx.ExecContext(r.Context(), s.store.Q(`UPDATE users SET name=?,updated_at=? WHERE id=?`), name, now(), u.ID)
			if err != nil {
				return err
			}
		default:
			_, err := tx.ExecContext(r.Context(), s.store.Q(`UPDATE users SET password_hash=?,updated_at=? WHERE id=?`), passwordHash, now(), u.ID)
			if err != nil {
				return err
			}
		}
		if newPassword != "" {
			cookie, err := r.Cookie(cookieName)
			if err != nil {
				return err
			}
			sessionID, _, err := parseJWT(cookie.Value)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(r.Context(), s.store.Q(`UPDATE sessions SET revoked_at=? WHERE user_id=? AND id<>? AND revoked_at IS NULL`), now(), u.ID, sessionID)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, 500, "Gagal memperbarui profil", nil)
		return
	}
	if hasName {
		u.Name = name
	}
	s.audit(r, u, "UPDATE_PROFILE", "User", u.ID, nil)
	var department any
	if u.DepartmentID != nil {
		var value string
		if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT name FROM departments WHERE id=?`), *u.DepartmentID).Scan(&value); err == nil {
			department = value
		}
	}
	writeJSON(w, 200, map[string]any{"id": u.ID, "name": u.Name, "email": u.Email, "role": u.Role, "department": department})
}

func (s *Server) backupDownload(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u.Role != "ADMIN" {
		authError(w, ok)
		return
	}
	s.backupGet(w, r, u)
}

func (s *Server) backupRestore(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u.Role != "ADMIN" {
		authError(w, ok)
		return
	}
	s.backupPost(w, r, u)
}
