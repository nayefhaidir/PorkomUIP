package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"uip-meeting-backend/internal/platform/sqlstore"
)

func TestMeetingWindowUsesHalfOpenValidDurations(t *testing.T) {
	now := time.Now()
	if got := meetingWindow(now.Add(time.Hour), now.Add(2*time.Hour)); got != "" {
		t.Fatalf("valid upcoming meeting rejected: %s", got)
	}
	if got := meetingWindow(now.Add(time.Hour), now.Add(time.Hour)); got == "" {
		t.Fatal("expected equal start and end to be rejected")
	}
	if got := meetingWindow(now.Add(-time.Minute), now.Add(time.Hour)); got == "" {
		t.Fatal("expected past start to be rejected")
	}
	if got := meetingWindow(now.Add(time.Hour), now.Add(26*time.Hour)); got == "" {
		t.Fatal("expected meeting longer than 24 hours to be rejected")
	}
}

func TestMeetingTitlesRejectHeaderNewlines(t *testing.T) {
	if !validMeetingTitle("Planning meeting") {
		t.Fatal("expected ordinary meeting title to be accepted")
	}
	if validMeetingTitle("Planning\r\nBcc: attacker@example.test") {
		t.Fatal("expected CR/LF in a meeting title to be rejected")
	}
}

func TestMailSubjectCannotInjectHeaders(t *testing.T) {
	subject := encodeMailSubject("Planning\r\nBcc: attacker@example.test")
	if strings.ContainsAny(subject, "\r\n") {
		t.Fatalf("encoded subject contains a header delimiter: %q", subject)
	}
}

func TestSessionCookieSecureFlagFollowsTrustedExternalScheme(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	httpRequest := httptest.NewRequest(http.MethodPost, "http://api:8080/api/auth/login", nil)
	httpRequest.Header.Set("X-Forwarded-Proto", "http")
	if secureSessionCookie(httpRequest) {
		t.Fatal("plain HTTP request behind the frontend proxy should not receive a Secure cookie")
	}
	httpsRequest := httptest.NewRequest(http.MethodPost, "http://api:8080/api/auth/login", nil)
	httpsRequest.Header.Set("X-Forwarded-Proto", "https")
	if !secureSessionCookie(httpsRequest) {
		t.Fatal("HTTPS request behind the frontend proxy must receive a Secure cookie")
	}
	directRequest := httptest.NewRequest(http.MethodPost, "http://api:8080/api/auth/login", nil)
	if !secureSessionCookie(directRequest) {
		t.Fatal("production requests without trusted proxy scheme must default to Secure cookies")
	}
}

func TestLoginThrottleDoesNotLockOutOtherUsersBehindSharedProxy(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	for _, account := range []struct {
		id, email string
	}{
		{"one", "one@example.test"},
		{"two", "two@example.test"},
	} {
		if _, err := store.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,role,page_access,active,created_at,updated_at) VALUES(?,?,?,'invalid','PARTICIPANT','[]',1,?,?)`, account.id, account.id, account.email, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: store}
	login := func(email string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"`+email+`","password":"wrong"}`))
		req.RemoteAddr = "172.20.0.3:3000"
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		s.login(recorder, req)
		return recorder.Code
	}
	for i := 0; i < 10; i++ {
		if status := login("one@example.test"); status != http.StatusUnauthorized {
			t.Fatalf("failed login %d status=%d, want 401", i+1, status)
		}
	}
	if status := login("two@example.test"); status != http.StatusUnauthorized {
		t.Fatalf("second user's failed login status=%d, want 401 rather than shared-proxy lockout", status)
	}
	if status := login("one@example.test"); status != http.StatusTooManyRequests {
		t.Fatalf("locked account status=%d, want 429", status)
	}
}

