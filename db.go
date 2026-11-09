package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// DBHolder allows closing/reopening the DB for restore.
type DBHolder struct {
	mu   sync.RWMutex
	db   *sql.DB
	path string
	dir  string
}

func (h *DBHolder) DB() *sql.DB {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.db
}

// exec/query helpers always go through the single write connection (MaxOpenConns=1).
func (h *DBHolder) Exec(query string, args ...any) (sql.Result, error) {
	return h.DB().Exec(query, args...)
}
func (h *DBHolder) Query(query string, args ...any) (*sql.Rows, error) {
	return h.DB().Query(query, args...)
}
func (h *DBHolder) QueryRow(query string, args ...any) *sql.Row {
	return h.DB().QueryRow(query, args...)
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

func nowISO() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS users(
 id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE, display_name TEXT NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('admin','judge','mc')), password_hash TEXT, pin_hash TEXT,
 allow_pin_login INTEGER NOT NULL DEFAULT 0, failed_login_count INTEGER NOT NULL DEFAULT 0,
 locked_until TEXT, is_active INTEGER NOT NULL DEFAULT 1, created_at TEXT, updated_at TEXT);
CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL, expires_at TEXT, created_at TEXT);
CREATE TABLE IF NOT EXISTS contests(
 id INTEGER PRIMARY KEY AUTOINCREMENT, name_vi TEXT NOT NULL, name_en TEXT, desc_vi TEXT, desc_en TEXT,
 location TEXT, starts_at TEXT, ends_at TEXT, status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','active','completed')),
 is_finalized INTEGER NOT NULL DEFAULT 0, finalized_at TEXT, finalized_by INTEGER, join_code TEXT NOT NULL UNIQUE,
 allow_judge_edit_after_submit INTEGER NOT NULL DEFAULT 0, drop_highest_lowest INTEGER NOT NULL DEFAULT 0,
 min_judges_for_trim INTEGER NOT NULL DEFAULT 5 CHECK(min_judges_for_trim>=4), tie_breaker_criterion_id INTEGER,
 require_total_match INTEGER NOT NULL DEFAULT 0, target_total_score REAL, standard_duration_sec INTEGER NOT NULL DEFAULT 600,
 warning_before_sec INTEGER NOT NULL DEFAULT 60, penalty_enabled INTEGER NOT NULL DEFAULT 0,
 penalty_interval_sec INTEGER, penalty_first_points REAL, penalty_next_points REAL, penalty_max_points REAL,
 allow_parallel_rounds INTEGER NOT NULL DEFAULT 0, created_at TEXT, updated_at TEXT);
CREATE TABLE IF NOT EXISTS rounds(
 id INTEGER PRIMARY KEY AUTOINCREMENT, contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
 name_vi TEXT NOT NULL, name_en TEXT, sort_order INTEGER NOT NULL DEFAULT 0, standard_duration_sec INTEGER,
 leaderboard_revealed INTEGER NOT NULL DEFAULT 0, created_at TEXT, updated_at TEXT);
CREATE TABLE IF NOT EXISTS teams(
 id INTEGER PRIMARY KEY AUTOINCREMENT, round_id INTEGER NOT NULL REFERENCES rounds(id) ON DELETE CASCADE,
 code TEXT NOT NULL, name_vi TEXT NOT NULL, name_en TEXT, organization TEXT, members_note TEXT,
 sort_order INTEGER NOT NULL DEFAULT 0, is_active INTEGER NOT NULL DEFAULT 1, created_at TEXT, updated_at TEXT,
 UNIQUE(round_id, code));
CREATE TABLE IF NOT EXISTS criteria_groups(
 id INTEGER PRIMARY KEY AUTOINCREMENT, contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
 name_vi TEXT NOT NULL, name_en TEXT, sort_order INTEGER NOT NULL DEFAULT 0, created_at TEXT, updated_at TEXT);
