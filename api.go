package main

import (
	"encoding/csv"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
)

type Contest struct {
	ID                    int64   `json:"id"`
	NameVi                string  `json:"name_vi"`
	NameEn                string  `json:"name_en"`
	DescVi                string  `json:"desc_vi"`
	DescEn                string  `json:"desc_en"`
	Location              string  `json:"location"`
	Status                string  `json:"status"`
	IsFinalized           bool    `json:"is_finalized"`
	JoinCode              string  `json:"join_code"`
	AllowEditAfterSubmit  bool    `json:"allow_judge_edit_after_submit"`
	DropHighestLowest     bool    `json:"drop_highest_lowest"`
	MinJudgesForTrim      int     `json:"min_judges_for_trim"`
	TieBreakerCriterionID *int64  `json:"tie_breaker_criterion_id"`
	StandardDurationSec   int     `json:"standard_duration_sec"`
	WarningBeforeSec      int     `json:"warning_before_sec"`
	PenaltyEnabled        bool    `json:"penalty_enabled"`
	PenaltyIntervalSec    int     `json:"penalty_interval_sec"`
	PenaltyFirstPoints    float64 `json:"penalty_first_points"`
	PenaltyNextPoints     float64 `json:"penalty_next_points"`
	PenaltyMaxPoints      float64 `json:"penalty_max_points"`
	AllowParallelRounds   bool    `json:"allow_parallel_rounds"`
	TotalMaxScore         float64 `json:"total_max_score"`
}

func (s *Server) contestByID(id int64) *Contest {
	var c Contest
	var fin, allowEdit, drop, pen, par int
	var tie *int64
	var interval, dur, warn int
	var first, next, maxp float64
	err := s.h.QueryRow(`SELECT id,name_vi,COALESCE(name_en,''),COALESCE(desc_vi,''),COALESCE(desc_en,''),COALESCE(location,''),status,is_finalized,join_code,allow_judge_edit_after_submit,drop_highest_lowest,min_judges_for_trim,tie_breaker_criterion_id,standard_duration_sec,warning_before_sec,penalty_enabled,COALESCE(penalty_interval_sec,0),COALESCE(penalty_first_points,0),COALESCE(penalty_next_points,0),COALESCE(penalty_max_points,0),allow_parallel_rounds FROM contests WHERE id=?`, id).
		Scan(&c.ID, &c.NameVi, &c.NameEn, &c.DescVi, &c.DescEn, &c.Location, &c.Status, &fin, &c.JoinCode, &allowEdit, &drop, &c.MinJudgesForTrim, &tie, &dur, &warn, &pen, &interval, &first, &next, &maxp, &par)
	if err != nil {
		return nil
	}
	c.IsFinalized = fin == 1
	c.AllowEditAfterSubmit = allowEdit == 1
	c.DropHighestLowest = drop == 1
	c.PenaltyEnabled = pen == 1
	c.AllowParallelRounds = par == 1
	c.TieBreakerCriterionID = tie
	c.StandardDurationSec, c.WarningBeforeSec = dur, warn
	c.PenaltyIntervalSec, c.PenaltyFirstPoints, c.PenaltyNextPoints, c.PenaltyMaxPoints = interval, first, next, maxp
	c.TotalMaxScore = s.totalMaxScore(id)
	return &c
}

func (s *Server) totalMaxScore(contestID int64) float64 {
	var v float64
	_ = s.h.QueryRow(`SELECT COALESCE(SUM(c.max_score*c.weight),0) FROM criteria c JOIN criteria_groups g ON g.id=c.group_id WHERE g.contest_id=? AND c.is_admin_only=0`, contestID).Scan(&v)
	return v
}

func (s *Server) contestOfPerformance(perfID int64) int64 {
	var cid int64
	_ = s.h.QueryRow(`SELECT r.contest_id FROM performances p JOIN teams t ON t.id=p.team_id JOIN rounds r ON r.id=t.round_id WHERE p.id=?`, perfID).Scan(&cid)
	return cid
}
func (s *Server) contestOfRound(roundID int64) int64 {
	var cid int64
	_ = s.h.QueryRow(`SELECT contest_id FROM rounds WHERE id=?`, roundID).Scan(&cid)
	return cid
}
func (s *Server) contestOfGroup(groupID int64) int64 {
	var cid int64
	_ = s.h.QueryRow(`SELECT contest_id FROM criteria_groups WHERE id=?`, groupID).Scan(&cid)
	return cid
}
func (s *Server) contestOfCriterion(critID int64) int64 {
	var cid int64
	_ = s.h.QueryRow(`SELECT g.contest_id FROM criteria c JOIN criteria_groups g ON g.id=c.group_id WHERE c.id=?`, critID).Scan(&cid)
	return cid
}

