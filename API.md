# API — Contest Scoring System

REST API và WebSocket cho PRD v2 (xem [PRD.md](PRD.md), [DATABASE.md](DATABASE.md)). Thuật ngữ, state machine và công thức trong tài liệu này khớp PRD.md; khi có mâu thuẫn, PRD.md là chuẩn nghiệp vụ và tài liệu này phải được sửa theo.

## 1. Quy ước chung

- **Base URL:** cùng origin với SPA, tiền tố `/api/v1`. Ví dụ trên LAN: `http://192.168.1.10:8080/api/v1/...`
- **Định dạng:** JSON, UTF-8. Thời gian ISO 8601 UTC do server ghi.
- **Xác thực:** cookie session `HttpOnly` (PRD Mục 13). Mọi endpoint trừ `POST /auth/login` và `GET /public/join/:join_code` đều yêu cầu session hợp lệ.
- **Phân quyền:** kiểm tra ở server theo vai trò `admin` / `judge` / `mc` và theo assignment. Ẩn nút ở frontend không thay thế kiểm tra này.
- **Song ngữ:** response trả cả `name_vi` và `name_en` (và các cặp tương tự); client tự chọn bản hiển thị. Server không lọc theo ngôn ngữ.
- **ID:** số nguyên theo DATABASE.md. `client_uuid` là UUID chuỗi do client tạo (chỉ dùng cho phiếu điểm).

## 2. Phân quyền theo nhóm endpoint (tóm tắt)

| Nhóm | admin | judge | mc |
|---|:---:|:---:|:---:|
| Auth (của chính mình) | ✓ | ✓ | ✓ |
| Users (quản trị) | ✓ | — | — |
| Contests / Rounds / Teams / Criteria / Assignments (ghi) | ✓ | — | — |
| Contests / Rounds / Teams (đọc) | ✓ | ✓ (phạm vi contest đang tham gia) | ✓ (chỉ phần hiển thị công khai) |
| Flow control + Timer (ghi) | ✓ | — | — |
| Scores (ghi phiếu thường) | — (dùng override) | ✓ (chỉ khi có assignment) | — |
| Scores (đọc) | ✓ (tất cả) | ✓ (chỉ phiếu của mình) | — (chỉ điểm đã reveal qua Public) |
| Admin-Only scores, Override, Audit, Backup | ✓ | — | — |
| Public view data | ✓ | ✓ | ✓ (chỉ dữ liệu đã reveal) |
| Export Excel | ✓ | — | — |

## 3. Format lỗi thống nhất

Mọi lỗi trả về cùng một cấu trúc:

```json
{
  "error": {
    "code": "SCORE_VERSION_CONFLICT",
    "message": "Phiếu điểm đã thay đổi trên server. Tải lại trước khi lưu.",
    "details": { "server_version": 4 }
  }
}
```

- `code`: mã máy đọc, viết hoa có gạch dưới, ổn định theo thời gian (không đổi tên khi đã phát hành).
- `message`: thông báo tiếng Việt hiển thị được cho người dùng.
- `details`: tùy chọn, dữ liệu phụ để client xử lý (ví dụ version hiện tại của server).
- HTTP status dùng chuẩn: `400` dữ liệu không hợp lệ, `401` chưa đăng nhập/session hết hạn, `403` không có quyền, `404` không tồn tại (hoặc không thuộc phạm vi nhìn thấy của người gọi), `409` xung đột trạng thái/phiên bản, `422` vi phạm luật nghiệp vụ (ví dụ chuyển trạng thái không hợp lệ, cấu hình chưa đạt checklist), `429` bị giới hạn đăng nhập sai.
- Mã lỗi tối thiểu phải có: `VALIDATION_ERROR`, `UNAUTHENTICATED`, `FORBIDDEN`, `NOT_FOUND`, `INVALID_STATE_TRANSITION`, `CONFIG_LOCKED` (sửa cấu hình khi contest active), `NOT_ASSIGNED`, `SCORE_VERSION_CONFLICT`, `SCORE_LOCKED`, `ACTIVATION_CHECKLIST_FAILED`, `LOGIN_LOCKED`, `BACKUP_RESTORE_BLOCKED`.

Ví dụ lỗi checklist kích hoạt (`422`):

