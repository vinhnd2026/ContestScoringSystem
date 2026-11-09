package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]*User
	seen    map[int64]time.Time // user_id -> last heartbeat/connection
	drafts  map[string]bool     // "perfID:judgeID" -> draft ping
}

func newHub() *Hub {
	return &Hub{clients: map[*websocket.Conn]*User{}, seen: map[int64]time.Time{}, drafts: map[string]bool{}}
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (s *Server) broadcast(v any) {
	b, _ := json.Marshal(v)
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	for c := range s.hub.clients {
		_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err := c.WriteMessage(websocket.TextMessage, b); err != nil {
			_ = c.Close()
			delete(s.hub.clients, c)
		}
	}
}

func (s *Server) isOnline(userID int64) bool {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	t, ok := s.hub.seen[userID]
	return ok && time.Since(t) < 35*time.Second
}
func (s *Server) isDraftPing(perfID, judgeID int64) bool {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.hub.drafts[key2(perfID, judgeID)]
}
func key2(a, b int64) string { return itoa(a) + ":" + itoa(b) }
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	out := ""
	for v > 0 {
		out = string(rune('0'+v%10)) + out
		v /= 10
	}
	if neg {
		out = "-" + out
	}
	return out
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	u, _ := s.currentUser(r)
	if u == nil {
		http.Error(w, "unauthenticated", 401)
		return
	}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.hub.mu.Lock()
	s.hub.clients[c] = u
	s.hub.seen[u.ID] = time.Now()
	s.hub.mu.Unlock()
	// initial state for active contest (first active, else first)
	contests := []int64{}
	rows, _ := s.h.Query(`SELECT id FROM contests ORDER BY (status='active') DESC, id`)
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		contests = append(contests, id)
	}
	rows.Close()
	if len(contests) > 0 {
		cid := contests[0]
		msg, _ := json.Marshal(map[string]any{"type": "contest.state", "contest_id": cid, "status": s.contestByID(cid).Status, "performances": s.perfsOfContest(cid)})
		_ = c.WriteMessage(websocket.TextMessage, msg)
	}
	go func() {
		defer func() {
			s.hub.mu.Lock()
			delete(s.hub.clients, c)
			s.hub.mu.Unlock()
			_ = c.Close()
			s.broadcast(map[string]any{"type": "presence.update", "user_id": u.ID, "online": false})
		}()
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			var m struct {
				Type          string `json:"type"`
				ContestID     int64  `json:"contest_id"`
				PerformanceID int64  `json:"performance_id"`
			}
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			s.hub.mu.Lock()
			s.hub.seen[u.ID] = time.Now()
			s.hub.mu.Unlock()
			switch m.Type {
			case "client.heartbeat":
				// seen updated above
			case "client.subscribe":
				if m.ContestID > 0 {
					msg, _ := json.Marshal(map[string]any{"type": "contest.state", "contest_id": m.ContestID, "performances": s.perfsOfContest(m.ContestID)})
					_ = c.WriteMessage(websocket.TextMessage, msg)
				}
			case "score.draft_ping":
				if m.PerformanceID > 0 && u.Role == "judge" {
					s.hub.mu.Lock()
					s.hub.drafts[itoa(m.PerformanceID)+":"+itoa(u.ID)] = true
					s.hub.mu.Unlock()
					s.broadcast(map[string]any{"type": "score.updated", "performance_id": m.PerformanceID, "judge_id": u.ID, "score_status": "draft"})
				}
			default:
				// ignore other client->server messages per API spec
			}
		}
	}()
}

// timer ticker: every 1s broadcast timer.sync for performing performances
func (s *Server) timerLoop() {
	for {
		time.Sleep(1 * time.Second)
		rows, _ := s.h.Query(`SELECT id FROM performances WHERE status='performing'`)
		ids := []int64{}
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			p := s.perfByID(id)
			if p == nil {
				continue
			}
			s.broadcast(map[string]any{"type": "timer.sync", "performance_id": id, "timer_status": p.TimerStatus, "elapsed_sec": p.ElapsedSec, "remaining_sec": p.RemainingSec, "overtime_sec": p.LiveOvertimeSec, "server_now": nowISO()})
		}
	}
}
