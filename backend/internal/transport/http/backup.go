package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var backupTables = []string{"departments", "users", "rooms", "meetings", "attendance", "meeting_guests", "meeting_requirements", "meeting_consumptions", "meeting_approvals", "breakout_rooms", "zoom_breakout_rooms", "meeting_zoom_breakout_bookings", "documents", "notifications", "sessions", "announcements", "audit_logs", "settings", "email_outbox"}

func (s *Server) backupGet(w http.ResponseWriter, r *http.Request, u User) {
	type snapshot struct {
		Version int                         `json:"version"`
		Tables  map[string][]map[string]any `json:"tables"`
	}
	data := snapshot{Version: 1, Tables: map[string][]map[string]any{}}
	err := s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		for _, table := range backupTables {
			rows, err := tx.QueryContext(r.Context(), "SELECT * FROM "+table)
			if err != nil {
				return err
			}
			cols, err := rows.Columns()
			if err != nil {
				rows.Close()
				return err
			}
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err = rows.Scan(ptrs...); err != nil {
					rows.Close()
					return err
				}
				item := map[string]any{}
				for i, col := range cols {
					switch v := vals[i].(type) {
					case []byte:
						item[col] = string(v)
					default:
						item[col] = v
					}
				}
				data.Tables[table] = append(data.Tables[table], item)
			}
			if err = rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
		}
		return nil
	})
	if err != nil {
		writeError(w, 500, "Backup gagal. Periksa log aplikasi dan ruang penyimpanan.", nil)
		return
	}
	payload, _ := json.Marshal(data)
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "database.json", Mode: 0600, Size: int64(len(payload)), ModTime: time.Now(), Typeflag: tar.TypeReg}); err != nil {
		writeError(w, 500, "Backup gagal", nil)
		return
	}
	if _, err := tw.Write(payload); err != nil {
		writeError(w, 500, "Backup gagal", nil)
		return
	}
	root := storageDir()
	_ = tw.WriteHeader(&tar.Header{Name: "uploads/", Mode: 0700, Typeflag: tar.TypeDir})
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || path == root {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil
		}
		if err = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(filepath.Join("uploads", rel)), Mode: 0600, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(tw, file)
		return err
	})
	if err := tw.Close(); err != nil {
		writeError(w, 500, "Backup gagal", nil)
		return
	}
	if err := gz.Close(); err != nil {
		writeError(w, 500, "Backup gagal", nil)
		return
	}
	id := newID()
	s.audit(r, u, "BACKUP", "ApplicationBackup", id, map[string]any{"includes": []string{"database", "uploads"}})
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="uip-backup-`+time.Now().UTC().Format("20060102T150405Z")+`.tar.gz"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(archive.Bytes())
}

