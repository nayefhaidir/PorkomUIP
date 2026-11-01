package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"uip-meeting-backend/internal/domain"
)

var (
	errRoomUnavailable     = errors.New("ROOM_UNAVAILABLE")
	errRoomCapacity        = errors.New("ROOM_CAPACITY")
	errScheduleConflict    = errors.New("SCHEDULE_CONFLICT")
	errParticipantConflict = errors.New("PARTICIPANT_CONFLICT")
	errTransition          = errors.New("STALE_WORKFLOW")
)

func (s *Server) meetingManager(r *http.Request, u User) bool {
	return u.Role == "ADMIN" || u.Role == "APPROVER" && (departmentShortContainsCSR(r, s, u) || containsPage(u, "approval", false))
}

func confidentialWhere(u User, manager bool) (string, []any) {
	if manager {
		return "", nil
	}
	return ` AND (m.confidential=0 OR m.organizer_id=? OR EXISTS(SELECT 1 FROM attendance a WHERE a.meeting_id=m.id AND a.user_id=?))`, []any{u.ID, u.ID}
}

func meetingWindow(start, end time.Time) string {
	return domain.MeetingWindow(start, end, time.Now())
}

func validMeetingTitle(title string) bool {
	return len(title) >= 3 && len(title) <= 180 && !strings.ContainsAny(title, "\r\n")
}

func (s *Server) meetingsList(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	page, size, offset := pageParams(r)
	where := []string{"1=1"}
	args := []any{}
	if term := strings.TrimSpace(r.URL.Query().Get("q")); term != "" {
		where = append(where, `(lower(m.title) LIKE ? OR lower(coalesce(m.description,'')) LIKE ? OR lower(coalesce(r.name,'')) LIKE ? OR lower(coalesce(d.name,'')) LIKE ?)`)
		like := "%" + strings.ToLower(term) + "%"
		args = append(args, like, like, like, like)
	}
	if v := r.URL.Query().Get("departmentId"); v != "" {
		where = append(where, "m.department_id=?")
		args = append(args, v)
	}
	if v := r.URL.Query().Get("status"); validEnum(v, "DRAFT", "SUBMITTED", "APPROVED", "REJECTED", "CANCELLED", "COMPLETED") {
		where = append(where, "m.status=?")
		args = append(args, v)
	}
	if v := r.URL.Query().Get("from"); v != "" {
		where = append(where, "m.start_at>=?")
		args = append(args, v)
	}
	if v := r.URL.Query().Get("to"); v != "" {
		where = append(where, "m.start_at<=?")
		args = append(args, v)
	}
	visibility, visibilityArgs := confidentialWhere(u, s.meetingManager(r, u))
	clause := strings.Join(where, " AND ") + visibility
	args = append(args, visibilityArgs...)
	var total int
	countQuery := `SELECT count(*) FROM meetings m LEFT JOIN rooms r ON r.id=coalesce(m.room_id,m.online_resource_id) LEFT JOIN departments d ON d.id=m.department_id WHERE ` + clause
	if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(countQuery), args...).Scan(&total); err != nil {
		writeError(w, 500, "Gagal memuat rapat", nil)
		return
	}
	queryArgs := append(append([]any{}, args...), size, offset)
	query := `SELECT m.id FROM meetings m LEFT JOIN rooms r ON r.id=coalesce(m.room_id,m.online_resource_id) LEFT JOIN departments d ON d.id=m.department_id WHERE ` + clause + ` ORDER BY m.start_at ASC LIMIT ? OFFSET ?`
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(query), queryArgs...)
	if err != nil {
		writeError(w, 500, "Gagal memuat rapat", nil)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			writeError(w, 500, "Gagal membaca daftar rapat", nil)
			return
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(w, 500, "Gagal membaca daftar rapat", nil)
		return
	}
	rows.Close()
	items := make([]any, 0, len(ids))
	for _, id := range ids {
		item, err := s.meetingJSON(r, id, false)
		if err != nil {
			writeError(w, 500, "Gagal memuat detail rapat", nil)
			return
		}
		if organizer, ok := item["organizer"].(map[string]any); ok {
			delete(organizer, "email")
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"items": items, "page": page, "pageSize": size, "total": total, "pages": (total + size - 1) / size})
}

