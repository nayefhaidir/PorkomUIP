package sqlstore

import (
	"context"
	"database/sql"
	"testing"
)

func TestLegacySQLiteMigrationAddsNotificationFieldsAndBackfillsEvidence(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`CREATE TABLE documents(id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL, document_type TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE announcements(id TEXT PRIMARY KEY, title TEXT NOT NULL)`,
		`CREATE TABLE email_outbox(id TEXT PRIMARY KEY, status TEXT NOT NULL, available_at TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE meetings(id TEXT PRIMARY KEY, status TEXT NOT NULL)`,
		`CREATE TABLE rooms(id TEXT PRIMARY KEY, online_platform TEXT, breakout_rooms INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE notification_history(id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL, sent_at TEXT NOT NULL)`,
		`INSERT INTO meetings(id,status) VALUES('completed','COMPLETED')`,
		`INSERT INTO rooms(id,online_platform,breakout_rooms) VALUES('teams','TEAMS',5)`,
		`INSERT INTO documents(id,meeting_id,document_type,created_at) VALUES('doc','completed','DOCUMENTATION','2025-01-01T00:00:00Z')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	store := &Store{db: db, driver: "sqlite"}
	if err := store.migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var verifiedAt string
	if err := db.QueryRow(`SELECT verified_at FROM documents WHERE id='doc'`).Scan(&verifiedAt); err != nil {
		t.Fatal(err)
	}
	if verifiedAt != "2025-01-01T00:00:00Z" {
		t.Fatalf("verified_at=%q, want the legacy creation timestamp", verifiedAt)
	}
	var version int
	if err := db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("migration version=%d, want 3", version)
	}
	var overlap int
	if err := db.QueryRow(`SELECT allow_participant_overlap FROM meetings WHERE id='completed'`).Scan(&overlap); err != nil {
		t.Fatal(err)
	}
	if overlap != 0 {
		t.Fatalf("legacy participant overlap setting=%d, want disabled by default", overlap)
	}
	var teamBreakouts int
	if err := db.QueryRow(`SELECT breakout_rooms FROM rooms WHERE id='teams'`).Scan(&teamBreakouts); err != nil {
		t.Fatal(err)
	}
	if teamBreakouts != 0 {
		t.Fatalf("legacy Teams breakout configuration=%d, want disabled", teamBreakouts)
	}
}

func TestPostgresMigrationQueryConvertsPlaceholders(t *testing.T) {
	store := &Store{driver: "pgx"}
	query := store.q(`INSERT INTO schema_migrations(version,applied_at) VALUES(1,?)`)
	if query != `INSERT INTO schema_migrations(version,applied_at) VALUES(1,$1)` {
		t.Fatalf("query=%q, expected PostgreSQL placeholder", query)
	}
}
