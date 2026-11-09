package main

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func penaltyCalc(overtime int, c *Contest) float64 {
	if !c.PenaltyEnabled || overtime <= 0 || c.PenaltyIntervalSec <= 0 {
		return 0
	}
	n := int(math.Ceil(float64(overtime) / float64(c.PenaltyIntervalSec)))
	p := c.PenaltyFirstPoints + float64(n-1)*c.PenaltyNextPoints
	if p > c.PenaltyMaxPoints {
		p = c.PenaltyMaxPoints
	}
	return p
}

func (s *Server) perfAction(w http.ResponseWriter, r *http.Request, u *User) {
	parts := pathParts(r, "/api/v1/performances/")
	if len(parts) < 2 {
		writeErr(w, 404, "NOT_FOUND", "Thiếu hành động.", nil)
		return
	}
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	action := parts[1]
	p := s.perfByID(id)
	if p == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy phần thi.", nil)
		return
	}
	c := s.contestByID(p.ContestID)
	if c != nil && c.IsFinalized {
		writeErr(w, 409, "CONFIG_LOCKED", "Cuộc thi đã chốt sổ.", nil)
		return
	}
	var in struct {
		Reason         string   `json:"reason"`
		PenaltyApplied *float64 `json:"penalty_applied"`
	}
	_ = decodeJSON(r, &in)
	t := nowISO()
	switch action {
	case "start":
		if p.Status != "pending" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Chỉ bắt đầu được phần thi đang pending.", nil)
			return
		}
		if c == nil || c.Status != "active" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Cuộc thi chưa active.", nil)
			return
		}
		if !c.AllowParallelRounds {
			var other int
			_ = s.h.QueryRow(`SELECT COUNT(*) FROM performances p2 JOIN teams t2 ON t2.id=p2.team_id JOIN rounds r2 ON r2.id=t2.round_id WHERE r2.contest_id=? AND p2.status='performing' AND p2.id<>?`, p.ContestID, id).Scan(&other)
			if other > 0 {
				writeErr(w, 422, "INVALID_STATE_TRANSITION", "Đang có phần thi khác đang diễn ra (allow_parallel_rounds=false).", nil)
				return
			}
		}
		_, _ = s.h.Exec(`INSERT INTO timer_sessions(performance_id,standard_duration_sec,started_at,created_at,updated_at) VALUES(?,?,?,?,?)`, id, p.DurationSec, t, t, t)
		_, _ = s.h.Exec(`UPDATE performances SET status='performing', timer_status='running', started_at=?, updated_at=? WHERE id=?`, t, t, id)
		s.broadcast(map[string]any{"type": "performance.started", "contest_id": p.ContestID, "performance_id": id, "team_id": p.TeamID, "round_id": p.RoundID, "started_at": t, "standard_duration_sec": p.DurationSec})
	case "pause":
		if p.Status != "performing" || p.TimerStatus == "paused" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Timer không ở trạng thái đang chạy.", nil)
			return
		}
		_, _ = s.h.Exec(`UPDATE timer_sessions SET paused_at=?, updated_at=? WHERE id=(SELECT MAX(id) FROM timer_sessions WHERE performance_id=?)`, t, t, id)
		_, _ = s.h.Exec(`UPDATE performances SET timer_status='paused', updated_at=? WHERE id=?`, t, id)
		s.broadcast(map[string]any{"type": "performance.paused", "performance_id": id, "paused_at": t})
	case "resume":
		if p.Status != "performing" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Phần thi không đang diễn ra.", nil)
			return
		}
		var pausedAt *string
		var sid int64
		err := s.h.QueryRow(`SELECT id,paused_at FROM timer_sessions WHERE performance_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&sid, &pausedAt)
		if err != nil || pausedAt == nil {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Timer không đang tạm dừng.", nil)
			return
		}
		pa, _ := time.Parse("2006-01-02T15:04:05Z", *pausedAt)
		add := int(time.Now().UTC().Sub(pa).Seconds())
		_, _ = s.h.Exec(`UPDATE timer_sessions SET paused_at=NULL, paused_total_sec=paused_total_sec+?, updated_at=? WHERE id=?`, add, t, sid)
		_, _ = s.h.Exec(`UPDATE performances SET timer_status='running', updated_at=? WHERE id=?`, t, id)
		s.broadcast(map[string]any{"type": "performance.resumed", "performance_id": id})
	case "end":
		if p.Status != "performing" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Phần thi không đang diễn ra.", nil)
			return
		}
		// close session (account for open pause)
		var sid int64
		var pausedAt *string
		_ = s.h.QueryRow(`SELECT id,paused_at FROM timer_sessions WHERE performance_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&sid, &pausedAt)
		if pausedAt != nil {
			pa, _ := time.Parse("2006-01-02T15:04:05Z", *pausedAt)
			add := int(time.Now().UTC().Sub(pa).Seconds())
			_, _ = s.h.Exec(`UPDATE timer_sessions SET paused_at=NULL, paused_total_sec=paused_total_sec+? WHERE id=?`, add, sid)
		}
		_, _ = s.h.Exec(`UPDATE timer_sessions SET ended_at=?, updated_at=? WHERE id=?`, t, t, sid)
		cur := s.perfByID(id)
		overtime := cur.LiveOvertimeSec
		_, _ = s.h.Exec(`UPDATE timer_sessions SET overtime_sec=? WHERE id=?`, overtime, sid)
		penStatus := "none"
		var proposed any
		if c != nil && c.PenaltyEnabled && overtime > 0 {
			pp := penaltyCalc(overtime, c)
			penStatus = "proposed"
			proposed = pp
			_, _ = s.h.Exec(`UPDATE performances SET status='scoring', timer_status='finished', ended_at=?, overtime_sec=?, penalty_status='proposed', penalty_proposed=?, updated_at=? WHERE id=?`, t, overtime, pp, t, id)
		} else {
			_, _ = s.h.Exec(`UPDATE performances SET status='scoring', timer_status='finished', ended_at=?, overtime_sec=?, penalty_status='none', updated_at=? WHERE id=?`, t, overtime, t, id)
		}
		_ = proposed
		s.broadcast(map[string]any{"type": "performance.ended", "performance_id": id, "status": "scoring", "overtime_sec": overtime, "penalty_status": penStatus, "penalty_proposed": proposed})
	case "approve-penalty", "reject-penalty":
		if p.PenaltyStatus != "proposed" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Không có đề xuất trừ điểm đang chờ duyệt.", nil)
			return
		}
		if action == "reject-penalty" {
			if strings.TrimSpace(in.Reason) == "" {
				writeErr(w, 400, "VALIDATION_ERROR", "Từ chối trừ điểm bắt buộc nhập lý do.", nil)
				return
			}
			_, _ = s.h.Exec(`UPDATE performances SET penalty_status='rejected', penalty_applied=NULL, penalty_reason=?, updated_at=? WHERE id=?`, in.Reason, t, id)
			audit(s.h, u.ID, "penalty.reject", "performance", id, nil, fmt.Sprint(*p.PenaltyProposed), "", in.Reason)
		} else {
			applied := *p.PenaltyProposed
			if in.PenaltyApplied != nil {
				applied = *in.PenaltyApplied
				if applied != *p.PenaltyProposed && strings.TrimSpace(in.Reason) == "" {
					writeErr(w, 400, "VALIDATION_ERROR", "Sửa mức trừ khi duyệt bắt buộc nhập lý do.", nil)
					return
				}
			}
			// write into penalty criterion detail of admin sheet
			var critID int64
			err := s.h.QueryRow(`SELECT c.id FROM criteria c JOIN criteria_groups g ON g.id=c.group_id WHERE g.contest_id=? AND c.is_penalty_criterion=1 LIMIT 1`, p.ContestID).Scan(&critID)
			if err != nil {
				writeErr(w, 422, "VALIDATION_ERROR", "Chưa có tiêu chí Admin-Only dành cho trừ giờ.", nil)
				return
			}
			sheetID := s.ensureAdminSheet(id, u.ID)
			_, _ = s.h.Exec(`INSERT INTO score_details(score_id,criterion_id,raw_score,comment,created_at,updated_at) VALUES(?,?,?, 'Trừ điểm quá giờ đã duyệt',?,?) ON CONFLICT(score_id,criterion_id) DO UPDATE SET raw_score=excluded.raw_score, updated_at=excluded.updated_at`, sheetID, critID, -applied, t, t)
			_, _ = s.h.Exec(`UPDATE performances SET penalty_status='approved', penalty_applied=?, penalty_reason=?, updated_at=? WHERE id=?`, applied, in.Reason, t, id)
			audit(s.h, u.ID, "penalty.approve", "performance", id, critID, fmt.Sprint(*p.PenaltyProposed), fmt.Sprint(applied), in.Reason)
		}
		s.broadcast(map[string]any{"type": "penalty.updated", "performance_id": id})
		s.broadcast(map[string]any{"type": "leaderboard.updated", "round_id": p.RoundID})
	case "lock":
		if p.Status != "scoring" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Chỉ khóa được phần thi đang scoring.", nil)
			return
		}
		var assigned, submitted int
		_ = s.h.QueryRow(`SELECT COUNT(*) FROM assignments WHERE performance_id=?`, id).Scan(&assigned)
		_ = s.h.QueryRow(`SELECT COUNT(*) FROM scores WHERE performance_id=? AND is_admin_sheet=0 AND status IN ('submitted','locked')`, id).Scan(&submitted)
		if submitted < assigned && strings.TrimSpace(in.Reason) == "" {
			writeErr(w, 400, "VALIDATION_ERROR", fmt.Sprintf("Còn %d giám khảo chưa nộp; khóa thiếu bắt buộc nhập lý do.", assigned-submitted), nil)
			return
		}
		_, _ = s.h.Exec(`UPDATE scores SET status='locked', updated_at=? WHERE performance_id=? AND status='submitted'`, t, id)
		_, _ = s.h.Exec(`UPDATE performances SET status='locked', updated_at=? WHERE id=?`, t, id)
		audit(s.h, u.ID, "performance.lock", "performance", id, nil, "scoring", "locked", in.Reason)
		_, _, _ = s.createBackup(p.ContestID, "auto_lock", u.ID)
		s.broadcast(map[string]any{"type": "performance.locked", "performance_id": id, "status": "locked"})
		s.broadcast(map[string]any{"type": "leaderboard.updated", "round_id": p.RoundID})
	case "unlock":
		if p.Status != "locked" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Chỉ mở khóa được phần thi đang locked.", nil)
			return
		}
		if strings.TrimSpace(in.Reason) == "" {
			writeErr(w, 400, "VALIDATION_ERROR", "Mở khóa bắt buộc nhập lý do.", nil)
			return
		}
		_, _ = s.h.Exec(`UPDATE scores SET status='submitted', updated_at=? WHERE performance_id=? AND status='locked' AND is_admin_sheet=0`, t, id)
		_, _ = s.h.Exec(`UPDATE performances SET status='scoring', updated_at=? WHERE id=?`, t, id)
		audit(s.h, u.ID, "performance.unlock", "performance", id, nil, "locked", "scoring", in.Reason)
		s.broadcast(map[string]any{"type": "contest.state", "contest_id": p.ContestID})
	case "reveal":
		if p.Status != "locked" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Chỉ công bố được phần thi đã khóa điểm.", nil)
			return
		}
		_, _ = s.h.Exec(`UPDATE performances SET status='revealed', updated_at=? WHERE id=?`, t, id)
		tr := s.teamResult(s.perfByID(id), c)
		s.broadcast(map[string]any{"type": "performance.revealed", "performance_id": id, "status": "revealed", "final_score": tr.FinalScore})
	case "unreveal":
		if p.Status != "revealed" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Phần thi chưa được công bố.", nil)
			return
		}
		_, _ = s.h.Exec(`UPDATE performances SET status='locked', updated_at=? WHERE id=?`, t, id)
		audit(s.h, u.ID, "performance.unreveal", "performance", id, nil, "revealed", "locked", in.Reason)
		s.broadcast(map[string]any{"type": "performance.unrevealed", "performance_id": id, "status": "locked"})
	default:
		writeErr(w, 404, "NOT_FOUND", "Hành động không tồn tại.", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"performance": s.perfByID(id)})
}

