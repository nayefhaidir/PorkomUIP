package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var reportHeaders = []string{"No", "Tanggal", "Waktu", "Judul Rapat", "Bidang", "Metode", "Lokasi / Zoom", "Peserta", "Kategori Tamu", "Konsumsi", "Dokumentasi", "Notulensi", "Status"}

func (s *Server) monthlyReport(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r, "ADMIN", "APPROVER", "ORGANIZER")
	if !ok {
		return
	}
	rows, month, err := s.reportsRows(r)
	if err != nil {
		writeError(w, 400, "Filter laporan tidak valid", nil)
		return
	}
	statusCount := map[string]int{"SUBMITTED": 0, "APPROVED": 0, "COMPLETED": 0}
	departments := map[string]map[string]any{}
	methods, locations, foods := map[string]int{}, map[string]int{}, map[string]int{}
	participants, duration, docs, minutes := 0, 0, 0, 0
	items := []any{}
	for _, row := range rows {
		status := row["status"].(string)
		statusCount[status]++
		method := row["method"].(string)
		methods[method]++
		dep := row["department"].(map[string]string)
		if departments[dep["id"]] == nil {
			departments[dep["id"]] = map[string]any{"id": dep["id"], "name": dep["name"], "count": 0}
		}
		departments[dep["id"]]["count"] = departments[dep["id"]]["count"].(int) + 1
		location := row["locationName"].(string)
		if location == "" {
			location = "Lokasi belum ditentukan"
		}
		locations[location]++
		participants += row["expectedParticipants"].(int)
		start, _ := time.Parse(time.RFC3339Nano, row["startAt"].(string))
		end, _ := time.Parse(time.RFC3339Nano, row["endAt"].(string))
		duration += int(end.Sub(start).Minutes() + .5)
		hasDocs, hasMinutes := false, false
		for _, raw := range row["documents"].([]string) {
			switch raw {
			case "DOCUMENTATION":
				hasDocs = true
			case "MINUTES":
				hasMinutes = true
			}
		}
		if hasDocs {
			docs++
		}
		if hasMinutes || strings.TrimSpace(fmt.Sprint(row["notes"])) != "" {
			minutes++
		}
		for _, food := range row["consumptions"].([]map[string]any) {
			key := food["itemType"].(string)
			foods[key] += food["quantity"].(int)
		}
		item, e := s.meetingJSON(r, row["id"].(string), true)
		if e != nil {
			writeError(w, 500, "Gagal memuat detail rapat untuk laporan", nil)
			return
		}
		items = append(items, item)
	}
	deptItems := []any{}
	for _, item := range departments {
		deptItems = append(deptItems, item)
	}
	yearMeetings, err := s.reportYearMeetings(r, month)
	if err != nil {
		writeError(w, 500, "Gagal memuat tren tahunan", nil)
		return
	}
	previousMonth, err := previousReportMonth(month)
	if err != nil {
		writeError(w, 400, "Filter laporan tidak valid", nil)
		return
	}
	previousURL := *r.URL
	previousQuery := previousURL.Query()
	previousQuery.Set("month", previousMonth)
	previousURL.RawQuery = previousQuery.Encode()
	previousRequest := r.Clone(r.Context())
	previousRequest.URL = &previousURL
	previousRows, _, err := s.reportsQuery(previousRequest)
	if err != nil {
		writeError(w, 500, "Gagal membandingkan periode sebelumnya", nil)
		return
	}
	previousParticipants := 0
	for _, row := range previousRows {
		previousParticipants += row["expectedParticipants"].(int)
	}
	var activeRooms int
	if err := s.store.DB().QueryRowContext(r.Context(), `SELECT count(*) FROM rooms WHERE active=1`).Scan(&activeRooms); err != nil {
		writeError(w, 500, "Gagal menghitung ruangan aktif", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"month": month, "updatedAt": now(), "totalMeetings": len(rows), "totalParticipants": participants, "durationMinutes": duration, "roomsUsed": len(locations), "activeRooms": activeRooms, "withDocumentation": docs, "withMinutes": minutes, "byStatus": statusCount, "byDepartment": deptItems, "byMethod": methods, "byLocation": locations, "consumptions": foods, "items": items, "yearMeetings": yearMeetings, "previousMeetings": len(previousRows), "previousParticipants": previousParticipants})
	_ = u
}