func (s *Server) meetingsCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !containsPage(u, "pengajuan", u.Role == "ADMIN" || u.Role == "ORGANIZER") {
		writeError(w, 403, "Akses pengajuan rapat tidak diberikan untuk akun ini", nil)
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	title := strings.TrimSpace(asString(b, "title"))
	startText, endText := asString(b, "startAt"), asString(b, "endAt")
	start, e1 := time.Parse(time.RFC3339, startText)
	end, e2 := time.Parse(time.RFC3339, endText)
	if !validMeetingTitle(title) || e1 != nil || e2 != nil {
		writeError(w, 400, "Data pengajuan tidak valid", nil)
		return
	}
	if msg := meetingWindow(start, end); msg != "" {
		writeError(w, 400, msg, nil)
		return
	}
	roomID := asString(b, "roomId")
	onlineID := asString(b, "onlineResourceId")
	external := strings.TrimSpace(asString(b, "externalLocation"))
	method := asString(b, "method")
	if method == "" {
		if roomID != "" {
			method = "OFFLINE"
		} else {
			method = "ONLINE"
		}
	}
	if !validEnum(method, "OFFLINE", "ONLINE", "HYBRID") ||
		(method == "OFFLINE" && (onlineID != "" || (roomID == "" && external == ""))) ||
		(method == "ONLINE" && (onlineID == "" || roomID != "" || external != "")) ||
		(method == "HYBRID" && ((roomID == "" && external == "") || onlineID == "")) {
		writeError(w, 400, "Pilih ruang fisik dan/atau resource online sesuai metode rapat", nil)
		return
	}
	breakouts := asInt(b, "breakoutRoomCount", 0)
	if breakouts < 0 || breakouts > 100 {
		writeError(w, 400, "Jumlah breakout room tidak valid", nil)
		return
	}
	if method == "OFFLINE" && breakouts > 0 {
		writeError(w, 400, "Breakout room hanya tersedia untuk rapat online atau hybrid", nil)
		return
	}
	departmentID := asString(b, "departmentId")
	if departmentID == "" && u.DepartmentID != nil {
		departmentID = *u.DepartmentID
	}
	if departmentID == "" {
		writeError(w, 400, "Bidang wajib ditentukan", nil)
		return
	}
	expected := asInt(b, "expectedParticipants", 0)
	if expected < 1 || expected > 1000 {
		writeError(w, 400, "Perkiraan peserta tidak valid", nil)
		return
	}
	attendees := stringArray(b["attendeeIds"])
	if len(attendees) > 200 || hasDuplicates(attendees) {
		writeError(w, 400, "Peserta duplikat", nil)
		return
	}
	groupIDs := stringArray(b["attendeeDepartmentIds"])
	if len(groupIDs) > 100 || hasDuplicates(groupIDs) {
		writeError(w, 400, "Grup bidang duplikat", nil)
		return
	}
	userIDs := unique(append([]string{u.ID}, attendees...))
	zoomSlotIDs := stringArray(b["zoomBreakoutRoomIds"])
	if len(zoomSlotIDs) > 100 || hasDuplicates(zoomSlotIDs) {
		writeError(w, 400, "Slot breakout Zoom duplikat", nil)
		return
	}
	guests, _ := b["manualGuests"].([]any)
	requirements, _ := b["requirements"].([]any)
	consumptions, _ := b["consumptions"].([]any)
	if len(guests) > 100 || len(requirements) > 8 || len(consumptions) > 5 {
		writeError(w, 400, "Data undangan, persyaratan, atau konsumsi melebihi batas", nil)
		return
	}
	for _, raw := range guests {
		guest, ok := raw.(map[string]any)
		email := strings.ToLower(asString(guest, "email"))
		if !ok || len(strings.TrimSpace(asString(guest, "name"))) < 2 || len(asString(guest, "name")) > 100 || len(email) > 254 || (email != "" && !validEmail(email)) || len(asString(guest, "organization")) > 120 {
			writeError(w, 400, "Data tamu manual tidak valid", nil)
			return
		}
	}
	validRequirements := []string{"MIC", "MC", "DOCUMENTATION", "PRESS_RELEASE", "PROJECTOR", "WHITEBOARD", "OTHER", "BACKDROP_BANNER", "EXTRA_CHAIRS"}
	requirementTypes := map[string]bool{}
	for _, raw := range requirements {
		item, ok := raw.(map[string]any)
		typ := asString(item, "type")
		if !ok || !validEnum(typ, validRequirements...) || len(asString(item, "detail")) > 300 || requirementTypes[typ] {
			writeError(w, 400, "Persyaratan rapat tidak valid", nil)
			return
		}
		requirementTypes[typ] = true
	}
	validConsumptions := []string{"SNACK_BOX", "RICE_BOX", "COFFEE_BREAK", "LUNCH", "OTHER", "BREAKFAST", "MORNING_SNACK", "AFTERNOON_SNACK", "VIP_FRUIT"}
	consumptionTypes := map[string]bool{}
	for _, raw := range consumptions {
		item, ok := raw.(map[string]any)
		quantity := asInt(item, "quantity", -1)
		typ := asString(item, "itemType")
		if !ok || !validEnum(typ, validConsumptions...) || quantity < 0 || quantity > 1000 || consumptionTypes[typ] {
			writeError(w, 400, "Konsumsi rapat tidak valid", nil)
			return
		}
		consumptionTypes[typ] = true
	}
	if value, ok := b["isConfidential"]; ok {
		if _, isBool := value.(bool); !isBool {
			writeError(w, 400, "Data pengajuan tidak valid", nil)
			return
		}
	}
	if !validEnum(asString(b, "guestCategory"), "", "REGULAR", "VIP") {
		writeError(w, 400, "Kategori tamu tidak valid", nil)
		return
	}
	guestCategory := asString(b, "guestCategory")
	if guestCategory == "" {
		guestCategory = "REGULAR"
	}
	isConfidential := boolInt(asBool(b, "isConfidential", false))
	if value, exists := b["allowParticipantOverlap"]; exists {
		if _, isBool := value.(bool); !isBool {
			writeError(w, 400, "Opsi bentrok peserta tidak valid", nil)
			return
		}
	}
	allowParticipantOverlap := asBool(b, "allowParticipantOverlap", false)
	id, meetingNumber := newID(), "UIP-"+start.UTC().Format("20060102")+"-"+strings.ToUpper(newID()[:6])
	stamp := now()
	mailConfig, err := s.loadMailConfig(r)
	if err != nil {
		writeError(w, 500, "Gagal memuat pengaturan email", nil)
		return
	}
	err = s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		var count int
		if tx.QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM departments WHERE id=?`), departmentID).Scan(&count) != nil || count == 0 {
			return fmt.Errorf("DEPARTMENT_UNKNOWN")
		}
		if roomID != "" {
			var capacity, active int
			var kind string
			if tx.QueryRowContext(r.Context(), s.store.Q(`SELECT capacity,active,kind FROM rooms WHERE id=?`), roomID).Scan(&capacity, &active, &kind) != nil || active == 0 || kind != "PHYSICAL" {
				return errRoomUnavailable
			}
			if capacity < expected {
				return errRoomCapacity
			}
		}
		onlinePlatform := ""
		if onlineID != "" {
			var capacity, active int
			var kind string
			if tx.QueryRowContext(r.Context(), s.store.Q(`SELECT capacity,active,kind,online_platform FROM rooms WHERE id=?`), onlineID).Scan(&capacity, &active, &kind, &onlinePlatform) != nil || active == 0 || kind != "ONLINE" {
				return errRoomUnavailable
			}
			if capacity < expected {
				return errRoomCapacity
			}
			if onlinePlatform == "TEAMS" && breakouts > 0 {
				return fmt.Errorf("BREAKOUT_PROVIDER")
			}
			if breakouts > 0 {
				var limit int
				if tx.QueryRowContext(r.Context(), s.store.Q(`SELECT breakout_rooms FROM rooms WHERE id=?`), onlineID).Scan(&limit) != nil || breakouts > limit {
					return fmt.Errorf("BREAKOUT_LIMIT")
				}
			}
			if len(zoomSlotIDs) > 0 && onlinePlatform != "ZOOM" {
				return fmt.Errorf("BREAKOUT_PROVIDER")
			}
			if onlinePlatform == "ZOOM" && len(zoomSlotIDs) != breakouts {
				return fmt.Errorf("BREAKOUT_SELECTION")
			}
			if onlinePlatform != "ZOOM" && len(zoomSlotIDs) > 0 {
				return fmt.Errorf("BREAKOUT_PROVIDER")
			}
			if onlineURL := asString(b, "onlineUrl"); onlineURL != "" {
				parsed, parseErr := url.Parse(onlineURL)
				host := strings.ToLower(parsed.Hostname())
				validHost := onlinePlatform == "ZOOM" && (host == "zoom.us" || strings.HasSuffix(host, ".zoom.us") || host == "zoom.com" || strings.HasSuffix(host, ".zoom.com")) ||
					onlinePlatform == "TEAMS" && (host == "teams.microsoft.com" || strings.HasSuffix(host, ".teams.microsoft.com") || host == "teams.live.com" || strings.HasSuffix(host, ".teams.live.com"))
				if parseErr != nil || parsed.Scheme != "https" || !validHost {
					return fmt.Errorf("ONLINE_URL_PROVIDER")
				}
			}
			if len(zoomSlotIDs) > 0 {
				marks := make([]string, len(zoomSlotIDs))
				slotArgs := []any{onlineID}
				for i, slotID := range zoomSlotIDs {
					marks[i] = "?"
					slotArgs = append(slotArgs, slotID)
				}
				var selected int
				if tx.QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM zoom_breakout_rooms WHERE resource_id=? AND active=1 AND id IN (`+strings.Join(marks, ",")+`)`), slotArgs...).Scan(&selected) != nil || selected != len(zoomSlotIDs) {
					return fmt.Errorf("BREAKOUT_SELECTION")
				}
				slotMarks := strings.TrimRight(strings.Repeat("?,", len(zoomSlotIDs)), ",")
				slotBookingArgs := []any{end.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano)}
				slotIDs := make([]string, len(zoomSlotIDs))
				copy(slotIDs, zoomSlotIDs)
				sort.Strings(slotIDs)
				for _, slotID := range slotIDs {
					slotBookingArgs = append(slotBookingArgs, slotID)
				}
				var conflict int
				q := `SELECT count(*) FROM meeting_zoom_breakout_bookings b JOIN meetings m ON m.id=b.meeting_id WHERE m.status IN ('SUBMITTED','APPROVED') AND m.start_at<? AND m.end_at>? AND b.breakout_room_id IN (` + slotMarks + `)`
				if tx.QueryRowContext(r.Context(), s.store.Q(q), slotBookingArgs...).Scan(&conflict) != nil || conflict > 0 {
					return fmt.Errorf("BREAKOUT_CONFLICT")
				}
			}
		}
		for _, department := range groupIDs {
			if tx.QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM departments WHERE id=?`), department).Scan(&count) != nil || count == 0 {
				return fmt.Errorf("GROUP_UNKNOWN")
			}
			groupRows, queryErr := tx.QueryContext(r.Context(), s.store.Q(`SELECT id FROM users WHERE department_id=? AND active=1`), department)
			if queryErr != nil {
				return queryErr
			}
			for groupRows.Next() {
				var memberID string
				if groupRows.Scan(&memberID) == nil {
					userIDs = append(userIDs, memberID)
				}
			}
			groupRows.Close()
		}
		userIDs = unique(userIDs)
		if expected < len(userIDs)+len(guests) {
			return fmt.Errorf("ATTENDEE_COUNT")
		}
		if len(userIDs) > 0 {
			marks := make([]string, len(userIDs))
			args := []any{end.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano)}
			for i, userID := range userIDs {
				marks[i] = "?"
				args = append(args, userID)
			}
			query := `SELECT count(*) FROM meetings m WHERE m.status IN ('SUBMITTED','APPROVED') AND m.start_at<? AND m.end_at>? AND m.organizer_id IN (` + strings.Join(marks, ",") + `)`
			if tx.QueryRowContext(r.Context(), s.store.Q(query), args...).Scan(&count) != nil {
				return fmt.Errorf("DATABASE")
			}
			if count > 0 {
				return errScheduleConflict
			}
		}
		{
			query := `SELECT count(*) FROM meetings m WHERE m.status IN ('SUBMITTED','APPROVED') AND m.start_at<? AND m.end_at>? AND (`
			args := []any{end.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano)}
			conditions := []string{"m.organizer_id=?", "EXISTS(SELECT 1 FROM attendance a WHERE a.meeting_id=m.id AND a.user_id=?)"}
			args = append(args, u.ID, u.ID)
			if roomID != "" {
				conditions = append(conditions, "m.room_id=?")
				args = append(args, roomID)
			}
			if onlineID != "" && !(onlinePlatform == "ZOOM" && len(zoomSlotIDs) > 0) {
				conditions = append(conditions, "m.online_resource_id=?")
				args = append(args, onlineID)
			}
			query += strings.Join(conditions, " OR ") + ")"
			if tx.QueryRowContext(r.Context(), s.store.Q(query), args...).Scan(&count) != nil {
				return fmt.Errorf("DATABASE")
			}
			if count > 0 {
				return errScheduleConflict
			}
		}
		if !allowParticipantOverlap && len(userIDs) > 0 {
			{
				query := `SELECT count(*) FROM meetings m WHERE m.status IN ('SUBMITTED','APPROVED') AND m.start_at<? AND m.end_at>? AND EXISTS(SELECT 1 FROM attendance a WHERE a.meeting_id=m.id AND a.user_id IN (`
				args := []any{end.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano)}
				marks := make([]string, len(userIDs))
				for i, uid := range userIDs {
					marks[i] = "?"
					args = append(args, uid)
				}
				query += strings.Join(marks, ",") + "))"
				if tx.QueryRowContext(r.Context(), s.store.Q(query), args...).Scan(&count) != nil {
					return fmt.Errorf("DATABASE")
				}
				if count > 0 {
					return errParticipantConflict
				}
			}
		}
		known := 0
		for _, userID := range userIDs {
			if tx.QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM users WHERE id=? AND active=1`), userID).Scan(&count) != nil {
				return fmt.Errorf("DATABASE")
			}
			known += count
		}
		if known != len(userIDs) {
			return fmt.Errorf("ATTENDEE_UNKNOWN")
		}
		_, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO meetings(id,meeting_number,title,description,start_at,end_at,status,method,confidential,allow_participant_overlap,guest_category,organizer_id,department_id,room_id,online_resource_id,breakout_room_count,online_url,external_location,notes,catering,expected_participants,submitted_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
			id, meetingNumber, title, nullable(asString(b, "description")), start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), "SUBMITTED", method, isConfidential, boolInt(allowParticipantOverlap), guestCategory, u.ID, departmentID, nullable(roomID), nullable(onlineID), breakouts, nullable(asString(b, "onlineUrl")), nullable(external), nullable(asString(b, "notes")), nullable(asString(b, "catering")), expected, stamp, stamp, stamp)
		if err != nil {
			return err
		}
		for _, userID := range userIDs {
			if _, err = tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO attendance(id,meeting_id,user_id) VALUES(?,?,?)`), newID(), id, userID); err != nil {
				return err
			}
		}
		for _, item := range guests {
			g, ok := item.(map[string]any)
			if !ok {
				continue
			}
			for _, slotID := range zoomSlotIDs {
				var name string
				var capacity int
				if tx.QueryRowContext(r.Context(), s.store.Q(`SELECT name,capacity FROM zoom_breakout_rooms WHERE id=? AND active=1`), slotID).Scan(&name, &capacity) == nil {
					_, err = tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO breakout_rooms(id,meeting_id,name,capacity) VALUES(?,?,?,?)`), newID(), id, name, capacity)
					if err == nil {
						_, err = tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO meeting_zoom_breakout_bookings(meeting_id,breakout_room_id,created_at) VALUES(?,?,?)`), id, slotID, stamp)
					}
				} else {
					err = fmt.Errorf("BREAKOUT_SELECTION")
				}
				if err != nil {
					return err
				}
			}
			if len(zoomSlotIDs) == 0 && breakouts > 0 {
				capacity := expected
				if onlineID != "" {
					_ = tx.QueryRowContext(r.Context(), s.store.Q(`SELECT capacity FROM rooms WHERE id=?`), onlineID).Scan(&capacity)
				}
				for i := 0; i < breakouts; i++ {
					_, err = tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO breakout_rooms(id,meeting_id,name,capacity) VALUES(?,?,?,?)`), newID(), id, fmt.Sprintf("Breakout %d", i+1), (capacity+breakouts-1)/breakouts)
					if err != nil {
						return err
					}
				}
			}
			_, err = tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO meeting_guests(id,meeting_id,name,email,organization) VALUES(?,?,?,?,?)`), newID(), id, asString(g, "name"), nullable(strings.ToLower(asString(g, "email"))), nullable(asString(g, "organization")))
			if err != nil {
				return err
			}
		}
		if items, ok := b["requirements"].([]any); ok {
			for _, raw := range items {
				item, _ := raw.(map[string]any)
				_, err = tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO meeting_requirements(id,meeting_id,type,detail) VALUES(?,?,?,?)`), newID(), id, asString(item, "type"), nullable(asString(item, "detail")))
				if err != nil {
					return err
				}
			}
		}
		if items, ok := b["consumptions"].([]any); ok {
			for _, raw := range items {
				item, _ := raw.(map[string]any)
				_, err = tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO meeting_consumptions(id,meeting_id,item_type,quantity) VALUES(?,?,?,?)`), newID(), id, asString(item, "itemType"), asInt(item, "quantity", 0))
				if err != nil {
					return err
				}
			}
		}
		details, _ := json.Marshal(map[string]any{"title": title, "startAt": start.UTC().Format(time.RFC3339Nano)})
		_, err = tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO audit_logs(id,user_id,action,entity,entity_id,details,created_at) VALUES(?,?,?,?,?,?,?)`), newID(), u.ID, "SUBMIT", "Meeting", id, string(details), stamp)
		if err != nil {
			return err
		}
		for _, userID := range userIDs {
			if userID == u.ID {
				continue
			}
			if _, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO notifications(id,user_id,title,body,created_at) VALUES(?,?,?,?,?)`), newID(), userID, "Pengajuan rapat baru", title+" telah diajukan dan menunggu persetujuan.", stamp); err != nil {
				return err
			}
		}
		approvers, err := tx.QueryContext(r.Context(), s.store.Q(`SELECT u.id,u.email FROM users u LEFT JOIN departments d ON d.id=u.department_id WHERE u.active=1 AND u.id<>? AND (u.role='ADMIN' OR (u.role='APPROVER' AND upper(coalesce(d.short_name,'')) LIKE '%CSR%'))`), u.ID)
		if err != nil {
			return err
		}
		approverIDs := []string{}
		approverEmails := []string{}
		for approvers.Next() {
			var approverID, approverEmail string
			if approvers.Scan(&approverID, &approverEmail) == nil {
				approverIDs = append(approverIDs, approverID)
				approverEmails = append(approverEmails, approverEmail)
			}
		}
		err = approvers.Err()
		approvers.Close()
		if err != nil {
			return err
		}
		for _, approverID := range approverIDs {
			if _, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO notifications(id,user_id,title,body,created_at) VALUES(?,?,?,?,?)`), newID(), approverID, "Pengajuan menunggu persetujuan", fmt.Sprintf("%s diajukan oleh %s.", title, u.Name), stamp); err != nil {
				return err
			}
		}
		return s.enqueueEventTx(r.Context(), tx, mailConfig, "submission", id+":submitted", title, meetingNumber, approverEmails)
	})
	if err != nil {
		switch {
		case errors.Is(err, errRoomUnavailable):
			writeError(w, 409, "Ruangan tidak aktif atau tidak ditemukan", nil)
		case errors.Is(err, errRoomCapacity):
			writeError(w, 409, "Kapasitas ruangan tidak mencukupi", nil)
		case errors.Is(err, errScheduleConflict):
			writeError(w, 409, "Jadwal bertabrakan dengan rapat ruang, penyelenggara, atau peserta", nil)
		case errors.Is(err, errParticipantConflict):
			writeError(w, 409, "Peserta memiliki jadwal rapat yang bertabrakan. Aktifkan opsi izin bentrok peserta jika kehadiran di rapat bersamaan dimungkinkan.", nil)
		case strings.Contains(err.Error(), "BREAKOUT_LIMIT"):
			writeError(w, 400, "Jumlah breakout room melebihi konfigurasi resource online", nil)
		case strings.Contains(err.Error(), "BREAKOUT_SELECTION"):
			writeError(w, 409, "Pilih slot breakout Zoom yang masih tersedia sesuai jumlah yang diminta", nil)
		case strings.Contains(err.Error(), "BREAKOUT_PROVIDER"):
			writeError(w, 400, "Slot breakout khusus Zoom tidak dapat digunakan pada Microsoft Teams", nil)
		case strings.Contains(err.Error(), "BREAKOUT_CONFLICT"):
			writeError(w, 409, "Satu atau beberapa breakout Zoom telah dibooking pada waktu tersebut", nil)
		case strings.Contains(err.Error(), "ONLINE_URL_PROVIDER"):
			writeError(w, 400, "Tautan harus menggunakan HTTPS dan domain Zoom atau Microsoft Teams yang sesuai dengan resource", nil)
		case strings.Contains(err.Error(), "GROUP_UNKNOWN"):
			writeError(w, 400, "Satu atau beberapa bidang peserta tidak ditemukan", nil)
		case strings.Contains(err.Error(), "ATTENDEE_COUNT"):
			writeError(w, 400, "Perkiraan peserta harus mencakup seluruh undangan", nil)
		case strings.Contains(err.Error(), "ATTENDEE_UNKNOWN"):
			writeError(w, 400, "Satu atau beberapa peserta tidak ditemukan", nil)
		case strings.Contains(err.Error(), "DEPARTMENT_UNKNOWN"):
			writeError(w, 400, "Bidang tidak ditemukan", nil)
		default:
			if isUniqueViolation(err) || strings.Contains(strings.ToLower(err.Error()), "serializ") {
				writeError(w, 409, "Jadwal baru saja berubah dan bertabrakan; periksa ketersediaan lalu coba kembali", nil)
			} else {
				writeError(w, 400, "Data pengajuan tidak valid", nil)
			}
		}
		return
	}
	item, err := s.meetingJSON(r, id, true)
	if err != nil {
		writeError(w, 500, "Rapat tersimpan tetapi tidak dapat dimuat", nil)
		return
	}
	writeJSON(w, 201, item)
}