CREATE TABLE IF NOT EXISTS criteria(
 id INTEGER PRIMARY KEY AUTOINCREMENT, group_id INTEGER NOT NULL REFERENCES criteria_groups(id) ON DELETE CASCADE,
 name_vi TEXT NOT NULL, name_en TEXT, desc_vi TEXT, desc_en TEXT, input_type TEXT NOT NULL CHECK(input_type IN ('range','discrete')),
 min_score REAL NOT NULL DEFAULT 0, max_score REAL NOT NULL, step REAL NOT NULL DEFAULT 1, weight REAL NOT NULL DEFAULT 1 CHECK(weight>0),
 is_admin_only INTEGER NOT NULL DEFAULT 0, is_penalty_criterion INTEGER NOT NULL DEFAULT 0,
 comment_mode TEXT NOT NULL DEFAULT 'optional' CHECK(comment_mode IN ('hidden','optional','required')),
 sort_order INTEGER NOT NULL DEFAULT 0, created_at TEXT, updated_at TEXT);
CREATE TABLE IF NOT EXISTS criteria_options(
 id INTEGER PRIMARY KEY AUTOINCREMENT, criterion_id INTEGER NOT NULL REFERENCES criteria(id) ON DELETE CASCADE,
 label_vi TEXT NOT NULL, label_en TEXT, score_value REAL NOT NULL, sort_order INTEGER NOT NULL DEFAULT 0,
 UNIQUE(criterion_id, score_value));
CREATE TABLE IF NOT EXISTS performances(
 id INTEGER PRIMARY KEY AUTOINCREMENT, team_id INTEGER NOT NULL UNIQUE REFERENCES teams(id) ON DELETE CASCADE,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','performing','scoring','locked','revealed')),
 timer_status TEXT NOT NULL DEFAULT 'idle' CHECK(timer_status IN ('idle','running','paused','finished','overtime')),
 started_at TEXT, ended_at TEXT, overtime_sec INTEGER NOT NULL DEFAULT 0,
 penalty_status TEXT NOT NULL DEFAULT 'none' CHECK(penalty_status IN ('none','proposed','approved','rejected')),
 penalty_proposed REAL, penalty_applied REAL, penalty_reason TEXT,
 sort_order INTEGER NOT NULL DEFAULT 0, created_at TEXT, updated_at TEXT);
CREATE TABLE IF NOT EXISTS assignments(
 performance_id INTEGER NOT NULL REFERENCES performances(id) ON DELETE CASCADE,
 judge_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, created_at TEXT,
 PRIMARY KEY(performance_id, judge_id));
CREATE TABLE IF NOT EXISTS scores(
 id INTEGER PRIMARY KEY AUTOINCREMENT, performance_id INTEGER NOT NULL REFERENCES performances(id) ON DELETE CASCADE,
 judge_id INTEGER REFERENCES users(id), is_admin_sheet INTEGER NOT NULL DEFAULT 0,
 client_uuid TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','submitted','locked')),
 version INTEGER NOT NULL DEFAULT 1, comment TEXT, submitted_at TEXT, created_at TEXT, updated_at TEXT);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scores_perf_judge ON scores(performance_id, judge_id) WHERE judge_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_scores_admin_sheet ON scores(performance_id) WHERE is_admin_sheet=1;
CREATE TABLE IF NOT EXISTS score_details(
 id INTEGER PRIMARY KEY AUTOINCREMENT, score_id INTEGER NOT NULL REFERENCES scores(id) ON DELETE CASCADE,
 criterion_id INTEGER NOT NULL REFERENCES criteria(id), raw_score REAL NOT NULL, option_id INTEGER REFERENCES criteria_options(id),
 comment TEXT, created_at TEXT, updated_at TEXT, UNIQUE(score_id, criterion_id));
CREATE TABLE IF NOT EXISTS timer_sessions(
 id INTEGER PRIMARY KEY AUTOINCREMENT, performance_id INTEGER NOT NULL REFERENCES performances(id) ON DELETE CASCADE,
 standard_duration_sec INTEGER NOT NULL, started_at TEXT NOT NULL, paused_total_sec INTEGER NOT NULL DEFAULT 0,
 paused_at TEXT, ended_at TEXT, overtime_sec INTEGER NOT NULL DEFAULT 0, created_at TEXT, updated_at TEXT);
