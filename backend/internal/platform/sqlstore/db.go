package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	driver  string
	writeMu sync.Mutex
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	driver, source := "sqlite", ""
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		driver, source = "pgx", dsn
	} else {
		if dsn == "" {
			dsn = "file:backend.db"
		}
		if strings.HasPrefix(dsn, "file:") {
			source = strings.TrimPrefix(dsn, "file:")
			if i := strings.Index(source, "?"); i >= 0 {
				source = source[:i]
			}
			if source == "" || source == ":memory:" {
				source = ":memory:"
			} else if !filepath.IsAbs(source) {
				source = filepath.Join(".", source)
			}
			if source != ":memory:" {
				if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
					return nil, err
				}
			}
			source += "?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
		} else {
			source = dsn
			if !strings.Contains(source, "?") {
				source += "?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
			}
		}
	}
	db, err := sql.Open(driver, source)
	if err != nil {
		return nil, err
	}
	if driver == "sqlite" {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(20)
	}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db, driver: driver}
	schema, err := os.ReadFile("schema.sql")
	if os.IsNotExist(err) {
		schema, err = os.ReadFile(filepath.Join("backend", "schema.sql"))
	}
	if os.IsNotExist(err) {
		_, source, _, ok := runtime.Caller(0)
		if ok {
			schema, err = os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "schema.sql"))
		}
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, statement := range strings.Split(string(schema), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err = db.ExecContext(ctx, statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}
	if err = store.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	if driver == "pgx" {
		if err = store.importPrismaData(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("import existing PostgreSQL data: %w", err)
		}
	}
	return store, nil
}

func (s *Store) migrate(ctx context.Context) error {
	var version int
	err := s.db.QueryRowContext(ctx, `SELECT coalesce(max(version),0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return err
	}
	if version < 1 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, column := range []struct {
			table, name, definition string
		}{
			{"documents", "verified_at", "TEXT"},
			{"documents", "verified_by_id", "TEXT"},
			{"announcements", "target_roles", `TEXT NOT NULL DEFAULT '["ADMIN","APPROVER","ORGANIZER","PARTICIPANT"]'`},
			{"announcements", "target_department_ids", `TEXT NOT NULL DEFAULT '[]'`},
			{"email_outbox", "claim_token", "TEXT"},
			{"email_outbox", "lease_until", "TEXT"},
			{"email_outbox", "last_attempt_at", "TEXT"},
		} {
			exists, err := s.columnExists(ctx, tx, column.table, column.name)
			if err != nil {
				return err
			}
			if !exists {
				if _, err := tx.ExecContext(ctx, `ALTER TABLE `+column.table+` ADD COLUMN `+column.name+` `+column.definition); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE documents SET verified_at=created_at WHERE verified_at IS NULL AND document_type IN ('DOCUMENTATION','ATTENDANCE_LIST') AND EXISTS(SELECT 1 FROM meetings WHERE meetings.id=documents.meeting_id AND meetings.status='COMPLETED')`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS email_outbox_dispatch_idx ON email_outbox(status,available_at,created_at)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS notification_history_meeting_idx ON notification_history(meeting_id,sent_at)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO schema_migrations(version,applied_at) VALUES(1,?)`), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 1
	}
	if version < 2 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		exists, err := s.columnExists(ctx, tx, "meetings", "allow_participant_overlap")
		if err != nil {
			return err
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE meetings ADD COLUMN allow_participant_overlap INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO schema_migrations(version,applied_at) VALUES(2,?)`), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 2
	}
	if version < 3 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `UPDATE rooms SET breakout_rooms=0 WHERE online_platform='TEAMS'`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO schema_migrations(version,applied_at) VALUES(3,?)`), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		return tx.Commit()
	}
	return nil
}

func (s *Store) columnExists(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	if s.driver == "pgx" {
		var count int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2`, table, column).Scan(&count)
		return count != 0, err
	}
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) q(query string) string {
	if s.driver != "pgx" {
		return query
	}
	var b strings.Builder
	n := 1
	for _, r := range query {
		if r == '?' {
			fmt.Fprintf(&b, "$%d", n)
			n++
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Driver() string { return s.driver }

func (s *Store) Q(query string) string { return s.q(query) }

func (s *Store) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.exec(ctx, query, args...)
}

func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.withTx(ctx, fn)
}

func (s *Store) Close() error { return s.close() }

func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.q(query), args...)
}

func (s *Store) write(ctx context.Context, fn func() error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return fn()
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) close() error { return s.db.Close() }