func stringArray(value any) []string {
	out := []string{}
	if items, ok := value.([]any); ok {
		for _, item := range items {
			if v, ok := item.(string); ok {
				out = append(out, v)
			}
		}
	}
	return out
}

func unique(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func hasDuplicates(in []string) bool { return len(unique(in)) != len(in) }

func (s *Server) meetingJSON(r *http.Request, id string, full bool) (map[string]any, error) {
	query := `SELECT m.id,m.meeting_number,m.title,m.description,m.start_at,m.end_at,m.status,m.method,m.confidential,m.allow_participant_overlap,m.guest_category,m.timezone,m.organizer_id,m.department_id,m.room_id,m.online_resource_id,m.breakout_room_count,m.online_url,m.external_location,m.notes,m.catering,m.expected_participants,m.reviewed_by_id,m.reviewed_at,m.rejection_reason,m.submitted_at,m.approved_at,m.cancelled_at,m.created_at,m.updated_at,u.id,u.name,u.email,d.id,d.name,d.short_name,d.color FROM meetings m JOIN users u ON u.id=m.organizer_id JOIN departments d ON d.id=m.department_id WHERE m.id=?`
	var idV, number, title, start, end, status, method, guestCategory, timezone, organizerID, deptID string
	var description, roomID, onlineID, onlineURL, external, notes, catering, reviewedBy, reviewedAt, rejection, submitted, approved, cancelled sql.NullString
	var confidential, allowParticipantOverlap int
	var breakout, expected int
	var created, updated, organizerName, organizerEmail, deptName, deptShort, deptColor string
	err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(query), id).Scan(&idV, &number, &title, &description, &start, &end, &status, &method, &confidential, &allowParticipantOverlap, &guestCategory, &timezone, &organizerID, &deptID, &roomID, &onlineID, &breakout, &onlineURL, &external, &notes, &catering, &expected, &reviewedBy, &reviewedAt, &rejection, &submitted, &approved, &cancelled, &created, &updated, newStringScanner(&organizerID), &organizerName, &organizerEmail, newStringScanner(&deptID), &deptName, &deptShort, &deptColor)
	if err != nil {
		return nil, err
	}
	item := map[string]any{
		"id": idV, "meetingNumber": number, "title": title, "description": nullableOut(description),
		"startAt": start, "endAt": end, "status": status, "method": method, "isConfidential": confidential != 0,
		"guestCategory": guestCategory, "timezone": timezone, "organizerId": organizerID, "departmentId": deptID,
		"allowParticipantOverlap": allowParticipantOverlap != 0,
		"roomId":                  nullableOut(roomID), "onlineResourceId": nullableOut(onlineID), "breakoutRoomCount": breakout,
		"onlineUrl": nullableOut(onlineURL), "externalLocation": nullableOut(external), "notes": nullableOut(notes),
		"catering": nullableOut(catering), "expectedParticipants": expected, "reviewedById": nullableOut(reviewedBy),
		"reviewedAt": nullableOut(reviewedAt), "rejectionReason": nullableOut(rejection), "submittedAt": nullableOut(submitted),
		"approvedAt": nullableOut(approved), "cancelledAt": nullableOut(cancelled), "createdAt": created, "updatedAt": updated,
		"organizer":  map[string]any{"id": organizerID, "name": organizerName, "email": organizerEmail},
		"department": map[string]any{"id": deptID, "name": deptName, "shortName": deptShort, "color": deptColor},
	}
	item["room"] = nil
	item["onlineResource"] = nil
	for field, resourceID := range map[string]sql.NullString{"room": roomID, "onlineResource": onlineID} {
		if !resourceID.Valid {
			continue
		}
		var rid, name, kind, facilities string
		var desc, platform, location, image sql.NullString
		var capacity, breakoutRooms, active int
		err = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT id,name,description,kind,online_platform,capacity,location,facilities,breakout_rooms,image_url,active FROM rooms WHERE id=?`), resourceID.String).Scan(&rid, &name, &desc, &kind, &platform, &capacity, &location, &facilities, &breakoutRooms, &image, &active)
		if err != nil {
			return nil, err
		}
		item[field] = map[string]any{"id": rid, "name": name, "description": nullableOut(desc), "kind": kind, "onlinePlatform": nullableOut(platform), "capacity": capacity, "location": nullableOut(location), "facilities": decodeList(facilities), "breakoutRooms": breakoutRooms, "imageUrl": nullableOut(image), "active": active != 0}
	}
	if !full {
		for field, query := range map[string]struct {
			query string
			kind  string
		}{
			"requirements":  {`SELECT id,type FROM meeting_requirements WHERE meeting_id=?`, "requirementSummary"},
			"consumptions":  {`SELECT id,item_type,quantity,unit FROM meeting_consumptions WHERE meeting_id=?`, "consumption"},
			"documents":     {`SELECT id,document_type FROM documents WHERE meeting_id=? ORDER BY created_at`, "documentSummary"},
			"breakoutRooms": {`SELECT id,name,capacity FROM breakout_rooms WHERE meeting_id=? ORDER BY name`, "breakout"},
		} {
			children, err := s.meetingChildren(r, query.query, id, query.kind)
			if err != nil {
				return nil, err
			}
			item[field] = children
		}
		return item, nil
	}
	for field, query := range map[string]struct {
		query string
		kind  string
	}{
		"attendances":          {`SELECT a.id,a.user_id,u.id,u.name,u.email FROM attendance a JOIN users u ON u.id=a.user_id WHERE a.meeting_id=?`, "attendance"},
		"guests":               {`SELECT id,name,email,organization FROM meeting_guests WHERE meeting_id=?`, "guest"},
		"requirements":         {`SELECT id,type,detail FROM meeting_requirements WHERE meeting_id=?`, "requirement"},
		"consumptions":         {`SELECT id,item_type,quantity,unit FROM meeting_consumptions WHERE meeting_id=?`, "consumption"},
		"documents":            {`SELECT id,filename,document_type,storage_key,mime_type,size,uploaded_by_id,created_at,verified_at FROM documents WHERE meeting_id=? ORDER BY created_at`, "document"},
		"breakoutRooms":        {`SELECT id,name,capacity FROM breakout_rooms WHERE meeting_id=? ORDER BY name`, "breakout"},
		"zoomBreakoutBookings": {`SELECT b.meeting_id,b.breakout_room_id,z.name,z.capacity FROM meeting_zoom_breakout_bookings b JOIN zoom_breakout_rooms z ON z.id=b.breakout_room_id WHERE b.meeting_id=? ORDER BY z.name`, "zoomBooking"},
		"approvals":            {`SELECT id,approver_id,decision,comment,decided_at FROM meeting_approvals WHERE meeting_id=? ORDER BY decided_at`, "approval"},
	} {
		children, err := s.meetingChildren(r, query.query, id, query.kind)
		if err != nil {
			return nil, err
		}
		item[field] = children
	}
	return item, nil
}

type stringScanner struct{ target *string }

func newStringScanner(target *string) *stringScanner { return &stringScanner{target: target} }
func (s *stringScanner) Scan(src any) error {
	if src == nil {
		*s.target = ""
		return nil
	}
	switch v := src.(type) {
	case string:
		*s.target = v
	case []byte:
		*s.target = string(v)
	default:
		return fmt.Errorf("invalid string")
	}
	return nil
}

func (s *Server) meetingChildren(r *http.Request, query, id, kind string) ([]any, error) {
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(query), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var item any
		switch kind {
		case "attendance":
			var aid, uid, userID, name, email string
			err = rows.Scan(&aid, &uid, &userID, &name, &email)
			item = map[string]any{"id": aid, "meetingId": id, "userId": uid, "user": map[string]string{"id": userID, "name": name, "email": email}}
		case "guest":
			var gid, name string
			var email, org sql.NullString
			err = rows.Scan(&gid, &name, &email, &org)
			item = map[string]any{"id": gid, "meetingId": id, "name": name, "email": nullableOut(email), "organization": nullableOut(org)}
		case "requirement":
			var rid, typ string
			var detail sql.NullString
			err = rows.Scan(&rid, &typ, &detail)
			item = map[string]any{"id": rid, "meetingId": id, "type": typ, "detail": nullableOut(detail)}
		case "requirementSummary":
			var rid, typ string
			err = rows.Scan(&rid, &typ)
			item = map[string]any{"id": rid, "type": typ}
		case "consumption":
			var cid, typ, unit string
			var qty int
			err = rows.Scan(&cid, &typ, &qty, &unit)
			item = map[string]any{"id": cid, "meetingId": id, "itemType": typ, "quantity": qty, "unit": unit}
		case "document":
			var did, filename, typ, key, mime, created string
			var size int
			var uploaded, verified sql.NullString
			err = rows.Scan(&did, &filename, &typ, &key, &mime, &size, &uploaded, &created, &verified)
			item = map[string]any{"id": did, "meetingId": id, "filename": filename, "documentType": typ, "storageKey": key, "mimeType": mime, "size": size, "uploadedById": nullableOut(uploaded), "createdAt": created, "verifiedAt": nullableOut(verified)}
		case "documentSummary":
			var did, typ string
			err = rows.Scan(&did, &typ)
			item = map[string]any{"id": did, "documentType": typ}
		case "approval":
			var aid, approver, decision, decided string
			var comment sql.NullString
			err = rows.Scan(&aid, &approver, &decision, &comment, &decided)
			item = map[string]any{"id": aid, "meetingId": id, "approverId": approver, "decision": decision, "comment": nullableOut(comment), "decidedAt": decided}
		case "breakout":
			var rid, name string
			var capacity int
			err = rows.Scan(&rid, &name, &capacity)
			item = map[string]any{"id": rid, "meetingId": id, "name": name, "capacity": capacity}
		case "zoomBooking":
			var meetingID, roomID, name string
			var capacity int
			err = rows.Scan(&meetingID, &roomID, &name, &capacity)
			item = map[string]any{"meetingId": meetingID, "breakoutRoomId": roomID, "breakoutRoom": map[string]any{"id": roomID, "name": name, "capacity": capacity}}
		default:
			return nil, fmt.Errorf("unknown meeting child type %q", kind)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Server) meetingGet(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var organizer string
	var confidential int
	var attendeeCount int
	if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT organizer_id,confidential,(SELECT count(*) FROM attendance WHERE meeting_id=meetings.id AND user_id=?) FROM meetings WHERE id=?`), u.ID, id).Scan(&organizer, &confidential, &attendeeCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "Rapat tidak ditemukan", nil)
		} else {
			writeError(w, 500, "Gagal memuat rapat", nil)
		}
		return
	}
	manager := s.meetingManager(r, u)
	if confidential != 0 && !manager && organizer != u.ID && attendeeCount == 0 {
		writeError(w, 404, "Rapat tidak ditemukan", nil)
		return
	}
	full := manager || organizer == u.ID || attendeeCount > 0
	item, err := s.meetingJSON(r, id, full)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "Rapat tidak ditemukan", nil)
		} else {
			writeError(w, 500, "Gagal memuat detail rapat", nil)
		}
		return
	}
	if !full {
		for _, field := range []string{"attendances", "guests", "requirements", "consumptions", "documents", "breakoutRooms", "zoomBreakoutBookings", "approvals"} {
			item[field] = []any{}
		}
	}
	if !manager && organizer != u.ID {
		if owner, ok := item["organizer"].(map[string]any); ok {
			delete(owner, "email")
		}
		for _, field := range []string{"attendances", "guests"} {
			items, ok := item[field].([]any)
			if !ok {
				continue
			}
			for _, raw := range items {
				entry, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if field == "guests" {
					delete(entry, "email")
				} else if user, ok := entry["user"].(map[string]string); ok {
					delete(user, "email")
				}
			}
		}
	}
	writeJSON(w, 200, item)
}

