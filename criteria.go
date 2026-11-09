package main

import (
	"net/http"
	"strconv"
	"strings"
)

type Option struct {
	ID         int64   `json:"id"`
	LabelVi    string  `json:"label_vi"`
	LabelEn    string  `json:"label_en"`
	ScoreValue float64 `json:"score_value"`
	SortOrder  int     `json:"sort_order"`
}
type Criterion struct {
	ID          int64    `json:"id"`
	GroupID     int64    `json:"group_id"`
	NameVi      string   `json:"name_vi"`
	NameEn      string   `json:"name_en"`
	DescVi      string   `json:"desc_vi"`
	DescEn      string   `json:"desc_en"`
	InputType   string   `json:"input_type"`
	MinScore    float64  `json:"min_score"`
	MaxScore    float64  `json:"max_score"`
	Step        float64  `json:"step"`
	Weight      float64  `json:"weight"`
	IsAdminOnly bool     `json:"is_admin_only"`
	IsPenalty   bool     `json:"is_penalty_criterion"`
	CommentMode string   `json:"comment_mode"`
	SortOrder   int      `json:"sort_order"`
	Options     []Option `json:"options"`
}
type Group struct {
	ID        int64       `json:"id"`
	ContestID int64       `json:"contest_id"`
	NameVi    string      `json:"name_vi"`
	NameEn    string      `json:"name_en"`
	SortOrder int         `json:"sort_order"`
	Criteria  []Criterion `json:"criteria"`
}

func (s *Server) criteriaTree(contestID int64, includeAdmin bool) []Group {
	rows, _ := s.h.Query(`SELECT id,contest_id,name_vi,COALESCE(name_en,''),sort_order FROM criteria_groups WHERE contest_id=? ORDER BY sort_order,id`, contestID)
	groups := []Group{}
	for rows.Next() {
		var g Group
		_ = rows.Scan(&g.ID, &g.ContestID, &g.NameVi, &g.NameEn, &g.SortOrder)
		g.Criteria = []Criterion{}
		groups = append(groups, g)
	}
	rows.Close()
	for gi := range groups {
		crows, _ := s.h.Query(`SELECT id,group_id,name_vi,COALESCE(name_en,''),COALESCE(desc_vi,''),COALESCE(desc_en,''),input_type,min_score,max_score,step,weight,is_admin_only,is_penalty_criterion,comment_mode,sort_order FROM criteria WHERE group_id=? ORDER BY sort_order,id`, groups[gi].ID)
		crits := []Criterion{}
		for crows.Next() {
			var c Criterion
			var ao, pen int
			_ = crows.Scan(&c.ID, &c.GroupID, &c.NameVi, &c.NameEn, &c.DescVi, &c.DescEn, &c.InputType, &c.MinScore, &c.MaxScore, &c.Step, &c.Weight, &ao, &pen, &c.CommentMode, &c.SortOrder)
			c.IsAdminOnly = ao == 1
			c.IsPenalty = pen == 1
			crits = append(crits, c)
		}
		crows.Close()
		for _, c := range crits {
			if c.IsAdminOnly && !includeAdmin {
				continue
			}
			c.Options = []Option{}
			orows, _ := s.h.Query(`SELECT id,label_vi,COALESCE(label_en,''),score_value,sort_order FROM criteria_options WHERE criterion_id=? ORDER BY sort_order,id`, c.ID)
			for orows.Next() {
				var o Option
				_ = orows.Scan(&o.ID, &o.LabelVi, &o.LabelEn, &o.ScoreValue, &o.SortOrder)
				c.Options = append(c.Options, o)
			}
			orows.Close()
			groups[gi].Criteria = append(groups[gi].Criteria, c)
		}
	}
	return groups
}

func (s *Server) criterionByID(id int64) *Criterion {
	for _, g := range s.criteriaTree(s.contestOfCriterion(id), true) {
		for _, c := range g.Criteria {
			if c.ID == id {
				cc := c
				return &cc
			}
		}
	}
	return nil
}

