package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Perf struct {
	ID              int64    `json:"id"`
	TeamID          int64    `json:"team_id"`
	RoundID         int64    `json:"round_id"`
	ContestID       int64    `json:"contest_id"`
	TeamCode        string   `json:"team_code"`
	TeamNameVi      string   `json:"team_name_vi"`
	TeamNameEn      string   `json:"team_name_en"`
	Status          string   `json:"status"`
	TimerStatus     string   `json:"timer_status"`
	SortOrder       int      `json:"sort_order"`
	StartedAt       string   `json:"started_at,omitempty"`
	EndedAt         string   `json:"ended_at,omitempty"`
	OvertimeSec     int      `json:"overtime_sec"`
	PenaltyStatus   string   `json:"penalty_status"`
	PenaltyProposed *float64 `json:"penalty_proposed"`
	PenaltyApplied  *float64 `json:"penalty_applied"`
	PenaltyReason   string   `json:"penalty_reason,omitempty"`
	DurationSec     int      `json:"standard_duration_sec"`
	ElapsedSec      int      `json:"elapsed_sec"`
	RemainingSec    int      `json:"remaining_sec"`
	LiveOvertimeSec int      `json:"live_overtime_sec"`
}

func (s *Server) perfByID(id int64) *Perf {
	var p Perf
	var started, ended *string
	err := s.h.QueryRow(`SELECT p.id,p.team_id,t.round_id,r.contest_id,t.code,t.name_vi,COALESCE(t.name_en,''),p.status,p.timer_status,p.sort_order,p.started_at,p.ended_at,p.overtime_sec,p.penalty_status,p.penalty_proposed,p.penalty_applied,COALESCE(p.penalty_reason,''),COALESCE(r.standard_duration_sec, c.standard_duration_sec)
	 FROM performances p JOIN teams t ON t.id=p.team_id JOIN rounds r ON r.id=t.round_id JOIN contests c ON c.id=r.contest_id WHERE p.id=?`, id).
		Scan(&p.ID, &p.TeamID, &p.RoundID, &p.ContestID, &p.TeamCode, &p.TeamNameVi, &p.TeamNameEn, &p.Status, &p.TimerStatus, &p.SortOrder, &started, &ended, &p.OvertimeSec, &p.PenaltyStatus, &p.PenaltyProposed, &p.PenaltyApplied, &p.PenaltyReason, &p.DurationSec)
	if err != nil {
		return nil
	}
	if started != nil {
		p.StartedAt = *started
	}
	if ended != nil {
		p.EndedAt = *ended
	}
	// live timer from latest session
	var sStarted *string
	var sPaused *string
	var sEnded *string
	var pausedTotal, dur int
	err = s.h.QueryRow(`SELECT started_at,paused_total_sec,paused_at,ended_at,standard_duration_sec FROM timer_sessions WHERE performance_id=? ORDER BY id DESC LIMIT 1`, id).
		Scan(&sStarted, &pausedTotal, &sPaused, &sEnded, &dur)
	if err == nil && sStarted != nil {
		p.DurationSec = dur
		start, _ := time.Parse("2006-01-02T15:04:05Z", *sStarted)
		end := time.Now().UTC()
		if sEnded != nil {
			end, _ = time.Parse("2006-01-02T15:04:05Z", *sEnded)
		}
		elapsed := int(end.Sub(start).Seconds()) - pausedTotal
		if sPaused != nil && sEnded == nil {
			pa, _ := time.Parse("2006-01-02T15:04:05Z", *sPaused)
			elapsed = int(pa.Sub(start).Seconds()) - pausedTotal
		}
		if elapsed < 0 {
			elapsed = 0
		}
		p.ElapsedSec = elapsed
		if elapsed > dur {
			p.LiveOvertimeSec = elapsed - dur
			p.RemainingSec = 0
		} else {
			p.RemainingSec = dur - elapsed
		}
		if p.Status == "performing" && elapsed > dur {
			p.TimerStatus = "overtime"
		}
	}
	return &p
}