func (s *Server) meetingPatch(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	id := r.PathValue("id")
	var organizer, status, startText, endText, title, meetingNumber string
	var description, room, onlineID, onlineURL, notes, catering sql.NullString
	var expected, allowParticipantOverlap int
	err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT organizer_id,status,start_at,end_at,title,description,room_id,online_resource_id,online_url,notes,catering,expected_participants,meeting_number,allow_participant_overlap FROM meetings WHERE id=?`), id).Scan(&organizer, &status, &startText, &endText, &title, &description, &room, &onlineID, &onlineURL, &notes, &catering, &expected, &meetingNumber, &allowParticipantOverlap)
	if err != nil {
		writeError(w, 404, "Rapat tidak ditemukan", nil)
		return
	}
	manager := u.Role == "ADMIN" || u.Role == "APPROVER" && departmentShortContainsCSR(r, s, u)
	if !manager && !(u.Role == "ORGANIZER" && organizer == u.ID) {
		writeError(w, 403, "Hanya pengelola rapat yang dapat mengubah rapat ini", nil)
		return
	}
	if status != "DRAFT" && status != "SUBMITTED" {
		writeError(w, 409, "Rapat dengan status ini tidak dapat diedit", nil)
		return
	}
	start, _ := time.Parse(time.RFC3339Nano, startText)
	end, _ := time.Parse(time.RFC3339Nano, endText)
	originalStart, originalEnd := start, end
	if v, ok := b["startAt"].(string); ok {
		start, err = time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, 400, "Perubahan tidak valid", nil)
			return
		}
	}
	if v, ok := b["endAt"].(string); ok {
		end, err = time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, 400, "Perubahan tidak valid", nil)
			return
		}
	}
	if _, changedStart := b["startAt"]; changedStart {
		if msg := meetingWindow(start, end); msg != "" {
			writeError(w, 400, msg, nil)
			return
		}
	}
	if _, changedEnd := b["endAt"]; changedEnd {
		if msg := meetingWindow(start, end); msg != "" {
			writeError(w, 400, msg, nil)
			return
		}
	}
	rescheduled := !start.Equal(originalStart) || !end.Equal(originalEnd)
	if v := asString(b, "title"); v != "" {
		title = strings.TrimSpace(v)
		if !validMeetingTitle(title) {
			writeError(w, 400, "Judul rapat tidak valid", nil)
			return
		}
	}
	if v, exists := b["description"]; exists {
		description = sql.NullString{String: jsonString(v), Valid: v != nil}
	}
	if v, exists := b["roomId"]; exists {
		room = sql.NullString{String: jsonString(v), Valid: v != nil}
	}
	if v, exists := b["onlineUrl"]; exists {
		onlineURL = sql.NullString{String: jsonString(v), Valid: v != nil}
	}
	if v, exists := b["notes"]; exists {
		notes = sql.NullString{String: jsonString(v), Valid: v != nil}
	}
	if v, exists := b["catering"]; exists {
		catering = sql.NullString{String: jsonString(v), Valid: v != nil}
	}
	if _, ok := b["expectedParticipants"]; ok {
		expected = asInt(b, "expectedParticipants", expected)
	}
	if value, ok := b["allowParticipantOverlap"]; ok {
		allowed, valid := value.(bool)
		if !valid {
			writeError(w, 400, "Opsi bentrok peserta tidak valid", nil)
			return
		}
		allowParticipantOverlap = boolInt(allowed)
	}
	mailConfig, err := s.loadMailConfig(r)
	if err != nil {
		writeError(w, 500, "Gagal memuat pengaturan email", nil)
		return
	}
	err = s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		if room.Valid {
			var capacity, active int
			var kind string
			if tx.QueryRowContext(r.Context(), s.store.Q(`SELECT capacity,active,kind FROM rooms WHERE id=?`), room.String).Scan(&capacity, &active, &kind) != nil || active == 0 || kind != "PHYSICAL" {
				return errRoomUnavailable
			}
			if capacity < expected {
				return errRoomCapacity
			}
		}
		usesZoomBreakoutSlots := false
		if onlineID.Valid {
			var capacity, active int
			var kind, platform string
			if tx.QueryRowContext(r.Context(), s.store.Q(`SELECT capacity,active,kind,coalesce(online_platform,'') FROM rooms WHERE id=?`), onlineID.String).Scan(&capacity, &active, &kind, &platform) != nil || active == 0 || kind != "ONLINE" {
				return errRoomUnavailable
			}
			if capacity < expected {
				return errRoomCapacity
			}
			var slotCount int
			if err := tx.QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM meeting_zoom_breakout_bookings WHERE meeting_id=?`), id).Scan(&slotCount); err != nil {
				return err
			}
			usesZoomBreakoutSlots = platform == "ZOOM" && slotCount > 0
		}
		var conflicts int
		args := []any{id, end.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano), organizer}
		query := `SELECT count(*) FROM meetings m WHERE m.id<>? AND m.status IN ('SUBMITTED','APPROVED') AND m.start_at<? AND m.end_at>? AND (m.organizer_id=?`
		if room.Valid {
			query += ` OR m.room_id=?`
			args = append(args, room.String)
		}
		if onlineID.Valid && !usesZoomBreakoutSlots {
			query += ` OR m.online_resource_id=?`
			args = append(args, onlineID.String)
		}
		query += `)`
		if tx.QueryRowContext(r.Context(), s.store.Q(query), args...).Scan(&conflicts) != nil {
			return errors.New("DATABASE")
		}
		if conflicts > 0 {
			return errScheduleConflict
		}
		if err := tx.QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM meetings m JOIN attendance a ON a.meeting_id=m.id WHERE m.id<>? AND m.status IN ('SUBMITTED','APPROVED') AND m.start_at<? AND m.end_at>? AND a.user_id=?`), id, end.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano), organizer).Scan(&conflicts); err != nil {
			return err
		}
		if conflicts > 0 {
			return errScheduleConflict
		}
		if usesZoomBreakoutSlots {
			if err := tx.QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM meeting_zoom_breakout_bookings mine JOIN meeting_zoom_breakout_bookings other ON other.breakout_room_id=mine.breakout_room_id JOIN meetings m ON m.id=other.meeting_id WHERE mine.meeting_id=? AND m.id<>? AND m.status IN ('SUBMITTED','APPROVED') AND m.start_at<? AND m.end_at>?`), id, id, end.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano)).Scan(&conflicts); err != nil {
				return err
			}
			if conflicts > 0 {
				return errScheduleConflict
			}
		}
		if err := tx.QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM meetings m WHERE m.id<>? AND m.status IN ('SUBMITTED','APPROVED') AND m.start_at<? AND m.end_at>? AND m.organizer_id IN (SELECT user_id FROM attendance WHERE meeting_id=?)`), id, end.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano), id).Scan(&conflicts); err != nil {
			return err
		}
		if conflicts > 0 {
			return errScheduleConflict
		}
		if allowParticipantOverlap == 0 {
			if err := tx.QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM meetings m WHERE m.id<>? AND m.status IN ('SUBMITTED','APPROVED') AND m.start_at<? AND m.end_at>? AND EXISTS(SELECT 1 FROM attendance a WHERE a.meeting_id=m.id AND a.user_id IN (SELECT user_id FROM attendance WHERE meeting_id=?))`), id, end.UTC().Format(time.RFC3339Nano), start.UTC().Format(time.RFC3339Nano), id).Scan(&conflicts); err != nil {
				return err
			}
			if conflicts > 0 {
				return errParticipantConflict
			}
		}
		_, e := tx.ExecContext(r.Context(), s.store.Q(`UPDATE meetings SET title=?,description=?,start_at=?,end_at=?,room_id=?,online_url=?,expected_participants=?,catering=?,notes=?,allow_participant_overlap=?,updated_at=? WHERE id=?`), title, nullStringArg(description), start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), nullStringArg(room), nullStringArg(onlineURL), expected, nullStringArg(catering), nullStringArg(notes), allowParticipantOverlap, now(), id)
		if e != nil {
			return e
		}
		if rescheduled {
			rows, err := tx.QueryContext(r.Context(), s.store.Q(`SELECT u.id,u.email FROM users u WHERE u.id=? OR u.id IN (SELECT user_id FROM attendance WHERE meeting_id=?)`), organizer, id)
			if err != nil {
				return err
			}
			recipients, userIDs := []string{}, []string{}
			for rows.Next() {
				var userID, email string
				if err := rows.Scan(&userID, &email); err != nil {
					rows.Close()
					return err
				}
				userIDs = append(userIDs, userID)
				recipients = append(recipients, email)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			details := fmt.Sprintf("Jadwal rapat %s (%s) berubah menjadi %s–%s WIB.", title, meetingNumber, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
			for _, userID := range unique(userIDs) {
				if _, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO notifications(id,user_id,title,body,created_at) VALUES(?,?,?,?,?)`), newID(), userID, "Jadwal rapat berubah", details, now()); err != nil {
					return err
				}
			}
			if err := s.enqueueTemplateTx(r.Context(), tx, mailConfig, "reschedule", id+":"+start.UTC().Format(time.RFC3339Nano), title, meetingNumber, recipients, map[string]string{"meetingDetails": details}); err != nil {
				return err
			}
		}
		return txAudit(s.store, r, tx, u, "UPDATE", "Meeting", id, b)
	})
	if errors.Is(err, errRoomUnavailable) {
		writeError(w, 409, "Ruangan tidak aktif atau tidak ditemukan", nil)
		return
	}
	if errors.Is(err, errRoomCapacity) {
		writeError(w, 409, "Kapasitas ruangan tidak mencukupi", nil)
		return
	}
	if errors.Is(err, errParticipantConflict) {
		writeError(w, 409, "Peserta memiliki jadwal rapat yang bertabrakan. Aktifkan opsi izin bentrok peserta jika kehadiran di rapat bersamaan dimungkinkan.", nil)
		return
	}
	if errors.Is(err, errScheduleConflict) || strings.Contains(strings.ToLower(fmt.Sprint(err)), "serializ") {
		writeError(w, 409, "Jadwal bertabrakan dengan rapat ruang, penyelenggara, atau peserta", nil)
		return
	}
	if err != nil {
		writeError(w, 500, "Gagal memperbarui rapat", nil)
		return
	}
	item, err := s.meetingJSON(r, id, true)
	if err != nil {
		writeError(w, 500, "Rapat diperbarui tetapi detailnya gagal dimuat", nil)
		return
	}
	writeJSON(w, 200, item)
}

func txAudit(store Storage, r *http.Request, tx *sql.Tx, u User, action, entity, entityID string, details any) error {
	raw, _ := json.Marshal(details)
	returnExec, err := tx.ExecContext(r.Context(), store.Q(`INSERT INTO audit_logs(id,user_id,action,entity,entity_id,details,created_at) VALUES(?,?,?,?,?,?,?)`), newID(), u.ID, action, entity, entityID, nullable(string(raw)), now())
	_ = returnExec
	return err
}

func departmentShortContainsCSR(r *http.Request, s *Server, u User) bool {
	if u.DepartmentID == nil {
		return false
	}
	var short string
	_ = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT short_name FROM departments WHERE id=?`), *u.DepartmentID).Scan(&short)
	return strings.Contains(strings.ToUpper(short), "CSR")
}

func (s *Server) meetingDelete(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN")
	if !ok {
		return
	}
	id := r.PathValue("id")
	var title, number, status, start string
	if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT title,meeting_number,status,start_at FROM meetings WHERE id=?`), id).Scan(&title, &number, &status, &start); err != nil {
		writeError(w, 404, "Rapat tidak ditemukan", nil)
		return
	}
	documents := []string{}
	drows, _ := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT storage_key FROM documents WHERE meeting_id=?`), id)
	for drows != nil && drows.Next() {
		var key string
		if drows.Scan(&key) == nil {
			documents = append(documents, key)
		}
	}
	if drows != nil {
		drows.Close()
	}
	err := s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		if e := txAudit(s.store, r, tx, u, "DELETE", "Meeting", id, map[string]any{"title": title, "meetingNumber": number, "status": status, "startAt": start, "deletedBy": u.Email}); e != nil {
			return e
		}
		_, e := tx.ExecContext(r.Context(), s.store.Q(`DELETE FROM meetings WHERE id=?`), id)
		return e
	})
	if err != nil {
		writeError(w, 500, "Gagal menghapus rapat", nil)
		return
	}
	filesRemoved := 0
	for _, key := range documents {
		if os.Remove(filepath.Join(storageDir(), filepath.Base(key))) == nil {
			filesRemoved++
		}
	}
	pending := len(documents) != filesRemoved
	if pending {
		s.audit(r, u, "CLEANUP_FAILED", "SystemStorage", id, map[string]int{"failedFiles": len(documents) - filesRemoved})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "deleted": id, "filesRemoved": filesRemoved, "cleanupPending": pending})
}