func (s *Server) handleLeaderboardReveal(w http.ResponseWriter, r *http.Request, u *User) {
	rid := pathID(r, "/api/v1/rounds/")
	var in struct {
		Revealed bool `json:"revealed"`
	}
	_ = decodeJSON(r, &in)
	v := 0
	if in.Revealed {
		v = 1
	}
	_, _ = s.h.Exec(`UPDATE rounds SET leaderboard_revealed=?, updated_at=? WHERE id=?`, v, nowISO(), rid)
	audit(s.h, u.ID, "round.leaderboard_reveal", "round", rid, nil, "", fmt.Sprint(in.Revealed), "")
	s.broadcast(map[string]any{"type": "leaderboard.revealed", "round_id": rid, "leaderboard_revealed": in.Revealed})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) ensureAdminSheet(perfID, _ int64) int64 {
	var id int64
	err := s.h.QueryRow(`SELECT id FROM scores WHERE performance_id=? AND is_admin_sheet=1`, perfID).Scan(&id)
	if err == nil {
		return id
	}
	res, _ := s.h.Exec(`INSERT INTO scores(performance_id,judge_id,is_admin_sheet,client_uuid,status,version,created_at,updated_at) VALUES(?,NULL,1,?,'submitted',1,?,?)`, perfID, "admin-"+randHex(16), nowISO(), nowISO())
	id, _ = res.LastInsertId()
	return id
}