func (s *Server) backupPost(w http.ResponseWriter, r *http.Request, u User) {
	r.Body = http.MaxBytesReader(w, r.Body, (512<<20)+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, 400, "Pilih arsip backup sampai maksimal 512 MiB.", nil)
		return
	}
	archivePath := "backup-upload-" + newID() + ".tar.gz"
	defer os.Remove(archivePath)
	foundArchive, confirmation := false, ""
	var archiveSize int64
	for {
		part, partErr := reader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			writeError(w, 400, "Arsip backup tidak valid.", nil)
			return
		}
		switch part.FormName() {
		case "archive":
			if foundArchive || part.FileName() == "" {
				part.Close()
				writeError(w, 400, "Pilih arsip backup sampai maksimal 512 MiB.", nil)
				return
			}
			foundArchive = true
			out, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				part.Close()
				writeError(w, 500, "Gagal mempersiapkan pemulihan", nil)
				return
			}
			archiveSize, err = io.Copy(out, io.LimitReader(part, (512<<20)+1))
			closeErr := out.Close()
			part.Close()
			if err != nil || closeErr != nil || archiveSize < 1 || archiveSize > 512<<20 {
				writeError(w, 400, "Pilih arsip backup sampai maksimal 512 MiB.", nil)
				return
			}
		case "confirmation":
			value, err := io.ReadAll(io.LimitReader(part, 64))
			part.Close()
			if err != nil {
				writeError(w, 400, "Konfirmasi pemulihan tidak valid.", nil)
				return
			}
			confirmation = string(value)
		default:
			part.Close()
			writeError(w, 400, "Formulir pemulihan tidak valid.", nil)
			return
		}
	}
	if !foundArchive {
		writeError(w, 400, "Pilih arsip backup sampai maksimal 512 MiB.", nil)
		return
	}
	if confirmation != "PULIHKAN" {
		writeError(w, 400, "Ketik PULIHKAN untuk mengonfirmasi penggantian data aktif.", nil)
		return
	}
	file, err := os.Open(archivePath)
	if err != nil {
		writeError(w, 400, "Arsip backup tidak valid.", nil)
		return
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		writeError(w, 400, "Arsip backup tidak valid.", nil)
		return
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var snapshot struct {
		Version int                         `json:"version"`
		Tables  map[string][]map[string]any `json:"tables"`
	}
	uploadStage := filepath.Join(filepath.Dir(storageDir()), ".restore-"+newID())
	if err := os.MkdirAll(uploadStage, 0700); err != nil {
		writeError(w, 500, "Gagal mempersiapkan pemulihan", nil)
		return
	}
	defer os.RemoveAll(uploadStage)
	foundDB, foundUploads := false, false
	var expanded int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, 400, "Arsip backup tidak valid.", nil)
			return
		}
		name := filepath.ToSlash(filepath.Clean(h.Name))
		if strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			writeError(w, 400, "Arsip berisi jalur atau tipe berkas yang tidak diizinkan.", nil)
			return
		}
		if h.Size < 0 {
			writeError(w, 400, "Metadata ukuran arsip tidak valid.", nil)
			return
		}
		expanded += h.Size
		if expanded > 2<<30 {
			writeError(w, 413, "Ukuran isi arsip melebihi batas pemulihan 2 GiB.", nil)
			return
		}
		switch {
		case name == "database.json" && h.Typeflag == tar.TypeReg:
			content, err := io.ReadAll(io.LimitReader(tr, 2<<30))
			if err != nil || json.Unmarshal(content, &snapshot) != nil {
				writeError(w, 400, "Dump database pada arsip tidak valid.", nil)
				return
			}
			foundDB = true
		case name == "uploads" && h.Typeflag == tar.TypeDir, name == "uploads/" && h.Typeflag == tar.TypeDir:
			foundUploads = true
		case strings.HasPrefix(name, "uploads/") && h.Typeflag == tar.TypeReg:
			foundUploads = true
			target := filepath.Join(uploadStage, filepath.FromSlash(strings.TrimPrefix(name, "uploads/")))
			if !within(uploadStage, target) {
				writeError(w, 400, "Arsip berisi jalur berkas yang tidak diizinkan.", nil)
				return
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				writeError(w, 400, "Arsip backup tidak valid.", nil)
				return
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				writeError(w, 400, "Arsip backup tidak valid.", nil)
				return
			}
			_, copyErr := io.Copy(out, io.LimitReader(tr, h.Size))
			closeErr := out.Close()
			if copyErr != nil || closeErr != nil {
				writeError(w, 400, "Arsip backup tidak valid.", nil)
				return
			}
		default:
			writeError(w, 400, "Arsip harus memuat database.json dan direktori uploads.", nil)
			return
		}
	}
	if !foundDB || !foundUploads || snapshot.Version != 1 {
		writeError(w, 400, "Arsip harus memuat database.json dan direktori uploads.", nil)
		return
	}
	for table := range snapshot.Tables {
		allowed := false
		for _, t := range backupTables {
			if table == t {
				allowed = true
				break
			}
		}
		if !allowed {
			writeError(w, 400, "Dump database berisi tabel yang tidak diizinkan.", nil)
			return
		}
	}
	err = s.store.WithTx(r.Context(), func(tx *sql.Tx) error {
		for i := len(backupTables) - 1; i >= 0; i-- {
			if _, err := tx.ExecContext(r.Context(), "DELETE FROM "+backupTables[i]); err != nil {
				return err
			}
		}
		for _, table := range backupTables {
			for _, row := range snapshot.Tables[table] {
				cols := []string{}
				vals := []any{}
				for col, val := range row {
					if !allowedColumn(table, col) {
						return fmt.Errorf("invalid column")
					}
					cols = append(cols, col)
					vals = append(vals, val)
				}
				if len(cols) == 0 {
					continue
				}
				sortStrings(cols)
				vals = vals[:0]
				marks := make([]string, len(cols))
				for i, col := range cols {
					vals = append(vals, row[col])
					marks[i] = "?"
				}
				query := "INSERT INTO " + table + "(" + strings.Join(cols, ",") + ") VALUES(" + strings.Join(marks, ",") + ")"
				if _, err := tx.ExecContext(r.Context(), s.store.Q(query), vals...); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, 400, "Pemulihan database gagal. Arsip tidak sesuai skema.", nil)
		return
	}
	current := storageDir()
	old := current + ".previous-" + newID()
	if err := os.Rename(current, old); err != nil && !os.IsNotExist(err) {
		writeError(w, 500, "Database dipulihkan tetapi berkas unggahan tidak dapat diganti.", nil)
		return
	}
	if err := os.Rename(uploadStage, current); err != nil {
		_ = os.Rename(old, current)
		writeError(w, 500, "Gagal mengaktifkan berkas unggahan yang dipulihkan.", nil)
		return
	}
	_ = os.RemoveAll(old)
	s.audit(r, u, "RESTORE", "ApplicationBackup", newID(), map[string]any{"actorSessionRestored": true})
	writeJSON(w, 200, map[string]any{"ok": true, "message": "Database dan berkas unggahan berhasil dipulihkan."})
}

func allowedColumn(table, column string) bool {
	allowed := map[string]string{
		"departments":                    "id name short_name color",
		"users":                          "id name email password_hash role job_title page_access active department_id created_at updated_at",
		"rooms":                          "id name description kind online_platform capacity location facilities breakout_rooms image_url active created_at updated_at",
		"meetings":                       "id meeting_number title description start_at end_at status method confidential guest_category timezone organizer_id department_id room_id online_resource_id breakout_room_count online_url external_location notes catering expected_participants reviewed_by_id reviewed_at rejection_reason submitted_at approved_at cancelled_at evidence_reminder_sent_at created_at updated_at",
		"attendance":                     "id meeting_id user_id",
		"meeting_guests":                 "id meeting_id name email organization",
		"meeting_requirements":           "id meeting_id type detail",
		"meeting_consumptions":           "id meeting_id item_type quantity unit",
		"meeting_approvals":              "id meeting_id approver_id decision comment decided_at",
		"breakout_rooms":                 "id meeting_id name capacity",
		"zoom_breakout_rooms":            "id resource_id name capacity active",
		"meeting_zoom_breakout_bookings": "meeting_id breakout_room_id created_at",
		"documents":                      "id meeting_id filename document_type storage_key mime_type size uploaded_by_id created_at",
		"notifications":                  "id user_id title body read_at created_at",
		"sessions":                       "id user_id token_hash expires_at revoked_at created_at",
		"announcements":                  "id type title body image_storage_key image_mime_type published_at expires_at",
		"audit_logs":                     "id user_id action entity entity_id details created_at",
		"settings":                       "key value updated_at",
		"email_outbox":                   "id event_key recipient subject text_body html_body status attempts last_error available_at sent_at created_at updated_at",
	}
	for _, v := range strings.Fields(allowed[table]) {
		if v == column {
			return true
		}
	}
	return false
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