func (s *Server) meetingWorkflow(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	b, valid := decodeMap(w, r)
	if !valid {
		return
	}
	action := asString(b, "action")
	if !validEnum(action, "approve", "reject", "cancel") || (action == "reject" && len(strings.TrimSpace(asString(b, "reason"))) < 3) {
		writeError(w, 400, "Aksi workflow tidak valid", nil)
		return
	}
	id := r.PathValue("id")
	var organizer, status, title, meetingNumber, startAt, endAt, location string
	if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT m.organizer_id,m.status,m.title,m.meeting_number,m.start_at,m.end_at,coalesce(r.name,oroom.name,m.external_location,'') FROM meetings m LEFT JOIN rooms r ON r.id=m.room_id LEFT JOIN rooms oroom ON oroom.id=m.online_resource_id WHERE m.id=?`), id).Scan(&organizer, &status, &title, &meetingNumber, &startAt, &endAt, &location); err != nil {
		writeError(w, 404, "Rapat tidak ditemukan", nil)
		return
	}
	approvalDefault := u.Role == "ADMIN" || u.Role == "APPROVER" && departmentShortContainsCSR(r, s, u)
	approvalAccess := containsPage(u, "approval", approvalDefault)
	manager := approvalAccess
	if (action == "approve" || action == "reject") && !approvalAccess {
		writeError(w, 403, "Akses approval tidak diberikan untuk akun ini", nil)
		return
	}
	if (action == "approve" || action == "reject") && organizer == u.ID {
		var val string
		_ = s.store.DB().QueryRowContext(r.Context(), `SELECT value FROM settings WHERE key='allowSelfApproval'`).Scan(&val)
		if val != "true" {
			writeError(w, 403, "Pengaju tidak boleh menyetujui atau menolak rapatnya sendiri", nil)
			return
		}
	}
	if action == "cancel" && !manager && !(u.Role == "ORGANIZER" && organizer == u.ID) {
		writeError(w, 403, "Hanya Super User, Senior Manager, atau Sekretariat pengaju yang dapat membatalkan", nil)
		return
	}
	if status != "SUBMITTED" && !(action == "cancel" && status == "APPROVED") {
		writeError(w, 409, "Transisi status tidak diizinkan", nil)
		return
	}
	nextStatus := map[string]string{"approve": "APPROVED", "reject": "REJECTED", "cancel": "CANCELLED"}[action]
	mailConfig, err := s.loadMailConfig(r)
	if err != nil {
		writeError(w, 500, "Gagal memuat pengaturan email", nil)
		return
	}
	err = s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		stamp := now()
		var result sql.Result
		var err error
		if action == "cancel" {
			result, err = tx.ExecContext(r.Context(), s.store.Q(`UPDATE meetings SET status=?,cancelled_at=?,updated_at=? WHERE id=? AND status=?`), nextStatus, stamp, stamp, id, status)
		} else {
			result, err = tx.ExecContext(r.Context(), s.store.Q(`UPDATE meetings SET status=?,reviewed_by_id=?,reviewed_at=?,approved_at=?,cancelled_at=NULL,rejection_reason=?,updated_at=? WHERE id=? AND status=?`), nextStatus, u.ID, stamp, nullable(map[bool]string{true: stamp, false: ""}[action == "approve"]), nullable(map[bool]string{true: asString(b, "reason"), false: ""}[action == "reject"]), stamp, id, status)
		}
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return errTransition
		}
		reason := asString(b, "reason")
		if action != "cancel" {
			decision := "REJECTED"
			if action == "approve" {
				decision = "APPROVED"
			}
			if _, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO meeting_approvals(id,meeting_id,approver_id,decision,comment,decided_at) VALUES(?,?,?,?,?,?)`), newID(), id, u.ID, decision, nullable(reason), stamp); err != nil {
				return err
			}
		}
		if err := txAudit(s.store, r, tx, u, strings.ToUpper(action), "Meeting", id, map[string]any{"reason": reason}); err != nil {
			return err
		}
		var organizerEmail string
		if err := tx.QueryRowContext(r.Context(), s.store.Q(`SELECT email FROM users WHERE id=? AND active=1`), organizer).Scan(&organizerEmail); err != nil {
			return err
		}
		attendeeRows, err := tx.QueryContext(r.Context(), s.store.Q(`SELECT u.id,u.email FROM attendance a JOIN users u ON u.id=a.user_id WHERE a.meeting_id=? AND u.active=1`), id)
		if err != nil {
			return err
		}
		attendeeIDs, attendeeEmails := []string{}, []string{}
		for attendeeRows.Next() {
			var attendeeID, attendeeEmail string
			if err := attendeeRows.Scan(&attendeeID, &attendeeEmail); err != nil {
				attendeeRows.Close()
				return err
			}
			attendeeIDs = append(attendeeIDs, attendeeID)
			attendeeEmails = append(attendeeEmails, attendeeEmail)
		}
		if err := attendeeRows.Err(); err != nil {
			attendeeRows.Close()
			return err
		}
		attendeeRows.Close()
		guestEmails := []string{}
		if action == "approve" || action == "cancel" {
			guestRows, err := tx.QueryContext(r.Context(), s.store.Q(`SELECT email FROM meeting_guests WHERE meeting_id=? AND email IS NOT NULL AND email<>''`), id)
			if err != nil {
				return err
			}
			for guestRows.Next() {
				var email string
				if err := guestRows.Scan(&email); err != nil {
					guestRows.Close()
					return err
				}
				guestEmails = append(guestEmails, email)
			}
			if err := guestRows.Err(); err != nil {
				guestRows.Close()
				return err
			}
			guestRows.Close()
		}
		stampBody := fmt.Sprintf("Rapat %s (%s) · %s · %s · %s.", title, meetingNumber, startAt, endAt, location)
		statusRecipients := []string{organizerEmail}
		statusUserIDs := []string{organizer}
		if action == "cancel" {
			statusRecipients = append(statusRecipients, attendeeEmails...)
			statusRecipients = append(statusRecipients, guestEmails...)
			statusUserIDs = append(statusUserIDs, attendeeIDs...)
		}
		for _, userID := range unique(statusUserIDs) {
			if _, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO notifications(id,user_id,title,body,created_at) VALUES(?,?,?,?,?)`), newID(), userID, "Rapat "+strings.ToLower(nextStatus), stampBody, now()); err != nil {
				return err
			}
		}
		values := map[string]string{"status": nextStatus, "reason": asString(b, "reason"), "meetingDetails": stampBody}
		if action == "cancel" {
			values["cancellationNotice"] = "Rapat telah dibatalkan."
		}
		if err := s.enqueueTemplateTx(r.Context(), tx, mailConfig, "status", id+":"+nextStatus, title, meetingNumber, statusRecipients, values); err != nil {
			return err
		}
		if action == "approve" {
			if err := s.enqueueTemplateTx(r.Context(), tx, mailConfig, "invite", id+":approved", title, meetingNumber, append(attendeeEmails, guestEmails...), map[string]string{"status": nextStatus, "meetingDetails": stampBody}); err != nil {
				return err
			}
			if mailConfig.FacilitiesEmail != "" {
				consumptionRows, err := tx.QueryContext(r.Context(), s.store.Q(`SELECT item_type,quantity,unit FROM meeting_consumptions WHERE meeting_id=?`), id)
				if err != nil {
					return err
				}
				consumptions := []string{}
				for consumptionRows.Next() {
					var itemType, unit string
					var quantity int
					if err := consumptionRows.Scan(&itemType, &quantity, &unit); err != nil {
						consumptionRows.Close()
						return err
					}
					consumptions = append(consumptions, fmt.Sprintf("%s: %d %s", itemType, quantity, unit))
				}
				if err := consumptionRows.Err(); err != nil {
					consumptionRows.Close()
					return err
				}
				consumptionRows.Close()
				if len(consumptions) > 0 {
					values := map[string]string{"status": nextStatus, "meetingDetails": stampBody, "consumption": strings.Join(consumptions, "\n")}
					if err := s.enqueueTemplateTx(r.Context(), tx, mailConfig, "catering", id+":approved", title, meetingNumber, []string{mailConfig.FacilitiesEmail}, values); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if errors.Is(err, errTransition) {
		writeError(w, 409, "Transisi status tidak diizinkan", nil)
		return
	}
	if err != nil {
		writeError(w, 500, "Gagal memproses workflow", nil)
		return
	}
	item, err := s.meetingJSON(r, id, true)
	if err != nil {
		writeError(w, 500, "Workflow tersimpan tetapi detail rapat gagal dimuat", nil)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) availability(w http.ResponseWriter, r *http.Request) {
	from, to, err := validDateRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil || to.Sub(from) > 90*24*time.Hour {
		writeError(w, 400, "Rentang ketersediaan tidak valid", nil)
		return
	}

	kind := r.URL.Query().Get("kind")
	minCapacity := 0
	_, _ = fmt.Sscanf(r.URL.Query().Get("minCapacity"), "%d", &minCapacity)
	query := `SELECT id,name,description,kind,online_platform,capacity,location,facilities,breakout_rooms,image_url,active,created_at,updated_at FROM rooms WHERE active=1`
	args := []any{}
	if validEnum(kind, "PHYSICAL", "ONLINE") {
		query += ` AND kind=?`
		args = append(args, kind)
	}
	if minCapacity > 0 {
		query += ` AND capacity>=?`
		args = append(args, minCapacity)
	}
	query += ` ORDER BY name`
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(query), args...)
	if err != nil {
		writeError(w, 500, "Gagal memuat ketersediaan", nil)
		return
	}
	type availableRoom struct {
		id, name, kind, facilities, created, updated string
		description, platform, location, image       sql.NullString
		capacity, breakout, active                   int
	}
	available := []availableRoom{}
	for rows.Next() {
		var room availableRoom
		if err := rows.Scan(&room.id, &room.name, &room.description, &room.kind, &room.platform, &room.capacity, &room.location, &room.facilities, &room.breakout, &room.image, &room.active, &room.created, &room.updated); err != nil {
			rows.Close()
			writeError(w, 500, "Gagal membaca daftar ruangan", nil)
			return
		}
		available = append(available, room)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(w, 500, "Gagal membaca daftar ruangan", nil)
		return
	}
	rows.Close()
	rooms := []any{}
	for _, room := range available {
		id, name, roomKind, capacity, location := room.id, room.name, room.kind, room.capacity, room.location
		meetings, onlineMeetings := []any{}, []any{}
		bookingColumn := "room_id"
		if roomKind == "ONLINE" {
			bookingColumn = "online_resource_id"
		}
		statusFilter := "m.status IN ('SUBMITTED','APPROVED')"
		if _, authenticated := userFrom(r); !authenticated {
			statusFilter = "m.status='APPROVED' AND m.confidential=0"
		}
		brows, queryErr := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT m.id,m.title,m.start_at,m.end_at,m.status,m.confidential,m.breakout_room_count,d.name FROM meetings m JOIN departments d ON d.id=m.department_id WHERE m.`+bookingColumn+`=? AND `+statusFilter+` AND m.start_at<? AND m.end_at>? ORDER BY m.start_at`), id, to.UTC().Format(time.RFC3339Nano), from.UTC().Format(time.RFC3339Nano))
		if queryErr != nil {
			writeError(w, 500, "Gagal memuat jadwal ruangan", nil)
			return
		}
		for brows != nil && brows.Next() {
			var mid, title, start, end, status, department string
			var confidential, breakoutCount int
			if brows.Scan(&mid, &title, &start, &end, &status, &confidential, &breakoutCount, &department) == nil {
				if confidential != 0 {
					title = "Rapat rahasia"
					mid = fmt.Sprintf("busy-%s-%d", id, parseUnixMillis(start))
					department = "Waktu terpakai"
					breakoutCount = 0
				}
				entry := map[string]any{"id": mid, "title": title, "startAt": start, "endAt": end, "status": status, "isConfidential": confidential != 0, "department": map[string]string{"name": department}, "breakoutRoomCount": breakoutCount}
				if roomKind == "ONLINE" {
					onlineMeetings = append(onlineMeetings, entry)
				} else {
					meetings = append(meetings, entry)
				}
			} else {
				brows.Close()
				writeError(w, 500, "Gagal membaca jadwal ruangan", nil)
				return
			}
		}
		if brows != nil {
			if err := brows.Err(); err != nil {
				brows.Close()
				writeError(w, 500, "Gagal membaca jadwal ruangan", nil)
				return
			}
			brows.Close()
		}
		bookings := meetings
		if roomKind == "ONLINE" {
			bookings = onlineMeetings
		}
		zoomSlots, err := s.availabilityZoomSlots(r, id, from, to)
		if err != nil {
			writeError(w, 500, "Gagal memuat jadwal breakout room", nil)
			return
		}
		rooms = append(rooms, map[string]any{"id": id, "name": name, "description": nullableOut(room.description), "kind": roomKind, "onlinePlatform": nullableOut(room.platform), "capacity": capacity, "location": nullableOut(location), "facilities": decodeList(room.facilities), "breakoutRooms": room.breakout, "imageUrl": nullableOut(room.image), "active": room.active != 0, "createdAt": room.created, "updatedAt": room.updated, "bookings": bookings, "meetings": meetings, "onlineMeetings": onlineMeetings, "zoomBreakoutRooms": zoomSlots, "available": len(bookings) == 0})
	}
	writeJSON(w, 200, map[string]any{"from": from.UTC(), "to": to.UTC(), "rooms": rooms})
}