func previousReportMonth(month string) (string, error) {
	parsed, err := time.Parse("2006-01", month)
	if err != nil {
		return "", err
	}
	return parsed.AddDate(0, -1, 0).Format("2006-01"), nil
}

func (s *Server) reportYearMeetings(r *http.Request, month string) ([]any, error) {
	parsed, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, err
	}
	wib := time.FixedZone("WIB", 7*60*60)
	from := time.Date(parsed.Year(), time.January, 1, 0, 0, 0, 0, wib).UTC()
	to := from.AddDate(1, 0, 0)
	query := `SELECT m.start_at,m.expected_participants FROM meetings m WHERE m.status IN ('SUBMITTED','APPROVED','COMPLETED') AND m.start_at>=? AND m.start_at<?`
	args := []any{from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano)}
	if departmentID := r.URL.Query().Get("departmentId"); departmentID != "" {
		query += ` AND m.department_id=?`
		args = append(args, departmentID)
	}
	if method := r.URL.Query().Get("method"); method != "" {
		query += ` AND m.method=?`
		args = append(args, method)
	}
	if u, ok := userFrom(r); ok && u.Role != "ADMIN" && u.Role != "APPROVER" {
		query += ` AND (m.confidential=0 OR m.organizer_id=? OR EXISTS(SELECT 1 FROM attendance a WHERE a.meeting_id=m.id AND a.user_id=?))`
		args = append(args, u.ID, u.ID)
	}
	rows, err := s.store.DB().QueryContext(r.Context(), s.store.Q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type monthlyTrend struct {
		meetings, participants int
	}
	trend := [12]monthlyTrend{}
	for rows.Next() {
		var start string
		var participants int
		if err := rows.Scan(&start, &participants); err != nil {
			return nil, err
		}
		startAt, err := time.Parse(time.RFC3339Nano, start)
		if err != nil {
			return nil, err
		}
		index := int(startAt.In(wib).Month()) - 1
		if index >= 0 && index < len(trend) {
			trend[index].meetings++
			trend[index].participants += participants
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]any, len(trend))
	for index, item := range trend {
		items[index] = map[string]int{"month": index + 1, "meetings": item.meetings, "expectedParticipants": item.participants}
	}
	return items, nil
}

