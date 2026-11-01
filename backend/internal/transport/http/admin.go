package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func (s *Server) departmentsList(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	rows, err := s.store.DB().QueryContext(r.Context(), `SELECT d.id,d.name,d.short_name,d.color,(SELECT count(*) FROM users u WHERE u.department_id=d.id),(SELECT count(*) FROM meetings m WHERE m.department_id=d.id) FROM departments d ORDER BY d.name`)
	if err != nil {
		writeError(w, 500, "Gagal memuat bidang", nil)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, name, short, color string
		var users, meetings int
		if rows.Scan(&id, &name, &short, &color, &users, &meetings) == nil {
			items = append(items, map[string]any{"id": id, "name": name, "shortName": short, "color": color, "_count": map[string]int{"users": users, "meetings": meetings}})
		}
	}
	writeJSON(w, 200, items)
}

func (s *Server) departmentsCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	name, short, color := strings.TrimSpace(asString(b, "name")), strings.ToUpper(strings.TrimSpace(asString(b, "shortName"))), strings.ToUpper(asString(b, "color"))
	if len(name) < 2 || len(name) > 100 || len(short) < 2 || len(short) > 12 || !validHexColor(color) {
		writeError(w, 400, "Nama, singkatan, atau warna bidang tidak valid", nil)
		return
	}
	var exists int
	_ = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM departments WHERE upper(color)=?`), color).Scan(&exists)
	if exists > 0 {
		writeError(w, 409, "Warna tersebut sudah digunakan bidang lain. Pilih warna yang berbeda.", nil)
		return
	}
	id := newID()
	_, err := s.store.Exec(r.Context(), `INSERT INTO departments(id,name,short_name,color) VALUES(?,?,?,?)`, id, name, short, color)
	if err != nil {
		writeError(w, 409, "Nama atau singkatan bidang sudah digunakan", nil)
		return
	}
	s.audit(r, u, "CREATE", "Department", id, map[string]any{"color": color, "shortName": short})
	writeJSON(w, 201, map[string]any{"id": id, "name": name, "shortName": short, "color": color})
}

func (s *Server) departmentPatch(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	id := r.PathValue("id")
	var name, short, color string
	if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT name,short_name,color FROM departments WHERE id=?`), id).Scan(&name, &short, &color); err != nil {
		writeError(w, 404, "Bidang tidak ditemukan", nil)
		return
	}
	if v := asString(b, "name"); v != "" {
		name = strings.TrimSpace(v)
	}
	if v := asString(b, "shortName"); v != "" {
		short = strings.ToUpper(strings.TrimSpace(v))
	}
	if v := asString(b, "color"); v != "" {
		color = strings.ToUpper(v)
	}
	if len(name) < 2 || len(name) > 100 || len(short) < 2 || len(short) > 12 || !validHexColor(color) {
		writeError(w, 400, "Nama, singkatan, atau warna bidang tidak valid", nil)
		return
	}
	var duplicateColor int
	_ = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM departments WHERE id<>? AND upper(color)=?`), id, color).Scan(&duplicateColor)
	if duplicateColor > 0 {
		writeError(w, 409, "Warna tersebut sudah digunakan bidang lain. Pilih warna yang berbeda.", nil)
		return
	}
	if _, err := s.store.Exec(r.Context(), `UPDATE departments SET name=?,short_name=?,color=? WHERE id=?`, name, short, color, id); err != nil {
		writeError(w, 409, "Nama atau singkatan bidang sudah digunakan", nil)
		return
	}

	s.audit(r, u, "UPDATE", "Department", id, b)
	writeJSON(w, 200, map[string]any{"id": id, "name": name, "shortName": short, "color": color})
}

func (s *Server) departmentDelete(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	id := r.PathValue("id")
	result, err := s.store.Exec(r.Context(), `DELETE FROM departments WHERE id=? AND NOT EXISTS(SELECT 1 FROM users WHERE department_id=?) AND NOT EXISTS(SELECT 1 FROM meetings WHERE department_id=?)`, id, id, id)
	if err != nil {
		writeError(w, 409, "Bidang masih digunakan oleh pengguna atau rapat", nil)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		writeError(w, 409, "Bidang tidak ditemukan atau masih digunakan", nil)
		return
	}
	s.audit(r, u, "DELETE", "Department", id, nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) roomsList(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	page, size, offset := pageParams(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := r.URL.Query().Get("kind")
	activeOnly := r.URL.Query().Get("active") != "false"
	where := []string{"1=1"}
	args := []any{}
	if q != "" {
		where = append(where, "(lower(name) LIKE ? OR lower(coalesce(location,'')) LIKE ?)")
		args = append(args, "%"+strings.ToLower(q)+"%", "%"+strings.ToLower(q)+"%")
	}
	if validEnum(kind, "PHYSICAL", "ONLINE") {
		where = append(where, "kind=?")
		args = append(args, kind)
	}
	if activeOnly {
		where = append(where, "active=1")
	}
	clause := strings.Join(where, " AND ")
	var total int
	_ = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM rooms WHERE `+clause), args...).Scan(&total)
	queryArgs := append(append([]any{}, args...), size, offset)
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT id,name,description,kind,online_platform,capacity,location,facilities,breakout_rooms,image_url,active,created_at,updated_at FROM rooms WHERE `+clause+` ORDER BY active DESC,name LIMIT ? OFFSET ?`), queryArgs...)
	if err != nil {
		writeError(w, 500, "Gagal memuat ruangan", nil)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, name, kind, facilities, created, updated string
		var description, platform, location, image sql.NullString
		var capacity, breakout, active int
		if rows.Scan(&id, &name, &description, &kind, &platform, &capacity, &location, &facilities, &breakout, &image, &active, &created, &updated) != nil {
			continue
		}
		var features []string
		_ = json.Unmarshal([]byte(facilities), &features)
		items = append(items, map[string]any{"id": id, "name": name, "description": nullableOut(description), "kind": kind, "onlinePlatform": nullableOut(platform), "capacity": capacity, "location": nullableOut(location), "facilities": features, "breakoutRooms": breakout, "imageUrl": nullableOut(image), "active": active != 0, "createdAt": created, "updatedAt": updated, "zoomBreakoutRooms": []any{}})
	}
	rows.Close()
	for _, raw := range items {
		room := raw.(map[string]any)
		if room["kind"] != "ONLINE" || room["onlinePlatform"] != "ZOOM" {
			continue
		}
		rooms, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT id,name,capacity,active FROM zoom_breakout_rooms WHERE resource_id=? AND active=1 ORDER BY name`), room["id"])
		if err != nil {
			continue
		}
		slots := []any{}
		for rooms.Next() {
			var id, name string
			var capacity, active int
			if rooms.Scan(&id, &name, &capacity, &active) == nil {
				slots = append(slots, map[string]any{"id": id, "name": name, "capacity": capacity, "active": active != 0})
			}
		}
		rooms.Close()
		room["zoomBreakoutRooms"] = slots
	}
	writeJSON(w, 200, map[string]any{"items": items, "page": page, "pageSize": size, "total": total, "pages": (total + size - 1) / size})
	_ = u
}