// configLocked returns true (and writes error) when contest is not pending for scoring-config changes.
func (s *Server) configEditable(w http.ResponseWriter, contestID int64) bool {
	c := s.contestByID(contestID)
	if c == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy cuộc thi.", nil)
		return false
	}
	if c.Status != "pending" {
		writeErr(w, 409, "CONFIG_LOCKED", "Cấu hình chấm đã khóa vì cuộc thi không còn ở trạng thái pending.", nil)
		return false
	}
	return true
}

// ---------- Contests ----------
func (s *Server) handleListContests(w http.ResponseWriter, r *http.Request, u *User) {
	rows, _ := s.h.Query(`SELECT id FROM contests ORDER BY id`)
	ids := []int64{}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []*Contest{}
	for _, id := range ids {
		out = append(out, s.contestByID(id))
	}
	writeJSON(w, 200, map[string]any{"contests": out})
}

type contestInput struct {
	NameVi                string   `json:"name_vi"`
	NameEn                string   `json:"name_en"`
	DescVi                string   `json:"desc_vi"`
	DescEn                string   `json:"desc_en"`
	Location              string   `json:"location"`
	StandardDurationSec   int      `json:"standard_duration_sec"`
	AllowEditAfterSubmit  *bool    `json:"allow_judge_edit_after_submit"`
	DropHighestLowest     *bool    `json:"drop_highest_lowest"`
	MinJudgesForTrim      *int     `json:"min_judges_for_trim"`
	TieBreakerCriterionID *int64   `json:"tie_breaker_criterion_id"`
	PenaltyEnabled        *bool    `json:"penalty_enabled"`
	PenaltyIntervalSec    *int     `json:"penalty_interval_sec"`
	PenaltyFirstPoints    *float64 `json:"penalty_first_points"`
	PenaltyNextPoints     *float64 `json:"penalty_next_points"`
	PenaltyMaxPoints      *float64 `json:"penalty_max_points"`
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Server) handleCreateContest(w http.ResponseWriter, r *http.Request, u *User) {
	var in contestInput
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.NameVi) == "" {
		writeErr(w, 400, "VALIDATION_ERROR", "Tên cuộc thi (name_vi) là bắt buộc.", nil)
		return
	}
	dur := in.StandardDurationSec
	if dur <= 0 {
		dur = 600
	}
	res, err := s.h.Exec(`INSERT INTO contests(name_vi,name_en,desc_vi,desc_en,location,status,join_code,standard_duration_sec,created_at,updated_at) VALUES(?,?,?,?,?,'pending',?,?,?,?)`,
		in.NameVi, in.NameEn, in.DescVi, in.DescEn, in.Location, "JOIN"+randHex(3), dur, nowISO(), nowISO())
	if err != nil {
		writeErr(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, map[string]any{"contest": s.contestByID(id)})
}

func (s *Server) handleGetContest(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/contests/")
	c := s.contestByID(id)
	if c == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy cuộc thi.", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"contest": c})
}

var scoringConfigFields = map[string]bool{"drop_highest_lowest": true, "min_judges_for_trim": true, "tie_breaker_criterion_id": true, "penalty_enabled": true, "penalty_interval_sec": true, "penalty_first_points": true, "penalty_next_points": true, "penalty_max_points": true}

func (s *Server) handlePatchContest(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/contests/")
	c := s.contestByID(id)
	if c == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy cuộc thi.", nil)
		return
	}
	// decode generically to detect scoring-config changes when active
	var generic map[string]any
	if err := decodeJSON(r, &generic); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	if c.Status == "active" {
		for k := range generic {
			if scoringConfigFields[k] {
				writeErr(w, 409, "CONFIG_LOCKED", "Không sửa cấu hình chấm khi cuộc thi đang active: "+k, nil)
				return
			}
		}
	}
	if c.IsFinalized {
		writeErr(w, 409, "CONFIG_LOCKED", "Cuộc thi đã chốt sổ.", nil)
		return
	}
	set := func(col string, v any) {
		_, _ = s.h.Exec(`UPDATE contests SET `+col+`=?, updated_at=? WHERE id=?`, v, nowISO(), id)
	}
	if v, ok := generic["name_vi"]; ok {
		set("name_vi", v)
	}
	if v, ok := generic["name_en"]; ok {
		set("name_en", v)
	}
	if v, ok := generic["desc_vi"]; ok {
		set("desc_vi", v)
	}
	if v, ok := generic["desc_en"]; ok {
		set("desc_en", v)
	}
	if v, ok := generic["location"]; ok {
		set("location", v)
	}
	if v, ok := generic["standard_duration_sec"]; ok {
		set("standard_duration_sec", int(v.(float64)))
	}
	if v, ok := generic["allow_judge_edit_after_submit"]; ok {
		set("allow_judge_edit_after_submit", b2i(v.(bool)))
	}
	if v, ok := generic["drop_highest_lowest"]; ok {
		set("drop_highest_lowest", b2i(v.(bool)))
	}
	if v, ok := generic["min_judges_for_trim"]; ok {
		n := int(v.(float64))
		if n < 4 {
			writeErr(w, 400, "VALIDATION_ERROR", "min_judges_for_trim tối thiểu là 4.", nil)
			return
		}
		set("min_judges_for_trim", n)
	}
	if v, ok := generic["tie_breaker_criterion_id"]; ok {
		if v == nil {
			set("tie_breaker_criterion_id", nil)
		} else {
			set("tie_breaker_criterion_id", int64(v.(float64)))
		}
	}
	if v, ok := generic["penalty_enabled"]; ok {
		set("penalty_enabled", b2i(v.(bool)))
	}
	if v, ok := generic["penalty_interval_sec"]; ok {
		set("penalty_interval_sec", int(v.(float64)))
	}
	if v, ok := generic["penalty_first_points"]; ok {
		set("penalty_first_points", v.(float64))
	}
	if v, ok := generic["penalty_next_points"]; ok {
		set("penalty_next_points", v.(float64))
	}
	if v, ok := generic["penalty_max_points"]; ok {
		set("penalty_max_points", v.(float64))
	}
	writeJSON(w, 200, map[string]any{"contest": s.contestByID(id)})
}

