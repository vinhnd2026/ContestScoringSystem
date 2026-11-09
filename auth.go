package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	IsActive    bool   `json:"is_active"`
	AllowPin    bool   `json:"allow_pin_login"`
	LockedUntil string `json:"locked_until,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeErr(w http.ResponseWriter, status int, code, msg string, details any) {
	body := map[string]any{"error": map[string]any{"code": code, "message": msg}}
	if details != nil {
		body["error"].(map[string]any)["details"] = details
	}
	writeJSON(w, status, body)
}
func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func tokenHash(tok string) string {
	s := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(s[:])
}

func (s *Server) currentUser(r *http.Request) (*User, string) {
	c, err := r.Cookie("session")
	if err != nil || c.Value == "" {
		return nil, ""
	}
	var uid int64
	var exp string
	err = s.h.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token_hash=?`, tokenHash(c.Value)).Scan(&uid, &exp)
	if err != nil || exp < nowISO() {
		return nil, ""
	}
	u, err := s.userByID(uid)
	if err != nil || u == nil || !u.IsActive {
		return nil, ""
	}
	return u, c.Value
}

func (s *Server) userByID(id int64) (*User, error) {
	var u User
	var active, pin int
	err := s.h.QueryRow(`SELECT id,username,display_name,role,is_active,allow_pin_login,COALESCE(locked_until,'') FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &active, &pin, &u.LockedUntil)
	if err != nil {
		return nil, nil
	}
	u.IsActive = active == 1
	u.AllowPin = pin == 1
	return &u, nil
}

func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _ := s.currentUser(r)
		if u == nil {
			writeErr(w, 401, "UNAUTHENTICATED", "Chưa đăng nhập hoặc phiên đã hết hạn.", nil)
			return
		}
		next(w, r, u)
	}
}
func (s *Server) requireAdmin(next func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, u *User) {
		if u.Role != "admin" {
			writeErr(w, 403, "FORBIDDEN", "Chỉ Admin mới thực hiện được thao tác này.", nil)
			return
		}
		next(w, r, u)
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Pin      string `json:"pin"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	secret := in.Password
	if secret == "" {
		secret = in.Pin
	}
	var id int64
	var pwHash, pinHash, lockedUntil sql.NullString
	var failed, active int
	err := s.h.QueryRow(`SELECT id,COALESCE(password_hash,''),COALESCE(pin_hash,''),failed_login_count,COALESCE(locked_until,''),is_active FROM users WHERE username=?`, strings.TrimSpace(in.Username)).
		Scan(&id, &pwHash, &pinHash, &failed, &lockedUntil, &active)
	if err != nil || active == 0 {
		writeErr(w, 401, "UNAUTHENTICATED", "Sai tên đăng nhập hoặc mật khẩu/PIN.", nil)
		return
	}
	if lockedUntil.String != "" && lockedUntil.String > nowISO() {
		writeErr(w, 429, "LOGIN_LOCKED", "Tài khoản đang bị khóa đăng nhập do nhập sai nhiều lần. Chờ hoặc nhờ Admin reset.", nil)
		return
	}
	ok := false
	if pwHash.String != "" && bcrypt.CompareHashAndPassword([]byte(pwHash.String), []byte(secret)) == nil {
		ok = true
	}
	if !ok && pinHash.String != "" && bcrypt.CompareHashAndPassword([]byte(pinHash.String), []byte(secret)) == nil {
		ok = true
	}
	if !ok {
		failed++
		var lock any
		if failed >= 5 {
			lock = time.Now().UTC().Add(15 * time.Minute).Format("2006-01-02T15:04:05Z")
		}
		_, _ = s.h.Exec(`UPDATE users SET failed_login_count=?, locked_until=?, updated_at=? WHERE id=?`, failed, lock, nowISO(), id)
		writeErr(w, 401, "UNAUTHENTICATED", "Sai tên đăng nhập hoặc mật khẩu/PIN.", nil)
		return
	}
	_, _ = s.h.Exec(`UPDATE users SET failed_login_count=0, locked_until=NULL, updated_at=? WHERE id=?`, nowISO(), id)
	tok := randHex(32)
	exp := time.Now().UTC().Add(12 * time.Hour).Format("2006-01-02T15:04:05Z")
	_, _ = s.h.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at,created_at) VALUES(?,?,?,?)`, tokenHash(tok), id, exp, nowISO())
	http.SetCookie(w, &http.Cookie{Name: "session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(12 * time.Hour)})
	u, _ := s.userByID(id)
	writeJSON(w, 200, map[string]any{"user": u})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, u *User) {
	_, tok := s.currentUser(r)
	if tok != "" {
		_, _ = s.h.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenHash(tok))
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, u *User) {
	writeJSON(w, 200, map[string]any{"user": u})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decodeJSON(r, &in); err != nil || len(in.New) < 4 {
		writeErr(w, 400, "VALIDATION_ERROR", "Mật khẩu/PIN mới phải có ít nhất 4 ký tự.", nil)
		return
	}
	var pwHash string
	_ = s.h.QueryRow(`SELECT COALESCE(password_hash,'') FROM users WHERE id=?`, u.ID).Scan(&pwHash)
	if pwHash == "" || bcrypt.CompareHashAndPassword([]byte(pwHash), []byte(in.Current)) != nil {
		writeErr(w, 403, "FORBIDDEN", "Mật khẩu hiện tại không đúng.", nil)
		return
	}
	_, _ = s.h.Exec(`UPDATE users SET password_hash=?, pin_hash=?, updated_at=? WHERE id=?`, hashPw(in.New), hashPw(in.New), nowISO(), u.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- Users admin ----------
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := s.h.Query(`SELECT id FROM users ORDER BY role, id`)
	if err != nil {
		writeErr(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []*User{}
	for _, id := range ids {
		if usr, _ := s.userByID(id); usr != nil {
			out = append(out, usr)
		}
	}
	writeJSON(w, 200, map[string]any{"users": out})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, u *User) {
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if err := decodeJSON(r, &in); err != nil || in.Username == "" || in.Password == "" || (in.Role != "admin" && in.Role != "judge" && in.Role != "mc") {
		writeErr(w, 400, "VALIDATION_ERROR", "Thiếu username/mật khẩu hoặc role không hợp lệ (admin/judge/mc).", nil)
		return
	}
	hash := hashPw(in.Password)
	allowPin := 0
	if in.Role == "judge" {
		allowPin = 1
	}
	res, err := s.h.Exec(`INSERT INTO users(username,display_name,role,password_hash,pin_hash,allow_pin_login,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		in.Username, in.DisplayName, in.Role, hash, hash, allowPin, nowISO(), nowISO())
	if err != nil {
		writeErr(w, 409, "VALIDATION_ERROR", "Username đã tồn tại.", nil)
		return
	}
	id, _ := res.LastInsertId()
	usr, _ := s.userByID(id)
	writeJSON(w, 201, map[string]any{"user": usr})
}