func nullableOut(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func (s *Server) roomsCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	name, kind := strings.TrimSpace(asString(b, "name")), asString(b, "kind")
	capacity := asInt(b, "capacity", 0)
	platform := asString(b, "onlinePlatform")
	if len(name) < 2 || capacity < 1 || capacity > 1000 || !validEnum(kind, "PHYSICAL", "ONLINE") || (kind == "ONLINE" && !validEnum(platform, "ZOOM", "TEAMS")) || (kind == "PHYSICAL" && platform != "") {
		writeError(w, 400, "Input ruangan tidak valid", nil)
		return
	}
	breakout := asInt(b, "breakoutRooms", 0)
	limit := 0
	if platform == "ZOOM" {
		limit = 100
	}
	if breakout < 0 || breakout > limit || (kind == "PHYSICAL" && breakout > 0) {
		if platform == "TEAMS" && breakout > 0 {
			writeError(w, 400, "Microsoft Teams tidak mendukung breakout room pada portal ini", nil)
		} else {
			writeError(w, 400, "Jumlah breakout room tidak valid", nil)
		}
		return
	}
	id, stamp := newID(), now()
	names := stringArray(b["breakoutRoomNames"])
	if len(names) > 0 && (platform != "ZOOM" || len(names) != breakout || hasDuplicateNames(names)) {
		writeError(w, 400, "Nama breakout harus unik dan jumlahnya harus sesuai jumlah slot", nil)
		return
	}
	if platform == "ZOOM" && len(names) == 0 {
		names = defaultSlotNames(breakout)
	}
	err := s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO rooms(id,name,description,kind,online_platform,capacity,location,facilities,breakout_rooms,image_url,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`), id, name, nullable(asString(b, "description")), kind, nullable(platform), capacity, nullable(asString(b, "location")), jsonValue(b["facilities"], "[]"), breakout, nullable(asString(b, "imageUrl")), boolInt(asBool(b, "active", true)), stamp, stamp)
		if err != nil {
			return err
		}
		if platform == "ZOOM" {
			return syncZoomBreakoutSlots(r, tx, s.store, id, capacity, names)
		}
		return nil
	})
	if err != nil {
		writeError(w, 409, "Ruangan gagal dibuat", nil)
		return
	}
	s.audit(r, u, "CREATE", "Room", id, nil)
	slots := roomSlots(r, s, id)
	writeJSON(w, 201, map[string]any{"id": id, "name": name, "kind": kind, "onlinePlatform": nullable(platform), "capacity": capacity, "location": nullable(asString(b, "location")), "facilities": b["facilities"], "breakoutRooms": breakout, "active": asBool(b, "active", true), "zoomBreakoutRooms": slots})
}

func (s *Server) roomPatch(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	id := r.PathValue("id")
	var name, kind, facilities string
	var description, platform, location, image sql.NullString
	var capacity, breakout, active int
	err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT name,kind,description,online_platform,capacity,location,facilities,breakout_rooms,image_url,active FROM rooms WHERE id=?`), id).Scan(&name, &kind, &description, &platform, &capacity, &location, &facilities, &breakout, &image, &active)
	if err != nil {
		writeError(w, 404, "Ruangan tidak ditemukan", nil)
		return
	}
	originalPlatform := platform
	if v, ok := b["name"].(string); ok {
		name = strings.TrimSpace(v)
	}
	if v, ok := b["capacity"].(float64); ok {
		capacity = int(v)
	}
	if v, ok := b["onlinePlatform"]; ok {
		platform = sql.NullString{String: jsonString(v), Valid: v != nil && jsonString(v) != ""}
	}
	if v, ok := b["location"]; ok {
		location = sql.NullString{String: jsonString(v), Valid: v != nil}
	}
	if v, ok := b["description"]; ok {
		description = sql.NullString{String: jsonString(v), Valid: v != nil}
	}
	if v, ok := b["imageUrl"]; ok {
		image = sql.NullString{String: jsonString(v), Valid: v != nil}
	}
	if v, ok := b["facilities"]; ok {
		facilities = jsonValue(v, "[]")
	}
	if v, ok := b["breakoutRooms"].(float64); ok {
		breakout = int(v)
	}
	if v, ok := b["active"].(bool); ok {
		active = boolInt(v)
	}
	p := ""
	if platform.Valid {
		p = platform.String
	}
	if p == "TEAMS" {
		if _, requested := b["breakoutRooms"]; requested && breakout > 0 {
			writeError(w, 400, "Microsoft Teams tidak mendukung breakout room pada portal ini", nil)
			return
		}
		breakout = 0
	}
	limit := 0
	if p == "ZOOM" {
		limit = 100
	}
	if capacity < 1 || capacity > 1000 || breakout < 0 || breakout > limit || (kind == "ONLINE" && !validEnum(p, "ZOOM", "TEAMS")) {
		if p == "TEAMS" && breakout > 0 {
			writeError(w, 400, "Microsoft Teams tidak mendukung breakout room pada portal ini", nil)
		} else {
			writeError(w, 400, "Input ruangan tidak valid", nil)
		}
		return
	}
	if kind == "ONLINE" && originalPlatform.Valid && platform.Valid && originalPlatform.String != platform.String {
		var meetings int
		_ = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM meetings WHERE online_resource_id=?`), id).Scan(&meetings)
		if meetings > 0 {
			writeError(w, 409, "Platform tidak dapat diganti karena resource memiliki riwayat rapat", nil)
			return
		}
	}
	names, hasNames := b["breakoutRoomNames"]
	var slotNames []string
	if hasNames {
		slotNames = stringArray(names)
		if p != "ZOOM" || len(slotNames) != breakout || hasDuplicateNames(slotNames) {
			writeError(w, 400, "Nama breakout harus unik dan jumlahnya harus sesuai jumlah slot", nil)
			return
		}
	} else if p == "ZOOM" {
		slotNames = roomSlotNames(r, s, id, breakout)
	}
	err = s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(r.Context(), s.store.Q(`UPDATE rooms SET name=?,description=?,online_platform=?,capacity=?,location=?,facilities=?,breakout_rooms=?,image_url=?,active=?,updated_at=? WHERE id=?`), name, nullStringArg(description), nullStringArg(platform), capacity, nullStringArg(location), facilities, breakout, nullStringArg(image), active, now(), id)
		if err != nil {
			return err
		}
		if p == "ZOOM" {
			return syncZoomBreakoutSlots(r, tx, s.store, id, capacity, slotNames)
		}
		_, err = tx.ExecContext(r.Context(), s.store.Q(`UPDATE zoom_breakout_rooms SET active=0 WHERE resource_id=? AND active=1`), id)
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "BREAKOUT_IN_USE") {
			writeError(w, 409, "Jumlah breakout tidak dapat dikurangi karena ada slot yang masih dibooking", nil)
			return
		}
		writeError(w, 409, "Perubahan ruangan tidak dapat disimpan", nil)
		return
	}
	s.audit(r, u, "UPDATE", "Room", id, b)
	writeJSON(w, 200, map[string]any{"id": id, "name": name, "kind": kind, "onlinePlatform": nullableOut(platform), "capacity": capacity, "location": nullableOut(location), "facilities": decodeList(facilities), "breakoutRooms": breakout, "active": active != 0, "zoomBreakoutRooms": roomSlots(r, s, id)})
}

func defaultSlotNames(count int) []string {
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("Breakout %d", i+1)
	}
	return names
}

func hasDuplicateNames(names []string) bool {
	seen := map[string]bool{}
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}

func syncZoomBreakoutSlots(r *http.Request, tx *sql.Tx, store Storage, resourceID string, capacity int, names []string) error {
	rows, err := tx.QueryContext(r.Context(), store.Q(`SELECT id,name FROM zoom_breakout_rooms WHERE resource_id=? AND active=1`), resourceID)
	if err != nil {
		return err
	}
	current := map[string]string{}
	for rows.Next() {
		var id, name string
		if rows.Scan(&id, &name) == nil {
			current[name] = id
		}
	}
	rows.Close()
	keep := map[string]bool{}
	for _, name := range names {
		keep[name] = true
	}
	for name, id := range current {
		if keep[name] {
			continue
		}
		var booked int
		err = tx.QueryRowContext(r.Context(), store.Q(`SELECT count(*) FROM meeting_zoom_breakout_bookings b JOIN meetings m ON m.id=b.meeting_id WHERE b.breakout_room_id=? AND m.status IN ('SUBMITTED','APPROVED') AND m.end_at>?`), id, now()).Scan(&booked)
		if err != nil {
			return err
		}
		if booked > 0 {
			return fmt.Errorf("BREAKOUT_IN_USE")
		}
	}
	_, err = tx.ExecContext(r.Context(), store.Q(`UPDATE zoom_breakout_rooms SET active=0 WHERE resource_id=?`), resourceID)
	if err != nil {
		return err
	}
	slotCapacity := 1
	if len(names) > 0 {
		slotCapacity = (capacity + len(names) - 1) / len(names)
	}
	for _, name := range names {
		if id, exists := current[name]; exists {
			_, err = tx.ExecContext(r.Context(), store.Q(`UPDATE zoom_breakout_rooms SET capacity=?,active=1 WHERE id=?`), slotCapacity, id)
		} else {
			var existingID string
			err = tx.QueryRowContext(r.Context(), store.Q(`SELECT id FROM zoom_breakout_rooms WHERE resource_id=? AND name=?`), resourceID, name).Scan(&existingID)
			if err == sql.ErrNoRows {
				_, err = tx.ExecContext(r.Context(), store.Q(`INSERT INTO zoom_breakout_rooms(id,resource_id,name,capacity,active) VALUES(?,?,?,?,1)`), newID(), resourceID, name, slotCapacity)
			} else if err == nil {
				_, err = tx.ExecContext(r.Context(), store.Q(`UPDATE zoom_breakout_rooms SET capacity=?,active=1 WHERE id=?`), slotCapacity, existingID)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func roomSlotNames(r *http.Request, s *Server, resourceID string, count int) []string {
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT name FROM zoom_breakout_rooms WHERE resource_id=? AND active=1 ORDER BY name LIMIT ?`), resourceID, count)
	if err != nil {
		return defaultSlotNames(count)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			names = append(names, name)
		}
	}
	for len(names) < count {
		names = append(names, fmt.Sprintf("Breakout %d", len(names)+1))
	}
	return names
}