func TestConfidentialMeetingAccessAndParticipantEmailRedaction(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	for _, row := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO departments(id,name,short_name) VALUES('dept','Department','DPT')`, nil},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('organizer','Organizer','organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('attendee','Attendee','attendee@example.test','unused','PARTICIPANT','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,confidential,organizer_id,department_id,created_at,updated_at) VALUES('meeting','M-1','Confidential review',?,?,'APPROVED','OFFLINE',1,'organizer','dept',?,?)`, []any{stamp, stamp, stamp, stamp}},
		{`INSERT INTO attendance(id,meeting_id,user_id) VALUES('attendance','meeting','attendee')`, nil},
	} {
		if _, err := store.Exec(ctx, row.query, row.args...); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: store}
	get := func(user User) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/meetings/meeting", nil)
		req.SetPathValue("id", "meeting")
		req = req.WithContext(withUser(req.Context(), user))
		recorder := httptest.NewRecorder()
		s.meetingGet(recorder, req)
		return recorder
	}
	if recorder := get(User{ID: "outsider", Role: "PARTICIPANT"}); recorder.Code != http.StatusNotFound {
		t.Fatalf("outsider status=%d body=%s, want 404", recorder.Code, recorder.Body.String())
	}
	recorder := get(User{ID: "attendee", Role: "PARTICIPANT"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("attendee status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "organizer@example.test") || strings.Contains(recorder.Body.String(), "attendee@example.test") {
		t.Fatalf("participant response leaked email addresses: %s", recorder.Body.String())
	}
}

func TestMeetingListIncludesCalendarSummaryArraysWithoutDocumentStorageKeys(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	for _, row := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO departments(id,name,short_name) VALUES('dept','Department','DPT')`, nil},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('organizer','Organizer','organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,organizer_id,department_id,created_at,updated_at) VALUES('meeting','M-1','Planning',?,?,'APPROVED','OFFLINE','organizer','dept',?,?)`, []any{stamp, stamp, stamp, stamp}},
		{`INSERT INTO meeting_requirements(id,meeting_id,type) VALUES('requirement','meeting','PROJECTOR')`, nil},
		{`INSERT INTO meeting_consumptions(id,meeting_id,item_type,quantity,unit) VALUES('consumption','meeting','SNACK_BOX',4,'box')`, nil},
		{`INSERT INTO documents(id,meeting_id,filename,document_type,storage_key,mime_type,size,created_at) VALUES('document','meeting','evidence.pdf','DOCUMENTATION','private-storage-key','application/pdf',20,?)`, []any{stamp}},
		{`INSERT INTO breakout_rooms(id,meeting_id,name,capacity) VALUES('breakout','meeting','Planning',10)`, nil},
	} {
		if _, err := store.Exec(ctx, row.query, row.args...); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/meetings", nil)
	recorder := httptest.NewRecorder()
	(&Server{store: store}).meetingsList(recorder, req.WithContext(withUser(req.Context(), User{ID: "admin", Role: "ADMIN"})))
	if recorder.Code != http.StatusOK {
		t.Fatalf("meeting list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("meeting list items=%d, want 1", len(response.Items))
	}
	for _, field := range []string{"documents", "requirements", "consumptions", "breakoutRooms"} {
		items, ok := response.Items[0][field].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("%s=%v, want one summary item", field, response.Items[0][field])
		}
	}
	document, ok := response.Items[0]["documents"].([]any)[0].(map[string]any)
	if !ok {
		t.Fatalf("document summary has unexpected shape: %v", response.Items[0]["documents"])
	}
	if _, leaked := document["storageKey"]; leaked {
		t.Fatal("meeting list exposed a document storage key")
	}
}

func TestExportFormatsProduceValidFiles(t *testing.T) {
	xlsx, err := makeXLSX([]string{"Title"}, [][]string{{"Report"}})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(xlsx), int64(len(xlsx)))
	if err != nil {
		t.Fatalf("invalid XLSX zip: %v", err)
	}
	foundSheet := false
	for _, file := range reader.File {
		if file.Name == "xl/worksheets/sheet1.xml" {
			foundSheet = true
		}
	}
	if !foundSheet {
		t.Fatal("XLSX worksheet is missing")
	}
	pdf := simplePDF("Report", [][]string{{"1", "Meeting"}})
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4")) {
		t.Fatal("PDF header is missing")
	}
}

