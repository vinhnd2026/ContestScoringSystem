package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Server) backupsDir() string {
	d := filepath.Join(s.h.dir, "backups")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// createBackup makes a consistent snapshot with VACUUM INTO. Returns backup id and file name.
func (s *Server) createBackup(contestID any, kind string, actorID int64) (int64, string, error) {
	name := fmt.Sprintf("backup-%s-%s.db", kind, time.Now().Format("20060102-150405"))
	path := filepath.Join(s.backupsDir(), name)
	esc := strings.ReplaceAll(path, "'", "''")
	if _, err := s.h.Exec(`VACUUM INTO '` + esc + `'`); err != nil {
		return 0, "", err
	}
	fi, _ := os.Stat(path)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	var cidArg any
	if v, ok := contestID.(int64); ok && v > 0 {
		cidArg = v
	}
	res, err := s.h.Exec(`INSERT INTO backups(contest_id,file_name,file_size,kind,created_by,created_at) VALUES(?,?,?,?,?,?)`, cidArg, name, size, kind, actorID, nowISO())
	if err != nil {
		return 0, "", err
	}
	id, _ := res.LastInsertId()
	return id, name, nil
}

type backupOut struct {
	ID        int64  `json:"id"`
	ContestID *int64 `json:"contest_id"`
	FileName  string `json:"file_name"`
	FileSize  int64  `json:"file_size"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request, u *User) {
	rows, _ := s.h.Query(`SELECT id,contest_id,file_name,file_size,kind,created_at FROM backups ORDER BY id DESC`)
	defer rows.Close()
	out := []backupOut{}
	for rows.Next() {
		var b backupOut
		_ = rows.Scan(&b.ID, &b.ContestID, &b.FileName, &b.FileSize, &b.Kind, &b.CreatedAt)
		out = append(out, b)
	}
	writeJSON(w, 200, map[string]any{"backups": out})
}
func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct {
		ContestID int64 `json:"contest_id"`
	}
	_ = decodeJSON(r, &in)
	id, _, err := s.createBackup(in.ContestID, "manual", u.ID)
	if err != nil {
		writeErr(w, 500, "INTERNAL", "Tạo backup thất bại: "+err.Error(), nil)
		return
	}
	audit(s.h, u.ID, "backup.create", "backup", id, nil, "", "", "Backup thủ công")
	writeJSON(w, 201, map[string]any{"backup_id": id})
}
func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/backups/")
	var name string
	if err := s.h.QueryRow(`SELECT file_name FROM backups WHERE id=?`, id).Scan(&name); err != nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy backup.", nil)
		return
	}
	path := filepath.Join(s.backupsDir(), filepath.Base(name))
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, 404, "NOT_FOUND", "File backup không còn trên đĩa.", nil)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(name)+"\"")
	_, _ = io.Copy(w, f)
}

func (s *Server) doRestore(w http.ResponseWriter, u *User, srcPath string) bool {
	var performing int
	_ = s.h.QueryRow(`SELECT COUNT(*) FROM performances WHERE status='performing'`).Scan(&performing)
	if performing > 0 {
		writeErr(w, 409, "BACKUP_RESTORE_BLOCKED", "Đang có phần thi đang diễn ra; kết thúc phần thi trước khi restore.", nil)
		return false
	}
	data, err := os.ReadFile(srcPath)
	if err != nil || len(data) < 16 || string(data[:16]) != "SQLite format 3\x00" {
		writeErr(w, 400, "VALIDATION_ERROR", "File tải lên không phải file SQLite hợp lệ.", nil)
		return false
	}
	// pre-restore snapshot of current data
	_, preName, err := s.createBackup(int64(0), "pre_restore", u.ID)
	if err != nil {
		writeErr(w, 500, "INTERNAL", "Không tạo được snapshot pre_restore: "+err.Error(), nil)
		return false
	}
	// swap DB file: close, replace (also remove -wal/-shm), reopen
	s.h.mu.Lock()
	_ = s.h.db.Close()
	dbPath := s.h.path
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.WriteFile(dbPath, data, 0o644); err != nil {
		s.h.mu.Unlock()
		writeErr(w, 500, "INTERNAL", "Ghi file DB thất bại: "+err.Error(), nil)
		return false
	}
	ndb, err := openDB(dbPath)
	if err != nil {
		s.h.mu.Unlock()
		writeErr(w, 500, "INTERNAL", "Mở DB sau restore thất bại: "+err.Error(), nil)
		return false
	}
	s.h.db = ndb
	s.h.mu.Unlock()
	if err := initDB(s.h); err != nil {
		writeErr(w, 500, "INTERNAL", "Khởi tạo DB sau restore thất bại: "+err.Error(), nil)
		return false
	}
	// re-catalog the pre_restore snapshot in the restored DB (its row lived in the replaced DB)
	if fi, err := os.Stat(filepath.Join(s.backupsDir(), preName)); err == nil {
		_, _ = s.h.Exec(`INSERT INTO backups(contest_id,file_name,file_size,kind,created_by,created_at) VALUES(NULL,?,?, 'pre_restore',?,?)`, preName, fi.Size(), u.ID, nowISO())
	}
	audit(s.h, u.ID, "backup.restore", "backup", 0, nil, "", filepath.Base(srcPath), "Restore từ snapshot")
	s.broadcast(map[string]any{"type": "contest.state", "restored": true})
	return true
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request, u *User) {
	parts := pathParts(r, "/api/v1/backups/")
	if len(parts) > 0 && parts[0] == "upload-restore" {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeErr(w, 400, "VALIDATION_ERROR", "Cần file qua multipart (field: file).", nil)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			writeErr(w, 400, "VALIDATION_ERROR", "Thiếu file.", nil)
			return
		}
		defer f.Close()
		tmp := filepath.Join(s.backupsDir(), "upload-"+randHex(4)+".db")
		out, _ := os.Create(tmp)
		_, _ = io.Copy(out, f)
		_ = out.Close()
		defer os.Remove(tmp)
		if s.doRestore(w, u, tmp) {
			writeJSON(w, 200, map[string]any{"ok": true})
		}
		return
	}
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	var name string
	if err := s.h.QueryRow(`SELECT file_name FROM backups WHERE id=?`, id).Scan(&name); err != nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy backup.", nil)
		return
	}
	if s.doRestore(w, u, filepath.Join(s.backupsDir(), filepath.Base(name))) {
		writeJSON(w, 200, map[string]any{"ok": true})
	}
}

// ---------- XLSX export (minimal OOXML writer) ----------
func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}
func colName(n int) string {
	s := ""
	for n > 0 {
		n--
		s = string(rune('A'+n%26)) + s
		n /= 26
	}
	return s
}
func sheetXML(rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for i, row := range rows {
		fmt.Fprintf(&b, `<row r="%d">`, i+1)
		for j, cell := range row {
			fmt.Fprintf(&b, `<c r="%s%d" t="inlineStr"><is><t>%s</t></is></c>`, colName(j+1), i+1, xmlEsc(cell))
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}
func buildXLSX(sheets map[string][][]string, order []string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_ = zw.SetComment("")
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`
	w, _ := zw.Create("_rels/.rels")
	_, _ = w.Write([]byte(rels))
	var wb strings.Builder
	wb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	var ct strings.Builder
	ct.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	for i, name := range order {
		fmt.Fprintf(&wb, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEsc(name), i+1, i+1)
		fmt.Fprintf(&ct, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1)
	}
	wb.WriteString(`</sheets></workbook>`)
	ct.WriteString(`</Types>`)
	w, _ = zw.Create("[Content_Types].xml")
	_, _ = w.Write([]byte(ct.String()))
	var wrels strings.Builder
	wrels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i, name := range order {
		_ = name
		fmt.Fprintf(&wrels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1)
	}
	wrels.WriteString(`</Relationships>`)
	w, _ = zw.Create("xl/workbook.xml")
	_, _ = w.Write([]byte(wb.String()))
	w, _ = zw.Create("xl/_rels/workbook.xml.rels")
	_, _ = w.Write([]byte(wrels.String()))
	for i, name := range order {
		w, _ = zw.Create(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1))
		_, _ = w.Write([]byte(sheetXML(sheets[name])))
	}
	_ = zw.Close()
	return buf.Bytes()
}
func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	c := s.contestByID(cid)
	if c == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy cuộc thi.", nil)
		return
	}
	sheets := map[string][][]string{}
	order := []string{"TongHop", "ChiTiet", "AdminOnly", "Audit"}
	sheets["TongHop"] = [][]string{{"Round", "SBD", "Đội", "Trạng thái", "Điểm giám khảo TB", "Admin-Only", "Điểm cuối", "Hạng", "Đồng điểm", "Ghi chú"}}
	sheets["ChiTiet"] = [][]string{{"SBD", "Đội", "Giám khảo", "Tiêu chí", "Điểm thô", "Trọng số", "Điểm x trọng số", "Trạng thái phiếu"}}
	sheets["AdminOnly"] = [][]string{{"SBD", "Đội", "Tiêu chí Admin-Only", "Điểm thô", "Trọng số", "Điểm x trọng số"}}
	for _, rd := range s.listRounds(cid) {
		for _, tr := range s.leaderboard(rd.ID) {
			sheets["TongHop"] = append(sheets["TongHop"], []string{rd.NameVi, tr.TeamCode, tr.TeamNameVi, tr.Status, f2(tr.JudgeAvg), f2(tr.AdminOnlyTotal), f2(tr.FinalScore), strconv.Itoa(tr.Rank), fmt.Sprint(tr.Tied), tr.TrimNote})
		}
	}
	// details
	drows, _ := s.h.Query(`SELECT t.code,t.name_vi,COALESCE(u.display_name,'Admin'),c.name_vi,sd.raw_score,c.weight,sc.status,c.is_admin_only FROM score_details sd JOIN scores sc ON sc.id=sd.score_id JOIN performances p ON p.id=sc.performance_id JOIN teams t ON t.id=p.team_id LEFT JOIN users u ON u.id=sc.judge_id JOIN criteria c ON c.id=sd.criterion_id JOIN criteria_groups g ON g.id=c.group_id WHERE g.contest_id=? ORDER BY t.sort_order, u.id, c.id`, cid)
	for drows.Next() {
		var code, team, judge, crit, status string
		var raw, weight float64
		var ao int
		_ = drows.Scan(&code, &team, &judge, &crit, &raw, &weight, &status, &ao)
		row := []string{code, team, judge, crit, f2(raw), f2(weight), f2(raw * weight), status}
		if ao == 1 {
			sheets["AdminOnly"] = append(sheets["AdminOnly"], []string{code, team, crit, f2(raw), f2(weight), f2(raw * weight)})
		} else {
			sheets["ChiTiet"] = append(sheets["ChiTiet"], row)
		}
	}
	drows.Close()
	sheets["Audit"] = [][]string{{"ID", "Người thực hiện", "Hành động", "Entity", "Entity ID", "Tiêu chí", "Cũ", "Mới", "Lý do", "Thời gian"}}
	arows, _ := s.h.Query(`SELECT id,actor_id,action,entity_type,entity_id,COALESCE(criterion_id,0),COALESCE(old_value,''),COALESCE(new_value,''),COALESCE(reason,''),created_at FROM audit_logs ORDER BY id`)
	for arows.Next() {
		var id, actor, eid, crit int64
		var action, et, ov, nv, reason, at string
		_ = arows.Scan(&id, &actor, &action, &et, &eid, &crit, &ov, &nv, &reason, &at)
		sheets["Audit"] = append(sheets["Audit"], []string{fmt.Sprint(id), fmt.Sprint(actor), action, et, fmt.Sprint(eid), fmt.Sprint(crit), ov, nv, reason, at})
	}
	arows.Close()
	data := buildXLSX(sheets, order)
	fname := fmt.Sprintf("ket-qua-contest-%d-%s.xlsx", cid, time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+fname+"\"")
	_, _ = w.Write(data)
}

func (s *Server) handleImportTemplate(w http.ResponseWriter, r *http.Request, u *User) {
	data := buildXLSX(map[string][][]string{"Mau": {{"code", "name_vi", "name_en", "organization", "sort_order"}, {"01", "Đội mẫu", "Sample Team", "Đơn vị", "1"}}}, []string{"Mau"})
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename=\"teams-import-template.xlsx\"")
	_, _ = w.Write(data)
}