func (s *Server) perfsOfContest(contestID int64) []*Perf {
	rows, _ := s.h.Query(`SELECT p.id FROM performances p JOIN teams t ON t.id=p.team_id JOIN rounds r ON r.id=t.round_id WHERE r.contest_id=? ORDER BY p.sort_order, p.id`, contestID)
	ids := []int64{}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []*Perf{}
	for _, id := range ids {
		out = append(out, s.perfByID(id))
	}
	return out
}

// sheetTotal: sum raw*weight for details of one score sheet.
func (s *Server) sheetTotal(scoreID int64) float64 {
	var v float64
	_ = s.h.QueryRow(`SELECT COALESCE(SUM(sd.raw_score*c.weight),0) FROM score_details sd JOIN criteria c ON c.id=sd.criterion_id WHERE sd.score_id=?`, scoreID).Scan(&v)
	return v
}

type TeamResult struct {
	TeamID         int64   `json:"team_id"`
	PerformanceID  int64   `json:"performance_id"`
	TeamCode       string  `json:"team_code"`
	TeamNameVi     string  `json:"team_name_vi"`
	TeamNameEn     string  `json:"team_name_en"`
	Status         string  `json:"status"`
	JudgeAvg       float64 `json:"judge_average"`
	AdminOnlyTotal float64 `json:"admin_only_total"`
	FinalScore     float64 `json:"final_score"`
	SheetCount     int     `json:"sheet_count"`
	TrimApplied    bool    `json:"trim_applied"`
	TrimNote       string  `json:"trim_note,omitempty"`
	HasScore       bool    `json:"has_score"`
	Tied           bool    `json:"tied"`
	Rank           int     `json:"rank"`
	ManualRank     bool    `json:"manual_rank"`
	TieBreakAvg    float64 `json:"tie_break_criterion_average"`
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func (s *Server) teamResult(perf *Perf, c *Contest) TeamResult {
	res := TeamResult{TeamID: perf.TeamID, PerformanceID: perf.ID, TeamCode: perf.TeamCode, TeamNameVi: perf.TeamNameVi, TeamNameEn: perf.TeamNameEn, Status: perf.Status}
	rows, _ := s.h.Query(`SELECT id FROM scores WHERE performance_id=? AND is_admin_sheet=0 AND status IN ('submitted','locked')`, perf.ID)
	sids := []int64{}
	for rows.Next() {
		var sid int64
		_ = rows.Scan(&sid)
		sids = append(sids, sid)
	}
	rows.Close()
	totals := []float64{}
	for _, sid := range sids {
		totals = append(totals, s.sheetTotal(sid))
	}
	res.SheetCount = len(totals)
	used := append([]float64{}, totals...)
	if c.DropHighestLowest {
		if len(totals) >= c.MinJudgesForTrim && len(totals) >= 3 {
			sort.Float64s(used)
			used = used[1 : len(used)-1]
			res.TrimApplied = true
		} else {
			res.TrimNote = "không áp dụng bỏ cao/thấp nhất — không đủ số giám khảo"
		}
	}
	if len(used) > 0 {
		sum := 0.0
		for _, v := range used {
			sum += v
		}
		res.JudgeAvg = sum / float64(len(used))
		res.HasScore = true
	}
	// admin-only sheet: sum of details (includes approved penalty detail stored as negative)
	var adminScoreID int64
	err := s.h.QueryRow(`SELECT id FROM scores WHERE performance_id=? AND is_admin_sheet=1`, perf.ID).Scan(&adminScoreID)
	if err == nil {
		res.AdminOnlyTotal = s.sheetTotal(adminScoreID)
	}
	res.FinalScore = round2(res.JudgeAvg + res.AdminOnlyTotal)
	res.JudgeAvg = round2(res.JudgeAvg)
	res.AdminOnlyTotal = round2(res.AdminOnlyTotal)
	// tie-break criterion average (raw avg across submitted sheets)
	if c.TieBreakerCriterionID != nil {
		var v float64
		var n int
		_ = s.h.QueryRow(`SELECT COALESCE(AVG(sd.raw_score),0), COUNT(*) FROM score_details sd JOIN scores sc ON sc.id=sd.score_id WHERE sc.performance_id=? AND sc.is_admin_sheet=0 AND sc.status IN ('submitted','locked') AND sd.criterion_id=?`, perf.ID, *c.TieBreakerCriterionID).Scan(&v, &n)
		if n > 0 {
			res.TieBreakAvg = round2(v)
		}
	}
	return res
}

func (s *Server) leaderboard(roundID int64) []TeamResult {
	var contestID int64
	_ = s.h.QueryRow(`SELECT contest_id FROM rounds WHERE id=?`, roundID).Scan(&contestID)
	c := s.contestByID(contestID)
	if c == nil {
		return []TeamResult{}
	}
	rows, _ := s.h.Query(`SELECT p.id FROM performances p JOIN teams t ON t.id=p.team_id WHERE t.round_id=? AND t.is_active=1 ORDER BY p.sort_order`, roundID)
	ids := []int64{}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []TeamResult{}
	for _, id := range ids {
		p := s.perfByID(id)
		out = append(out, s.teamResult(p, c))
	}
	// manual tie-break ordering from settings: tiebreak_<roundID> = [team_id,...]
	manual := s.manualOrder(roundID)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HasScore != out[j].HasScore {
			return out[i].HasScore
		}
		if out[i].FinalScore != out[j].FinalScore {
			return out[i].FinalScore > out[j].FinalScore
		}
		// tied: manual order wins
		mi, mj := indexOf(manual, out[i].TeamID), indexOf(manual, out[j].TeamID)
		if mi >= 0 && mj >= 0 && mi != mj {
			return mi < mj
		}
		if mi >= 0 && mj < 0 {
			return true
		}
		if mj >= 0 && mi < 0 {
			return false
		}
		if c.TieBreakerCriterionID != nil && out[i].TieBreakAvg != out[j].TieBreakAvg {
			return out[i].TieBreakAvg > out[j].TieBreakAvg
		}
		return out[i].TeamCode < out[j].TeamCode
	})
	// ranks + tied flags
	for i := range out {
		out[i].Rank = i + 1
		if indexOf(manual, out[i].TeamID) >= 0 {
			out[i].ManualRank = true
		}
		if out[i].HasScore {
			for j := range out {
				if j != i && out[j].HasScore && out[j].FinalScore == out[i].FinalScore {
					out[i].Tied = true
				}
			}
		}
	}
	return out
}
func indexOf(a []int64, v int64) int {
	for i, x := range a {
		if x == v {
			return i
		}
	}
	return -1
}
func (s *Server) manualOrder(roundID int64) []int64 {
	var v string
	err := s.h.QueryRow(`SELECT value FROM settings WHERE key=?`, fmt.Sprintf("tiebreak_%d", roundID)).Scan(&v)
	if err != nil || v == "" {
		return nil
	}
	var out []int64
	_ = json.Unmarshal([]byte(v), &out)
	return out
}

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request, u *User) {
	rid := pathID(r, "/api/v1/rounds/")
	res := s.leaderboard(rid)
	if u.Role == "mc" {
		var lr int
		_ = s.h.QueryRow(`SELECT leaderboard_revealed FROM rounds WHERE id=?`, rid).Scan(&lr)
		if lr != 1 {
			writeJSON(w, 200, map[string]any{"leaderboard": []TeamResult{}, "revealed": false})
			return
		}
		filtered := []TeamResult{}
		for _, x := range res {
			if x.Status == "revealed" {
				filtered = append(filtered, x)
			}
		}
		writeJSON(w, 200, map[string]any{"leaderboard": filtered, "revealed": true})
		return
	}
	writeJSON(w, 200, map[string]any{"leaderboard": res})
}
func (s *Server) handleResults(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	out := map[string]any{}
	for _, rd := range s.listRounds(cid) {
		out[strconv.FormatInt(rd.ID, 10)] = s.leaderboard(rd.ID)
	}
	writeJSON(w, 200, map[string]any{"results_by_round": out})
}
func (s *Server) handleTieBreak(w http.ResponseWriter, r *http.Request, u *User) {
	rid := pathID(r, "/api/v1/rounds/")
	var in struct {
		Ordering []int64 `json:"ordering"`
		Reason   string  `json:"reason"`
	}
	if err := decodeJSON(r, &in); err != nil || len(in.Ordering) == 0 || strings.TrimSpace(in.Reason) == "" {
		writeErr(w, 400, "VALIDATION_ERROR", "Chốt thủ công cần ordering (danh sách team_id) và reason.", nil)
		return
	}
	b, _ := json.Marshal(in.Ordering)
	_, _ = s.h.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, fmt.Sprintf("tiebreak_%d", rid), string(b), nowISO())
	audit(s.h, u.ID, "round.tie_break_manual", "round", rid, nil, "", fmt.Sprint(in.Ordering), in.Reason)
	s.broadcast(map[string]any{"type": "leaderboard.updated", "round_id": rid})
	writeJSON(w, 200, map[string]any{"leaderboard": s.leaderboard(rid)})
}