```json
{
  "error": {
    "code": "ACTIVATION_CHECKLIST_FAILED",
    "message": "Cuộc thi chưa đủ điều kiện kích hoạt.",
    "details": {
      "blockers": [
        { "type": "team_without_judge", "team_id": 12, "message": "Đội 12 chưa có giám khảo nào được phân công" },
        { "type": "no_criteria", "message": "Chưa có tiêu chí chấm thường nào" }
      ],
      "warnings": [
        { "type": "missing_translation", "criterion_id": 5, "message": "Tiêu chí 5 thiếu bản dịch tiếng Anh" }
      ]
    }
  }
}
```

## 4. Idempotency và đồng bộ phiếu điểm

Áp dụng cho các endpoint ghi phiếu điểm (Mục 6.7):

1. Client tạo `client_uuid` (UUID v4) khi bắt đầu nháp một phiếu và giữ nguyên cho phiếu đó; lưu cùng nháp trong IndexedDB.
2. `PUT /scores/by-client-uuid/:client_uuid` là endpoint ghi nháp/nộp dạng upsert theo `client_uuid`:
   - Chưa có phiếu với uuid này → tạo mới (kiểm tra assignment trước), `version = 1`.
   - Đã có → cập nhật dựa trên `base_version` trong body: khớp version server thì ghi và tăng `version`; không khớp thì `409 SCORE_VERSION_CONFLICT` kèm `server_version` và bản phiếu hiện tại của server.
   - Gửi lại **nguyên văn** một yêu cầu đã thành công (cùng `client_uuid`, cùng `base_version`, cùng nội dung) → trả lại kết quả đã ghi, không tăng version, không tạo bản ghi mới. Server nhận diện bằng cách so khớp nội dung đã lưu với yêu cầu lặp; nếu nội dung khác mà `base_version` cũ thì đó là xung đột (điểm 2), không phải retry.
3. Nộp phiếu là cờ trong cùng upsert (`"submit": true`) hoặc endpoint riêng `POST /scores/:id/submit` cho phiếu đã có trên server — cả hai đều idempotent theo nghĩa: nộp một phiếu đã `submitted` trả về `200` với trạng thái hiện tại, không lỗi, không đổi dữ liệu.
4. Luật xung đột nội dung tuân thủ PRD Mục 10.3: bản `submitted`/`locked` trên server thắng bản `draft` local; server trả `409` kèm bản server để client hiển thị đối chiếu.
5. Các endpoint ghi còn lại (tạo contest, đội…) không yêu cầu idempotency key ở MVP; riêng tạo đội qua import Excel có cơ chế kiểm tra trùng SBD thay thế (PRD Mục 6.2.2).

## 5. WebSocket

### 5.1. Kết nối

- Endpoint: `GET /ws` (upgrade từ cùng origin, xác thực bằng cookie session như REST).
- Khi kết nối, server gửi ngay một sự kiện `contest.state` cho contest mà người dùng liên quan (giám khảo: contest đang active mà mình có assignment; mc: contest đang active; admin: contest đang được chọn trên dashboard — client có thể gửi `{"type":"subscribe","contest_id":3}` để đổi contest theo dõi).
- **Heartbeat:** client gửi `client.heartbeat` mỗi 10 giây; server coi thiết bị là mất kết nối nếu không có heartbeat quá 30 giây và phản ánh lên Live Matrix (sự kiện `presence.update`). Heartbeat cũng là đường để giám khảo báo "vẫn đang chấm" mà không cần gửi dữ liệu điểm.
- Mất kết nối: client tự kết nối lại với backoff (1s, 2s, 5s, 10s, tối đa 30s); sau khi nối lại, client **không** dựa vào các sự kiện đã lỡ — phải gọi `GET /contests/:id/state` để lấy trạng thái đầy đủ rồi mới tiếp tục nhận sự kiện.

### 5.2. Sự kiện server → client

