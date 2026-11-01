package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"uip-meeting-backend/internal/platform/sqlstore"
)

func TestSMTPTargetIPRejectsNonPublicAddressesUnlessExplicitlyEnabled(t *testing.T) {
	ctx := context.Background()
	for _, host := range []string{"127.0.0.1", "100.64.0.1", "192.0.2.1", "2001:db8::1"} {
		if _, err := smtpTargetIP(ctx, host, false); err == nil {
			t.Fatalf("expected non-public SMTP target %q to be rejected by default", host)
		}
	}
	if ip, err := smtpTargetIP(ctx, "127.0.0.1", true); err != nil || !ip.IsLoopback() {
		t.Fatalf("private SMTP target should be allowed when explicitly enabled: ip=%v err=%v", ip, err)
	}
	if ip, err := smtpTargetIP(ctx, "8.8.8.8", false); err != nil || ip.String() != "8.8.8.8" {
		t.Fatalf("public SMTP target should be accepted: ip=%v err=%v", ip, err)
	}
}

func TestBuildEmailMessageEscapesHTMLAndPreventsHeaderInjection(t *testing.T) {
	message, err := buildEmailMessage(mailConfig{FromName: "UIP Portal", FromEmail: "portal@example.test"}, "user@example.test", "Meeting\r\nBcc: attacker@example.test", "<script>alert(1)</script>", "")
	if err != nil {
		t.Fatal(err)
	}
	raw := string(message)
	headers := strings.SplitN(raw, "\r\n\r\n", 2)[0]
	if strings.Contains(headers, "\r\nBcc:") {
		t.Fatalf("message contains an injected header: %q", headers)
	}
	if !strings.Contains(raw, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("HTML body was not escaped: %q", raw)
	}
}

func TestEmailFailureDiagnosticsDoNotExposeSMTPResponseText(t *testing.T) {
	err := mailFailure("MAIL_SMTP_AUTH_FAILED", &textproto.Error{Code: 535, Msg: "private SMTP response"})
	if code := emailFailureCode(err); code != "MAIL_SMTP_AUTH_FAILED" {
		t.Fatalf("failure code=%q, want stable SMTP auth code", code)
	}
	if got := emailFailureMessage(emailFailureCode(err)); strings.Contains(got, "private SMTP response") || !strings.Contains(got, "username") {
		t.Fatalf("user guidance is not safe/actionable: %q", got)
	}
	if got := emailFailureClass(err); got != "smtp_response_535" || strings.Contains(got, "private") {
		t.Fatalf("diagnostic class=%q, want sanitized SMTP response class", got)
	}
	if got := emailFailureMessage("MAIL_SMTP_TIMEOUT"); !strings.Contains(got, "firewall") {
		t.Fatalf("timeout guidance=%q, want network troubleshooting guidance", got)
	}
}

func TestLoadMailConfigRejectsMalformedStoredJSON(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Exec(ctx, `INSERT INTO settings(key,value,updated_at) VALUES('emailConfig','{"enabled":',?)`, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Server{store: store}).loadMailConfigContext(ctx); err == nil {
		t.Fatal("malformed persisted email configuration must not silently fall back to defaults")
	}
}

func TestAnnouncementTargetRequiresBothRoleAndDepartmentMatch(t *testing.T) {
	roles := []string{"ORGANIZER"}
	departments := []string{"dept-csr"}
	if !announcementTargetsMatch("ORGANIZER", "dept-csr", roles, departments) {
		t.Fatal("expected exact role and department match")
	}
	if announcementTargetsMatch("APPROVER", "dept-csr", roles, departments) {
		t.Fatal("unexpected role match")
	}
	if announcementTargetsMatch("ORGANIZER", "dept-finance", roles, departments) {
		t.Fatal("unexpected department match")
	}
}