func pathID(r *http.Request, prefix string) int64 {
	p := strings.TrimPrefix(r.URL.Path, prefix)
	p = strings.Trim(p, "/")
	parts := strings.Split(p, "/")
	if len(parts) == 0 {
		return 0
	}
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	return id
}
func pathParts(r *http.Request, prefix string) []string {
	p := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func (s *Server) handlePatchUser(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/users/")
	var in struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		IsActive    *bool   `json:"is_active"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	if in.DisplayName != nil {
		_, _ = s.h.Exec(`UPDATE users SET display_name=?, updated_at=? WHERE id=?`, *in.DisplayName, nowISO(), id)
	}
	if in.Role != nil {
		_, _ = s.h.Exec(`UPDATE users SET role=?, updated_at=? WHERE id=?`, *in.Role, nowISO(), id)
	}
	if in.IsActive != nil {
		v := 0
		if *in.IsActive {
			v = 1
		}
		_, _ = s.h.Exec(`UPDATE users SET is_active=?, updated_at=? WHERE id=?`, v, nowISO(), id)
	}
	usr, _ := s.userByID(id)
	if usr == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy tài khoản.", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"user": usr})
}

func (s *Server) handleResetCredential(w http.ResponseWriter, r *http.Request, u *User) {
	parts := pathParts(r, "/api/v1/users/")
	if len(parts) < 1 {
		writeErr(w, 404, "NOT_FOUND", "Thiếu id.", nil)
		return
	}
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	var in struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	_ = decodeJSON(r, &in)
	val := in.Value
	generated := false
	if val == "" {
		val = randHex(3)
		generated = true
	}
	col := "password_hash"
	action := "user.reset_password"
	if in.Type == "pin" {
		col = "pin_hash"
		action = "user.reset_pin"
	}
	_, _ = s.h.Exec(`UPDATE users SET `+col+`=?, failed_login_count=0, locked_until=NULL, updated_at=? WHERE id=?`, hashPw(val), nowISO(), id)
	audit(s.h, u.ID, action, "user", id, nil, "", "", "Admin reset tại hiện trường")
	resp := map[string]any{"ok": true}
	if generated {
		resp["generated_value"] = val // trả một lần duy nhất khi server sinh
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleUnlockUser(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/users/")
	_, _ = s.h.Exec(`UPDATE users SET failed_login_count=0, locked_until=NULL, updated_at=? WHERE id=?`, nowISO(), id)
	writeJSON(w, 200, map[string]any{"ok": true})
}