| Sự kiện (`type`) | Khi nào gửi | Payload chính |
|---|---|---|
| `contest.state` | Kết nối lần đầu, contest đổi trạng thái, sau restore | `contest_id`, `status`, `is_finalized`, toàn bộ snapshot nhẹ: performance đang `performing` (nếu có), danh sách trạng thái performance |
| `performance.started` | Admin bắt đầu thi một đội | `performance_id`, `team_id`, `round_id`, `started_at`, `standard_duration_sec` |
| `performance.paused` | Timer tạm dừng | `performance_id`, `paused_at` |
| `performance.resumed` | Timer tiếp tục | `performance_id`, `paused_total_sec` |
| `performance.ended` | Admin kết thúc phần thi | `performance_id`, `status: "scoring"`, `overtime_sec`, `penalty_status`, `penalty_proposed` |
| `timer.sync` | Mỗi 1 giây khi có performance đang `performing` | `performance_id`, `timer_status`, `elapsed_sec`, `remaining_sec`, `overtime_sec`, `server_now` |
| `score.updated` | Một phiếu thay đổi trạng thái/nội dung (nộp, sửa, override, khóa) | `performance_id`, `judge_id`, `score_status`, `version` — **không** chứa điểm chi tiết khi gửi cho judge khác/mc; bản cho chính giám khảo và admin mới kèm chi tiết theo quyền |
| `performance.locked` | Admin khóa điểm | `performance_id`, `status: "locked"` |
| `performance.revealed` | Admin công bố | `performance_id`, `status: "revealed"`, `final_score` (chỉ gửi kèm điểm vì đã reveal) |
| `performance.unrevealed` | Admin thu hồi công bố | `performance_id`, `status: "locked"` |
| `leaderboard.updated` | Điểm cuối của một đội thay đổi hoặc thứ hạng đổi | `round_id`, danh sách đã xếp hạng **chỉ gồm các đội đã `locked`/`revealed`**; bản cho `mc` chỉ gồm đội đã `revealed` và chỉ khi `leaderboard_revealed = 1` |
| `leaderboard.revealed` | Admin bật/tắt reveal bảng xếp hạng | `round_id`, `leaderboard_revealed` |
| `presence.update` | Thiết bị giám khảo mất/khôi phục kết nối | `user_id`, `online` |
| `penalty.updated` | Đề xuất trừ giờ được duyệt/từ chối/sửa mức | `performance_id`, `penalty_status`, `penalty_applied` |
| `system.notice` | Thông báo vận hành (sắp backup xong, restore hoàn tất, server sắp khởi động lại…) | `level`, `message` |

### 5.3. Tin nhắn client → server

WebSocket phía client chủ yếu để **nhận**; ghi dữ liệu nghiệp vụ đi qua REST để tận dụng idempotency và format lỗi. Chỉ ba loại tin nhắn client → server được chấp nhận:

| Tin nhắn (`type`) | Ý nghĩa |
|---|---|
| `client.heartbeat` | Báo thiết bị còn trực tuyến (mỗi 10 giây) |
| `client.subscribe` | Đổi contest mà kết nối này theo dõi (admin) |
| `score.draft_ping` | Báo "đang chấm dở" cho một performance để Live Matrix hiện trạng thái vàng ngay cả khi nháp chưa đồng bộ lên server: `performance_id` — không chứa điểm |

Mọi tin nhắn client → server khác (đặc biệt là gửi điểm qua WebSocket) bị server bỏ qua và ghi log vận hành.

### 5.4. Đồng hồ qua WebSocket

`timer.sync` mang `server_now` để client hiệu chỉnh độ lệch hiển thị. Client tính thời gian còn lại từ `started_at`, `standard_duration_sec`, `paused_total_sec` của server trừ `server_now`, không tự cộng dồn bộ đếm riêng (PRD Mục 3.4).

## 6. REST endpoints theo module

Quy ước: `:id` là id số nguyên. Danh sách phân trang dùng `?page=&page_size=` khi có thể dài (audit log, teams); các danh sách cấu hình của một contest trả toàn bộ (quy mô MVP nhỏ, PRD Mục 14).

### 6.1. Auth & phiên

| Method & path | Quyền | Mô tả |
|---|---|---|
| `POST /auth/login` | công khai | Đăng nhập bằng `username` + `password` hoặc `pin`. Trả thông tin user (không trả hash). Lỗi `429 LOGIN_LOCKED` khi đang bị khóa |
| `POST /auth/logout` | đã đăng nhập | Hủy session phía server |
| `GET /auth/me` | đã đăng nhập | Người dùng hiện tại + vai trò |
| `POST /auth/change-password` | đã đăng nhập | Đổi mật khẩu/PIN của chính mình (gửi kèm mật khẩu hiện tại) |
| `GET /public/join/:join_code` | công khai | Thông tin tối thiểu của contest theo join code (tên, trạng thái) để trang quét QR hiển thị đúng cuộc thi trước khi đăng nhập; không chứa điểm |

### 6.2. Users (Module 1)