func roomSlots(r *http.Request, s *Server, resourceID string) []any {
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT id,name,capacity,active FROM zoom_breakout_rooms WHERE resource_id=? ORDER BY name`), resourceID)
	if err != nil {
		return []any{}
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, name string
		var capacity, active int
		if rows.Scan(&id, &name, &capacity, &active) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "capacity": capacity, "active": active != 0})
		}
	}
	return out
}

func nullStringArg(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func decodeList(raw string) []string {
	var out []string
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		return []string{}
	}
	return out
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Server) roomDelete(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	id := r.PathValue("id")
	var used int
	_ = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT (SELECT count(*) FROM meetings WHERE room_id=?)+(SELECT count(*) FROM meetings WHERE online_resource_id=?)`), id, id).Scan(&used)
	if used > 0 {
		writeError(w, 409, "Ruangan memiliki riwayat atau agenda rapat; nonaktifkan ruangan sebagai gantinya", nil)
		return
	}
	result, err := s.store.Exec(r.Context(), `DELETE FROM rooms WHERE id=?`, id)
	if err != nil {
		writeError(w, 409, "Ruangan masih digunakan", nil)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		writeError(w, 404, "Ruangan tidak ditemukan", nil)
		return
	}
	s.audit(r, u, "DELETE", "Room", id, nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) usersList(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN", "APPROVER", "ORGANIZER")
	if !ok {
		return
	}
	page, size, offset := pageParams(r)
	args := []any{}
	where := []string{"1=1"}
	if r.URL.Query().Get("includeInactive") != "true" || u.Role != "ADMIN" {
		where = append(where, "u.active=1")
	}
	if d := r.URL.Query().Get("departmentId"); d != "" {
		where = append(where, "u.department_id=?")
		args = append(args, d)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		where = append(where, "(lower(u.name) LIKE ? OR lower(u.email) LIKE ? OR lower(coalesce(d.name,'')) LIKE ?)")
		p := "%" + strings.ToLower(q) + "%"
		args = append(args, p, p, p)
	}
	clause := strings.Join(where, " AND ")
	var total int
	_ = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM users u LEFT JOIN departments d ON d.id=u.department_id WHERE `+clause), args...).Scan(&total)
	args = append(args, size, offset)
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT u.id,u.name,u.email,u.role,u.active,u.department_id,u.job_title,u.page_access,d.id,d.name FROM users u LEFT JOIN departments d ON d.id=u.department_id WHERE `+clause+` ORDER BY u.active DESC,u.name LIMIT ? OFFSET ?`), args...)
	if err != nil {
		writeError(w, 500, "Gagal memuat pengguna", nil)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, name, email, role, access string
		var active int
		var deptID, title, joinedID, deptName sql.NullString
		if rows.Scan(&id, &name, &email, &role, &active, &deptID, &title, &access, &joinedID, &deptName) != nil {
			continue
		}
		var visibleEmail any
		if u.Role == "ADMIN" {
			visibleEmail = email
		}
		item := map[string]any{"id": id, "name": name, "role": role, "isActive": active != 0, "departmentId": nullableOut(deptID), "jobTitle": nullableOut(title), "pageAccess": decodeList(access), "department": nil}
		if visibleEmail != nil {
			item["email"] = visibleEmail
		}
		if joinedID.Valid {
			item["department"] = map[string]any{"id": joinedID.String, "name": deptName.String}
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": size})
}

func (s *Server) usersCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	name, email, password := strings.TrimSpace(asString(b, "name")), strings.ToLower(strings.TrimSpace(asString(b, "email"))), asString(b, "password")
	role := asString(b, "role")
	if role == "" {
		role = "PARTICIPANT"
	}
	if len(name) < 2 || len(name) > 100 || !validEmail(email) || len(password) < 12 || len(password) > 128 || !validEnum(role, "ADMIN", "APPROVER", "ORGANIZER", "PARTICIPANT") || len(asString(b, "jobTitle")) > 120 || !validPageAccess(b["pageAccess"]) {
		writeError(w, 400, "Data pengguna tidak valid", nil)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		writeError(w, 500, "Gagal membuat akun", nil)
		return
	}
	id, stamp := newID(), now()
	access := jsonValue(b["pageAccess"], "[]")
	_, err = s.store.Exec(r.Context(), `INSERT INTO users(id,name,email,password_hash,role,job_title,page_access,active,department_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, name, email, string(hash), role, nullable(asString(b, "jobTitle")), access, 1, nullable(asString(b, "departmentId")), stamp, stamp)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, 409, "Email tersebut sudah terdaftar", nil)
		} else {
			writeError(w, 400, "Bidang yang dipilih tidak ditemukan", nil)
		}
		return
	}
	s.audit(r, u, "CREATE", "User", id, map[string]any{"role": role})
	writeJSON(w, 201, map[string]any{"id": id, "name": name, "email": email, "role": role, "isActive": true})
}

func (s *Server) userPatch(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	id := r.PathValue("id")
	var name, email, role, hash string
	var active int
	var title, dept sql.NullString
	var access string
	err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT name,email,role,active,job_title,department_id,page_access,password_hash FROM users WHERE id=?`), id).Scan(&name, &email, &role, &active, &title, &dept, &access, &hash)
	if err != nil {
		writeError(w, 404, "Pengguna tidak ditemukan", nil)
		return
	}
	existingRole := role
	if v, ok := b["name"].(string); ok {
		name = strings.TrimSpace(v)
	}
	if v, ok := b["email"].(string); ok {
		email = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := b["role"].(string); ok {
		role = v
	}
	if v, ok := b["isActive"].(bool); ok {
		active = boolInt(v)
	}
	if v, ok := b["jobTitle"]; ok {
		title = sql.NullString{String: jsonString(v), Valid: v != nil}
	}
	if v, ok := b["departmentId"]; ok {
		dept = sql.NullString{String: jsonString(v), Valid: v != nil}
	}
	if v, ok := b["pageAccess"]; ok {
		access = jsonValue(v, "[]")
	}
	if v := asString(b, "password"); v != "" {
		if len(v) < 12 || len(v) > 128 {
			writeError(w, 400, "Kata sandi harus memiliki 12 hingga 128 karakter", nil)
			return
		}
		h, err := bcrypt.GenerateFromPassword([]byte(v), 12)
		if err != nil {
			writeError(w, 500, "Gagal memperbarui kata sandi", nil)
			return
		}
		hash = string(h)
	}
	if len(name) < 2 || len(name) > 100 || !validEmail(email) || !validEnum(role, "ADMIN", "APPROVER", "ORGANIZER", "PARTICIPANT") || (title.Valid && len(title.String) > 120) || !validPageAccess(accessValue(b, access)) {
		writeError(w, 400, "Perubahan pengguna tidak valid", nil)
		return
	}
	if id == u.ID && (active == 0 || role != u.Role) {
		writeError(w, 409, "Anda tidak dapat menonaktifkan atau menurunkan peran akun sendiri", nil)
		return
	}

	if existingRole == "ADMIN" && (role != "ADMIN" || active == 0) {
		var admins int
		_ = s.store.DB().QueryRowContext(r.Context(), `SELECT count(*) FROM users WHERE role='ADMIN' AND active=1`).Scan(&admins)
		if admins <= 1 {
			writeError(w, 409, "Akun Super User aktif terakhir tidak dapat dinonaktifkan atau diturunkan perannya", nil)
			return
		}
	}
	_, err = s.store.Exec(r.Context(), `UPDATE users SET name=?,email=?,role=?,active=?,job_title=?,department_id=?,page_access=?,password_hash=?,updated_at=? WHERE id=?`, name, email, role, active, nullStringArg(title), nullStringArg(dept), access, hash, now(), id)
	if err != nil {
		writeError(w, 409, "Pengguna tidak ditemukan atau email/bidang sudah digunakan", nil)
		return
	}
	if active == 0 {
		_, _ = s.store.Exec(r.Context(), `UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`, now(), id)
	}
	s.audit(r, u, "UPDATE", "User", id, map[string]any{"role": role, "isActive": active != 0})
	writeJSON(w, 200, map[string]any{"id": id, "name": name, "email": email, "role": role, "isActive": active != 0, "departmentId": nullableOut(dept)})
}