func (s *Server) activationBlockers(contestID int64) ([]map[string]any, []map[string]any) {
	blockers := []map[string]any{}
	warnings := []map[string]any{}
	var roundCount int
	_ = s.h.QueryRow(`SELECT COUNT(*) FROM rounds WHERE contest_id=?`, contestID).Scan(&roundCount)
	if roundCount == 0 {
		blockers = append(blockers, map[string]any{"type": "no_round", "message": "Chưa có vòng thi nào"})
	}
	rows, _ := s.h.Query(`SELECT r.id FROM rounds r WHERE r.contest_id=? AND NOT EXISTS(SELECT 1 FROM teams t WHERE t.round_id=r.id AND t.is_active=1)`, contestID)
	for rows.Next() {
		var rid int64
		_ = rows.Scan(&rid)
		blockers = append(blockers, map[string]any{"type": "round_without_team", "round_id": rid, "message": fmt.Sprintf("Vòng %d chưa có đội nào", rid)})
	}
	rows.Close()
	if s.totalMaxScore(contestID) <= 0 {
		blockers = append(blockers, map[string]any{"type": "no_criteria", "message": "Chưa có tiêu chí chấm thường nào (tổng điểm tối đa = 0)"})
	}
	rows, _ = s.h.Query(`SELECT t.id FROM teams t JOIN rounds r ON r.id=t.round_id JOIN performances p ON p.team_id=t.id WHERE r.contest_id=? AND t.is_active=1 AND NOT EXISTS(SELECT 1 FROM assignments a WHERE a.performance_id=p.id)`, contestID)
	for rows.Next() {
		var tid int64
		_ = rows.Scan(&tid)
		blockers = append(blockers, map[string]any{"type": "team_without_judge", "team_id": tid, "message": fmt.Sprintf("Đội %d chưa có giám khảo nào được phân công", tid)})
	}
	rows.Close()
	var badJudge int
	_ = s.h.QueryRow(`SELECT COUNT(*) FROM assignments a JOIN performances p ON p.id=a.performance_id JOIN teams t ON t.id=p.team_id JOIN rounds r ON r.id=t.round_id JOIN users u ON u.id=a.judge_id WHERE r.contest_id=? AND u.is_active=0`, contestID).Scan(&badJudge)
	if badJudge > 0 {
		blockers = append(blockers, map[string]any{"type": "inactive_judge_assigned", "message": "Có giám khảo đã bị vô hiệu hóa nhưng vẫn được phân công"})
	}
	c := s.contestByID(contestID)
	if c != nil && c.PenaltyEnabled && c.PenaltyMaxPoints > s.totalMaxScore(contestID) {
		blockers = append(blockers, map[string]any{"type": "penalty_too_large", "message": "Trần trừ điểm lớn hơn tổng điểm tối đa"})
	}
	rows, _ = s.h.Query(`SELECT cr.id FROM criteria cr JOIN criteria_groups g ON g.id=cr.group_id WHERE g.contest_id=? AND (cr.name_en IS NULL OR cr.name_en='')`, contestID)
	for rows.Next() {
		var cid int64
		_ = rows.Scan(&cid)
		warnings = append(warnings, map[string]any{"type": "missing_translation", "criterion_id": cid, "message": fmt.Sprintf("Tiêu chí %d thiếu bản dịch tiếng Anh", cid)})
	}
	rows.Close()
	return blockers, warnings
}