| Method & path | Quyền | Mô tả |
|---|---|---|
| `GET /users` | admin | Danh sách tài khoản (không có hash) |
| `POST /users` | admin | Tạo tài khoản: `username`, `display_name`, `role`, mật khẩu hoặc PIN ban đầu |
| `PATCH /users/:id` | admin | Sửa thông tin / vai trò / `is_active` |
| `POST /users/:id/reset-credential` | admin | Reset mật khẩu hoặc PIN (body: `type: "password" \| "pin"`, giá trị mới do Admin đặt hoặc yêu cầu server sinh ngẫu nhiên và trả về **một lần duy nhất** trong response này). Ghi audit `user.reset_pin`/`user.reset_password` |
| `POST /users/:id/unlock` | admin | Mở khóa đăng nhập sớm |

### 6.3. Contests (Module 1)

| Method & path | Quyền | Mô tả |
|---|---|---|
| `GET /contests` | admin; judge/mc: contest liên quan | Danh sách cuộc thi |
| `POST /contests` | admin | Tạo contest (trạng thái `pending`, server sinh `join_code`) |
| `GET /contests/:id` | theo phạm vi | Chi tiết + cấu hình (gồm luật trừ điểm) |
| `PATCH /contests/:id` | admin | Sửa thông tin/cấu hình. Sửa các trường thuộc cấu hình chấm khi `status='active'` → `409 CONFIG_LOCKED` |
| `GET /contests/:id/state` | theo phạm vi | Snapshot trạng thái đầy đủ cho client đồng bộ lại (contest, performances, timer hiện tại, phiếu của chính judge nếu có) |
| `POST /contests/:id/activate` | admin | `pending → active`; chạy checklist (PRD Mục 6.1.4), lỗi trả `422` theo format Mục 3 |
| `POST /contests/:id/complete` | admin | `active → completed` |
| `POST /contests/:id/finalize` | admin | Chốt sổ (`is_finalized = 1`), ghi audit |
| `POST /contests/:id/unfinalize` | admin | Mở chốt sổ; body bắt buộc `reason`; ghi audit |
| `POST /contests/:id/clone` | admin | Nhân bản cấu hình sang contest mới; trả contest mới |
| `POST /contests/:id/reset-scores` | admin | Reset điểm về trạng thái sạch; xác nhận lần hai bằng body `confirm: true`; ghi audit |
| `GET /contests/:id/export.xlsx` | admin | File Excel 4 sheet (PRD Mục 6.6.5) |

### 6.4. Rounds & Teams (Module 2)

| Method & path | Quyền | Mô tả |
|---|---|---|
| `GET /contests/:id/rounds` | theo phạm vi | Danh sách round kèm `leaderboard_revealed` |
| `POST /contests/:id/rounds` | admin | Tạo round (chỉ khi contest `pending` — thuộc cấu hình) |
| `PATCH /rounds/:id` | admin | Sửa round |
| `GET /rounds/:id/teams` | theo phạm vi | Đội trong round theo `sort_order`, kèm trạng thái performance |
| `POST /rounds/:id/teams` | admin | Tạo đội; server tự tạo performance `pending` tương ứng |
| `PATCH /teams/:id` | admin | Sửa đội (SBD kiểm tra trùng trong contest) |
| `POST /teams/:id/deactivate` | admin | Vô hiệu hóa đội chưa thi |
| `POST /rounds/:id/teams/import` | admin | Import Excel (`multipart/form-data`); trả báo cáo theo dòng: `imported`, `errors: [{row, message}]`; body có `mode: "valid_only" \| "all_or_nothing"` (mặc định `valid_only` sau khi Admin xem trước — endpoint xem trước là `POST /rounds/:id/teams/import/preview` với cùng file, không ghi dữ liệu) |
| `GET /teams/import-template.xlsx` | admin | File mẫu import |
| `POST /rounds/:id/shuffle-order` | admin | Bốc thăm: xáo trộn `sort_order` của đội trong round; `422` nếu đã có performance rời `pending`; ghi audit |

### 6.5. Criteria & Assignments (Module 3 + Ma trận phân công)