func (s *Server) handleGetCriteria(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	includeAdmin := u.Role == "admin"
	writeJSON(w, 200, map[string]any{"groups": s.criteriaTree(cid, includeAdmin), "total_max_score": s.totalMaxScore(cid)})
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	if !s.configEditable(w, cid) {
		return
	}
	var in struct {
		NameVi    string `json:"name_vi"`
		NameEn    string `json:"name_en"`
		SortOrder int    `json:"sort_order"`
	}
	if err := decodeJSON(r, &in); err != nil || in.NameVi == "" {
		writeErr(w, 400, "VALIDATION_ERROR", "name_vi là bắt buộc.", nil)
		return
	}
	res, err := s.h.Exec(`INSERT INTO criteria_groups(contest_id,name_vi,name_en,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?)`, cid, in.NameVi, in.NameEn, in.SortOrder, nowISO(), nowISO())
	if err != nil {
		writeErr(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, map[string]any{"id": id, "groups": s.criteriaTree(cid, true)})
}

func (s *Server) handleGroupByID(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/criteria-groups/")
	cid := s.contestOfGroup(id)
	if !s.configEditable(w, cid) {
		return
	}
	if r.Method == http.MethodDelete {
		var refs int
		_ = s.h.QueryRow(`SELECT COUNT(*) FROM score_details sd JOIN criteria c ON c.id=sd.criterion_id WHERE c.group_id=?`, id).Scan(&refs)
		if refs > 0 {
			writeErr(w, 409, "VALIDATION_ERROR", "Nhóm đã có điểm tham chiếu, không xóa được.", nil)
			return
		}
		_, _ = s.h.Exec(`DELETE FROM criteria_groups WHERE id=?`, id)
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	var in map[string]any
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	if v, ok := in["name_vi"]; ok {
		_, _ = s.h.Exec(`UPDATE criteria_groups SET name_vi=?, updated_at=? WHERE id=?`, v, nowISO(), id)
	}
	if v, ok := in["name_en"]; ok {
		_, _ = s.h.Exec(`UPDATE criteria_groups SET name_en=?, updated_at=? WHERE id=?`, v, nowISO(), id)
	}
	if v, ok := in["sort_order"]; ok {
		_, _ = s.h.Exec(`UPDATE criteria_groups SET sort_order=?, updated_at=? WHERE id=?`, int(v.(float64)), nowISO(), id)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type criterionInput struct {
	NameVi      string  `json:"name_vi"`
	NameEn      string  `json:"name_en"`
	DescVi      string  `json:"desc_vi"`
	DescEn      string  `json:"desc_en"`
	InputType   string  `json:"input_type"`
	MinScore    float64 `json:"min_score"`
	MaxScore    float64 `json:"max_score"`
	Step        float64 `json:"step"`
	Weight      float64 `json:"weight"`
	IsAdminOnly bool    `json:"is_admin_only"`
	IsPenalty   bool    `json:"is_penalty_criterion"`
	CommentMode string  `json:"comment_mode"`
	SortOrder   int     `json:"sort_order"`
}

func validateCriterion(in criterionInput) string {
	if strings.TrimSpace(in.NameVi) == "" {
		return "name_vi là bắt buộc"
	}
	if in.InputType != "range" && in.InputType != "discrete" {
		return "input_type phải là range hoặc discrete"
	}
	if in.MaxScore <= in.MinScore {
		return "max_score phải lớn hơn min_score"
	}
	if in.Weight <= 0 {
		return "weight phải > 0"
	}
	if in.InputType == "range" && (in.Step <= 0 || in.Step > in.MaxScore-in.MinScore) {
		return "step phải > 0 và không lớn hơn khoảng điểm"
	}
	return ""
}
func (s *Server) handleCreateCriterion(w http.ResponseWriter, r *http.Request, u *User) {
	gid := pathID(r, "/api/v1/criteria-groups/")
	cid := s.contestOfGroup(gid)
	if !s.configEditable(w, cid) {
		return
	}
	var in criterionInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	if in.Step == 0 {
		in.Step = 1
	}
	if in.CommentMode == "" {
		in.CommentMode = "optional"
	}
	if msg := validateCriterion(in); msg != "" {
		writeErr(w, 400, "VALIDATION_ERROR", msg, nil)
		return
	}
	res, err := s.h.Exec(`INSERT INTO criteria(group_id,name_vi,name_en,desc_vi,desc_en,input_type,min_score,max_score,step,weight,is_admin_only,is_penalty_criterion,comment_mode,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		gid, in.NameVi, in.NameEn, in.DescVi, in.DescEn, in.InputType, in.MinScore, in.MaxScore, in.Step, in.Weight, b2i(in.IsAdminOnly), b2i(in.IsPenalty), in.CommentMode, in.SortOrder, nowISO(), nowISO())
	if err != nil {
		writeErr(w, 500, "INTERNAL", err.Error(), nil)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) handleCriterionByID(w http.ResponseWriter, r *http.Request, u *User) {
	parts := pathParts(r, "/api/v1/criteria/")
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	// sub-resource: options
	if len(parts) > 1 && parts[1] == "options" {
		s.handleCreateOption(w, r, u, id)
		return
	}
	cid := s.contestOfCriterion(id)
	if !s.configEditable(w, cid) {
		return
	}
	if r.Method == http.MethodDelete {
		var refs int
		_ = s.h.QueryRow(`SELECT COUNT(*) FROM score_details WHERE criterion_id=?`, id).Scan(&refs)
		if refs > 0 {
			writeErr(w, 409, "VALIDATION_ERROR", "Tiêu chí đã có điểm, không xóa được.", nil)
			return
		}
		_, _ = s.h.Exec(`DELETE FROM criteria WHERE id=?`, id)
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	var in criterionInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	cur := s.criterionByID(id)
	if cur == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy tiêu chí.", nil)
		return
	}
	// merge with current for partial updates: if name empty treat as invalid per full-update contract
	if msg := validateCriterion(in); msg != "" {
		writeErr(w, 400, "VALIDATION_ERROR", msg, nil)
		return
	}
	_, _ = s.h.Exec(`UPDATE criteria SET name_vi=?,name_en=?,desc_vi=?,desc_en=?,input_type=?,min_score=?,max_score=?,step=?,weight=?,is_admin_only=?,comment_mode=?,sort_order=?,updated_at=? WHERE id=?`,
		in.NameVi, in.NameEn, in.DescVi, in.DescEn, in.InputType, in.MinScore, in.MaxScore, in.Step, in.Weight, b2i(in.IsAdminOnly), in.CommentMode, in.SortOrder, nowISO(), id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleCreateOption(w http.ResponseWriter, r *http.Request, _ *User, critID int64) {
	cid := s.contestOfCriterion(critID)
	if !s.configEditable(w, cid) {
		return
	}
	var in struct {
		LabelVi    string  `json:"label_vi"`
		LabelEn    string  `json:"label_en"`
		ScoreValue float64 `json:"score_value"`
		SortOrder  int     `json:"sort_order"`
	}
	if err := decodeJSON(r, &in); err != nil || in.LabelVi == "" {
		writeErr(w, 400, "VALIDATION_ERROR", "label_vi là bắt buộc.", nil)
		return
	}
	c := s.criterionByID(critID)
	if c == nil {
		writeErr(w, 404, "NOT_FOUND", "Không tìm thấy tiêu chí.", nil)
		return
	}
	if c.InputType != "discrete" {
		writeErr(w, 400, "VALIDATION_ERROR", "Chỉ tiêu chí discrete mới có options.", nil)
		return
	}
	if in.ScoreValue < c.MinScore || in.ScoreValue > c.MaxScore {
		writeErr(w, 400, "VALIDATION_ERROR", "score_value phải nằm trong [min_score, max_score] của tiêu chí.", nil)
		return
	}
	res, err := s.h.Exec(`INSERT INTO criteria_options(criterion_id,label_vi,label_en,score_value,sort_order) VALUES(?,?,?,?,?)`, critID, in.LabelVi, in.LabelEn, in.ScoreValue, in.SortOrder)
	if err != nil {
		writeErr(w, 409, "VALIDATION_ERROR", "Giá trị điểm của option đã trùng trong tiêu chí.", nil)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) handleOptionByID(w http.ResponseWriter, r *http.Request, u *User) {
	id := pathID(r, "/api/v1/criteria-options/")
	var critID int64
	_ = s.h.QueryRow(`SELECT criterion_id FROM criteria_options WHERE id=?`, id).Scan(&critID)
	cid := s.contestOfCriterion(critID)
	if !s.configEditable(w, cid) {
		return
	}
	if r.Method == http.MethodDelete {
		_, _ = s.h.Exec(`DELETE FROM criteria_options WHERE id=?`, id)
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	var in map[string]any
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	if v, ok := in["label_vi"]; ok {
		_, _ = s.h.Exec(`UPDATE criteria_options SET label_vi=? WHERE id=?`, v, id)
	}
	if v, ok := in["label_en"]; ok {
		_, _ = s.h.Exec(`UPDATE criteria_options SET label_en=? WHERE id=?`, v, id)
	}
	if v, ok := in["score_value"]; ok {
		_, _ = s.h.Exec(`UPDATE criteria_options SET score_value=? WHERE id=?`, v.(float64), id)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- Assignments ----------
func (s *Server) handleGetAssignments(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	rows, _ := s.h.Query(`SELECT a.performance_id, a.judge_id FROM assignments a JOIN performances p ON p.id=a.performance_id JOIN teams t ON t.id=p.team_id JOIN rounds r ON r.id=t.round_id WHERE r.contest_id=? ORDER BY a.performance_id, a.judge_id`, cid)
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var p, j int64
		_ = rows.Scan(&p, &j)
		out = append(out, map[string]any{"performance_id": p, "judge_id": j})
	}
	writeJSON(w, 200, map[string]any{"assignments": out})
}
func (s *Server) handlePutAssignments(w http.ResponseWriter, r *http.Request, u *User) {
	cid := pathID(r, "/api/v1/contests/")
	if !s.configEditable(w, cid) {
		return
	}
	var in struct {
		Assignments []struct {
			PerformanceID int64 `json:"performance_id"`
			JudgeID       int64 `json:"judge_id"`
		} `json:"assignments"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, 400, "VALIDATION_ERROR", "Dữ liệu không hợp lệ.", nil)
		return
	}
	_, _ = s.h.Exec(`DELETE FROM assignments WHERE performance_id IN (SELECT p.id FROM performances p JOIN teams t ON t.id=p.team_id JOIN rounds r ON r.id=t.round_id WHERE r.contest_id=?)`, cid)
	for _, a := range in.Assignments {
		var role string
		_ = s.h.QueryRow(`SELECT role FROM users WHERE id=? AND is_active=1`, a.JudgeID).Scan(&role)
		if role != "judge" {
			continue
		}
		if s.contestOfPerformance(a.PerformanceID) != cid {
			continue
		}
		_, _ = s.h.Exec(`INSERT OR IGNORE INTO assignments(performance_id,judge_id,created_at) VALUES(?,?,?)`, a.PerformanceID, a.JudgeID, nowISO())
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