// ---------- Scores ----------
type detailIn struct {
	CriterionID int64   `json:"criterion_id"`
	RawScore    float64 `json:"raw_score"`
	OptionID    *int64  `json:"option_id"`
	Comment     string  `json:"comment"`
}
type detailOut struct {
	CriterionID int64   `json:"criterion_id"`
	RawScore    float64 `json:"raw_score"`
	OptionID    *int64  `json:"option_id"`
	Comment     string  `json:"comment"`
}
type scoreOut struct {
	ID            int64       `json:"id"`
	PerformanceID int64       `json:"performance_id"`
	JudgeID       *int64      `json:"judge_id"`
	IsAdminSheet  bool        `json:"is_admin_sheet"`
	ClientUUID    string      `json:"client_uuid"`
	Status        string      `json:"status"`
	Version       int         `json:"version"`
	Comment       string      `json:"comment"`
	Details       []detailOut `json:"details"`
	Total         float64     `json:"total"`
}

func (s *Server) scoreByID(id int64) *scoreOut {
	var o scoreOut
	var judge *int64
	var admin int
	err := s.h.QueryRow(`SELECT id,performance_id,judge_id,is_admin_sheet,client_uuid,status,version,COALESCE(comment,'') FROM scores WHERE id=?`, id).
		Scan(&o.ID, &o.PerformanceID, &judge, &admin, &o.ClientUUID, &o.Status, &o.Version, &o.Comment)
	if err != nil {
		return nil
	}
	o.JudgeID = judge
	o.IsAdminSheet = admin == 1
	o.Details = []detailOut{}
	rows, _ := s.h.Query(`SELECT criterion_id,raw_score,option_id,COALESCE(comment,'') FROM score_details WHERE score_id=? ORDER BY criterion_id`, id)
	for rows.Next() {
		var d detailOut
		_ = rows.Scan(&d.CriterionID, &d.RawScore, &d.OptionID, &d.Comment)
		o.Details = append(o.Details, d)
	}
	rows.Close()
	o.Total = round2(s.sheetTotal(id))
	return &o
}
func (s *Server) scoreByUUID(uuid string) *scoreOut {
	var id int64
	err := s.h.QueryRow(`SELECT id FROM scores WHERE client_uuid=?`, uuid).Scan(&id)
	if err != nil {
		return nil
	}
	return s.scoreByID(id)
}