| Method & path | Quyền | Mô tả |
|---|---|---|
| `GET /contests/:id/criteria` | admin; judge: chỉ tiêu chí không Admin-Only của contest mình tham gia | Toàn bộ cây nhóm → tiêu chí → options, kèm `total_max_score` đã tính và tổng trọng số từng nhóm |
| `POST /contests/:id/criteria-groups` | admin | Tạo nhóm (khóa khi active) |
| `PATCH /criteria-groups/:id` / `DELETE /criteria-groups/:id` | admin | Sửa/xóa nhóm (xóa chỉ khi chưa có điểm tham chiếu) |
| `POST /criteria-groups/:id/criteria` | admin | Tạo tiêu chí với `input_type`, `min_score`, `max_score`, `step`, `weight`, `is_admin_only`, `comment_mode` |
| `PATCH /criteria/:id` / `DELETE /criteria/:id` | admin | Sửa/xóa tiêu chí (khóa khi active; validate theo DATABASE Mục 4) |
| `POST /criteria/:id/options` | admin | Thêm mức cho tiêu chí `discrete` |
| `PATCH /criteria-options/:id` / `DELETE /criteria-options/:id` | admin | Sửa/xóa mức |
| `GET /contests/:id/assignments` | admin | Ma trận đầy đủ đội × giám khảo |
| `PUT /contests/:id/assignments` | admin | Lưu ma trận: body là danh sách `{performance_id, judge_id}` được gán (thay thế toàn bộ trong phạm vi contest; khóa khi active). Có thao tác nhanh phía client (All-to-All, theo round) — server chỉ nhận kết quả cuối |
| `GET /judges/me/assignments` | judge | Các performance mình được phân công + trạng thái phiếu của mình (nền của Progress Tracker) |

### 6.6. Flow control & Timer (Module 4 + 6)

| Method & path | Quyền | Mô tả |
|---|---|---|
| `POST /performances/:id/start` | admin | `pending → performing`; tạo `timer_session`, phát `performance.started` + bắt đầu `timer.sync`. `422` nếu vi phạm quy tắc một performance đang diễn ra (PRD Mục 4.2) |
| `POST /performances/:id/pause` | admin | Tạm dừng timer (`timer_status: running → paused`) |
| `POST /performances/:id/resume` | admin | Tiếp tục timer |
| `POST /performances/:id/end` | admin | `performing → scoring`; chốt `overtime_sec`; nếu bật luật trừ, tính `penalty_proposed` và đặt `penalty_status='proposed'` |
| `POST /performances/:id/approve-penalty` | admin | Duyệt trừ giờ; body có thể kèm `penalty_applied` (sửa mức) + `reason` khi sửa/từ chối kèm trong endpoint dưới; ghi detail vào tiêu chí trừ giờ và audit |
| `POST /performances/:id/reject-penalty` | admin | Không áp trừ; body bắt buộc `reason`; `penalty_status='rejected'` |
| `POST /performances/:id/lock` | admin | `scoring → locked`; nếu còn giám khảo chưa nộp, body bắt buộc `reason` (xác nhận khóa thiếu); khóa mọi phiếu thuộc performance; tạo auto-backup |
| `POST /performances/:id/unlock` | admin | Ngoại lệ: `locked → scoring`; bắt buộc `reason`; ghi audit |
| `POST /performances/:id/reveal` | admin | `locked → revealed`; phát `performance.revealed` kèm điểm cuối |
| `POST /performances/:id/unreveal` | admin | Thu hồi công bố: `revealed → locked`; ghi audit |
| `POST /rounds/:id/leaderboard-reveal` | admin | Bật/tắt `leaderboard_revealed` của round; body `revealed: true/false` |

### 6.7. Scores (Module 5 + Override)

| Method & path | Quyền | Mô tả |
|---|---|---|
| `GET /performances/:id/my-score` | judge | Phiếu của chính mình cho performance (kèm details); chưa có thì trả cấu trúc rỗng để client tạo nháp với `client_uuid` mới |
| `PUT /scores/by-client-uuid/:client_uuid` | judge (có assignment) / admin (phiếu Admin-Only) | Upsert nháp hoặc nộp theo Mục 4: body `{performance_id, base_version, details: [{criterion_id, raw_score, option_id?, comment?}], comment?, submit?: boolean}` |
| `POST /scores/:id/submit` | judge (chủ phiếu) | Nộp một phiếu đã tồn tại trên server; idempotent (Mục 4.3) |
| `PATCH /scores/:id` | judge (chủ phiếu) | Sửa phiếu của mình: chỉ khi `draft`, hoặc `submitted` + contest cho phép sửa sau nộp + performance chưa khóa; sửa bản `submitted` bắt buộc `reason` trong body, ghi audit từng tiêu chí đổi |
| `GET /performances/:id/scores` | admin | Mọi phiếu của một performance (cho Live Matrix chi tiết) |
| `POST /scores/:id/override` | admin | Override: body `{details: [{criterion_id, raw_score}], reason}` — `reason` bắt buộc; lưu giá trị cũ vào audit theo từng tiêu chí; tăng `version` |
| `PUT /performances/:id/admin-sheet` | admin | Nhập/cập nhật phiếu Admin-Only của performance (phạt/bonus thủ công); ghi chú (`comment`) bắt buộc; ghi audit khi sửa giá trị đã có |
| `GET /judges/me/progress` | judge | Tiến độ cá nhân: tổng số đội được phân công, số đã nộp, danh sách theo trạng thái (nộp/dở/chưa) |