CREATE TABLE IF NOT EXISTS audit_logs(
 id INTEGER PRIMARY KEY AUTOINCREMENT, actor_id INTEGER NOT NULL, action TEXT NOT NULL, entity_type TEXT NOT NULL,
 entity_id INTEGER NOT NULL, criterion_id INTEGER, old_value TEXT, new_value TEXT, reason TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS backups(
 id INTEGER PRIMARY KEY AUTOINCREMENT, contest_id INTEGER, file_name TEXT NOT NULL, file_size INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('manual','auto_lock','auto_completed','pre_restore')), created_by INTEGER, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT);
`

func initDB(h *DBHolder) error {
	if _, err := h.Exec(schemaSQL); err != nil {
		return err
	}
	defaults := map[string]string{"schema_version": "1", "login_max_failed": "5", "login_lock_minutes": "15", "session_ttl_hours": "12", "server_port": "8080"}
	for k, v := range defaults {
		if _, err := h.Exec(`INSERT OR IGNORE INTO settings(key,value,updated_at) VALUES(?,?,?)`, k, v, nowISO()); err != nil {
			return err
		}
	}
	var n int
	if err := h.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return seed(h)
	}
	return nil
}

func hashPw(s string) string {
	b, _ := bcrypt.GenerateFromPassword([]byte(s), bcrypt.DefaultCost)
	return string(b)
}

func seed(h *DBHolder) error {
	t := nowISO()
	mk := func(username, name, role, secret string, pin bool) (int64, error) {
		hash := hashPw(secret)
		var pinHash any
		allowPin := 0
		if pin {
			pinHash = hash
			allowPin = 1
		}
		r, err := h.Exec(`INSERT INTO users(username,display_name,role,password_hash,pin_hash,allow_pin_login,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			username, name, role, hash, pinHash, allowPin, t, t)
		if err != nil {
			return 0, err
		}
		return r.LastInsertId()
	}
	if _, err := mk("admin", "Trưởng ban (Admin)", "admin", "admin123", false); err != nil {
		return err
	}
	if _, err := mk("mc", "MC / Màn hình khán giả", "mc", "mc123", false); err != nil {
		return err
	}
	judgeIDs := []int64{}
	for i, pin := range []string{"1111", "2222", "3333"} {
		id, err := mk(fmt.Sprintf("judge%d", i+1), fmt.Sprintf("Giám khảo %d", i+1), "judge", pin, true)
		if err != nil {
			return err
		}
		judgeIDs = append(judgeIDs, id)
	}
	r, err := h.Exec(`INSERT INTO contests(name_vi,name_en,desc_vi,desc_en,location,status,join_code,allow_judge_edit_after_submit,drop_highest_lowest,min_judges_for_trim,standard_duration_sec,warning_before_sec,penalty_enabled,penalty_interval_sec,penalty_first_points,penalty_next_points,penalty_max_points,created_at,updated_at)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"Cuộc thi mẫu — Tổng duyệt", "Sample Contest — Dry Run", "Cuộc thi mẫu để tổng duyệt ngay sau khi cài đặt. Hãy đổi/nhân bản trước khi thi thật.", "Sample contest for dry run.", "Hội trường mẫu",
		"pending", "DEMO"+randHex(2), 1, 1, 5, 600, 60, 1, 30, 1.0, 0.5, 5.0, t, t)
	if err != nil {
		return err
	}
	cid, _ := r.LastInsertId()
	r, err = h.Exec(`INSERT INTO rounds(contest_id,name_vi,name_en,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?)`, cid, "Vòng chung kết", "Final Round", 1, t, t)
	if err != nil {
		return err
	}
	roundID, _ := r.LastInsertId()
	teamNames := [][2]string{{"Đội 1 — Hoa Sen", "Team 1 — Lotus"}, {"Đội 2 — Trống Đồng", "Team 2 — Bronze Drum"}, {"Đội 3 — Sông Hồng", "Team 3 — Red River"}, {"Đội 4 — Tre Việt", "Team 4 — Bamboo"}}
	perfIDs := []int64{}
	for i, tn := range teamNames {
		r, err = h.Exec(`INSERT INTO teams(round_id,code,name_vi,name_en,organization,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			roundID, fmt.Sprintf("%02d", i+1), tn[0], tn[1], "Đơn vị mẫu", i+1, t, t)
		if err != nil {
			return err
		}
		teamID, _ := r.LastInsertId()
		r, err = h.Exec(`INSERT INTO performances(team_id,status,sort_order,created_at,updated_at) VALUES(?,?,?,?,?)`, teamID, "pending", i+1, t, t)
		if err != nil {
			return err
		}
		pid, _ := r.LastInsertId()
		perfIDs = append(perfIDs, pid)
	}
	r, err = h.Exec(`INSERT INTO criteria_groups(contest_id,name_vi,name_en,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?)`, cid, "Nội dung & Trình bày", "Content & Presentation", 1, t, t)
	if err != nil {
		return err
	}
	gid, _ := r.LastInsertId()
	mkCrit := func(nameVi, nameEn, typ string, min, max, step, weight float64, adminOnly, penalty int, opts [][3]any) error {
		r, err := h.Exec(`INSERT INTO criteria(group_id,name_vi,name_en,input_type,min_score,max_score,step,weight,is_admin_only,is_penalty_criterion,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			gid, nameVi, nameEn, typ, min, max, step, weight, adminOnly, penalty, 0, t, t)
		if err != nil {
			return err
		}
		critID, _ := r.LastInsertId()
		for i, o := range opts {
			if _, err := h.Exec(`INSERT INTO criteria_options(criterion_id,label_vi,label_en,score_value,sort_order) VALUES(?,?,?,?,?)`, critID, o[0], o[1], o[2], i+1); err != nil {
				return err
			}
		}
		if penalty == 1 {
			// penalty criterion id stored nowhere special; found by is_penalty_criterion within contest
			_ = critID
		}
		return nil
	}
	if err := mkCrit("Nội dung", "Content", "range", 0, 10, 0.5, 1, 0, 0, nil); err != nil {
		return err
	}
	if err := mkCrit("Sáng tạo", "Creativity", "discrete", 0, 10, 1, 2, 0, 0, [][3]any{{"Kém", "Poor", 2.0}, {"Khá", "Fair", 5.0}, {"Tốt", "Good", 8.0}, {"Xuất sắc", "Excellent", 10.0}}); err != nil {
		return err
	}
	if err := mkCrit("Trình bày", "Presentation", "range", 0, 10, 0.5, 1, 0, 0, nil); err != nil {
		return err
	}
	if err := mkCrit("Điểm hội đồng (Admin)", "Council bonus/penalty (Admin)", "range", 0, 5, 0.5, 1, 1, 0, nil); err != nil {
		return err
	}
	if err := mkCrit("Trừ điểm quá giờ", "Overtime penalty", "range", 0, 5, 0.5, 1, 1, 1, nil); err != nil {
		return err
	}
	for _, pid := range perfIDs {
		for _, jid := range judgeIDs {
			if _, err := h.Exec(`INSERT INTO assignments(performance_id,judge_id,created_at) VALUES(?,?,?)`, pid, jid, t); err != nil {
				return err
			}
		}
	}
	_ = os.MkdirAll(filepath.Join(h.dir, "backups"), 0o755)
	fmt.Println("[seed] Đã tạo dữ liệu mẫu: admin/admin123, judge1..3 (PIN 1111/2222/3333), mc/mc123, 1 contest mẫu (pending).")
	return nil
}

func audit(h *DBHolder, actorID int64, action, entityType string, entityID int64, criterionID any, oldVal, newVal, reason string) {
	_, _ = h.Exec(`INSERT INTO audit_logs(actor_id,action,entity_type,entity_id,criterion_id,old_value,new_value,reason,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		actorID, action, entityType, entityID, criterionID, oldVal, newVal, reason, nowISO())
}