func (s *Server) validateDetails(contestID int64, details []detailIn, adminSheet bool, submit bool) (string, []detailIn) {
	tree := s.criteriaTree(contestID, true)
	byID := map[int64]Criterion{}
	for _, g := range tree {
		for _, c := range g.Criteria {
			byID[c.ID] = c
		}
	}
	seen := map[int64]bool{}
	for _, d := range details {
		c, ok := byID[d.CriterionID]
		if !ok {
			return fmt.Sprintf("Tiêu chí %d không thuộc cuộc thi", d.CriterionID), nil
		}
		if c.IsAdminOnly != adminSheet && !c.IsPenalty {
			// judge sheets: only non-admin; admin sheets: only admin-only
			if adminSheet && !c.IsAdminOnly {
				return fmt.Sprintf("Phiếu Admin chỉ chứa tiêu chí Admin-Only (%d)", d.CriterionID), nil
			}
			if !adminSheet && c.IsAdminOnly {
				return fmt.Sprintf("Tiêu chí %d là Admin-Only, giám khảo không chấm được", d.CriterionID), nil
			}
		}
		if c.IsPenalty && !adminSheet {
			return "Tiêu chí trừ giờ chỉ dành cho Admin", nil
		}
		if c.IsPenalty && adminSheet {
			// penalty detail stored negative, magnitude within max
			if d.RawScore > 0 || -d.RawScore > c.MaxScore {
				return "Điểm trừ giờ phải là số âm trong trần của tiêu chí", nil
			}
		} else if c.IsAdminOnly && adminSheet {
			if d.RawScore < -c.MaxScore || d.RawScore > c.MaxScore {
				return fmt.Sprintf("Điểm Admin-Only của tiêu chí %d ngoài khoảng cho phép", d.CriterionID), nil
			}
		} else {
			if d.RawScore < c.MinScore-1e-9 || d.RawScore > c.MaxScore+1e-9 {
				return fmt.Sprintf("Điểm tiêu chí %d ngoài khoảng [%v, %v]", d.CriterionID, c.MinScore, c.MaxScore), nil
			}
			if c.InputType == "range" {
				steps := (d.RawScore - c.MinScore) / c.Step
				if math.Abs(steps-math.Round(steps)) > 1e-6 {
					return fmt.Sprintf("Điểm tiêu chí %d lệch step %v", d.CriterionID, c.Step), nil
				}
			}
			if c.InputType == "discrete" {
				if d.OptionID == nil {
					return fmt.Sprintf("Tiêu chí discrete %d cần option_id", d.CriterionID), nil
				}
				found := false
				for _, o := range c.Options {
					if o.ID == *d.OptionID && o.ScoreValue == d.RawScore {
						found = true
					}
				}
				if !found {
					return fmt.Sprintf("Option không khớp tiêu chí discrete %d", d.CriterionID), nil
				}
			}
		}
		seen[d.CriterionID] = true
	}
	if submit && !adminSheet {
		for _, c := range byID {
			if !c.IsAdminOnly && !seen[c.ID] {
				return fmt.Sprintf("Thiếu điểm tiêu chí %d (%s)", c.ID, c.NameVi), nil
			}
		}
	}
	return "", details
}

