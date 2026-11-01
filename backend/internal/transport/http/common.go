package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string, details any) {
	out := map[string]any{"error": message}
	if details != nil {
		out["details"] = details
	}
	writeJSON(w, status, out)
}

func authError(w http.ResponseWriter, authenticated bool) {
	if !authenticated {
		writeError(w, 401, "Unauthenticated", nil)
	} else {
		writeError(w, 403, "Forbidden", nil)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("body must contain one JSON value")
	}
	return nil
}

func requireUser(w http.ResponseWriter, r *http.Request, roles ...string) (User, bool) {
	u, ok := userFrom(r)
	if !ok {
		authError(w, false)
		return User{}, false
	}
	if len(roles) > 0 {
		for _, role := range roles {
			if u.Role == role {
				return u, true
			}
		}
		authError(w, true)
		return User{}, false
	}
	return u, true
}

func (s *Server) audit(r *http.Request, u User, action, entity, entityID string, details any) {
	raw, _ := json.Marshal(details)
	var userID any
	if u.ID != "" {
		userID = u.ID
	}
	_, _ = s.store.Exec(r.Context(), `INSERT INTO audit_logs(id,user_id,action,entity,entity_id,details,created_at) VALUES(?,?,?,?,?,?,?)`, newID(), userID, action, entity, entityID, nullable(string(raw)), now())
}

func (s *Server) notifyAllActive(r *http.Request, excludedUserID, title, body string) {
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT id FROM users WHERE active=1 AND id<>?`), excludedUserID)
	if err != nil {
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_, _ = s.store.Exec(r.Context(), `INSERT INTO notifications(id,user_id,title,body,created_at) VALUES(?,?,?,?,?)`, newID(), id, title, body, now())
	}
}

func asString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func asBool(m map[string]any, key string, fallback bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return fallback
}

func asInt(m map[string]any, key string, fallback int) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return fallback
	}
}

func jsonValue(value any, fallback string) string {
	if value == nil {
		return fallback
	}
	b, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(b)
}

func decodeMap(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil || body == nil {
		writeError(w, 400, "Invalid JSON request", nil)
		return nil, false
	}
	return body, true
}

func sqlBool(value int) bool { return value != 0 }

func validEnum(v string, options ...string) bool {
	for _, option := range options {
		if v == option {
			return true
		}
	}
	return false
}

func pageParams(r *http.Request) (page, size, offset int) {
	page, size = 1, 50
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("pageSize")); err == nil && n > 0 {
		size = n
	}
	if size > 200 {
		size = 200
	}
	return page, size, (page - 1) * size
}

func validDateRange(from, to string) (time.Time, time.Time, error) {
	start, e1 := time.Parse(time.RFC3339, from)
	end, e2 := time.Parse(time.RFC3339, to)
	if e1 != nil || e2 != nil || !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid date range")
	}
	return start, end, nil
}

func wantsRole(u User, role string) bool { return u.Role == "ADMIN" || u.Role == role }

func containsPage(u User, page string, fallback bool) bool {
	if u.Role == "ADMIN" {
		return true
	}
	for _, p := range u.PageAccess {
		if p == "__custom__" {
			for _, allowed := range u.PageAccess {
				if allowed == page {
					return true
				}
			}
			return false
		}
	}
	return fallback
}

func jsonString(value any) string {
	if v, ok := value.(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