func (s *Store) importPrismaData(ctx context.Context) error {
	var legacy int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='Department'`).Scan(&legacy); err != nil || legacy == 0 {
		return nil
	}
	mappings := []struct {
		table, columns, selectSQL string
	}{
		{"departments", "id,name,short_name,color", `SELECT "id","name","shortName","color" FROM "Department"`},
		{"users", "id,name,email,password_hash,role,job_title,page_access,active,department_id,created_at,updated_at", `SELECT "id","name","email","passwordHash","role"::text,"jobTitle",to_json("pageAccess")::text,CASE WHEN "isActive" THEN 1 ELSE 0 END,"departmentId","createdAt","updatedAt" FROM "User"`},
		{"rooms", "id,name,description,kind,online_platform,capacity,location,facilities,breakout_rooms,image_url,active,created_at,updated_at", `SELECT "id","name","description","kind"::text,"onlinePlatform"::text,"capacity","location",to_json("facilities")::text,"breakoutRooms","imageUrl",CASE WHEN "active" THEN 1 ELSE 0 END,"createdAt","updatedAt" FROM "Room"`},
		{"meetings", "id,meeting_number,title,description,start_at,end_at,status,method,confidential,guest_category,timezone,organizer_id,department_id,room_id,online_resource_id,breakout_room_count,online_url,external_location,notes,catering,expected_participants,reviewed_by_id,reviewed_at,rejection_reason,submitted_at,approved_at,cancelled_at,evidence_reminder_sent_at,created_at,updated_at", `SELECT "id","meetingNumber","title","description","startAt","endAt","status"::text,"method"::text,CASE WHEN "isConfidential" THEN 1 ELSE 0 END,"guestCategory"::text,"timezone","organizerId","departmentId","roomId","onlineResourceId","breakoutRoomCount","onlineUrl","externalLocation","notes","catering","expectedParticipants","reviewedById","reviewedAt","rejectionReason","submittedAt","approvedAt","cancelledAt","evidenceReminderSentAt","createdAt","updatedAt" FROM "Meeting"`},
		{"attendance", "id,meeting_id,user_id", `SELECT "id","meetingId","userId" FROM "Attendance"`},
		{"meeting_guests", "id,meeting_id,name,email,organization", `SELECT "id","meetingId","name","email","organization" FROM "MeetingGuest"`},
		{"meeting_requirements", "id,meeting_id,type,detail", `SELECT "id","meetingId","type"::text,"detail" FROM "MeetingRequirement"`},
		{"meeting_consumptions", "id,meeting_id,item_type,quantity,unit", `SELECT "id","meetingId","itemType"::text,"quantity","unit" FROM "MeetingConsumption"`},
		{"meeting_approvals", "id,meeting_id,approver_id,decision,comment,decided_at", `SELECT "id","meetingId","approverId","decision"::text,"comment","decidedAt" FROM "MeetingApproval"`},
		{"breakout_rooms", "id,meeting_id,name,capacity", `SELECT "id","meetingId","name","capacity" FROM "BreakoutRoom"`},
		{"zoom_breakout_rooms", "id,resource_id,name,capacity,active", `SELECT "id","resourceId","name","capacity",CASE WHEN "active" THEN 1 ELSE 0 END FROM "ZoomBreakoutRoom"`},
		{"meeting_zoom_breakout_bookings", "meeting_id,breakout_room_id,created_at", `SELECT "meetingId","breakoutRoomId","createdAt" FROM "MeetingZoomBreakoutBooking"`},
		{"documents", "id,meeting_id,filename,document_type,storage_key,mime_type,size,uploaded_by_id,created_at", `SELECT "id","meetingId","filename","documentType"::text,"storageKey","mimeType","size","uploadedById","createdAt" FROM "Document"`},
		{"notifications", "id,user_id,title,body,read_at,created_at", `SELECT "id","userId","title","body","readAt","createdAt" FROM "Notification"`},
		{"sessions", "id,user_id,token_hash,expires_at,revoked_at,created_at", `SELECT "id","userId","tokenHash","expiresAt","revokedAt","createdAt" FROM "Session"`},
		{"announcements", "id,type,title,body,image_storage_key,image_mime_type,published_at,expires_at", `SELECT "id","type"::text,"title","body","imageStorageKey","imageMimeType","publishedAt","expiresAt" FROM "Announcement"`},
		{"audit_logs", "id,user_id,action,entity,entity_id,details,created_at", `SELECT "id","userId","action","entity","entityId","details"::text,"createdAt" FROM "AuditLog"`},
		{"settings", "key,value,updated_at", `SELECT "key","value"::text,"updatedAt" FROM "SystemSetting"`},
		{"email_outbox", "id,event_key,recipient,subject,text_body,html_body,status,attempts,last_error,available_at,sent_at,created_at,updated_at", `SELECT "id","eventKey","recipient","subject","textBody","htmlBody","status","attempts","lastError","availableAt","sentAt","createdAt","updatedAt" FROM "EmailOutbox"`},
	}
	for _, mapping := range mappings {
		rows, err := s.db.QueryContext(ctx, mapping.selectSQL)
		if err != nil {
			// Old installations may predate optional models/fields; the baseline tables remain usable.
			if strings.Contains(strings.ToLower(err.Error()), "does not exist") || strings.Contains(strings.ToLower(err.Error()), "undefined") {
				continue
			}
			return err
		}
		columns := strings.Split(mapping.columns, ",")
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return err
			}
			for i, value := range values {
				switch v := value.(type) {
				case []byte:
					values[i] = string(v)
				case time.Time:
					values[i] = v.UTC().Format(time.RFC3339Nano)
				case bool:
					if v {
						values[i] = 1
					} else {
						values[i] = 0
					}
				case float64:
					if v == float64(int64(v)) {
						values[i] = int64(v)
					}
				}
			}
			marks := strings.TrimRight(strings.Repeat("?,", len(columns)), ",")
			query := `INSERT INTO ` + mapping.table + `(` + mapping.columns + `) VALUES(` + marks + `) ON CONFLICT DO NOTHING`
			if _, err := s.exec(ctx, query, values...); err != nil {
				rows.Close()
				return fmt.Errorf("migrate %s row: %w", mapping.table, err)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func ParseDBURL(raw string) string {
	if strings.HasPrefix(raw, "sqlite://") {
		u, err := url.Parse(raw)
		if err == nil {
			p := u.Path
			if u.Host != "" {
				p = filepath.Join("/", u.Host, strings.TrimPrefix(u.Path, "/"))
			}
			return "file:" + p
		}
	}
	return raw
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate")
}

func nullable(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

var errNotFound = errors.New("not found")

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