func (s *Server) upsertDetails(scoreID int64, details []detailIn) {
	t := nowISO()
	for _, d := range details {
		_, _ = s.h.Exec(`INSERT INTO score_details(score_id,criterion_id,raw_score,option_id,comment,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(score_id,criterion_id) DO UPDATE SET raw_score=excluded.raw_score, option_id=excluded.option_id, comment=excluded.comment, updated_at=excluded.updated_at`,
			scoreID, d.CriterionID, d.RawScore, d.OptionID, d.Comment, t, t)
	}
}

type scoreUpsertIn struct {
	PerformanceID int64      `json:"performance_id"`
	BaseVersion   *int       `json:"base_version"`
	Details       []detailIn `json:"details"`
	Comment       string     `json:"comment"`
	Submit        bool       `json:"submit"`
	Reason        string     `json:"reason"`
}

func (s *Server) handleUpsertScore(w http.ResponseWriter, r *http.Request, u *User) {
	uuid := strings.TrimPrefix(r.URL.Path, "/api/v1/scores/by-client-uuid/")
	var in scoreUpsertIn
	if err := decodeJSON(r, &in); err != nil || in.PerformanceID == 0 {
		writeErr(w, 400, "VALIDATION_ERROR", "Thiếu performance_id hoặc dữ liệu không hợp lệ.", nil)
		return
	}
	p := s.perfByID(in.PerformanceID)
	if p == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy phần thi.", nil)
		return
	}
	c := s.contestByID(p.ContestID)
	if c != nil && c.IsFinalized {
		writeErr(w, 409, "SCORE_LOCKED", "Cuộc thi đã chốt sổ.", nil)
		return
	}
	adminSheet := false
	switch u.Role {
	case "admin":
		adminSheet = true
	case "judge":
		var assigned int
		_ = s.h.QueryRow(`SELECT COUNT(*) FROM assignments WHERE performance_id=? AND judge_id=?`, in.PerformanceID, u.ID).Scan(&assigned)
		if assigned == 0 {
			writeErr(w, 403, "NOT_ASSIGNED", "Bạn không được phân công chấm phần thi này.", nil)
			return
		}
	default:
		writeErr(w, 403, "FORBIDDEN", "Vai trò của bạn không ghi điểm được.", nil)
		return
	}
	if p.Status == "locked" || p.Status == "revealed" {
		writeErr(w, 409, "SCORE_LOCKED", "Phần thi đã khóa điểm.", nil)
		return
	}
	if msg, _ := s.validateDetails(p.ContestID, in.Details, adminSheet, in.Submit); msg != "" {
		writeErr(w, 422, "VALIDATION_ERROR", msg, nil)
		return
	}
	existing := s.scoreByUUID(uuid)
	t := nowISO()
	if existing == nil {
		// create
		var judgeArg any
		if !adminSheet {
			judgeArg = u.ID
		}
		status := "draft"
		var submittedAt any
		if in.Submit {
			status = "submitted"
			submittedAt = t
		}
		res, err := s.h.Exec(`INSERT INTO scores(performance_id,judge_id,is_admin_sheet,client_uuid,status,version,comment,submitted_at,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?,?,?)`,
			in.PerformanceID, judgeArg, b2i(adminSheet), uuid, status, in.Comment, submittedAt, t, t)
		if err != nil {
			// race: another sheet for same judge/perf
			if ex2 := s.scoreByUUID(uuid); ex2 != nil {
				writeJSON(w, 200, map[string]any{"score": ex2})
				return
			}
			writeErr(w, 409, "VALIDATION_ERROR", "Giám khảo đã có phiếu cho phần thi này (client_uuid khác).", nil)
			return
		}
		sid, _ := res.LastInsertId()
		s.upsertDetails(sid, in.Details)
		s.broadcast(map[string]any{"type": "score.updated", "performance_id": in.PerformanceID, "judge_id": u.ID, "score_status": status, "version": 1})
		writeJSON(w, 201, map[string]any{"score": s.scoreByID(sid)})
		return
	}
	// existing: ownership check
	if !adminSheet && (existing.JudgeID == nil || *existing.JudgeID != u.ID) {
		writeErr(w, 403, "FORBIDDEN", "Phiếu này không thuộc về bạn.", nil)
		return
	}
	if existing.Status == "locked" {
		writeErr(w, 409, "SCORE_LOCKED", "Phiếu đã khóa.", nil)
		return
	}
	// idempotent retry: same base_version-1 == current version? Server rule: if base_version == existing.Version and content identical -> return as-is.
	if in.BaseVersion != nil {
		if *in.BaseVersion == existing.Version {
			// check identical content
			same := len(in.Details) == len(existing.Details)
			if same {
				m := map[int64]detailOut{}
				for _, d := range existing.Details {
					m[d.CriterionID] = d
				}
				for _, d := range in.Details {
					e, ok := m[d.CriterionID]
					if !ok || e.RawScore != d.RawScore {
						same = false
					}
				}
			}
			if same && (!in.Submit || existing.Status == "submitted") {
				writeJSON(w, 200, map[string]any{"score": existing, "idempotent": true})
				return
			}
		}
		if *in.BaseVersion != existing.Version && !(*in.BaseVersion == existing.Version-1 && false) {
			// base_version must equal current version to edit a draft; for submitted-edit the client sends current version too
			if existing.Status == "draft" || existing.Status == "submitted" {
				// If base_version is stale (not current), conflict — except when base_version == version the client created with (handled above)
				if *in.BaseVersion != existing.Version {
					writeErr(w, 409, "SCORE_VERSION_CONFLICT", "Phiếu điểm đã thay đổi trên server. Tải lại trước khi lưu.", map[string]any{"server_version": existing.Version, "server_score": existing})
					return
				}
			}
		}
	}
	// Editing a submitted sheet requires contest permission + reason, and audits per changed criterion
	wasSubmitted := existing.Status == "submitted"
	if wasSubmitted {
		if c == nil || !c.AllowEditAfterSubmit {
			writeErr(w, 403, "FORBIDDEN", "Cuộc thi không cho phép sửa điểm sau khi nộp.", nil)
			return
		}
		if strings.TrimSpace(in.Reason) == "" {
			writeErr(w, 400, "VALIDATION_ERROR", "Sửa điểm đã nộp bắt buộc nhập lý do.", nil)
			return
		}
		oldMap := map[int64]float64{}
		for _, d := range existing.Details {
			oldMap[d.CriterionID] = d.RawScore
		}
		for _, d := range in.Details {
			if ov, ok := oldMap[d.CriterionID]; ok && ov != d.RawScore {
				audit(s.h, u.ID, "score.edit_after_submit", "score", existing.ID, d.CriterionID, fmt.Sprint(ov), fmt.Sprint(d.RawScore), in.Reason)
			}
		}
	}
	newVersion := existing.Version + 1
	status := existing.Status
	var submittedAt any
	if in.Submit && status == "draft" {
		status = "submitted"
		submittedAt = t
	}
	if submittedAt != nil {
		_, _ = s.h.Exec(`UPDATE scores SET version=?, status=?, comment=?, submitted_at=?, updated_at=? WHERE id=?`, newVersion, status, in.Comment, submittedAt, t, existing.ID)
	} else {
		_, _ = s.h.Exec(`UPDATE scores SET version=?, status=?, comment=?, updated_at=? WHERE id=?`, newVersion, status, in.Comment, t, existing.ID)
	}
	s.upsertDetails(existing.ID, in.Details)
	s.broadcast(map[string]any{"type": "score.updated", "performance_id": in.PerformanceID, "judge_id": u.ID, "score_status": status, "version": newVersion})
	writeJSON(w, 200, map[string]any{"score": s.scoreByID(existing.ID)})
}

