package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

func RunBackgroundJobs(ctx context.Context, storage Storage) {
	server := &Server{store: storage}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := server.processNotificationJobs(ctx); err != nil {
			log.Printf("notification scheduler failed: %v", err)
		}
		if _, err := server.dispatchOutbox(ctx, 10); err != nil {
			log.Printf("email outbox processing failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type scheduledMeeting struct {
	id, number, title, startAt, endAt, organizerID string
}

type reminderRecipient struct {
	userID, email string
}

func (s *Server) processNotificationJobs(ctx context.Context) error {
	cfg, err := s.loadMailConfigContext(ctx)
	if err != nil {
		return err
	}
	if err := s.processPreMeetingReminders(ctx, cfg); err != nil {
		return err
	}
	return s.processEvidenceReminders(ctx, cfg)
}

func (s *Server) processPreMeetingReminders(ctx context.Context, cfg mailConfig) error {
	if len(cfg.PreMeetingReminderMinutes) == 0 {
		return nil
	}
	maximum := cfg.PreMeetingReminderMinutes[0]
	for _, interval := range cfg.PreMeetingReminderMinutes[1:] {
		if interval > maximum {
			maximum = interval
		}
	}
	nowTime := time.Now().UTC()
	rows, err := s.store.DB().QueryContext(ctx, s.store.Q(`SELECT id,meeting_number,title,start_at,end_at,organizer_id FROM meetings WHERE status='APPROVED' AND start_at>? AND start_at<=? ORDER BY start_at LIMIT 500`), nowTime.Format(time.RFC3339Nano), nowTime.Add(time.Duration(maximum)*time.Minute).Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	meetings := []scheduledMeeting{}
	for rows.Next() {
		var item scheduledMeeting
		if err := rows.Scan(&item.id, &item.number, &item.title, &item.startAt, &item.endAt, &item.organizerID); err != nil {
			rows.Close()
			return err
		}
		meetings = append(meetings, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, meeting := range meetings {
		start, err := time.Parse(time.RFC3339Nano, meeting.startAt)
		if err != nil {
			return err
		}
		recipients, err := s.meetingReminderRecipients(ctx, meeting)
		if err != nil {
			return err
		}
		for _, interval := range cfg.PreMeetingReminderMinutes {
			if start.Sub(nowTime) > time.Duration(interval)*time.Minute {
				continue
			}
			eventKey := fmt.Sprintf("beforeMeeting:T-%d", interval)
			body := fmt.Sprintf("Rapat %s (%s) akan dimulai pada %s. Silakan buka portal untuk detail rapat.", meeting.title, meeting.number, start.Format(time.RFC3339))
			if err := s.recordReminder(ctx, cfg, meeting, eventKey, "Pengingat rapat mendatang", body, recipients); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) processEvidenceReminders(ctx context.Context, cfg mailConfig) error {
	if len(cfg.EvidenceReminderHours) == 0 {
		return nil
	}
	maximum := cfg.EvidenceReminderHours[0]
	for _, interval := range cfg.EvidenceReminderHours[1:] {
		if interval > maximum {
			maximum = interval
		}
	}
	rows, err := s.store.DB().QueryContext(ctx, s.store.Q(`SELECT id,meeting_number,title,start_at,end_at,organizer_id FROM meetings WHERE status='APPROVED' AND end_at<=? ORDER BY end_at DESC LIMIT 500`), now())
	if err != nil {
		return err
	}
	meetings := []scheduledMeeting{}
	for rows.Next() {
		var item scheduledMeeting
		if err := rows.Scan(&item.id, &item.number, &item.title, &item.startAt, &item.endAt, &item.organizerID); err != nil {
			rows.Close()
			return err
		}
		meetings = append(meetings, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, meeting := range meetings {
		end, err := time.Parse(time.RFC3339Nano, meeting.endAt)
		if err != nil {
			return err
		}
		var documentation, attendance, verifiedDocumentation, verifiedAttendance int
		err = s.store.DB().QueryRowContext(ctx, s.store.Q(`SELECT coalesce(sum(CASE WHEN document_type='DOCUMENTATION' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN document_type='ATTENDANCE_LIST' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN document_type='DOCUMENTATION' AND verified_at IS NOT NULL THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN document_type='ATTENDANCE_LIST' AND verified_at IS NOT NULL THEN 1 ELSE 0 END),0) FROM documents WHERE meeting_id=?`), meeting.id).Scan(&documentation, &attendance, &verifiedDocumentation, &verifiedAttendance)
		if err != nil {
			return err
		}
		if documentation > 0 && attendance > 0 && verifiedDocumentation > 0 && verifiedAttendance > 0 {
			continue
		}
		elapsed := time.Since(end)
		recipients, err := s.meetingReminderRecipients(ctx, meeting)
		if err != nil {
			return err
		}
		missing := []string{}
		if documentation == 0 {
			missing = append(missing, "dokumentasi rapat")
		} else if verifiedDocumentation == 0 {
			missing = append(missing, "verifikasi dokumentasi")
		}
		if attendance == 0 {
			missing = append(missing, "daftar hadir")
		} else if verifiedAttendance == 0 {
			missing = append(missing, "verifikasi daftar hadir")
		}
		for _, interval := range cfg.EvidenceReminderHours {
			if elapsed < time.Duration(interval)*time.Hour {
				continue
			}
			eventKey := fmt.Sprintf("evidence:H+%d", interval)
			body := fmt.Sprintf("Rapat %s (%s). Mohon lengkapi atau verifikasi: %s.", meeting.title, meeting.number, strings.Join(missing, " dan "))
			dueRecipients := append([]reminderRecipient(nil), recipients...)
			if interval == maximum && cfg.EvidenceEscalationEmail != "" {
				dueRecipients = append(dueRecipients, reminderRecipient{email: cfg.EvidenceEscalationEmail})
			}
			if err := s.recordReminder(ctx, cfg, meeting, eventKey, "Pengingat evidence rapat", body, dueRecipients); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) meetingReminderRecipients(ctx context.Context, meeting scheduledMeeting) ([]reminderRecipient, error) {
	rows, err := s.store.DB().QueryContext(ctx, s.store.Q(`SELECT id,email FROM users WHERE active=1 AND (id=? OR id IN (SELECT user_id FROM attendance WHERE meeting_id=?))`), meeting.organizerID, meeting.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []reminderRecipient{}
	for rows.Next() {
		var item reminderRecipient
		if err := rows.Scan(&item.userID, &item.email); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Server) recordReminder(ctx context.Context, cfg mailConfig, meeting scheduledMeeting, eventKey, notificationTitle, body string, recipients []reminderRecipient) error {
	template := cfg.Templates["evidence"]
	if strings.HasPrefix(eventKey, "beforeMeeting:") {
		template = cfg.Templates["beforeMeeting"]
	}
	subject := strings.TrimSpace(template.Subject)
	if subject == "" {
		subject = notificationTitle
	}
	subject = renderTemplate(subject, meeting.title, meeting.number)
	emailBody := strings.TrimSpace(template.Body)
	if emailBody == "" {
		emailBody = body
	} else {
		emailBody = strings.ReplaceAll(emailBody, "{{title}}", meeting.title)
		emailBody = strings.ReplaceAll(emailBody, "{{meetingTitle}}", meeting.title)
		emailBody = strings.ReplaceAll(emailBody, "{{meetingNumber}}", meeting.number)
		emailBody = strings.ReplaceAll(emailBody, "{{meetingDetails}}", body)
		emailBody = strings.ReplaceAll(emailBody, "{{missingEvidence}}", strings.TrimPrefix(body, "Rapat "+meeting.title+" ("+meeting.number+"). Mohon lengkapi atau verifikasi: "))
		emailBody = strings.ReplaceAll(emailBody, "{{portalUrl}}", publicPortalURL())
	}
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		stamp := now()
		event := "evidence"
		if strings.HasPrefix(eventKey, "beforeMeeting:") {
			event = "beforeMeeting"
		}
		for _, recipient := range recipients {
			historyKey := eventKey + ":inapp"
			if recipient.userID != "" {
				result, err := tx.ExecContext(ctx, s.store.Q(`INSERT INTO notification_history(id,meeting_id,event_key,recipient,sent_at) VALUES(?,?,?,?,?) ON CONFLICT(meeting_id,event_key,recipient) DO NOTHING`), newID(), meeting.id, historyKey, recipient.userID, stamp)
				if err != nil {
					return err
				}
				count, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if count > 0 {
					if _, err := tx.ExecContext(ctx, s.store.Q(`INSERT INTO notifications(id,user_id,title,body,created_at) VALUES(?,?,?,?,?)`), newID(), recipient.userID, notificationTitle, body, stamp); err != nil {
						return err
					}
				}
			}
			if recipient.email == "" || !cfg.Enabled || !cfg.Events[event] {
				continue
			}
			result, err := tx.ExecContext(ctx, s.store.Q(`INSERT INTO notification_history(id,meeting_id,event_key,recipient,sent_at) VALUES(?,?,?,?,?) ON CONFLICT(meeting_id,event_key,recipient) DO NOTHING`), newID(), meeting.id, eventKey, strings.ToLower(recipient.email), stamp)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count > 0 {
				if err := queueEmailTx(ctx, tx, s, event, meeting.id+":"+eventKey, recipient.email, subject, emailBody); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