func TestEmailConfigIsRestrictedToAdministrators(t *testing.T) {
	store, err := sqlstore.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := &Server{store: store}
	req := httptest.NewRequest("GET", "/api/email/config", nil)
	req = req.WithContext(withUser(req.Context(), User{ID: "organizer", Role: "ORGANIZER"}))
	recorder := httptest.NewRecorder()
	s.emailConfigGet(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
}

func TestOutboxClaimsAreExclusive(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	if _, err := store.Exec(ctx, `INSERT INTO email_outbox(id,event_key,recipient,subject,text_body,html_body,status,available_at,created_at,updated_at) VALUES('mail','status:meeting:user@example.test','user@example.test','subject','text','html','PENDING',?,?,?)`, stamp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store}
	first, err := s.claimMail(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.claimMail(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 0 {
		t.Fatalf("claim batch sizes=%d then %d; want 1 then 0", len(first), len(second))
	}
}

func TestFinalEmailFailureQueuesAdminAlert(t *testing.T) {
	t.Setenv("SMTP_ALLOW_PRIVATE_NETWORK", "true")
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := defaultMailConfig()
	cfg.Enabled = true
	cfg.Host = "127.0.0.1"
	cfg.Port = 2525
	cfg.Secure = true
	cfg.FromEmail = "portal@example.test"
	cfg.AdminEmail = "admin@example.test"
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	stamp := now()
	if _, err := store.Exec(ctx, `INSERT INTO settings(key,value,updated_at) VALUES('emailConfig',?,?)`, string(raw), stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO email_outbox(id,event_key,recipient,subject,text_body,html_body,status,attempts,available_at,created_at,updated_at) VALUES('failed-mail','status:meeting:user@example.test','user@example.test','subject','text','html','PENDING',7,?,?,?)`, stamp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	result, err := (&Server{store: store}).dispatchOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["failed"] != 1 {
		t.Fatalf("failed count=%d, want 1", result["failed"])
	}
	var status string
	if err := store.DB().QueryRowContext(ctx, `SELECT status FROM email_outbox WHERE id='failed-mail'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "FAILED" {
		t.Fatalf("status=%q, want FAILED", status)
	}
	var alerts int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM email_outbox WHERE event_key LIKE 'systemAlert:failed:%' AND recipient='admin@example.test'`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("system alert count=%d, want 1", alerts)
	}
}

func TestDocumentVerificationRequiresAdminAndBothEvidenceTypes(t *testing.T) {
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
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('admin','Admin','admin@example.test','unused','ADMIN','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('organizer','Organizer','organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,organizer_id,department_id,created_at,updated_at) VALUES('meeting','M-1','Review',?,?, 'APPROVED','OFFLINE','organizer','dept',?,?)`, []any{stamp, stamp, stamp, stamp}},
		{`INSERT INTO documents(id,meeting_id,filename,document_type,storage_key,mime_type,size,created_at) VALUES('documentation','meeting','documentation.pdf','DOCUMENTATION','documentation.pdf','application/pdf',10,?)`, []any{stamp}},
		{`INSERT INTO documents(id,meeting_id,filename,document_type,storage_key,mime_type,size,created_at) VALUES('attendance','meeting','attendance.pdf','ATTENDANCE_LIST','attendance.pdf','application/pdf',10,?)`, []any{stamp}},
	} {
		if _, err := store.Exec(ctx, row.query, row.args...); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: store}
	request := func(id, role string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/documents/"+id+"/verification", nil)
		req.SetPathValue("id", id)
		req = req.WithContext(withUser(req.Context(), User{ID: map[string]string{"ADMIN": "admin", "ORGANIZER": "organizer"}[role], Role: role}))
		recorder := httptest.NewRecorder()
		s.documentVerify(recorder, req)
		return recorder
	}
	if recorder := request("documentation", "ORGANIZER"); recorder.Code != http.StatusForbidden {
		t.Fatalf("non-admin verification status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
	first := request("documentation", "ADMIN")
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"meetingCompleted":false`) {
		t.Fatalf("first evidence verification status=%d body=%s", first.Code, first.Body.String())
	}
	second := request("attendance", "ADMIN")
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"meetingCompleted":true`) {
		t.Fatalf("second evidence verification status=%d body=%s", second.Code, second.Body.String())
	}
	var status string
	if err := store.DB().QueryRowContext(ctx, `SELECT status FROM meetings WHERE id='meeting'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "COMPLETED" {
		t.Fatalf("meeting status=%q, want COMPLETED", status)
	}
}

func TestDocumentVerificationRejectsEvidenceBeforeMeetingEnds(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := now()
	end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	for _, row := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO departments(id,name,short_name) VALUES('dept','Department','DPT')`, nil},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('admin','Admin','admin@example.test','unused','ADMIN','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES('organizer','Organizer','organizer@example.test','unused','ORGANIZER','dept','[]',1,?,?)`, []any{stamp, stamp}},
		{`INSERT INTO meetings(id,meeting_number,title,start_at,end_at,status,method,organizer_id,department_id,created_at,updated_at) VALUES('meeting','M-1','Review',?,?,'APPROVED','OFFLINE','organizer','dept',?,?)`, []any{stamp, end, stamp, stamp}},
		{`INSERT INTO documents(id,meeting_id,filename,document_type,storage_key,mime_type,size,created_at) VALUES('documentation','meeting','documentation.pdf','DOCUMENTATION','documentation.pdf','application/pdf',10,?)`, []any{stamp}},
	} {
		if _, err := store.Exec(ctx, row.query, row.args...); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/documents/documentation/verification", nil)
	req.SetPathValue("id", "documentation")
	recorder := httptest.NewRecorder()
	(&Server{store: store}).documentVerify(recorder, req.WithContext(withUser(req.Context(), User{ID: "admin", Role: "ADMIN"})))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "setelah waktu rapat selesai") {
		t.Fatalf("verification status=%d body=%s, want rejection before meeting end", recorder.Code, recorder.Body.String())
	}
	var verifiedAt sql.NullString
	if err := store.DB().QueryRowContext(ctx, `SELECT verified_at FROM documents WHERE id='documentation'`).Scan(&verifiedAt); err != nil {
		t.Fatal(err)
	}
	if verifiedAt.Valid {
		t.Fatalf("evidence was unexpectedly verified at %q", verifiedAt.String)
	}
}

func TestGeneralAnnouncementRequiresAdministrator(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Exec(ctx, `INSERT INTO announcements(id,type,title,body,published_at) VALUES('general','GENERAL','General information','Portal information.',?)`, now()); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}
	approver := User{ID: "approver", Role: "APPROVER"}

	createRequest := httptest.NewRequest(http.MethodPost, "/api/announcements", strings.NewReader(`{"type":"GENERAL","title":"General information","body":"Portal information for everyone."}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	server.announcementsCreate(createResponse, createRequest.WithContext(withUser(createRequest.Context(), approver)))
	if createResponse.Code != http.StatusForbidden {
		t.Fatalf("general announcement create status=%d body=%s, want 403", createResponse.Code, createResponse.Body.String())
	}

	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/announcements/general", strings.NewReader(`{"title":"Updated announcement","body":"Updated portal information."}`))
	patchRequest.SetPathValue("id", "general")
	patchRequest = patchRequest.WithContext(withUser(patchRequest.Context(), approver))
	patchResponse := httptest.NewRecorder()
	server.announcementPatch(patchResponse, patchRequest)
	if patchResponse.Code != http.StatusForbidden {
		t.Fatalf("general announcement patch status=%d body=%s, want 403", patchResponse.Code, patchResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/announcements/general", nil)
	deleteRequest.SetPathValue("id", "general")
	deleteRequest = deleteRequest.WithContext(withUser(deleteRequest.Context(), approver))
	deleteResponse := httptest.NewRecorder()
	server.announcementDelete(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusForbidden {
		t.Fatalf("general announcement delete status=%d body=%s, want 403", deleteResponse.Code, deleteResponse.Body.String())
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM announcements WHERE id='general'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("general announcement count=%d, want unchanged row", count)
	}
}