func (s *Server) handleMyScore(w http.ResponseWriter, r *http.Request, u *User) {
	pid := pathID(r, "/api/v1/performances/")
	var sid int64
	err := s.h.QueryRow(`SELECT id FROM scores WHERE performance_id=? AND judge_id=? AND is_admin_sheet=0`, pid, u.ID).Scan(&sid)
	if err != nil {
		writeJSON(w, 200, map[string]any{"score": nil})
		return
	}
	writeJSON(w, 200, map[string]any{"score": s.scoreByID(sid)})
}

func (s *Server) handleSubmitScore(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/scores/")
	sc := s.scoreByID(id)
	if sc == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy phiếu.", nil)
		return
	}
	if sc.JudgeID == nil || *sc.JudgeID != u.ID {
		writeErr(w, 403, "FORBIDDEN", "Phiếu này không thuộc về bạn.", nil)
		return
	}
	if sc.Status == "submitted" || sc.Status == "locked" {
		writeJSON(w, 200, map[string]any{"score": sc})
		return
	}
	// validate completeness
	p := s.perfByID(sc.PerformanceID)
	details := []detailIn{}
	for _, d := range sc.Details {
		details = append(details, detailIn{CriterionID: d.CriterionID, RawScore: d.RawScore, OptionID: d.OptionID, Comment: d.Comment})
	}
	if msg, _ := s.validateDetails(p.ContestID, details, false, true); msg != "" {
		writeErr(w, 422, "VALIDATION_ERROR", msg, nil)
		return
	}
	_, _ = s.h.Exec(`UPDATE scores SET status='submitted', submitted_at=?, version=version+1, updated_at=? WHERE id=?`, nowISO(), nowISO(), id)
	s.broadcast(map[string]any{"type": "score.updated", "performance_id": sc.PerformanceID, "judge_id": u.ID, "score_status": "submitted"})
	writeJSON(w, 200, map[string]any{"score": s.scoreByID(id)})
}