func accessValue(b map[string]any, serialized string) any {
	if value, ok := b["pageAccess"]; ok {
		return value
	}
	return decodeList(serialized)
}

func validPageAccess(value any) bool {
	if value == nil {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			items = make([]any, len(strings))
			for i, item := range strings {
				items[i] = item
			}
		} else {
			return false
		}
	}
	options := map[string]bool{"__custom__": true, "beranda": true, "kalender": true, "pengajuan": true, "rekap": true, "ruangan": true, "approval": true, "management-talk": true, "berita-uip": true, "pengaturan": true, "pengelolaan": true}
	if len(items) > len(options) {
		return false
	}
	for _, item := range items {
		value, ok := item.(string)
		if !ok || !options[value] {
			return false
		}
	}
	return true
}

func validEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && len(email) <= 254
}

func validHexColor(color string) bool {
	if len(color) != 7 || color[0] != '#' {
		return false
	}
	for _, c := range color[1:] {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s *Server) userDelete(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == u.ID {
		writeError(w, 409, "Anda tidak dapat menghapus akun sendiri", nil)
		return
	}
	var count, active int
	var existingRole string
	err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT active,role,(SELECT count(*) FROM meetings WHERE organizer_id=users.id)+(SELECT count(*) FROM attendance WHERE user_id=users.id)+(SELECT count(*) FROM notifications WHERE user_id=users.id)+(SELECT count(*) FROM sessions WHERE user_id=users.id)+(SELECT count(*) FROM audit_logs WHERE user_id=users.id)+(SELECT count(*) FROM meeting_approvals WHERE approver_id=users.id)+(SELECT count(*) FROM documents WHERE uploaded_by_id=users.id) FROM users WHERE id=?`), id).Scan(&active, &existingRole, &count)
	if err != nil {
		writeError(w, 404, "Pengguna tidak ditemukan", nil)
		return
	}
	if existingRole == "ADMIN" && active != 0 {
		var admins int
		_ = s.store.DB().QueryRowContext(r.Context(), `SELECT count(*) FROM users WHERE role='ADMIN' AND active=1`).Scan(&admins)
		if admins <= 1 {
			writeError(w, 409, "Akun Super User aktif terakhir tidak dapat dihapus", nil)
			return
		}
	}
	if count > 0 {
		writeError(w, 409, "Akun masih memiliki riwayat, sesi, atau relasi data. Nonaktifkan akun agar riwayat dan audit tetap tersimpan.", nil)
		return
	}
	if _, err := s.store.Exec(r.Context(), `DELETE FROM users WHERE id=?`, id); err != nil {
		writeError(w, 409, "Akun masih terhubung dengan data lain. Nonaktifkan akun agar riwayat tetap tersimpan.", nil)
		return
	}
	s.audit(r, u, "DELETE", "User", id, nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) settingsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r, "ADMIN"); !ok {
		return
	}
	var value string
	err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT value FROM settings WHERE key='allowSelfApproval'`)).Scan(&value)
	writeJSON(w, 200, map[string]bool{"allowSelfApproval": err == nil && value == "true"})
}