func parseUnixMillis(value string) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return parsed.UnixMilli()
}

func (s *Server) availabilityZoomSlots(r *http.Request, resourceID string, from, to time.Time) ([]any, error) {
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT id,name,capacity FROM zoom_breakout_rooms WHERE resource_id=? AND active=1 ORDER BY name`), resourceID)
	if err != nil {
		return nil, err
	}
	type slot struct {
		id, name string
		capacity int
	}
	slots := []slot{}
	for rows.Next() {
		var item slot
		if err := rows.Scan(&item.id, &item.name, &item.capacity); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan breakout room")
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := []any{}
	for _, item := range slots {
		bookings := []any{}
		bookRows, queryErr := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT m.id,m.title,m.start_at,m.end_at,m.status,m.confidential,m.breakout_room_count,d.name FROM meeting_zoom_breakout_bookings b JOIN meetings m ON m.id=b.meeting_id JOIN departments d ON d.id=m.department_id WHERE b.breakout_room_id=? AND m.status IN ('SUBMITTED','APPROVED') AND m.start_at<? AND m.end_at>? ORDER BY m.start_at`), item.id, to.UTC().Format(time.RFC3339Nano), from.UTC().Format(time.RFC3339Nano))
		if queryErr != nil {
			return nil, queryErr
		}
		for bookRows.Next() {
			var id, title, start, end, status, department string
			var confidential, breakoutCount int
			if err := bookRows.Scan(&id, &title, &start, &end, &status, &confidential, &breakoutCount, &department); err == nil {
				if confidential != 0 {
					id, title, department, breakoutCount = fmt.Sprintf("busy-%s-%d", item.id, parseUnixMillis(start)), "Rapat rahasia", "Waktu terpakai", 0
				}
				bookings = append(bookings, map[string]any{"id": id, "title": title, "startAt": start, "endAt": end, "status": status, "isConfidential": confidential != 0, "department": map[string]string{"name": department}, "breakoutRoomCount": breakoutCount})
			} else {
				bookRows.Close()
				return nil, err
			}
		}
		bookErr := bookRows.Err()
		bookRows.Close()
		if bookErr != nil {
			return nil, bookErr
		}
		out = append(out, map[string]any{"id": item.id, "name": item.name, "capacity": item.capacity, "bookings": bookings})
	}
	return out, nil
}