func (s *Server) handlePatchScore(w http.ResponseWriter, r *http.Request, u *User) {
	// PATCH /scores/:id — treat like edit using current details payload
	parts := pathParts(r, "/api/v1/scores/")
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	sc := s.scoreByID(id)
	if sc == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy phiếu.", nil)
		return
	}
	if sc.JudgeID == nil || *sc.JudgeID != u.ID {
		writeErr(w, 403, "FORBIDDEN", "Phiếu này không thuộc về bạn.", nil)
		return
	}
	var in scoreUpsertIn
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	in.PerformanceID = sc.PerformanceID
	// delegate by re-encoding through upsert handler using the score's uuid: emulate directly
	p := s.perfByID(sc.PerformanceID)
	c := s.contestByID(p.ContestID)
	if sc.Status == "locked" || p.Status == "locked" || p.Status == "revealed" {
		writeErr(w, 409, "SCORE_LOCKED", "Phiếu đã khóa.", nil)
		return
	}
	if sc.Status == "submitted" {
		if c == nil || !c.AllowEditAfterSubmit {
			writeErr(w, 403, "FORBIDDEN", "Cuộc thi không cho phép sửa điểm sau khi nộp.", nil)
			return
		}
		if strings.TrimSpace(in.Reason) == "" {
			writeErr(w, 400, "VALIDATION_ERROR", "Sửa điểm đã nộp bắt buộc nhập lý do.", nil)
			return
		}
	}
	if msg, _ := s.validateDetails(p.ContestID, in.Details, false, false); msg != "" {
		writeErr(w, 422, "VALIDATION_ERROR", msg, nil)
		return
	}
	oldMap := map[int64]float64{}
	for _, d := range sc.Details {
		oldMap[d.CriterionID] = d.RawScore
	}
	s.upsertDetails(id, in.Details)
	_, _ = s.h.Exec(`UPDATE scores SET version=version+1, updated_at=? WHERE id=?`, nowISO(), id)
	if sc.Status == "submitted" {
		for _, d := range in.Details {
			if ov, ok := oldMap[d.CriterionID]; ok && ov != d.RawScore {
				audit(s.h, u.ID, "score.edit_after_submit", "score", id, d.CriterionID, fmt.Sprint(ov), fmt.Sprint(d.RawScore), in.Reason)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"score": s.scoreByID(id)})
}

func (s *Server) handlePerfScores(w http.ResponseWriter, r *http.Request, u *User) {
	pid := pathID(r, "/api/v1/performances/")
	rows, _ := s.h.Query(`SELECT id FROM scores WHERE performance_id=? ORDER BY is_admin_sheet, judge_id`, pid)
	ids := []int64{}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []*scoreOut{}
	for _, id := range ids {
		out = append(out, s.scoreByID(id))
	}
	writeJSON(w, 200, map[string]any{"scores": out})
}

func (s *Server) handleOverride(w http.ResponseWriter, r *http.Request, u *User) {
	parts := pathParts(r, "/api/v1/scores/")
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	sc := s.scoreByID(id)
	if sc == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy phiếu.", nil)
		return
	}
	var in struct {
		Details []detailIn `json:"details"`
		Reason  string     `json:"reason"`
	}
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Reason) == "" {
		writeErr(w, 400, "VALIDATION_ERROR", "Override bắt buộc nhập lý do và details.", nil)
		return
	}
	p := s.perfByID(sc.PerformanceID)
	c := s.contestByID(p.ContestID)
	if c != nil && c.IsFinalized {
		writeErr(w, 409, "SCORE_LOCKED", "Cuộc thi đã chốt sổ.", nil)
		return
	}
	if msg, _ := s.validateDetails(p.ContestID, in.Details, sc.IsAdminSheet, false); msg != "" {
		writeErr(w, 422, "VALIDATION_ERROR", msg, nil)
		return
	}
	oldMap := map[int64]float64{}
	for _, d := range sc.Details {
		oldMap[d.CriterionID] = d.RawScore
	}
	s.upsertDetails(id, in.Details)
	_, _ = s.h.Exec(`UPDATE scores SET version=version+1, updated_at=? WHERE id=?`, nowISO(), id)
	for _, d := range in.Details {
		ov := oldMap[d.CriterionID]
		if ov != d.RawScore || oldMap[d.CriterionID] == 0 {
			audit(s.h, u.ID, "score.override", "score", id, d.CriterionID, fmt.Sprint(ov), fmt.Sprint(d.RawScore), in.Reason)
		}
	}
	s.broadcast(map[string]any{"type": "score.updated", "performance_id": sc.PerformanceID, "score_status": sc.Status})
	s.broadcast(map[string]any{"type": "leaderboard.updated", "round_id": p.RoundID})
	writeJSON(w, 200, map[string]any{"score": s.scoreByID(id)})
}