func (s *Server) settingsPatch(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	v, exists := b["allowSelfApproval"].(bool)
	if !exists {
		writeError(w, 400, "Pengaturan tidak valid", nil)
		return
	}
	_, err := s.store.Exec(r.Context(), `INSERT INTO settings(key,value,updated_at) VALUES('allowSelfApproval',?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, fmt.Sprint(v), now())
	if err != nil {
		writeError(w, 500, "Gagal menyimpan pengaturan", nil)
		return
	}
	s.audit(r, u, "UPDATE", "SystemSetting", "allowSelfApproval", map[string]bool{"value": v})
	writeJSON(w, 200, map[string]bool{"allowSelfApproval": v})
}

func (s *Server) notificationsList(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	page, size, offset := pageParams(r)
	var total, unread int
	_ = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*),coalesce(sum(CASE WHEN read_at IS NULL THEN 1 ELSE 0 END),0) FROM notifications WHERE user_id=?`), u.ID).Scan(&total, &unread)
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT id,user_id,title,body,read_at,created_at FROM notifications WHERE user_id=? ORDER BY created_at DESC LIMIT ? OFFSET ?`), u.ID, size, offset)
	if err != nil {
		writeError(w, 500, "Gagal memuat notifikasi", nil)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, userID, title, body, created string
		var read sql.NullString
		if rows.Scan(&id, &userID, &title, &body, &read, &created) == nil {
			items = append(items, map[string]any{"id": id, "userId": userID, "title": title, "body": body, "readAt": nullableOut(read), "createdAt": created})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "unread": unread, "page": page, "pageSize": size})
}

func (s *Server) notificationIDs(w http.ResponseWriter, r *http.Request) (User, []string, bool, bool) {
	u, ok := requireUser(w, r)
	if !ok {
		return User{}, nil, false, false
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return User{}, nil, false, false
	}
	all := asBool(b, "all", false)
	ids := []string{}
	if raw, ok := b["ids"].([]any); ok {
		for _, x := range raw {
			if id, ok := x.(string); ok {
				ids = append(ids, id)
			}
		}
	}
	if all == (len(ids) > 0) {
		writeError(w, 400, "Pilih notifikasi atau konfirmasi seluruhnya", nil)
		return User{}, nil, false, false
	}
	return u, ids, all, true
}

func (s *Server) notificationsMark(w http.ResponseWriter, r *http.Request) {
	u, ids, all, ok := s.notificationIDs(w, r)
	if !ok {
		return
	}
	query, args := `UPDATE notifications SET read_at=? WHERE user_id=? AND read_at IS NULL`, []any{now(), u.ID}
	if !all {
		marks := make([]string, len(ids))
		for i, id := range ids {
			marks[i] = "?"
			args = append(args, id)
		}
		query += ` AND id IN (` + strings.Join(marks, ",") + `)`
	}
	result, err := s.store.Exec(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "Gagal memperbarui notifikasi", nil)
		return
	}
	n, _ := result.RowsAffected()
	writeJSON(w, 200, map[string]int64{"updated": n})
}

func (s *Server) notificationsDelete(w http.ResponseWriter, r *http.Request) {
	u, ids, all, ok := s.notificationIDs(w, r)
	if !ok {
		return
	}
	query, args := `DELETE FROM notifications WHERE user_id=?`, []any{u.ID}
	if !all {
		marks := make([]string, len(ids))
		for i, id := range ids {
			marks[i] = "?"
			args = append(args, id)
		}
		query += ` AND id IN (` + strings.Join(marks, ",") + `)`
	}
	result, err := s.store.Exec(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "Gagal menghapus notifikasi", nil)
		return
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		s.audit(r, u, "DELETE", "Notification", u.ID, map[string]any{"deletedCount": n, "scope": map[bool]string{true: "all", false: "selected"}[all]})
	}
	writeJSON(w, 200, map[string]int64{"deleted": n})
}

func (s *Server) auditLogs(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	category := r.URL.Query().Get("category")
	if category == "" {
		category = "system"
	}
	if !validEnum(category, "system", "meeting") {
		writeError(w, 400, "Kategori log tidak valid.", nil)
		return
	}
	page, size, offset := pageParams(r)
	entityClause := "entity<>'Meeting'"
	if category == "meeting" {
		entityClause = "entity='Meeting'"
	}
	var total int
	_ = s.store.DB().QueryRowContext(r.Context(), `SELECT count(*) FROM audit_logs WHERE `+entityClause).Scan(&total)
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT a.id,a.user_id,a.action,a.entity,a.entity_id,a.details,a.created_at,u.name,u.email FROM audit_logs a LEFT JOIN users u ON u.id=a.user_id WHERE `+strings.ReplaceAll(entityClause, "entity", "a.entity")+` ORDER BY a.created_at DESC LIMIT ? OFFSET ?`), size, offset)
	if err != nil {
		writeError(w, 500, "Gagal memuat audit log", nil)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, action, entity, entityID, created string
		var userID, details, name, email sql.NullString
		if rows.Scan(&id, &userID, &action, &entity, &entityID, &details, &created, &name, &email) != nil {
			continue
		}
		var parsed any
		if details.Valid {
			_ = json.Unmarshal([]byte(details.String), &parsed)
		}
		item := map[string]any{"id": id, "userId": nullableOut(userID), "action": action, "entity": entity, "entityId": entityID, "details": parsed, "createdAt": created, "user": nil}
		if name.Valid {
			item["user"] = map[string]string{"name": name.String, "email": email.String}
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": size, "category": category, "retentionDays": 365})
	_ = u
}

func (s *Server) auditDelete(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid || asString(b, "confirmation") != "HAPUS LOG LAMA" {
		writeError(w, 400, "Konfirmasi penghapusan log lama tidak valid.", nil)
		return
	}
	cutoff := time.Now().Add(-365 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	result, err := s.store.Exec(r.Context(), `DELETE FROM audit_logs WHERE entity<>'Meeting' AND created_at<?`, cutoff)
	if err != nil {
		writeError(w, 500, "Gagal menghapus audit log lama", nil)
		return
	}
	n, _ := result.RowsAffected()
	s.audit(r, u, "PURGE", "SystemLogRetention", cutoff, map[string]any{"deleted": n, "retentionDays": 365})
	writeJSON(w, 200, map[string]any{"deleted": n, "cutoff": cutoff, "retentionDays": 365})
}