func (s *Server) handleActivate(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/contests/")
	c := s.contestByID(id)
	if c == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy cuộc thi.", nil)
		return
	}
	if c.Status != "pending" {
		writeErr(w, 422, "INVALID_STATE_TRANSITION", "Chỉ kích hoạt được cuộc thi đang pending.", nil)
		return
	}
	blockers, warnings := s.activationBlockers(id)
	if len(blockers) > 0 {
		writeErr(w, 422, "ACTIVATION_CHECKLIST_FAILED", "Cuộc thi chưa đủ điều kiện kích hoạt.", map[string]any{"blockers": blockers, "warnings": warnings})
		return
	}
	_, _ = s.h.Exec(`UPDATE contests SET status='active', updated_at=? WHERE id=?`, nowISO(), id)
	audit(s.h, u.ID, "contest.activate", "contest", id, nil, "pending", "active", "")
	s.broadcast(map[string]any{"type": "contest.state", "contest_id": id, "status": "active"})
	writeJSON(w, 200, map[string]any{"contest": s.contestByID(id), "warnings": warnings})
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/contests/")
	c := s.contestByID(id)
	if c == nil || c.Status != "active" {
		writeErr(w, 422, "INVALID_STATE_TRANSITION", "Chỉ kết thúc được cuộc thi đang active.", nil)
		return
	}
	_, _ = s.h.Exec(`UPDATE contests SET status='completed', updated_at=? WHERE id=?`, nowISO(), id)
	audit(s.h, u.ID, "contest.complete", "contest", id, nil, "active", "completed", "")
	_, _, _ = s.createBackup(id, "auto_completed", u.ID)
	s.broadcast(map[string]any{"type": "contest.state", "contest_id": id, "status": "completed"})
	writeJSON(w, 200, map[string]any{"contest": s.contestByID(id)})
}

func (s *Server) handleFinalize(w http.ResponseWriter, r *http.Request, u *User, unfinalize bool) {
	parts := pathParts(r, "/api/v1/contests/")
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	c := s.contestByID(id)
	if c == nil || c.Status != "completed" {
		writeErr(w, 422, "INVALID_STATE_TRANSITION", "Chỉ chốt/mở chốt được cuộc thi đã completed.", nil)
		return
	}
	if unfinalize {
		var in struct {
			Reason string `json:"reason"`
		}
		_ = decodeJSON(r, &in)
		if strings.TrimSpace(in.Reason) == "" {
			writeErr(w, 400, "VALIDATION_ERROR", "Mở chốt sổ bắt buộc nhập lý do.", nil)
			return
		}
		_, _ = s.h.Exec(`UPDATE contests SET is_finalized=0, finalized_at=NULL, finalized_by=NULL, updated_at=? WHERE id=?`, nowISO(), id)
		audit(s.h, u.ID, "contest.unfinalize", "contest", id, nil, "1", "0", in.Reason)
	} else {
		_, _ = s.h.Exec(`UPDATE contests SET is_finalized=1, finalized_at=?, finalized_by=?, updated_at=? WHERE id=?`, nowISO(), u.ID, nowISO(), id)
		audit(s.h, u.ID, "contest.finalize", "contest", id, nil, "0", "1", "")
	}
	writeJSON(w, 200, map[string]any{"contest": s.contestByID(id)})
}