func (s *Server) publicSchedule(w http.ResponseWriter, r *http.Request) {
	from, end := time.Now(), time.Now().Add(7*24*time.Hour)
	customRange := r.URL.Query().Get("from") != "" || r.URL.Query().Get("to") != ""
	if customRange {
		var err error
		from, end, err = validDateRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
		if err != nil || end.Sub(from) > 370*24*time.Hour {
			writeError(w, 400, "Rentang jadwal publik tidak valid", nil)
			return
		}
	}
	query := `SELECT m.id,m.title,m.description,m.start_at,m.end_at,m.status,m.method,m.expected_participants,m.notes,m.breakout_room_count,d.id,d.name,d.short_name,d.color,r.id,r.name,oroom.id,oroom.name,m.external_location FROM meetings m JOIN departments d ON d.id=m.department_id LEFT JOIN rooms r ON r.id=m.room_id LEFT JOIN rooms oroom ON oroom.id=m.online_resource_id WHERE m.status='APPROVED' AND m.confidential=0 AND m.start_at<? AND m.end_at>? ORDER BY m.start_at LIMIT 1000`
	args := []any{end.UTC().Format(time.RFC3339Nano), from.UTC().Format(time.RFC3339Nano)}
	if !customRange {
		query = strings.Replace(query, ` AND m.start_at<? AND m.end_at>?`, ` AND m.end_at>? AND m.start_at<?`, 1)
		args = []any{now(), end.UTC().Format(time.RFC3339Nano)}
	}
	if departmentID := r.URL.Query().Get("departmentId"); departmentID != "" {
		query = strings.Replace(query, " ORDER BY m.start_at", " AND m.department_id=? ORDER BY m.start_at", 1)
		args = append(args, departmentID)
	}
	if method := r.URL.Query().Get("method"); validEnum(method, "OFFLINE", "ONLINE", "HYBRID") {
		query = strings.Replace(query, " ORDER BY m.start_at", " AND m.method=? ORDER BY m.start_at", 1)
		args = append(args, method)
	}
	if roomID := r.URL.Query().Get("roomId"); roomID != "" {
		query = strings.Replace(query, " ORDER BY m.start_at", " AND (m.room_id=? OR m.online_resource_id=?) ORDER BY m.start_at", 1)
		args = append(args, roomID, roomID)
	}
	if term := strings.TrimSpace(r.URL.Query().Get("q")); term != "" {
		query = strings.Replace(query, " ORDER BY m.start_at", " AND (lower(m.title) LIKE ? OR lower(d.name) LIKE ? OR lower(coalesce(r.name,'')) LIKE ? OR lower(coalesce(oroom.name,'')) LIKE ?) ORDER BY m.start_at", 1)
		like := "%" + strings.ToLower(term) + "%"
		args = append(args, like, like, like, like)
	}
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(query), args...)
	if err != nil {
		writeError(w, 500, "Gagal memuat jadwal publik", nil)
		return
	}
	type publicMeeting struct {
		id, title, start, finish, status, method, departmentID, department, shortName, color string
		description, notes, roomID, roomName, onlineID, onlineName, external                 sql.NullString
		participants, breakouts                                                              int
	}
	meetings := []publicMeeting{}
	for rows.Next() {
		var item publicMeeting
		if err := rows.Scan(&item.id, &item.title, &item.description, &item.start, &item.finish, &item.status, &item.method, &item.participants, &item.notes, &item.breakouts, &item.departmentID, &item.department, &item.shortName, &item.color, &item.roomID, &item.roomName, &item.onlineID, &item.onlineName, &item.external); err != nil {
			rows.Close()
			writeError(w, 500, "Gagal membaca jadwal publik", nil)
			return
		}
		meetings = append(meetings, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(w, 500, "Gagal membaca jadwal publik", nil)
		return
	}
	rows.Close()
	items := make([]any, 0, len(meetings))
	for _, meeting := range meetings {
		documentRows, queryErr := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT document_type FROM documents WHERE meeting_id=?`), meeting.id)
		if queryErr != nil {
			writeError(w, 500, "Gagal memuat dokumen jadwal publik", nil)
			return
		}
		documents := []any{}
		for documentRows.Next() {
			var typ string
			if err := documentRows.Scan(&typ); err != nil {
				documentRows.Close()
				writeError(w, 500, "Gagal membaca dokumen jadwal publik", nil)
				return
			}
			documents = append(documents, map[string]string{"documentType": typ})
		}
		documentErr := documentRows.Err()
		documentRows.Close()
		if documentErr != nil {
			writeError(w, 500, "Gagal membaca dokumen jadwal publik", nil)
			return
		}
		items = append(items, map[string]any{
			"id": meeting.id, "title": meeting.title, "description": nullableOut(meeting.description),
			"startAt": meeting.start, "endAt": meeting.finish, "status": meeting.status, "method": meeting.method, "isConfidential": false,
			"expectedParticipants": meeting.participants, "notes": nullableOut(meeting.notes),
			"breakoutRoomCount": meeting.breakouts, "externalLocation": nullableOut(meeting.external),
			"department": map[string]string{"id": meeting.departmentID, "name": meeting.department, "shortName": meeting.shortName, "color": meeting.color},
			"room":       optionalObject(meeting.roomID, meeting.roomName), "onlineResource": optionalObject(meeting.onlineID, meeting.onlineName),
			"documents": documents, "requirements": []any{}, "consumptions": []any{}, "breakoutRooms": []any{},
		})
	}
	announcements, err := s.publicAnnouncements(r)
	if err != nil {
		writeError(w, 500, "Gagal memuat pengumuman publik", nil)
		return
	}
	departments, err := s.publicDepartments(r)
	if err != nil {
		writeError(w, 500, "Gagal memuat bidang", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	writeJSON(w, 200, map[string]any{"generatedAt": now(), "meetings": items, "announcements": announcements, "departments": departments})
}

func optionalObject(id, name sql.NullString) any {
	if !id.Valid || !name.Valid {
		return nil
	}
	return map[string]string{"id": id.String, "name": name.String}
}

func (s *Server) publicDepartments(r *http.Request) ([]any, error) {
	rows, err := s.store.DB().QueryContext(r.Context(), `SELECT id,name,short_name,color FROM departments ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, name, shortName, color string
		if err := rows.Scan(&id, &name, &shortName, &color); err != nil {
			return nil, err
		}
		items = append(items, map[string]string{"id": id, "name": name, "shortName": shortName, "color": color})
	}
	return items, rows.Err()
}