func (s *Server) handleLiveMatrix(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	// judges
	jrows, _ := s.h.Query(`SELECT id,username,display_name FROM users WHERE role='judge' AND is_active=1 ORDER BY id`)
	type J struct {
		ID          int64  `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Online      bool   `json:"online"`
	}
	judges := []J{}
	for jrows.Next() {
		var j J
		_ = jrows.Scan(&j.ID, &j.Username, &j.DisplayName)
		j.Online = s.isOnline(j.ID)
		judges = append(judges, j)
	}
	jrows.Close()
	type Cell struct {
		JudgeID int64  `json:"judge_id"`
		State   string `json:"state"`
		ScoreID *int64 `json:"score_id"`
	}
	type Row struct {
		Performance *Perf  `json:"performance"`
		Cells       []Cell `json:"cells"`
	}
	out := []Row{}
	for _, p := range s.perfsOfContest(cid) {
		row := Row{Performance: p, Cells: []Cell{}}
		for _, j := range judges {
			cell := Cell{JudgeID: j.ID, State: "not_assigned"}
			var assigned int
			_ = s.h.QueryRow(`SELECT COUNT(*) FROM assignments WHERE performance_id=? AND judge_id=?`, p.ID, j.ID).Scan(&assigned)
			if assigned > 0 {
				cell.State = "not_started"
				var sid int64
				var st string
				err := s.h.QueryRow(`SELECT id,status FROM scores WHERE performance_id=? AND judge_id=? AND is_admin_sheet=0`, p.ID, j.ID).Scan(&sid, &st)
				if err == nil {
					cell.ScoreID = &sid
					switch st {
					case "submitted":
						cell.State = "submitted"
					case "locked":
						cell.State = "locked"
					default:
						cell.State = "draft"
					}
				} else if s.isDraftPing(p.ID, j.ID) {
					cell.State = "draft"
				}
			}
			row.Cells = append(row.Cells, cell)
		}
		out = append(out, row)
	}
	writeJSON(w, 200, map[string]any{"judges": judges, "rows": out})
}

func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	_ = cid
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(q.Get("page_size"))
	if size < 1 || size > 200 {
		size = 50
	}
	where := ` WHERE 1=1`
	args := []any{}
	if v := q.Get("actor_id"); v != "" {
		where += ` AND actor_id=?`
		args = append(args, v)
	}
	if v := q.Get("entity_type"); v != "" {
		where += ` AND entity_type=?`
		args = append(args, v)
	}
	if v := q.Get("entity_id"); v != "" {
		where += ` AND entity_id=?`
		args = append(args, v)
	}
	var total int
	_ = s.h.QueryRow(`SELECT COUNT(*) FROM audit_logs`+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, _ := s.h.Query(`SELECT id,actor_id,action,entity_type,entity_id,criterion_id,COALESCE(old_value,''),COALESCE(new_value,''),COALESCE(reason,''),created_at FROM audit_logs`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	defer rows.Close()
	type A struct {
		ID, ActorID, EntityID                    int64
		Action, EntityType, Old, New, Reason, At string
		CriterionID                              *int64
	}
	out := []A{}
	for rows.Next() {
		var a A
		_ = rows.Scan(&a.ID, &a.ActorID, &a.Action, &a.EntityType, &a.EntityID, &a.CriterionID, &a.Old, &a.New, &a.Reason, &a.At)
		out = append(out, a)
	}
	// fix json field names
	type AOut struct {
		ID          int64  `json:"id"`
		ActorID     int64  `json:"actor_id"`
		Action      string `json:"action"`
		EntityType  string `json:"entity_type"`
		EntityID    int64  `json:"entity_id"`
		CriterionID *int64 `json:"criterion_id"`
		OldValue    string `json:"old_value"`
		NewValue    string `json:"new_value"`
		Reason      string `json:"reason"`
		CreatedAt   string `json:"created_at"`
	}
	out2 := []AOut{}
	for _, a := range out {
		out2 = append(out2, AOut{a.ID, a.ActorID, a.Action, a.EntityType, a.EntityID, a.CriterionID, a.Old, a.New, a.Reason, a.At})
	}
	writeJSON(w, 200, map[string]any{"audit_logs": out2, "total": total, "page": page, "page_size": size})
}

func (s *Server) handlePublicState(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	c := s.contestByID(cid)
	if c == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy cuộc thi.", nil)
		return
	}
	perfs := s.perfsOfContest(cid)
	var current *Perf
	revealed := []TeamResult{}
	boards := map[string]any{}
	for _, p := range perfs {
		if p.Status == "performing" {
			current = p
		}
		if p.Status == "revealed" {
			tr := s.teamResult(p, c)
			revealed = append(revealed, tr)
		}
	}
	for _, rd := range s.listRounds(cid) {
		if rd.LeaderboardRevealed {
			filtered := []TeamResult{}
			for _, x := range s.leaderboard(rd.ID) {
				if x.Status == "revealed" {
					filtered = append(filtered, x)
				}
			}
			boards[strconv.FormatInt(rd.ID, 10)] = filtered
		}
	}
	writeJSON(w, 200, map[string]any{"contest": c, "current_performance": current, "revealed_results": revealed, "leaderboards": boards, "server_now": nowISO()})
}

func (s *Server) handleContestState(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	c := s.contestByID(cid)
	if c == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy cuộc thi.", nil)
		return
	}
	resp := map[string]any{"contest": c, "rounds": s.listRounds(cid), "performances": s.perfsOfContest(cid), "server_now": nowISO()}
	if u.Role == "judge" {
		prog, _ := s.judgeProgress(u.ID, cid)
		resp["my_progress"] = prog
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleJoinInfo(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimPrefix(r.URL.Path, "/api/v1/public/join/")
	var id int64
	var vi, en, status string
	err := s.h.QueryRow(`SELECT id,name_vi,COALESCE(name_en,''),status FROM contests WHERE join_code=?`, code).Scan(&id, &vi, &en, &status)
	if err != nil {
		writeErr(w, 404, "NOT_FOUND", "Join code không tồn tại.", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"contest_id": id, "name_vi": vi, "name_en": en, "status": status})
}