func TestConcurrentMeetingRequestsCannotDoubleBookRoom(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	if _, err := store.Exec(ctx, `INSERT INTO departments(id,name,short_name) VALUES('dept','Department','DPT')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('organizer','Organizer','organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO rooms(id,name,kind,capacity,facilities,active,created_at,updated_at) VALUES('room','Room','PHYSICAL',20,'[]',1,?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store}
	start := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	end := start.Add(time.Hour)
	body, _ := json.Marshal(map[string]any{
		"title": "Planning meeting", "startAt": start.Format(time.RFC3339), "endAt": end.Format(time.RFC3339),
		"roomId": "room", "method": "OFFLINE", "departmentId": "dept", "expectedParticipants": 1,
	})
	user := User{ID: "organizer", Role: "ORGANIZER", DepartmentID: stringPointer("dept")}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/meetings", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			s.meetingsCreate(recorder, req.WithContext(withUser(req.Context(), user)))
			codes <- recorder.Code
		}()
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		switch code {
		case http.StatusCreated:
			success++
		case http.StatusConflict:
			conflict++
		default:
			t.Errorf("unexpected response status %d", code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("got %d successful requests and %d conflicts; want one of each", success, conflict)
	}
}

func TestParticipantOverlapOptInDoesNotAllowResourceConflict(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	for _, row := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO departments(id,name,short_name) VALUES('dept','Department','DPT')`, nil},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('existing-organizer','Existing Organizer','existing@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('new-organizer','New Organizer','new@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('second-organizer','Second Organizer','second@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('third-organizer','Third Organizer','third@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('room-organizer','Room Organizer','room@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('attendee','Attendee','attendee@example.test','unused','PARTICIPANT','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('attendee-organizer','Attendee Organizer','attendee-organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('busy-organizer','Busy Organizer','busy-organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO rooms(id,name,kind,capacity,facilities,active,created_at,updated_at) VALUES('busy-room','Busy Room','PHYSICAL',20,'[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO rooms(id,name,kind,capacity,facilities,active,created_at,updated_at) VALUES('available-room','Available Room','PHYSICAL',20,'[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO rooms(id,name,kind,capacity,facilities,active,created_at,updated_at) VALUES('second-room','Second Room','PHYSICAL',20,'[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO rooms(id,name,kind,capacity,facilities,active,created_at,updated_at) VALUES('third-room','Third Room','PHYSICAL',20,'[]',1,?,?)`, []any{stamp, stamp}},
	} {
		if _, err := store.Exec(ctx, row.query, row.args...); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	end := start.Add(time.Hour)
	if _, err := store.Exec(ctx, `INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,organizer_id,department_id,room_id,expected_participants,created_at,updated_at) VALUES('existing','M-1','Existing',?,?,'APPROVED','OFFLINE','existing-organizer','dept','busy-room',2,?,?)`, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO attendance(id,meeting_id,user_id) VALUES('existing-attendance','existing','attendee')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO attendance(id,meeting_id,user_id) VALUES('organizer-attendance','existing','busy-organizer')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,organizer_id,department_id,room_id,expected_participants,created_at,updated_at) VALUES('organizer-existing','M-2','Organizer existing',?,?,'APPROVED','OFFLINE','attendee-organizer','dept',NULL,1,?,?)`, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), stamp, stamp); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: store}
	create := func(userID, roomID string, allowOverlap bool, attendeeIDs []string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]any{
			"title": "Planning meeting", "startAt": start.Format(time.RFC3339), "endAt": end.Format(time.RFC3339),
			"roomId": roomID, "method": "OFFLINE", "departmentId": "dept", "expectedParticipants": len(attendeeIDs) + 1,
			"attendeeIds": attendeeIDs, "allowParticipantOverlap": allowOverlap,
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/meetings", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.meetingsCreate(recorder, req.WithContext(withUser(req.Context(), User{ID: userID, Role: "ORGANIZER", DepartmentID: stringPointer("dept")})))
		return recorder
	}
	if recorder := create("new-organizer", "available-room", false, []string{"attendee"}); recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "Peserta memiliki jadwal") {
		t.Fatalf("participant conflict status=%d body=%s, want conflict guidance", recorder.Code, recorder.Body.String())
	}
	allowed := create("new-organizer", "available-room", true, []string{"attendee"})
	if allowed.Code != http.StatusCreated {
		t.Fatalf("overlap opt-in status=%d body=%s, want 201", allowed.Code, allowed.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(allowed.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	var persisted int
	if err := store.DB().QueryRowContext(ctx, `SELECT allow_participant_overlap FROM meetings WHERE id=?`, created["id"]).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 1 {
		t.Fatalf("allow_participant_overlap=%d, want persisted opt-in", persisted)
	}
	if recorder := create("second-organizer", "second-room", true, []string{"attendee"}); recorder.Code != http.StatusCreated {
		t.Fatalf("second allowed participant overlap status=%d body=%s, want 201", recorder.Code, recorder.Body.String())
	}
	if recorder := create("busy-organizer", "third-room", true, nil); recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "Jadwal bertabrakan") {
		t.Fatalf("organizer attendee conflict status=%d body=%s, want conflict despite participant opt-in", recorder.Code, recorder.Body.String())
	}
	if recorder := create("third-organizer", "third-room", true, []string{"attendee-organizer"}); recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "Jadwal bertabrakan") {
		t.Fatalf("invited organizer conflict status=%d body=%s, want conflict despite participant opt-in", recorder.Code, recorder.Body.String())
	}
	editStart := start.Add(2 * time.Hour)
	editEnd := end.Add(2 * time.Hour)
	if _, err := store.Exec(ctx, `INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,organizer_id,department_id,room_id,expected_participants,allow_participant_overlap,created_at,updated_at) VALUES('editable','M-3','Editable',?,?,'SUBMITTED','OFFLINE','third-organizer','dept','third-room',2,1,?,?)`, editStart.Format(time.RFC3339Nano), editEnd.Format(time.RFC3339Nano), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO attendance(id,meeting_id,user_id) VALUES('editable-attendance','editable','attendee-organizer')`); err != nil {
		t.Fatal(err)
	}
	editBody, err := json.Marshal(map[string]any{"startAt": start.Format(time.RFC3339), "endAt": end.Format(time.RFC3339), "allowParticipantOverlap": true})
	if err != nil {
		t.Fatal(err)
	}
	editRequest := httptest.NewRequest(http.MethodPatch, "/api/meetings/editable", bytes.NewReader(editBody))
	editRequest.SetPathValue("id", "editable")
	editRequest.Header.Set("Content-Type", "application/json")
	editResponse := httptest.NewRecorder()
	server.meetingPatch(editResponse, editRequest.WithContext(withUser(editRequest.Context(), User{ID: "third-organizer", Role: "ORGANIZER"})))
	if editResponse.Code != http.StatusConflict || !strings.Contains(editResponse.Body.String(), "Jadwal bertabrakan") {
		t.Fatalf("editing into invited-organizer conflict status=%d body=%s, want conflict despite participant opt-in", editResponse.Code, editResponse.Body.String())
	}
	if recorder := create("room-organizer", "busy-room", true, nil); recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "Jadwal bertabrakan") {
		t.Fatalf("resource conflict status=%d body=%s, want resource conflict despite participant opt-in", recorder.Code, recorder.Body.String())
	}
}

func TestTeamsRoomRejectsBreakoutConfiguration(t *testing.T) {
	store, err := sqlstore.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := &Server{store: store}
	body := strings.NewReader(`{"name":"Teams account","kind":"ONLINE","onlinePlatform":"TEAMS","capacity":100,"breakoutRooms":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/rooms", body)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.roomsCreate(recorder, req.WithContext(withUser(req.Context(), User{ID: "admin", Role: "ADMIN"})))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "tidak mendukung breakout") {
		t.Fatalf("Teams resource status=%d body=%s, want unsupported breakout rejection", recorder.Code, recorder.Body.String())
	}
	stamp := now()
	if _, err := store.Exec(context.Background(), `INSERT INTO rooms(id,name,kind,online_platform,capacity,facilities,breakout_rooms,active,created_at,updated_at) VALUES('legacy-teams','Legacy Teams','ONLINE','TEAMS',100,'[]',5,1,?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/rooms/legacy-teams", strings.NewReader(`{"active":false}`))
	patchReq.SetPathValue("id", "legacy-teams")
	patchRecorder := httptest.NewRecorder()
	server.roomPatch(patchRecorder, patchReq.WithContext(withUser(patchReq.Context(), User{ID: "admin", Role: "ADMIN"})))
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("legacy Teams room update status=%d body=%s, want 200", patchRecorder.Code, patchRecorder.Body.String())
	}
	var persistedBreakouts int
	if err := store.DB().QueryRowContext(context.Background(), `SELECT breakout_rooms FROM rooms WHERE id='legacy-teams'`).Scan(&persistedBreakouts); err != nil {
		t.Fatal(err)
	}
	if persistedBreakouts != 0 {
		t.Fatalf("legacy Teams breakout configuration=%d, want disabled", persistedBreakouts)
	}
}

func TestEvidenceReminderWorkerDoesNotCloseReservation(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339Nano)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO departments(id,name,short_name) VALUES('dept','Department','DPT')`, nil},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('organizer','Organizer','organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,organizer_id,department_id,created_at,updated_at) VALUES('meeting','M-1','Review',?,?,'APPROVED','OFFLINE','organizer','dept',?,?)`, []any{stamp, stamp, stamp, stamp}},
		{`INSERT INTO documents(id,meeting_id,filename,document_type,storage_key,mime_type,size,verified_at,created_at) VALUES('doc','meeting','doc.pdf','DOCUMENTATION','doc.pdf','application/pdf',10,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO documents(id,meeting_id,filename,document_type,storage_key,mime_type,size,verified_at,created_at) VALUES('attendance','meeting','attendance.pdf','ATTENDANCE_LIST','attendance.pdf','application/pdf',10,?,?)`, []any{stamp, stamp}},
	} {
		if _, err := store.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&Server{store: store}).processEvidenceReminders(ctx, mailConfig{EvidenceReminderHours: []int{1}}); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := store.DB().QueryRowContext(ctx, `SELECT status FROM meetings WHERE id='meeting'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "APPROVED" {
		t.Fatalf("meeting status=%q, want APPROVED until explicit verification closes reservation", status)
	}
}

func TestSQLiteFileURLRetainsDataAcrossConnections(t *testing.T) {
	path := "backend_persistence_test.sqlite"
	defer os.Remove(path)
	defer os.Remove(path + "-shm")
	defer os.Remove(path + "-wal")
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO departments(id,name,short_name) VALUES('persistent','Persistent','P')`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlstore.Open(ctx, "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err := store.DB().QueryRow(`SELECT count(*) FROM departments WHERE id='persistent'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persistent row count=%d, want 1", count)
	}
}

func TestManagementOverviewReadsDocumentArchiveFromGoStore(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO departments(id,name,short_name) VALUES('dept','Department','DPT')`, nil},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('admin','Admin','admin@example.test','unused','ADMIN','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,organizer_id,department_id,created_at,updated_at) VALUES('meeting','M-1','Quarterly review',?,?, 'APPROVED','OFFLINE','admin','dept',?,?)`, []any{stamp, stamp, stamp, stamp}},
		{`INSERT INTO documents(id,meeting_id,filename,document_type,storage_key,mime_type,size,created_at) VALUES('document','meeting','minutes.pdf','DOCUMENTATION','minutes.pdf','application/pdf',10,?)`, []any{stamp}},
	} {
		if _, err := store.Exec(ctx, query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: store}
	req := httptest.NewRequest(http.MethodGet, "/api/management?page=1&pageSize=50&q=Quarterly&type=DOCUMENTATION", nil)
	req = req.WithContext(withUser(req.Context(), User{ID: "admin", Role: "ADMIN"}))
	recorder := httptest.NewRecorder()
	s.managementOverview(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Total     int `json:"total"`
		Documents []struct {
			Meeting struct {
				Title string `json:"title"`
			} `json:"meeting"`
		} `json:"documents"`
		DocumentCounts map[string]int `json:"documentCounts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Documents) != 1 || response.Documents[0].Meeting.Title != "Quarterly review" {
		t.Fatalf("unexpected management archive response: %+v", response)
	}
	if response.DocumentCounts["DOCUMENTATION"] != 1 {
		t.Fatalf("documentation count=%d, want 1", response.DocumentCounts["DOCUMENTATION"])
	}
}

func TestPublicScheduleHonorsDateRangeAndIncludesPublicAnnouncements(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	start := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	end := start.Add(time.Hour)
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO departments(id,name,short_name,color) VALUES('dept','Department','DPT','#123456')`, nil},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('organizer','Organizer','organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,organizer_id,department_id,created_at,updated_at) VALUES('meeting','M-1','Public review',?,?,'APPROVED','OFFLINE','organizer','dept',?,?)`, []any{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,confidential,organizer_id,department_id,created_at,updated_at) VALUES('secret-meeting','M-2','Private review',?,?,'APPROVED','OFFLINE',1,'organizer','dept',?,?)`, []any{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), stamp, stamp}},
		{`INSERT INTO announcements(id,type,title,body,published_at) VALUES('announcement','GENERAL','Notice','Public notice',?)`, []any{stamp}},
	} {
		if _, err := store.Exec(ctx, query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: store}
	from, to := start.Add(-time.Minute), end.Add(time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/public/schedule?from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339), nil)
	recorder := httptest.NewRecorder()
	s.publicSchedule(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Meetings []struct {
			ID         string `json:"id"`
			Department struct {
				ShortName string `json:"shortName"`
			} `json:"department"`
		} `json:"meetings"`
		Announcements []struct {
			Type string `json:"type"`
		} `json:"announcements"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Meetings) != 1 || response.Meetings[0].ID != "meeting" || response.Meetings[0].Department.ShortName != "DPT" {
		t.Fatalf("unexpected public meeting response: %+v", response.Meetings)
	}
	if len(response.Announcements) != 1 || response.Announcements[0].Type != "GENERAL" {
		t.Fatalf("unexpected public announcements: %+v", response.Announcements)
	}
}

func TestAvailabilityMasksConfidentialMeetingDetails(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	start := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	end := start.Add(time.Hour)
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO departments(id,name,short_name) VALUES('dept','Sensitive Department','SENSITIVE')`, nil},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('organizer','Organizer','organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO rooms(id,name,kind,capacity,facilities,active,created_at,updated_at) VALUES('room','Room','PHYSICAL',20,'[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,confidential,organizer_id,department_id,room_id,created_at,updated_at) VALUES('meeting','M-1','Highly private review',?,?,'APPROVED','OFFLINE',1,'organizer','dept','room',?,?)`, []any{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), stamp, stamp}},
	} {
		if _, err := store.Exec(ctx, query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: store}
	from, to := start.Add(-time.Minute), end.Add(time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/availability?from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339), nil)
	recorder := httptest.NewRecorder()
	s.availability(recorder, req.WithContext(withUser(req.Context(), User{ID: "viewer", Role: "PARTICIPANT"})))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Rooms []struct {
			Meetings []struct {
				Title          string `json:"title"`
				IsConfidential bool   `json:"isConfidential"`
				Department     struct {
					Name string `json:"name"`
				} `json:"department"`
			} `json:"meetings"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Rooms) != 1 || len(response.Rooms[0].Meetings) != 1 {
		t.Fatalf("unexpected availability response: %+v", response)
	}
	item := response.Rooms[0].Meetings[0]
	if !item.IsConfidential || item.Title != "Rapat rahasia" || item.Department.Name != "Waktu terpakai" {
		t.Fatalf("confidential meeting details were not masked: %+v", item)
	}
}

func TestPublicAvailabilityExcludesSubmittedMeetings(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	start := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	end := start.Add(time.Hour)
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO departments(id,name,short_name) VALUES('dept','Department','DPT')`, nil},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('organizer','Organizer','organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO rooms(id,name,kind,capacity,facilities,active,created_at,updated_at) VALUES('room','Room','PHYSICAL',20,'[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,confidential,organizer_id,department_id,room_id,created_at,updated_at) VALUES('pending','M-1','Pending review',?,?,'SUBMITTED','OFFLINE',0,'organizer','dept','room',?,?)`, []any{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,confidential,organizer_id,department_id,room_id,created_at,updated_at) VALUES('approved','M-2','Public approved meeting',?,?,'APPROVED','OFFLINE',0,'organizer','dept','room',?,?)`, []any{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), stamp, stamp}},
	} {
		if _, err := store.Exec(ctx, query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewHandler(store)
	from, to := start.Add(-time.Minute), end.Add(time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/availability?from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339), nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Rooms []struct {
			Meetings []struct {
				Title string `json:"title"`
			} `json:"meetings"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Rooms) != 1 || len(response.Rooms[0].Meetings) != 1 || response.Rooms[0].Meetings[0].Title != "Public approved meeting" {
		t.Fatalf("public availability disclosed pending meetings or omitted approved bookings: %+v", response)
	}
}

func stringPointer(s string) *string { return &s }