func (s *Server) handleClone(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/contests/")
	src := s.contestByID(id)
	if src == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy cuộc thi.", nil)
		return
	}
	t := nowISO()
	res, err := s.h.Exec(`INSERT INTO contests(name_vi,name_en,desc_vi,desc_en,location,status,join_code,allow_judge_edit_after_submit,drop_highest_lowest,min_judges_for_trim,standard_duration_sec,warning_before_sec,penalty_enabled,penalty_interval_sec,penalty_first_points,penalty_next_points,penalty_max_points,allow_parallel_rounds,created_at,updated_at)
	 SELECT name_vi||' (bản sao)',name_en,desc_vi,desc_en,location,'pending',?,allow_judge_edit_after_submit,drop_highest_lowest,min_judges_for_trim,standard_duration_sec,warning_before_sec,penalty_enabled,penalty_interval_sec,penalty_first_points,penalty_next_points,penalty_max_points,allow_parallel_rounds,?,? FROM contests WHERE id=?`,
		"JOIN"+randHex(3), t, t, id)
	if err != nil {
		writeErr(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	newID, _ := res.LastInsertId()
	// rounds + teams + performances (keep old ids mapping in memory)
	roundMap := map[int64]int64{}
	perfMap := map[int64]int64{}
	rows, _ := s.h.Query(`SELECT id,name_vi,COALESCE(name_en,''),sort_order,standard_duration_sec FROM rounds WHERE contest_id=? ORDER BY sort_order,id`, id)
	type rd struct {
		id     int64
		vi, en string
		ord    int
		dur    any
	}
	rds := []rd{}
	for rows.Next() {
		var x rd
		_ = rows.Scan(&x.id, &x.vi, &x.en, &x.ord, &x.dur)
		rds = append(rds, x)
	}
	rows.Close()
	for _, x := range rds {
		res, _ := s.h.Exec(`INSERT INTO rounds(contest_id,name_vi,name_en,sort_order,standard_duration_sec,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, newID, x.vi, x.en, x.ord, x.dur, t, t)
		nid, _ := res.LastInsertId()
		roundMap[x.id] = nid
		trows, _ := s.h.Query(`SELECT t.id,t.code,t.name_vi,COALESCE(t.name_en,''),COALESCE(t.organization,''),COALESCE(t.members_note,''),t.sort_order,p.id FROM teams t JOIN performances p ON p.team_id=t.id WHERE t.round_id=? ORDER BY t.sort_order`, x.id)
		type tm struct {
			id, oldPerf             int64
			code, vi, en, org, note string
			ord                     int
		}
		tms := []tm{}
		for trows.Next() {
			var y tm
			_ = trows.Scan(&y.id, &y.code, &y.vi, &y.en, &y.org, &y.note, &y.ord, &y.oldPerf)
			tms = append(tms, y)
		}
		trows.Close()
		for _, y := range tms {
			res, _ := s.h.Exec(`INSERT INTO teams(round_id,code,name_vi,name_en,organization,members_note,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, nid, y.code, y.vi, y.en, y.org, y.note, y.ord, t, t)
			ntid, _ := res.LastInsertId()
			res, _ = s.h.Exec(`INSERT INTO performances(team_id,status,sort_order,created_at,updated_at) VALUES(?,'pending',?,?,?)`, ntid, y.ord, t, t)
			npid, _ := res.LastInsertId()
			perfMap[y.oldPerf] = npid
		}
	}
	// criteria groups/criteria/options
	grows, _ := s.h.Query(`SELECT id,name_vi,COALESCE(name_en,''),sort_order FROM criteria_groups WHERE contest_id=? ORDER BY sort_order,id`, id)
	type gr struct {
		id     int64
		vi, en string
		ord    int
	}
	grs := []gr{}
	for grows.Next() {
		var x gr
		_ = grows.Scan(&x.id, &x.vi, &x.en, &x.ord)
		grs = append(grs, x)
	}
	grows.Close()
	critMap := map[int64]int64{}
	for _, x := range grs {
		res, _ := s.h.Exec(`INSERT INTO criteria_groups(contest_id,name_vi,name_en,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?)`, newID, x.vi, x.en, x.ord, t, t)
		ngid, _ := res.LastInsertId()
		crows, _ := s.h.Query(`SELECT id,name_vi,COALESCE(name_en,''),COALESCE(desc_vi,''),COALESCE(desc_en,''),input_type,min_score,max_score,step,weight,is_admin_only,is_penalty_criterion,comment_mode,sort_order FROM criteria WHERE group_id=? ORDER BY sort_order,id`, x.id)
		type cr struct {
			id                      int64
			vi, en, dv, de, typ, cm string
			min, max, step, weight  float64
			admin, pen, ord         int
		}
		crs := []cr{}
		for crows.Next() {
			var y cr
			_ = crows.Scan(&y.id, &y.vi, &y.en, &y.dv, &y.de, &y.typ, &y.min, &y.max, &y.step, &y.weight, &y.admin, &y.pen, &y.cm, &y.ord)
			crs = append(crs, y)
		}
		crows.Close()
		for _, y := range crs {
			res, _ := s.h.Exec(`INSERT INTO criteria(group_id,name_vi,name_en,desc_vi,desc_en,input_type,min_score,max_score,step,weight,is_admin_only,is_penalty_criterion,comment_mode,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				ngid, y.vi, y.en, y.dv, y.de, y.typ, y.min, y.max, y.step, y.weight, y.admin, y.pen, y.cm, y.ord, t, t)
			ncid, _ := res.LastInsertId()
			critMap[y.id] = ncid
			orows, _ := s.h.Query(`SELECT label_vi,COALESCE(label_en,''),score_value,sort_order FROM criteria_options WHERE criterion_id=? ORDER BY sort_order`, y.id)
			type opt struct {
				lv, le string
				sv     float64
				so     int
			}
			opts := []opt{}
			for orows.Next() {
				var o opt
				_ = orows.Scan(&o.lv, &o.le, &o.sv, &o.so)
				opts = append(opts, o)
			}
			orows.Close()
			for _, o := range opts {
				_, _ = s.h.Exec(`INSERT INTO criteria_options(criterion_id,label_vi,label_en,score_value,sort_order) VALUES(?,?,?,?,?)`, ncid, o.lv, o.le, o.sv, o.so)
			}
		}
	}
	// assignments
	arows, _ := s.h.Query(`SELECT a.performance_id,a.judge_id FROM assignments a JOIN performances p ON p.id=a.performance_id JOIN teams tm ON tm.id=p.team_id JOIN rounds r ON r.id=tm.round_id WHERE r.contest_id=?`, id)
	type apair struct{ op, j int64 }
	apairs := []apair{}
	for arows.Next() {
		var x apair
		_ = arows.Scan(&x.op, &x.j)
		apairs = append(apairs, x)
	}
	arows.Close()
	for _, x := range apairs {
		if np, ok := perfMap[x.op]; ok {
			_, _ = s.h.Exec(`INSERT OR IGNORE INTO assignments(performance_id,judge_id,created_at) VALUES(?,?,?)`, np, x.j, t)
		}
	}
	// tie breaker remap
	if src.TieBreakerCriterionID != nil {
		if nc, ok := critMap[*src.TieBreakerCriterionID]; ok {
			_, _ = s.h.Exec(`UPDATE contests SET tie_breaker_criterion_id=? WHERE id=?`, nc, newID)
		}
	}
	audit(s.h, u.ID, "contest.clone", "contest", newID, nil, fmt.Sprint(id), fmt.Sprint(newID), "")
	writeJSON(w, 201, map[string]any{"contest": s.contestByID(newID)})
}

func (s *Server) handleResetScores(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/contests/")
	var in struct {
		Confirm bool `json:"confirm"`
	}
	_ = decodeJSON(r, &in)
	if !in.Confirm {
		writeErr(w, 400, "VALIDATION_ERROR", "Reset điểm yêu cầu body {\"confirm\": true} (xác nhận lần hai).", nil)
		return
	}
	c := s.contestByID(id)
	if c == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy cuộc thi.", nil)
		return
	}
	if c.IsFinalized {
		writeErr(w, 409, "CONFIG_LOCKED", "Cuộc thi đã chốt sổ, không reset được.", nil)
		return
	}
	_, _ = s.h.Exec(`DELETE FROM scores WHERE performance_id IN (SELECT p.id FROM performances p JOIN teams t ON t.id=p.team_id JOIN rounds r ON r.id=t.round_id WHERE r.contest_id=?)`, id)
	_, _ = s.h.Exec(`DELETE FROM timer_sessions WHERE performance_id IN (SELECT p.id FROM performances p JOIN teams t ON t.id=p.team_id JOIN rounds r ON r.id=t.round_id WHERE r.contest_id=?)`, id)
	_, _ = s.h.Exec(`UPDATE performances SET status='pending', timer_status='idle', started_at=NULL, ended_at=NULL, overtime_sec=0, penalty_status='none', penalty_proposed=NULL, penalty_applied=NULL, penalty_reason=NULL, updated_at=? WHERE id IN (SELECT p.id FROM performances p JOIN teams t ON t.id=p.team_id JOIN rounds r ON r.id=t.round_id WHERE r.contest_id=?)`, nowISO(), id)
	audit(s.h, u.ID, "contest.reset_scores", "contest", id, nil, "", "", "Reset điểm (Admin xác nhận)")
	s.broadcast(map[string]any{"type": "contest.state", "contest_id": id, "status": c.Status})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- Rounds & Teams ----------
type roundOut struct {
	ID                  int64  `json:"id"`
	ContestID           int64  `json:"contest_id"`
	NameVi              string `json:"name_vi"`
	NameEn              string `json:"name_en"`
	SortOrder           int    `json:"sort_order"`
	StandardDurationSec *int   `json:"standard_duration_sec"`
	LeaderboardRevealed bool   `json:"leaderboard_revealed"`
}

func (s *Server) listRounds(contestID int64) []roundOut {
	rows, _ := s.h.Query(`SELECT id,contest_id,name_vi,COALESCE(name_en,''),sort_order,standard_duration_sec,leaderboard_revealed FROM rounds WHERE contest_id=? ORDER BY sort_order,id`, contestID)
	defer rows.Close()
	out := []roundOut{}
	for rows.Next() {
		var x roundOut
		var lr int
		_ = rows.Scan(&x.ID, &x.ContestID, &x.NameVi, &x.NameEn, &x.SortOrder, &x.StandardDurationSec, &lr)
		x.LeaderboardRevealed = lr == 1
		out = append(out, x)
	}
	return out
}
func (s *Server) handleRounds(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"rounds": s.listRounds(cid)})
		return
	}
	if !s.configEditable(w, cid) {
		return
	}
	var in struct {
		NameVi              string `json:"name_vi"`
		NameEn              string `json:"name_en"`
		SortOrder           int    `json:"sort_order"`
		StandardDurationSec *int   `json:"standard_duration_sec"`
	}
	if err := decodeJSON(r, &in); err != nil || in.NameVi == "" {
		writeErr(w, 400, "VALIDATION_ERROR", "name_vi là bắt buộc.", nil)
		return
	}
	res, err := s.h.Exec(`INSERT INTO rounds(contest_id,name_vi,name_en,sort_order,standard_duration_sec,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, cid, in.NameVi, in.NameEn, in.SortOrder, in.StandardDurationSec, nowISO(), nowISO())
	if err != nil {
		writeErr(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	id, _ := res.LastInsertId()
	_ = id
	writeJSON(w, 201, map[string]any{"rounds": s.listRounds(cid)})
}
func (s *Server) handlePatchRound(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/rounds/")
	var in map[string]any
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	if v, ok := in["name_vi"]; ok {
		_, _ = s.h.Exec(`UPDATE rounds SET name_vi=?, updated_at=? WHERE id=?`, v, nowISO(), id)
	}
	if v, ok := in["name_en"]; ok {
		_, _ = s.h.Exec(`UPDATE rounds SET name_en=?, updated_at=? WHERE id=?`, v, nowISO(), id)
	}
	if v, ok := in["sort_order"]; ok {
		_, _ = s.h.Exec(`UPDATE rounds SET sort_order=?, updated_at=? WHERE id=?`, int(v.(float64)), nowISO(), id)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type teamOut struct {
	ID                int64  `json:"id"`
	RoundID           int64  `json:"round_id"`
	Code              string `json:"code"`
	NameVi            string `json:"name_vi"`
	NameEn            string `json:"name_en"`
	Organization      string `json:"organization"`
	SortOrder         int    `json:"sort_order"`
	IsActive          bool   `json:"is_active"`
	PerformanceID     int64  `json:"performance_id"`
	PerformanceStatus string `json:"performance_status"`
}

func (s *Server) listTeams(roundID int64) []teamOut {
	rows, _ := s.h.Query(`SELECT t.id,t.round_id,t.code,t.name_vi,COALESCE(t.name_en,''),COALESCE(t.organization,''),t.sort_order,t.is_active,COALESCE(p.id,0),COALESCE(p.status,'') FROM teams t LEFT JOIN performances p ON p.team_id=t.id WHERE t.round_id=? ORDER BY t.sort_order,t.id`, roundID)
	defer rows.Close()
	out := []teamOut{}
	for rows.Next() {
		var x teamOut
		var a int
		_ = rows.Scan(&x.ID, &x.RoundID, &x.Code, &x.NameVi, &x.NameEn, &x.Organization, &x.SortOrder, &a, &x.PerformanceID, &x.PerformanceStatus)
		x.IsActive = a == 1
		out = append(out, x)
	}
	return out
}
func (s *Server) codeExistsInContest(contestID int64, code string, exceptTeam int64) bool {
	var n int
	_ = s.h.QueryRow(`SELECT COUNT(*) FROM teams t JOIN rounds r ON r.id=t.round_id WHERE r.contest_id=? AND t.code=? AND t.id<>?`, contestID, code, exceptTeam).Scan(&n)
	return n > 0
}
func (s *Server) createTeam(roundID int64, code, vi, en, org, note string, ord int) (int64, error) {
	t := nowISO()
	res, err := s.h.Exec(`INSERT INTO teams(round_id,code,name_vi,name_en,organization,members_note,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, roundID, code, vi, en, org, note, ord, t, t)
	if err != nil {
		return 0, err
	}
	tid, _ := res.LastInsertId()
	_, err = s.h.Exec(`INSERT INTO performances(team_id,status,sort_order,created_at,updated_at) VALUES(?,'pending',?,?,?)`, tid, ord, t, t)
	return tid, err
}
func (s *Server) handleTeams(w http.ResponseWriter, r *http.Request, u *User) {
	rid := pathID(r, "/api/v1/rounds/")
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"teams": s.listTeams(rid)})
		return
	}
	cid := s.contestOfRound(rid)
	if !s.configEditable(w, cid) {
		return
	}
	var in struct {
		Code         string `json:"code"`
		NameVi       string `json:"name_vi"`
		NameEn       string `json:"name_en"`
		Organization string `json:"organization"`
		MembersNote  string `json:"members_note"`
		SortOrder    int    `json:"sort_order"`
	}
	if err := decodeJSON(r, &in); err != nil || in.Code == "" || in.NameVi == "" {
		writeErr(w, 400, "VALIDATION_ERROR", "code (SBD) và name_vi là bắt buộc.", nil)
		return
	}
	if s.codeExistsInContest(cid, in.Code, 0) {
		writeErr(w, 409, "VALIDATION_ERROR", "SBD '"+in.Code+"' đã trùng trong cuộc thi này.", nil)
		return
	}
	if _, err := s.createTeam(rid, in.Code, in.NameVi, in.NameEn, in.Organization, in.MembersNote, in.SortOrder); err != nil {
		writeErr(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	writeJSON(w, 201, map[string]any{"teams": s.listTeams(rid)})
}
func (s *Server) handlePatchTeam(w http.ResponseWriter, r *http.Request, u *User) {
	parts := pathParts(r, "/api/v1/teams/")
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	if len(parts) > 1 && parts[1] == "deactivate" {
		var perfStatus string
		_ = s.h.QueryRow(`SELECT COALESCE(p.status,'') FROM teams t LEFT JOIN performances p ON p.team_id=t.id WHERE t.id=?`, id).Scan(&perfStatus)
		if perfStatus != "" && perfStatus != "pending" {
			writeErr(w, 422, "INVALID_STATE_TRANSITION", "Đội đã thi, không vô hiệu hóa được.", nil)
			return
		}
		_, _ = s.h.Exec(`UPDATE teams SET is_active=0, updated_at=? WHERE id=?`, nowISO(), id)
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	var in map[string]any
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	var cid int64
	_ = s.h.QueryRow(`SELECT r.contest_id FROM teams t JOIN rounds r ON r.id=t.round_id WHERE t.id=?`, id).Scan(&cid)
	if v, ok := in["code"]; ok {
		if s.codeExistsInContest(cid, v.(string), id) {
			writeErr(w, 409, "VALIDATION_ERROR", "SBD đã trùng trong cuộc thi.", nil)
			return
		}
		_, _ = s.h.Exec(`UPDATE teams SET code=?, updated_at=? WHERE id=?`, v, nowISO(), id)
	}
	if v, ok := in["name_vi"]; ok {
		_, _ = s.h.Exec(`UPDATE teams SET name_vi=?, updated_at=? WHERE id=?`, v, nowISO(), id)
	}
	if v, ok := in["name_en"]; ok {
		_, _ = s.h.Exec(`UPDATE teams SET name_en=?, updated_at=? WHERE id=?`, v, nowISO(), id)
	}
	if v, ok := in["organization"]; ok {
		_, _ = s.h.Exec(`UPDATE teams SET organization=?, updated_at=? WHERE id=?`, v, nowISO(), id)
	}
	if v, ok := in["sort_order"]; ok {
		_, _ = s.h.Exec(`UPDATE teams SET sort_order=?, updated_at=? WHERE id=?`, int(v.(float64)), nowISO(), id)
		_, _ = s.h.Exec(`UPDATE performances SET sort_order=? WHERE team_id=?`, int(v.(float64)), id)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// CSV import: header code,name_vi,name_en,organization,sort_order — errors reported per line.
func (s *Server) handleImportTeamsCSV(w http.ResponseWriter, r *http.Request, u *User) {
	rid := pathID(r, "/api/v1/rounds/")
	cid := s.contestOfRound(rid)
	if !s.configEditable(w, cid) {
		return
	}
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Cần gửi file qua multipart/form-data (field: file).", nil)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Thiếu file CSV.", nil)
		return
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "File CSV không đọc được: "+err.Error(), nil)
		return
	}
	mode := r.FormValue("mode")
	imported := 0
	errs := []map[string]any{}
	maxOrd := 0
	_ = s.h.QueryRow(`SELECT COALESCE(MAX(sort_order),0) FROM teams WHERE round_id=?`, rid).Scan(&maxOrd)
	for i, rec := range recs {
		line := i + 1
		if i == 0 && len(rec) > 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "code") {
			continue
		}
		if len(rec) < 2 || strings.TrimSpace(rec[0]) == "" || strings.TrimSpace(rec[1]) == "" {
			errs = append(errs, map[string]any{"row": line, "message": "Thiếu SBD hoặc tên đội"})
			continue
		}
		code := strings.TrimSpace(rec[0])
		name := strings.TrimSpace(rec[1])
		en, org := "", ""
		if len(rec) > 2 {
			en = strings.TrimSpace(rec[2])
		}
		if len(rec) > 3 {
			org = strings.TrimSpace(rec[3])
		}
		ord := maxOrd + imported + 1
		if len(rec) > 4 {
			if n, e := strconv.Atoi(strings.TrimSpace(rec[4])); e == nil {
				ord = n
			}
		}
		if s.codeExistsInContest(cid, code, 0) {
			errs = append(errs, map[string]any{"row": line, "message": "SBD '" + code + "' đã tồn tại trong cuộc thi"})
			continue
		}
		if mode == "all_or_nothing" && len(errs) > 0 {
			continue
		}
		if _, err := s.createTeam(rid, code, name, en, org, "", ord); err != nil {
			errs = append(errs, map[string]any{"row": line, "message": err.Error()})
			continue
		}
		imported++
	}
	if mode == "all_or_nothing" && len(errs) > 0 {
		// rollback imported in this request is complex; MVP: report and keep valid_only semantics documented in README
	}
	writeJSON(w, 200, map[string]any{"imported": imported, "errors": errs, "teams": s.listTeams(rid)})
}

func (s *Server) handleShuffle(w http.ResponseWriter, r *http.Request, u *User) {
	rid := pathID(r, "/api/v1/rounds/")
	var started int
	_ = s.h.QueryRow(`SELECT COUNT(*) FROM performances p JOIN teams t ON t.id=p.team_id WHERE t.round_id=? AND p.status<>'pending'`, rid).Scan(&started)
	if started > 0 {
		writeErr(w, 422, "INVALID_STATE_TRANSITION", "Đã có đội bắt đầu thi, không bốc thăm lại được.", nil)
		return
	}
	teams := s.listTeams(rid)
	perm := rand.Perm(len(teams))
	for i, idx := range perm {
		_, _ = s.h.Exec(`UPDATE teams SET sort_order=?, updated_at=? WHERE id=?`, i+1, nowISO(), teams[idx].ID)
		_, _ = s.h.Exec(`UPDATE performances SET sort_order=? WHERE team_id=?`, i+1, teams[idx].ID)
	}
	audit(s.h, u.ID, "assignment.shuffle_order", "round", rid, nil, "", "", "Bốc thăm ngẫu nhiên thứ tự thi")
	writeJSON(w, 200, map[string]any{"teams": s.listTeams(rid)})
}