func (s *Server) monthlyExport(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r, "ADMIN", "APPROVER", "ORGANIZER"); !ok {
		return
	}
	rows, month, err := s.reportsRows(r)
	if err != nil {
		writeError(w, 400, "Parameter ekspor tidak valid", nil)
		return
	}
	format := r.URL.Query().Get("format")
	if !validEnum(format, "csv", "xlsx", "pdf") {
		writeError(w, 400, "Parameter ekspor tidak valid", nil)
		return
	}
	values := make([][]string, 0, len(rows))
	for i, row := range rows {
		start, _ := time.Parse(time.RFC3339Nano, row["startAt"].(string))
		end, _ := time.Parse(time.RFC3339Nano, row["endAt"].(string))
		location := row["locationName"].(string)
		if location == "" {
			location = "—"
		}
		dept := row["department"].(map[string]string)
		foods := []string{}
		docTypes := row["documents"].([]string)
		hasDocs, hasMinutes := false, strings.TrimSpace(fmt.Sprint(row["notes"])) != ""
		for _, item := range row["consumptions"].([]map[string]any) {
			if item["quantity"].(int) > 0 {
				foods = append(foods, strings.ReplaceAll(item["itemType"].(string), "_", " ")+": "+strconv.Itoa(item["quantity"].(int)))
			}
		}
		for _, typ := range docTypes {
			if typ == "DOCUMENTATION" {
				hasDocs = true
			}
			if typ == "MINUTES" {
				hasMinutes = true
			}
		}
		statusName := map[string]string{"SUBMITTED": "Menunggu", "APPROVED": "Disetujui", "COMPLETED": "Selesai"}[row["status"].(string)]
		guestName := "Biasa"
		if row["guestCategory"] == "VIP" {
			guestName = "VIP"
		}
		food := strings.Join(foods, "; ")
		if food == "" {
			food = "—"
		}
		documentText, minutesText := "Belum", "Belum"
		if hasDocs {
			documentText = "Ada"
		}
		if hasMinutes {
			minutesText = "Ada"
		}
		values = append(values, []string{strconv.Itoa(i + 1), start.In(time.FixedZone("WIB", 7*3600)).Format("02 Jan 2006"), start.In(time.FixedZone("WIB", 7*3600)).Format("15:04") + "–" + end.In(time.FixedZone("WIB", 7*3600)).Format("15:04"), row["title"].(string), dept["name"], row["method"].(string), location, strconv.Itoa(row["expectedParticipants"].(int)), guestName, food, documentText, minutesText, statusName})
	}
	filename := "rekap-" + month
	if format == "csv" {
		var buffer bytes.Buffer
		buffer.Write([]byte{0xef, 0xbb, 0xbf})
		writer := csv.NewWriter(&buffer)
		_ = writer.Write(reportHeaders)
		for _, row := range values {
			for i, cell := range row {
				trimmed := strings.TrimLeft(cell, " \t\r\n")
				if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
					row[i] = "'" + cell
				}
			}
			_ = writer.Write(row)
		}
		writer.Flush()
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.csv"`)
		_, _ = w.Write(buffer.Bytes())
		return
	}
	if format == "xlsx" {
		content, err := makeXLSX(reportHeaders, values)
		if err != nil {
			writeError(w, 500, "Gagal membuat berkas Excel", nil)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.xlsx"`)
		_, _ = w.Write(content)
		return
	}
	content := simplePDF("Rekap Rapat "+month, values)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.pdf"`)
	_, _ = w.Write(content)
}

func makeXLSX(headers []string, values [][]string) ([]byte, error) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	files := map[string]string{
		"[Content_Types].xml":        `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`,
		"_rels/.rels":                `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":            `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Rekap Bulanan" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
	}
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	rows := append([][]string{headers}, values...)
	for ri, row := range rows {
		fmt.Fprintf(&sheet, `<row r="%d">`, ri+1)
		for ci, value := range row {
			fmt.Fprintf(&sheet, `<c r="%s%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, excelColumn(ci+1), ri+1, xmlEscape(value))
		}
		sheet.WriteString("</row>")
	}
	sheet.WriteString("</sheetData></worksheet>")
	files["xl/worksheets/sheet1.xml"] = sheet.String()
	for name, content := range files {
		file, err := z.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := file.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func excelColumn(n int) string {
	out := ""
	for n > 0 {
		n--
		out = string(rune('A'+n%26)) + out
		n /= 26
	}
	return out
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}

func simplePDF(title string, rows [][]string) []byte {
	lines := []string{title, strings.Join(reportHeaders, " | ")}
	for _, row := range rows {
		lines = append(lines, strings.Join(row, " | "))
	}
	// A small valid one-page PDF suitable for printable text exports.
	text := "BT /F1 8 Tf 36 800 Td 10 TL "
	for i, line := range lines {
		if i >= 65 {
			break
		}
		line = strings.ReplaceAll(line, `\`, `\\`)
		line = strings.ReplaceAll(line, "(", `\(`)
		line = strings.ReplaceAll(line, ")", `\)`)
		if len(line) > 180 {
			line = line[:180]
		}
		text += "(" + line + ") Tj T* "
	}
	text += "ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 842 595] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(text), text),
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}
