package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed web
var webFS embed.FS

type Server struct {
	h   *DBHolder
	hub *Hub
}

func main() {
	port := flag.Int("port", 8080, "cổng lắng nghe")
	dbPath := flag.String("db", "contest.db", "đường dẫn file SQLite")
	flag.Parse()
	if v := os.Getenv("PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			*port = p
		}
	}
	abs, _ := filepath.Abs(*dbPath)
	holder := &DBHolder{path: abs, dir: filepath.Dir(abs)}
	db, err := openDB(abs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Không mở được database:", err)
		os.Exit(1)
	}
	holder.db = db
	if err := initDB(holder); err != nil {
		fmt.Fprintln(os.Stderr, "Khởi tạo database thất bại:", err)
		os.Exit(1)
	}
	s := &Server{h: holder, hub: newHub()}
	go s.timerLoop()
	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("Contest Scoring System đang chạy: http://localhost:%d\n", *port)
	if ips, err := net.InterfaceAddrs(); err == nil {
		for _, a := range ips {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				fmt.Printf("  Trên mạng LAN: http://%s:%d  (giám khảo/MC truy cập địa chỉ này hoặc quét QR trong Admin Dashboard)\n", ipnet.IP.String(), *port)
			}
		}
	}
	fmt.Println("Đăng nhập mẫu: admin/admin123 — HÃY ĐỔI MẬT KHẨU TRƯỚC KHI THI THẬT.")
	if err := http.ListenAndServe(addr, s.routes()); err != nil {
		fmt.Fprintln(os.Stderr, "Server dừng:", err)
		os.Exit(1)
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/", s.apiDispatch)
	mux.HandleFunc("/ws", s.handleWS)
	sub, _ := fs.Sub(webFS, "web")
	fileServer := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// SPA fallback: non-file paths return index.html
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(sub, p); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		data, _ := fs.ReadFile(sub, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
	return mux
}

func seg(path, prefix string) []string {
	p := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func (s *Server) apiDispatch(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// public
	if path == "/api/v1/auth/login" && r.Method == http.MethodPost {
		s.handleLogin(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/v1/public/join/") {
		s.handleJoinInfo(w, r)
		return
	}
	// everything else needs auth; handlers enforce role via wrappers below
	auth := func(fn func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc { return s.requireAuth(fn) }
	admin := func(fn func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc { return s.requireAdmin(fn) }

	switch {
	// auth self
	case path == "/api/v1/auth/logout" && r.Method == http.MethodPost:
		auth(s.handleLogout)(w, r)
	case path == "/api/v1/auth/me" && r.Method == http.MethodGet:
		auth(s.handleMe)(w, r)
	case path == "/api/v1/auth/change-password" && r.Method == http.MethodPost:
		auth(s.handleChangePassword)(w, r)
	// users
	case path == "/api/v1/users" && r.Method == http.MethodGet:
		admin(s.handleListUsers)(w, r)
	case path == "/api/v1/users" && r.Method == http.MethodPost:
		admin(s.handleCreateUser)(w, r)
	case strings.HasPrefix(path, "/api/v1/users/") && strings.HasSuffix(path, "/reset-credential"):
		admin(s.handleResetCredential)(w, r)
	case strings.HasPrefix(path, "/api/v1/users/") && strings.HasSuffix(path, "/unlock"):
		admin(s.handleUnlockUser)(w, r)
	case strings.HasPrefix(path, "/api/v1/users/") && r.Method == http.MethodPatch:
		admin(s.handlePatchUser)(w, r)
	// contests collection
	case path == "/api/v1/contests" && r.Method == http.MethodGet:
		auth(s.handleListContests)(w, r)
	case path == "/api/v1/contests" && r.Method == http.MethodPost:
		admin(s.handleCreateContest)(w, r)
	// contest sub-resources with suffixes
	case strings.HasPrefix(path, "/api/v1/contests/"):
		s.contestSub(w, r, auth, admin)
	// rounds
	case strings.HasPrefix(path, "/api/v1/rounds/"):
		s.roundSub(w, r, auth, admin)
	case strings.HasPrefix(path, "/api/v1/teams/import-template"):
		admin(s.handleImportTemplate)(w, r)
	case strings.HasPrefix(path, "/api/v1/teams/"):
		admin(s.handlePatchTeam)(w, r)
	case strings.HasPrefix(path, "/api/v1/criteria-groups/") && r.Method == http.MethodPost:
		// POST /criteria-groups/:id/criteria handled inside group dispatch below
		s.groupSub(w, r, admin)
	case strings.HasPrefix(path, "/api/v1/criteria-groups/"):
		s.groupSub(w, r, admin)
	case strings.HasPrefix(path, "/api/v1/criteria-options/"):
		admin(s.handleOptionByID)(w, r)
	case strings.HasPrefix(path, "/api/v1/criteria/"):
		s.criterionSub(w, r, admin)
	case strings.HasPrefix(path, "/api/v1/performances/"):
		s.perfSub(w, r, auth, admin)
	case strings.HasPrefix(path, "/api/v1/scores/"):
		s.scoreSub(w, r, auth, admin)
	case path == "/api/v1/judges/me/progress":
		auth(s.handleMyProgress)(w, r)
	case path == "/api/v1/judges/me/assignments":
		auth(s.handleMyAssignments)(w, r)
	case path == "/api/v1/backups" && r.Method == http.MethodGet:
		admin(s.handleListBackups)(w, r)
	case path == "/api/v1/backups" && r.Method == http.MethodPost:
		admin(s.handleCreateBackup)(w, r)
	case strings.HasPrefix(path, "/api/v1/backups/"):
		admin(s.handleRestoreOrDownload)(w, r)
	default:
		writeErr(w, 404, "NOT_FOUND", "Endpoint không tồn tại.", nil)
	}
}

func (s *Server) handleRestoreOrDownload(w http.ResponseWriter, r *http.Request, u *User) {
	if strings.HasSuffix(r.URL.Path, "/download") {
		s.handleDownloadBackup(w, r, u)
		return
	}
	s.handleRestoreBackup(w, r, u)
}

func (s *Server) contestSub(w http.ResponseWriter, r *http.Request, auth, admin func(func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc) {
	parts := seg(r.URL.Path, "/api/v1/contests/")
	if len(parts) == 0 {
		writeErr(w, 404, "NOT_FOUND", "Thiếu contest id.", nil)
		return
	}
	if len(parts) == 1 {
		if r.Method == http.MethodGet {
			auth(s.handleGetContest)(w, r)
		} else if r.Method == http.MethodPatch {
			admin(s.handlePatchContest)(w, r)
		}
		return
	}
	switch parts[1] {
	case "activate":
		admin(s.handleActivate)(w, r)
	case "complete":
		admin(s.handleComplete)(w, r)
	case "finalize":
		admin(func(w2 http.ResponseWriter, r2 *http.Request, u2 *User) { s.handleFinalize(w2, r2, u2, false) })(w, r)
	case "unfinalize":
		admin(func(w2 http.ResponseWriter, r2 *http.Request, u2 *User) { s.handleFinalize(w2, r2, u2, true) })(w, r)
	case "clone":
		admin(s.handleClone)(w, r)
	case "reset-scores":
		admin(s.handleResetScores)(w, r)
	case "state":
		auth(s.handleContestState)(w, r)
	case "rounds":
		if r.Method == http.MethodGet {
			auth(s.handleRounds)(w, r)
		} else {
			admin(s.handleRounds)(w, r)
		}
	case "criteria":
		auth(s.handleGetCriteria)(w, r)
	case "criteria-groups":
		admin(s.handleCreateGroup)(w, r)
	case "assignments":
		if r.Method == http.MethodGet {
			admin(s.handleGetAssignments)(w, r)
		} else {
			admin(s.handlePutAssignments)(w, r)
		}
	case "live-matrix":
		admin(s.handleLiveMatrix)(w, r)
	case "audit-logs":
		admin(s.handleAuditLogs)(w, r)
	case "results":
		admin(s.handleResults)(w, r)
	case "public-state":
		auth(s.handlePublicState)(w, r)
	case "export.xlsx":
		admin(s.handleExport)(w, r)
	default:
		writeErr(w, 404, "NOT_FOUND", "Endpoint không tồn tại.", nil)
	}
}

func (s *Server) roundSub(w http.ResponseWriter, r *http.Request, auth, admin func(func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc) {
	parts := seg(r.URL.Path, "/api/v1/rounds/")
	if len(parts) == 1 {
		if r.Method == http.MethodPatch {
			admin(s.handlePatchRound)(w, r)
		}
		return
	}
	switch parts[1] {
	case "teams":
		if len(parts) > 2 && parts[2] == "import" {
			admin(s.handleImportTeamsCSV)(w, r)
			return
		}
		if r.Method == http.MethodGet {
			auth(s.handleTeams)(w, r)
		} else {
			admin(s.handleTeams)(w, r)
		}
	case "leaderboard":
		auth(s.handleLeaderboard)(w, r)
	case "tie-break":
		admin(s.handleTieBreak)(w, r)
	case "leaderboard-reveal":
		admin(s.handleLeaderboardReveal)(w, r)
	case "shuffle-order":
		admin(s.handleShuffle)(w, r)
	default:
		writeErr(w, 404, "NOT_FOUND", "Endpoint không tồn tại.", nil)
	}
}

func (s *Server) groupSub(w http.ResponseWriter, r *http.Request, admin func(func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc) {
	parts := seg(r.URL.Path, "/api/v1/criteria-groups/")
	if len(parts) > 1 && parts[1] == "criteria" {
		admin(s.handleCreateCriterion)(w, r)
		return
	}
	admin(s.handleGroupByID)(w, r)
}

func (s *Server) criterionSub(w http.ResponseWriter, r *http.Request, admin func(func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc) {
	admin(s.handleCriterionByID)(w, r)
}

func (s *Server) perfSub(w http.ResponseWriter, r *http.Request, auth, admin func(func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc) {
	parts := seg(r.URL.Path, "/api/v1/performances/")
	if len(parts) < 2 {
		writeErr(w, 404, "NOT_FOUND", "Thiếu hành động.", nil)
		return
	}
	switch parts[1] {
	case "my-score":
		auth(s.handleMyScore)(w, r)
	case "scores":
		admin(s.handlePerfScores)(w, r)
	case "admin-sheet":
		admin(s.handleAdminSheet)(w, r)
	default:
		admin(s.perfAction)(w, r)
	}
}

func (s *Server) scoreSub(w http.ResponseWriter, r *http.Request, auth, admin func(func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc) {
	parts := seg(r.URL.Path, "/api/v1/scores/")
	if len(parts) >= 2 && parts[0] == "by-client-uuid" {
		auth(s.handleUpsertScore)(w, r)
		return
	}
	if len(parts) >= 2 && parts[1] == "submit" {
		auth(s.handleSubmitScore)(w, r)
		return
	}
	if len(parts) >= 2 && parts[1] == "override" {
		admin(s.handleOverride)(w, r)
		return
	}
	if r.Method == http.MethodPatch {
		auth(s.handlePatchScore)(w, r)
		return
	}
	writeErr(w, 404, "NOT_FOUND", "Endpoint không tồn tại.", nil)
}