Lỗi đặc thù nhóm này: `403 NOT_ASSIGNED` (không có phân công), `409 SCORE_LOCKED` (performance/phiếu đã khóa), `409 SCORE_VERSION_CONFLICT`, `422` khi điểm ngoài khoảng/lệch step/thiếu tiêu chí bắt buộc (details liệt kê từng `criterion_id` lỗi).

### 6.8. Dashboard, Leaderboard, Audit (Module 6)

| Method & path | Quyền | Mô tả |
|---|---|---|
| `GET /contests/:id/live-matrix` | admin | Ma trận đội × giám khảo với trạng thái từng ô + trạng thái kết nối |
| `GET /rounds/:id/leaderboard` | admin: đầy đủ; judge: thứ hạng đội (điểm chỉ của đội đã khóa trở đi theo chính sách hiển thị nội bộ — mặc định judge thấy điểm cuối của đội đã `locked`); mc: chỉ khi `leaderboard_revealed=1` và chỉ đội đã `revealed` | Bảng xếp hạng của round: điểm cuối, điểm giám khảo trung bình, khoản Admin-Only, cờ đồng điểm, tiêu chí tie-break |
| `POST /rounds/:id/tie-break` | admin | Chốt thủ công thứ hạng cho nhóm đồng điểm: body `{ordering: [team_id...], reason}`; ghi audit |
| `GET /contests/:id/audit-logs` | admin | Audit log phân trang, lọc `?actor_id=&entity_type=&entity_id=&from=&to=` |
| `GET /contests/:id/results` | admin | Kết quả tổng hợp theo round (dữ liệu nền cho export và leaderboard): áp dụng đúng công thức PRD Mục 6.6.5, kèm ghi chú khi không áp bỏ cao/thấp nhất |

### 6.9. Public View (Module 7)

| Method & path | Quyền | Mô tả |
|---|---|---|
| `GET /contests/:id/public-state` | mc / mọi role đã đăng nhập | Trạng thái cho màn hình khán giả: contest, round hiện tại, đội đang thi, timer, và **chỉ** các điểm đã `revealed` + bảng xếp hạng khi `leaderboard_revealed=1`. Endpoint này là nơi duy nhất role `mc` lấy dữ liệu hiển thị; server lọc reveal ở đây bất kể client yêu cầu gì thêm |

### 6.10. Backup & Restore (Mục 11 PRD)

| Method & path | Quyền | Mô tả |
|---|---|---|
| `GET /backups` | admin | Danh sách snapshot (theo bảng `backups`) |
| `POST /backups` | admin | Tạo snapshot mới bằng `VACUUM INTO`; body tùy chọn `contest_id`; trả thông tin file |
| `GET /backups/:id/download` | admin | Tải file snapshot |
| `POST /backups/:id/restore` | admin | Restore từ snapshot: server tự tạo snapshot `pre_restore` trước, yêu cầu xác nhận trong body (theo PRD Mục 11.2), `409 BACKUP_RESTORE_BLOCKED` khi đang có performance `performing`; ghi audit; sau restore phát `contest.state` cho mọi client |
| `POST /backups/upload-restore` | admin | Tải lên một file backup rồi restore theo cùng quy trình |

## 7. Phiên bản API

- Tiền tố `/api/v1` cố định cho MVP. Thay đổi phá vỡ (đổi tên trường, đổi mã lỗi, đổi payload WebSocket) phải lên `/api/v2` và ghi chú trong PRD.
- Thêm trường mới trong response được phép ở v1 (client bỏ qua trường lạ); xóa/đổi nghĩa trường thì không.
- Payload WebSocket tuân thủ cùng nguyên tắc: thêm `type` mới được, đổi payload của `type` cũ thì không.