func (s *Server) handleAdminSheet(w http.ResponseWriter, r *http.Request, u *User) {
	pid := pathID(r, "/api/v1/performances/")
	var in struct {
		Details []detailIn `json:"details"`
		Comment string     `json:"comment"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	if strings.TrimSpace(in.Comment) == "" {
		writeErr(w, 400, "VALIDATION_ERROR", "Phiếu Admin-Only bắt buộc có ghi chú (comment).", nil)
		return
	}
	p := s.perfByID(pid)
	if p == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy phần thi.", nil)
		return
	}
	if p.Status == "locked" || p.Status == "revealed" {
		// admin sheet still editable by admin until finalized? PRD: admin-only nhập trước khóa; sau khóa dùng override. Block here.
		writeErr(w, 409, "SCORE_LOCKED", "Phần thi đã khóa; dùng override cho phiếu Admin-Only.", nil)
		return
	}
	if msg, _ := s.validateDetails(p.ContestID, in.Details, true, false); msg != "" {
		writeErr(w, 422, "VALIDATION_ERROR", msg, nil)
		return
	}
	sheetID := s.ensureAdminSheet(pid, u.ID)
	// audit changes to existing values
	if old := s.scoreByID(sheetID); old != nil {
		oldMap := map[int64]float64{}
		for _, d := range old.Details {
			oldMap[d.CriterionID] = d.RawScore
		}
		for _, d := range in.Details {
			if ov, ok := oldMap[d.CriterionID]; ok && ov != d.RawScore {
				audit(s.h, u.ID, "score.admin_sheet_edit", "score", sheetID, d.CriterionID, fmt.Sprint(ov), fmt.Sprint(d.RawScore), in.Comment)
			}
		}
	}
	s.upsertDetails(sheetID, in.Details)
	_, _ = s.h.Exec(`UPDATE scores SET comment=?, version=version+1, updated_at=? WHERE id=?`, in.Comment, nowISO(), sheetID)
	s.broadcast(map[string]any{"type": "leaderboard.updated", "round_id": p.RoundID})
	writeJSON(w, 200, map[string]any{"score": s.scoreByID(sheetID)})
}

type progressItem struct {
	PerformanceID int64  `json:"performance_id"`
	TeamID        int64  `json:"team_id"`
	TeamCode      string `json:"team_code"`
	TeamNameVi    string `json:"team_name_vi"`
	TeamNameEn    string `json:"team_name_en"`
	PerfStatus    string `json:"performance_status"`
	MyStatus      string `json:"my_status"` // submitted | draft | not_started
}
type progressOut struct {
	Total     int            `json:"total"`
	Submitted int            `json:"submitted"`
	Items     []progressItem `json:"items"`
}

func (s *Server) judgeProgress(judgeID, contestID int64) (progressOut, error) {
	q := `SELECT p.id FROM assignments a JOIN performances p ON p.id=a.performance_id JOIN teams t ON t.id=p.team_id JOIN rounds r ON r.id=t.round_id WHERE a.judge_id=?`
	args := []any{judgeID}
	if contestID > 0 {
		q += ` AND r.contest_id=?`
		args = append(args, contestID)
	}
	q += ` ORDER BY p.sort_order`
	rows, _ := s.h.Query(q, args...)
	ids := []int64{}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := progressOut{Items: []progressItem{}}
	for _, id := range ids {
		p := s.perfByID(id)
		item := progressItem{PerformanceID: id, TeamID: p.TeamID, TeamCode: p.TeamCode, TeamNameVi: p.TeamNameVi, TeamNameEn: p.TeamNameEn, PerfStatus: p.Status, MyStatus: "not_started"}
		var st string
		err := s.h.QueryRow(`SELECT status FROM scores WHERE performance_id=? AND judge_id=? AND is_admin_sheet=0`, id, judgeID).Scan(&st)
		if err == nil {
			if st == "submitted" || st == "locked" {
				item.MyStatus = "submitted"
				out.Submitted++
			} else {
				item.MyStatus = "draft"
			}
		}
		out.Items = append(out.Items, item)
		out.Total++
	}
	return out, nil
}
func (s *Server) handleMyProgress(w http.ResponseWriter, r *http.Request, u *User) {
	prog, _ := s.judgeProgress(u.ID, 0)
	writeJSON(w, 200, prog)
}
func (s *Server) handleMyAssignments(w http.ResponseWriter, r *http.Request, u *User) {
	prog, _ := s.judgeProgress(u.ID, 0)
	writeJSON(w, 200, map[string]any{"assignments": prog.Items, "progress": prog})
}
