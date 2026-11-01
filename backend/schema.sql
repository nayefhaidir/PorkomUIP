CREATE TABLE IF NOT EXISTS departments (
 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, short_name TEXT NOT NULL UNIQUE, color TEXT NOT NULL DEFAULT '#2563EB'
);
CREATE TABLE IF NOT EXISTS users (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
 role TEXT NOT NULL DEFAULT 'PARTICIPANT', job_title TEXT, page_access TEXT NOT NULL DEFAULT '[]',
 active INTEGER NOT NULL DEFAULT 1, department_id TEXT REFERENCES departments(id), created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rooms (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT, kind TEXT NOT NULL DEFAULT 'PHYSICAL',
 online_platform TEXT, capacity INTEGER NOT NULL, location TEXT, facilities TEXT NOT NULL DEFAULT '[]',
 breakout_rooms INTEGER NOT NULL DEFAULT 0, image_url TEXT, active INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS meetings (
 id TEXT PRIMARY KEY, meeting_number TEXT NOT NULL UNIQUE, title TEXT NOT NULL, description TEXT,
 start_at TEXT NOT NULL, end_at TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'DRAFT', method TEXT NOT NULL DEFAULT 'OFFLINE',
 confidential INTEGER NOT NULL DEFAULT 0, allow_participant_overlap INTEGER NOT NULL DEFAULT 0, guest_category TEXT NOT NULL DEFAULT 'REGULAR', timezone TEXT NOT NULL DEFAULT 'Asia/Jakarta',
 organizer_id TEXT NOT NULL REFERENCES users(id), department_id TEXT NOT NULL REFERENCES departments(id),
 room_id TEXT REFERENCES rooms(id), online_resource_id TEXT REFERENCES rooms(id), breakout_room_count INTEGER NOT NULL DEFAULT 0,
 online_url TEXT, external_location TEXT, notes TEXT, catering TEXT, expected_participants INTEGER NOT NULL DEFAULT 1,
 reviewed_by_id TEXT, reviewed_at TEXT, rejection_reason TEXT, submitted_at TEXT, approved_at TEXT, cancelled_at TEXT, evidence_reminder_sent_at TEXT,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS meetings_schedule_idx ON meetings(status,start_at,end_at);
CREATE TABLE IF NOT EXISTS attendance (id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE, user_id TEXT NOT NULL REFERENCES users(id), UNIQUE(meeting_id,user_id));
CREATE TABLE IF NOT EXISTS meeting_guests (id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE, name TEXT NOT NULL, email TEXT, organization TEXT);
CREATE TABLE IF NOT EXISTS meeting_requirements (id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE, type TEXT NOT NULL, detail TEXT, UNIQUE(meeting_id,type));
CREATE TABLE IF NOT EXISTS meeting_consumptions (id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE, item_type TEXT NOT NULL, quantity INTEGER NOT NULL, unit TEXT NOT NULL DEFAULT 'box', UNIQUE(meeting_id,item_type));
CREATE TABLE IF NOT EXISTS meeting_approvals (id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE, approver_id TEXT NOT NULL REFERENCES users(id), decision TEXT NOT NULL, comment TEXT, decided_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS breakout_rooms (id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE, name TEXT NOT NULL, capacity INTEGER NOT NULL, UNIQUE(meeting_id,name));
CREATE TABLE IF NOT EXISTS zoom_breakout_rooms (id TEXT PRIMARY KEY, resource_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, name TEXT NOT NULL, capacity INTEGER NOT NULL DEFAULT 30, active INTEGER NOT NULL DEFAULT 1, UNIQUE(resource_id,name));
CREATE TABLE IF NOT EXISTS meeting_zoom_breakout_bookings (meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE, breakout_room_id TEXT NOT NULL REFERENCES zoom_breakout_rooms(id), created_at TEXT NOT NULL, PRIMARY KEY(meeting_id,breakout_room_id));
CREATE TABLE IF NOT EXISTS documents (id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE, filename TEXT NOT NULL, document_type TEXT NOT NULL DEFAULT 'SUPPORTING', storage_key TEXT NOT NULL UNIQUE, mime_type TEXT NOT NULL, size INTEGER NOT NULL, uploaded_by_id TEXT, verified_at TEXT, verified_by_id TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS notifications (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, title TEXT NOT NULL, body TEXT NOT NULL, read_at TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, token_hash TEXT NOT NULL UNIQUE, expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS announcements (id TEXT PRIMARY KEY, type TEXT NOT NULL DEFAULT 'GENERAL', title TEXT NOT NULL, body TEXT NOT NULL, image_storage_key TEXT, image_mime_type TEXT, target_roles TEXT NOT NULL DEFAULT '["ADMIN","APPROVER","ORGANIZER","PARTICIPANT"]', target_department_ids TEXT NOT NULL DEFAULT '[]', published_at TEXT NOT NULL, expires_at TEXT);
CREATE TABLE IF NOT EXISTS audit_logs (id TEXT PRIMARY KEY, user_id TEXT, action TEXT NOT NULL, entity TEXT NOT NULL, entity_id TEXT NOT NULL, details TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS email_outbox (id TEXT PRIMARY KEY, event_key TEXT NOT NULL UNIQUE, recipient TEXT NOT NULL, subject TEXT NOT NULL, text_body TEXT NOT NULL, html_body TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'PENDING', attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT, available_at TEXT NOT NULL, sent_at TEXT, claim_token TEXT, lease_until TEXT, last_attempt_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS email_outbox_dispatch_idx ON email_outbox(status,available_at,created_at);
CREATE TABLE IF NOT EXISTS notification_history (id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE, event_key TEXT NOT NULL, recipient TEXT NOT NULL, sent_at TEXT NOT NULL, UNIQUE(meeting_id,event_key,recipient));
CREATE INDEX IF NOT EXISTS notification_history_meeting_idx ON notification_history(meeting_id,sent_at);
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