func (s *Server) meetingDocumentUpload(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}

	id := r.PathValue("id")
	var organizer, status, endAt, meetingTitle, meetingNumber string
	if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT organizer_id,status,end_at,title,meeting_number FROM meetings WHERE id=?`), id).Scan(&organizer, &status, &endAt, &meetingTitle, &meetingNumber); err != nil {
		writeError(w, 404, "Rapat tidak ditemukan", nil)
		return
	}
	manager := u.Role == "ADMIN" || u.Role == "APPROVER" && departmentShortContainsCSR(r, s, u)
	if !manager && !(u.Role == "ORGANIZER" && organizer == u.ID) {
		writeError(w, 403, "Tidak berwenang mengunggah dokumen", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		writeError(w, 413, "Ukuran file maksimal 10 MiB", nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "File wajib diunggah", nil)
		return
	}
	defer file.Close()
	if header.Size <= 0 || header.Size > 10<<20 {
		writeError(w, 413, "Ukuran file maksimal 10 MiB", nil)
		return
	}
	mime := header.Header.Get("Content-Type")
	ext := map[string]string{"application/pdf": ".pdf", "image/png": ".png", "image/jpeg": ".jpg", "application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": ".xlsx"}[mime]
	if ext == "" || !strings.HasSuffix(strings.ToLower(header.Filename), ext) {
		writeError(w, 415, "Jenis file tidak didukung", nil)
		return
	}
	typ := r.FormValue("documentType")
	if typ == "" {
		typ = "SUPPORTING"
	}
	if !validEnum(typ, "SUPPORTING", "DOCUMENTATION", "MINUTES", "ATTENDANCE_LIST") {
		writeError(w, 400, "Kategori dokumen tidak valid", nil)
		return
	}
	if typ == "DOCUMENTATION" || typ == "MINUTES" || typ == "ATTENDANCE_LIST" {
		ends, _ := time.Parse(time.RFC3339Nano, endAt)
		if ends.After(time.Now()) {
			writeError(w, 409, "Dokumentasi, daftar hadir, dan notulensi hanya dapat diunggah setelah waktu rapat selesai", nil)
			return
		}
	}
	var organizerEmail string
	if err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT email FROM users WHERE id=? AND active=1`), organizer).Scan(&organizerEmail); err != nil && err != sql.ErrNoRows {
		writeError(w, 500, "Gagal memuat penyelenggara rapat", nil)
		return
	}
	mailConfig, err := s.loadMailConfig(r)
	if err != nil {
		writeError(w, 500, "Gagal memuat pengaturan email", nil)
		return
	}
	bytes, err := io.ReadAll(io.LimitReader(file, 10<<20+1))
	if err != nil || int64(len(bytes)) > 10<<20 || !validSignature(mime, bytes) {
		writeError(w, 415, "Signature berkas tidak sesuai dengan jenis file", nil)
		return
	}
	key := newID() + ext
	if err = os.WriteFile(filepath.Join(storageDir(), key), bytes, 0600); err != nil {
		writeError(w, 500, "Gagal menyimpan berkas", nil)
		return
	}
	docID, stamp := newID(), now()
	err = s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO documents(id,meeting_id,filename,document_type,storage_key,mime_type,size,uploaded_by_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)`), docID, id, truncate(header.Filename, 180), typ, key, mime, len(bytes), u.ID, stamp)
		if err != nil {
			return err
		}
		if organizerEmail != "" && (typ == "DOCUMENTATION" || typ == "ATTENDANCE_LIST") {
			body := fmt.Sprintf("%s diunggah untuk rapat %s (%s). Evidence menunggu verifikasi administrator.", typ, meetingTitle, meetingNumber)
			if _, err := tx.ExecContext(r.Context(), s.store.Q(`INSERT INTO notifications(id,user_id,title,body,created_at) VALUES(?,?,?,?,?)`), newID(), organizer, "Evidence rapat diunggah", body, stamp); err != nil {
				return err
			}
			if err := s.enqueueEventTx(r.Context(), tx, mailConfig, "evidenceUploaded", id+":"+docID, meetingTitle, meetingNumber, []string{organizerEmail}); err != nil {
				return err
			}
		}
		return txAudit(s.store, r, tx, u, "UPLOAD", "Document", docID, map[string]any{"meetingId": id, "size": len(bytes)})
	})
	if err != nil {
		_ = os.Remove(filepath.Join(storageDir(), key))
		writeError(w, 500, "Gagal menyimpan dokumen", nil)
		return
	}
	writeJSON(w, 201, map[string]any{"id": docID, "meetingId": id, "filename": truncate(header.Filename, 180), "documentType": typ, "storageKey": key, "mimeType": mime, "size": len(bytes), "uploadedById": u.ID, "createdAt": stamp, "verifiedAt": nil})
}

func validSignature(mime string, b []byte) bool {
	switch mime {
	case "application/pdf":
		return len(b) >= 5 && string(b[:5]) == "%PDF-"
	case "image/png":
		return len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n"
	case "image/jpeg":
		return len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff
	default:
		return len(b) >= 4 && b[0] == 'P' && b[1] == 'K' && b[2] == 3 && b[3] == 4
	}
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func (s *Server) documentDownload(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	var key, filename, mime, organizer string
	var size int
	err := s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT d.storage_key,d.filename,d.mime_type,d.size,m.organizer_id FROM documents d JOIN meetings m ON m.id=d.meeting_id WHERE d.id=?`), r.PathValue("id")).Scan(&key, &filename, &mime, &size, &organizer)
	if err != nil {
		writeError(w, 404, "Dokumen tidak ditemukan", nil)
		return
	}
	if !s.meetingManager(r, u) && organizer != u.ID {
		var count int
		_ = s.store.DB().QueryRowContext(r.Context(), s.store.Q(`SELECT count(*) FROM attendance a JOIN documents d ON d.meeting_id=a.meeting_id WHERE d.id=? AND a.user_id=?`), r.PathValue("id"), u.ID).Scan(&count)
		if count == 0 {
			writeError(w, 403, "Anda tidak berhak mengakses dokumen rapat ini", nil)
			return
		}
	}
	content, err := os.ReadFile(filepath.Join(storageDir(), filepath.Base(key)))
	if err != nil {
		writeError(w, 410, "Berkas fisik tidak tersedia", nil)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", fmt.Sprint(size))
	w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''`+url.PathEscape(filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(content)
}

func (s *Server) publicAnnouncements(r *http.Request) ([]any, error) {
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(`SELECT id,type,title,body,image_storage_key,published_at,expires_at,target_roles,target_department_ids FROM announcements WHERE expires_at IS NULL OR expires_at>? ORDER BY published_at DESC LIMIT 1000`), now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, typ, title, body, published string
		var image, expiry sql.NullString
		var targetRoles, targetDepartments string
		if err := rows.Scan(&id, &typ, &title, &body, &image, &published, &expiry, &targetRoles, &targetDepartments); err != nil {
			return nil, err
		}
		if targetDepartments != "[]" {
			continue
		}
		roles := decodeList(targetRoles)
		if len(roles) != 0 && !(len(roles) == 4 && containsString(roles, "ADMIN") && containsString(roles, "APPROVER") && containsString(roles, "ORGANIZER") && containsString(roles, "PARTICIPANT")) {
			continue
		}
		var imageURL any
		if image.Valid {
			imageURL = "/api/announcements/" + id + "/image"
		}
		out = append(out, map[string]any{"id": id, "type": typ, "title": title, "body": body, "publishedAt": published, "expiresAt": nullableOut(expiry), "imageUrl": imageURL})
	}
	return out, rows.Err()
}

func containsString(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}

func parseMonth(month string) (time.Time, time.Time, error) {
	start, err := time.Parse("2006-01", month)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.FixedZone("WIB", 7*3600))
	return start.UTC(), start.AddDate(0, 1, 0).UTC(), nil
}

func sortStrings(items []string) { sort.Strings(items) }
