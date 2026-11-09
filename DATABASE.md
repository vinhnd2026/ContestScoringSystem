# DATABASE — Contest Scoring System

Schema SQLite cho PRD v2 (xem [PRD.md](PRD.md)). Tài liệu này là chuẩn khi tạo migration: tên bảng, tên cột, khóa và ràng buộc không được tự ý đổi; nếu cần đổi phải sửa đồng thời PRD.md và API.md.

## 1. Nguyên tắc chung

- **Engine:** SQLite 3, một file `contest.db` cạnh binary.
- **WAL mode bắt buộc:** ngay khi mở kết nối, chạy:
  ```sql
  PRAGMA journal_mode = WAL;
  PRAGMA foreign_keys = ON;
  PRAGMA busy_timeout = 5000;
  ```
  WAL cho phép nhiều reader đồng thời với 1 writer. Tầng ứng dụng giới hạn pool ghi ở 1 kết nối (`maxOpenConns = 1` cho các thao tác ghi) để tránh `database is locked`; đọc có thể dùng pool riêng.
- **Khóa chính:** mọi bảng dùng `id INTEGER PRIMARY KEY AUTOINCREMENT`, trừ bảng liên kết `assignments` dùng khóa chính ghép gồm nhiều cột (xem chi tiết tại bảng đó).
- **Thời gian:** mọi cột thời gian lưu TEXT theo ISO 8601 UTC do **server** ghi (ví dụ `2026-11-09T07:30:05Z`), tên cột hậu tố `_at`. Không nhận thời gian từ client cho các mốc nghiệp vụ.
- **Tiền/điểm:** điểm lưu `REAL`; các cột cấu hình điểm (max, step, weight, penalty) cũng `REAL`. Làm tròn chỉ ở tầng hiển thị/tính điểm cuối theo PRD Mục 6.6.5, không làm tròn khi lưu.
- **Song ngữ:** nội dung BTC nhập có cặp cột `*_vi` / `*_en`; cột `*_vi` là bắt buộc (NOT NULL đối với tên), cột `*_en` cho phép NULL/ rỗng và fallback theo PRD Mục 3.3.
- **Xóa mềm:** các thực thể đã phát sinh dữ liệu nghiệp vụ (users, teams, contests) không xóa cứng; dùng `is_active` / trạng thái. Xóa cứng chỉ áp cho dữ liệu cấu hình còn ở trạng thái nháp và chưa có điểm tham chiếu.
- **Migration:** schema có bảng `settings` giữ `schema_version`; mọi thay đổi schema đi qua migration có số thứ tự, không sửa tay file db.

## 2. Danh sách bảng

| # | Bảng | Vai trò |
|---|---|---|
| 1 | `users` | Tài khoản Admin / Giám khảo / MC |
| 2 | `contests` | Cuộc thi và cấu hình cấp contest |
| 3 | `rounds` | Vòng thi / hạng mục thuộc contest |
| 4 | `teams` | Đội thi thuộc round |
| 5 | `criteria_groups` | Nhóm tiêu chí thuộc contest |
| 6 | `criteria` | Tiêu chí chấm thuộc nhóm |
| 7 | `criteria_options` | Các mức điểm định sẵn cho tiêu chí `discrete` |
| 8 | `assignments` | Phân công giám khảo ↔ performance |
| 9 | `performances` | Một lượt thi của một đội trong một round (đơn vị điều khiển luồng + bấm giờ) |
| 10 | `scores` | Phiếu điểm của một giám khảo cho một performance (hoặc phiếu khoản Admin-Only cấp performance, xem Mục 3.10) |
| 11 | `score_details` | Điểm từng tiêu chí trong một phiếu |
| 12 | `timer_sessions` | Phiên bấm giờ của một performance |
| 13 | `audit_logs` | Vết thao tác bất biến (append-only) |
| 14 | `backups` | Danh mục các snapshot backup đã tạo |
| 15 | `settings` | Cấu hình key–value toàn hệ thống |

Quan hệ chính:

```
users ──< assignments >── performances ──1:1── teams ──> rounds ──> contests
contests ──< criteria_groups ──< criteria ──< criteria_options
performances ──< scores ──< score_details >── criteria
performances ──< timer_sessions
contests ──< backups
* ──< audit_logs (tham chiếu mềm bằng entity_type + entity_id)
```

## 3. Chi tiết từng bảng

### 3.1. `users`

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `username` | TEXT | NOT NULL, UNIQUE — tên đăng nhập |
| `display_name` | TEXT | NOT NULL — tên hiển thị (ví dụ tên giám khảo) |
| `role` | TEXT | NOT NULL, CHECK `role IN ('admin','judge','mc')` |
| `password_hash` | TEXT | Nullable — băm bcrypt/argon2id; NULL nếu tài khoản chỉ dùng PIN |
| `pin_hash` | TEXT | Nullable — băm của PIN, cùng thuật toán như mật khẩu; ít nhất một trong hai hash phải có |
| `allow_pin_login` | INTEGER (0/1) | NOT NULL DEFAULT 1 với role `judge`, 0 với role khác |
| `failed_login_count` | INTEGER | NOT NULL DEFAULT 0 — đếm đăng nhập sai liên tiếp |
| `locked_until` | TEXT | Nullable — mốc hết khóa đăng nhập (PRD Mục 13) |
| `is_active` | INTEGER (0/1) | NOT NULL DEFAULT 1 |
| `created_at`, `updated_at` | TEXT | Server ghi |

Index: `UNIQUE(username)`; `INDEX(role, is_active)`.

### 3.2. `contests`

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `name_vi`, `name_en` | TEXT | `name_vi` NOT NULL |
| `desc_vi`, `desc_en` | TEXT | Nullable |
| `location` | TEXT | Nullable — địa điểm |
| `starts_at`, `ends_at` | TEXT | Nullable — thời gian tổ chức dự kiến (thông tin hiển thị, không điều khiển state) |
| `status` | TEXT | NOT NULL DEFAULT `'pending'`, CHECK `IN ('pending','active','completed')` |
| `is_finalized` | INTEGER (0/1) | NOT NULL DEFAULT 0 — chốt sổ; chỉ có nghĩa khi `status='completed'` |
| `finalized_at`, `finalized_by` | TEXT / INTEGER FK→users | Nullable |
| `join_code` | TEXT | NOT NULL, UNIQUE — mã tham gia dùng trong QR code (PRD Mục 12) |
| `allow_judge_edit_after_submit` | INTEGER (0/1) | NOT NULL DEFAULT 0 |
| `drop_highest_lowest` | INTEGER (0/1) | NOT NULL DEFAULT 0 |
| `min_judges_for_trim` | INTEGER | NOT NULL DEFAULT 5, CHECK `>= 4` — ngưỡng tối thiểu số phiếu để áp bỏ cao/thấp nhất |
| `tie_breaker_criterion_id` | INTEGER FK→criteria | Nullable — tiêu chí ưu tiên xử lý đồng điểm |
| `require_total_match` | INTEGER (0/1) | NOT NULL DEFAULT 0 — bật thì checklist kích hoạt chặn khi tổng điểm tính được khác tổng mục tiêu |
| `target_total_score` | REAL | Nullable — tổng điểm mục tiêu Admin khai báo |
| `standard_duration_sec` | INTEGER | NOT NULL — thời lượng chuẩn một phần thi (giây) |
| `warning_before_sec` | INTEGER | NOT NULL DEFAULT 60 — mốc cảnh báo còn bao nhiêu giây |
| `penalty_enabled` | INTEGER (0/1) | NOT NULL DEFAULT 0 |
| `penalty_interval_sec` | INTEGER | Điều kiện: > 0 khi `penalty_enabled=1` |
| `penalty_first_points` | REAL | Điểm trừ khoảng lố đầu tiên |
| `penalty_next_points` | REAL | Điểm trừ mỗi khoảng lố tiếp theo |
| `penalty_max_points` | REAL | Trần trừ điểm cho một performance; > 0 và ≤ tổng điểm tối đa khi penalty bật |
| `allow_parallel_rounds` | INTEGER (0/1) | NOT NULL DEFAULT 0 |
| `created_at`, `updated_at` | TEXT | |

Index: `UNIQUE(join_code)`; `INDEX(status)`.

Ghi chú: `total_max_score` **không** lưu cứng trong bảng này — nó được tính từ tiêu chí theo công thức PRD Mục 6.3.4 mỗi khi cần hiển thị/kiểm tra, để tránh lệch với cấu hình tiêu chí.

### 3.3. `rounds`

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `contest_id` | INTEGER FK→contests | NOT NULL, ON DELETE CASCADE (chỉ xóa được khi contest chưa có điểm — tầng ứng dụng kiểm tra) |
| `name_vi`, `name_en` | TEXT | `name_vi` NOT NULL |
| `sort_order` | INTEGER | NOT NULL DEFAULT 0 |
| `standard_duration_sec` | INTEGER | Nullable — ghi đè thời lượng chuẩn của contest khi có giá trị |
| `leaderboard_revealed` | INTEGER (0/1) | NOT NULL DEFAULT 0 — cờ reveal bảng xếp hạng của round ở Public View |
| `created_at`, `updated_at` | TEXT | |

Index: `INDEX(contest_id, sort_order)`.

### 3.4. `teams`

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `round_id` | INTEGER FK→rounds | NOT NULL |
| `code` | TEXT | NOT NULL — SBD; UNIQUE trong cùng contest: thực thi bằng index duy nhất trên `(round_id, code)` cộng kiểm tra tầng ứng dụng theo contest (vì round thuộc contest) — chốt: UNIQUE trên `(round_id, code)` và quy ước SBD không trùng trong toàn contest do API kiểm tra khi tạo/import |
| `name_vi`, `name_en` | TEXT | `name_vi` NOT NULL |
| `organization` | TEXT | Nullable — đơn vị/trường |
| `members_note` | TEXT | Nullable — thành viên/ghi chú |
| `sort_order` | INTEGER | NOT NULL DEFAULT 0 — thứ tự thi |
| `is_active` | INTEGER (0/1) | NOT NULL DEFAULT 1 |
| `created_at`, `updated_at` | TEXT | |

Index: `UNIQUE(round_id, code)`; `INDEX(round_id, sort_order)`.

### 3.5. `criteria_groups`

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `contest_id` | INTEGER FK→contests | NOT NULL |
| `name_vi`, `name_en` | TEXT | `name_vi` NOT NULL |
| `sort_order` | INTEGER | NOT NULL DEFAULT 0 |
| `created_at`, `updated_at` | TEXT | |

Index: `INDEX(contest_id, sort_order)`.

### 3.6. `criteria`

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `group_id` | INTEGER FK→criteria_groups | NOT NULL |
| `name_vi`, `name_en` | TEXT | `name_vi` NOT NULL |
| `desc_vi`, `desc_en` | TEXT | Nullable — hướng dẫn chấm |
| `input_type` | TEXT | NOT NULL, CHECK `IN ('range','discrete')` |
| `min_score` | REAL | NOT NULL DEFAULT 0, CHECK `>= 0` |
| `max_score` | REAL | NOT NULL, CHECK `> min_score` |
| `step` | REAL | NOT NULL DEFAULT 1 — chỉ áp cho `range`; CHECK `> 0` |
| `weight` | REAL | NOT NULL DEFAULT 1, CHECK `> 0` — đây là "hệ số nhân" của v1, đã chuyển thành thuộc tính |
| `is_admin_only` | INTEGER (0/1) | NOT NULL DEFAULT 0 |
| `is_penalty_criterion` | INTEGER (0/1) | NOT NULL DEFAULT 0 — đánh dấu tiêu chí Admin-Only dành riêng cho trừ giờ do hệ thống tạo (tối đa 1 dòng như vậy trên mỗi contest) |
| `comment_mode` | TEXT | NOT NULL DEFAULT `'optional'`, CHECK `IN ('hidden','optional','required')` |
| `sort_order` | INTEGER | NOT NULL DEFAULT 0 |
| `created_at`, `updated_at` | TEXT | |

Index: `INDEX(group_id, sort_order)`.

### 3.7. `criteria_options` (mức điểm của tiêu chí discrete)

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `criterion_id` | INTEGER FK→criteria | NOT NULL |
| `label_vi`, `label_en` | TEXT | `label_vi` NOT NULL |
| `score_value` | REAL | NOT NULL — phải nằm trong `[min_score, max_score]` của tiêu chí cha (API kiểm tra); UNIQUE trên `(criterion_id, score_value)` |
| `sort_order` | INTEGER | NOT NULL DEFAULT 0 |

Index: `INDEX(criterion_id, sort_order)`; `UNIQUE(criterion_id, score_value)`.

### 3.8. `assignments` (phân công giám khảo ↔ performance)

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `performance_id` | INTEGER FK→performances | Khóa chính ghép |
| `judge_id` | INTEGER FK→users | Khóa chính ghép; user phải có `role='judge'` (API kiểm tra) |
| `created_at` | TEXT | |

Khóa chính: `PRIMARY KEY (performance_id, judge_id)`. Index thêm: `INDEX(judge_id)`.

Ghi chú thiết kế: phân công theo performance (không theo team) vì performance là đơn vị chấm thực tế; khi tạo performance cho team, hệ thống sinh assignment từ mẫu phân công của round/team theo thao tác của Admin trên ma trận (API.md Mục 6.5). Mọi performance thuộc cùng một đội dùng cùng tập giám khảo trừ khi Admin sửa riêng.

### 3.9. `performances`

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `team_id` | INTEGER FK→teams | NOT NULL, UNIQUE — MVP: một team có đúng một performance trong round của mình |
| `status` | TEXT | NOT NULL DEFAULT `'pending'`, CHECK `IN ('pending','performing','scoring','locked','revealed')` — đúng state machine PRD Mục 4.2 |
| `timer_status` | TEXT | NOT NULL DEFAULT `'idle'`, CHECK `IN ('idle','running','paused','finished','overtime')` — `overtime` dùng khi timer vẫn chạy/dừng sau khi hết giờ chuẩn nhưng performance chưa kết thúc |
| `started_at`, `ended_at` | TEXT | Nullable — mốc server bắt đầu/kết thúc phần thi |
| `overtime_sec` | INTEGER | NOT NULL DEFAULT 0 — thời gian lố đã chốt khi kết thúc |
| `penalty_status` | TEXT | NOT NULL DEFAULT `'none'`, CHECK `IN ('none','proposed','approved','rejected')` |
| `penalty_proposed` | REAL | Nullable — mức trừ hệ thống tính theo luật (PRD Mục 6.4.3) |
| `penalty_applied` | REAL | Nullable — mức trừ Admin duyệt (có thể khác mức đề xuất) |
| `penalty_reason` | TEXT | Nullable — lý do khi Admin sửa mức trừ hoặc từ chối |
| `sort_order` | INTEGER | NOT NULL DEFAULT 0 — thứ tự điều khiển luồng, mặc định lấy theo `teams.sort_order` |
| `created_at`, `updated_at` | TEXT | |

Index: `UNIQUE(team_id)`; `INDEX(status)`.

### 3.10. `scores` (phiếu điểm)

Một dòng = một phiếu của một giám khảo cho một performance. Các khoản Admin-Only cấp performance (điểm phạt/bonus/trừ giờ) lưu thành phiếu đặc biệt: `judge_id = NULL` và `is_admin_sheet = 1`, do Admin nhập — để dùng chung cơ chế score_details và tính điểm, nhưng khi tính trung bình giám khảo phải loại các phiếu này ra (chúng cộng một lần vào điểm đội, PRD Mục 6.6.5).

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `performance_id` | INTEGER FK→performances | NOT NULL |
| `judge_id` | INTEGER FK→users | Nullable — NULL chỉ khi `is_admin_sheet = 1` |
| `is_admin_sheet` | INTEGER (0/1) | NOT NULL DEFAULT 0 |
| `client_uuid` | TEXT | NOT NULL, **UNIQUE** — UUID do client của giám khảo tạo từ lần lưu nháp đầu tiên; với phiếu Admin-Only do server tạo. Đây là khóa chống nộp trùng (idempotency) |
| `status` | TEXT | NOT NULL DEFAULT `'draft'`, CHECK `IN ('draft','submitted','locked')` |
| `version` | INTEGER | NOT NULL DEFAULT 1 — tăng 1 mỗi lần nội dung được server chấp nhận thay đổi; nền của luật xung đột (PRD Mục 10.3) |
| `comment` | TEXT | Nullable — ghi chú chung của phiếu |
| `submitted_at` | TEXT | Nullable — mốc nộp chính thức gần nhất |
| `created_at`, `updated_at` | TEXT | |

Ràng buộc duy nhất theo nghiệp vụ: một giám khảo chỉ có một phiếu thường cho một performance — `UNIQUE(performance_id, judge_id)` áp dụng khi `judge_id IS NOT NULL` (SQLite: tạo unique index có điều kiện `WHERE judge_id IS NOT NULL`), và tối đa một phiếu Admin-Only mỗi performance — unique index có điều kiện trên `(performance_id) WHERE is_admin_sheet = 1`.

### 3.11. `score_details`

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `score_id` | INTEGER FK→scores | NOT NULL, ON DELETE CASCADE |
| `criterion_id` | INTEGER FK→criteria | NOT NULL |
| `raw_score` | REAL | NOT NULL — điểm thô chưa nhân trọng số; với `discrete`, bằng `score_value` của lựa chọn đã chọn |
| `option_id` | INTEGER FK→criteria_options | Nullable — bắt buộc có khi tiêu chí là `discrete`, để truy vết mức đã chọn |
| `comment` | TEXT | Nullable — ghi chú theo tiêu chí |
| `created_at`, `updated_at` | TEXT | |

Ràng buộc: `UNIQUE(score_id, criterion_id)` — một tiêu chí chỉ có một dòng điểm trong một phiếu. Điểm có trọng số (`raw_score × weight`) **không lưu cứng**, tính khi đọc/tính điểm để tránh lệch khi đối chiếu; nếu cần tối ưu ở Phase 2 mới thêm cột cache kèm cơ chế vô hiệu hóa.

### 3.12. `timer_sessions`

Một performance có thể có nhiều phiên bấm giờ nếu bị đặt lại trước khi thi (chỉ khi còn `pending`); phiên đang hiệu lực là phiên chưa có `ended_at` hoặc phiên mới nhất — tầng ứng dụng đảm bảo tại một thời điểm chỉ có một phiên chưa kết thúc cho một performance.

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `performance_id` | INTEGER FK→performances | NOT NULL |
| `standard_duration_sec` | INTEGER | NOT NULL — thời lượng chuẩn đã chốt tại thời điểm bắt đầu (lấy từ round hoặc contest) |
| `started_at` | TEXT | NOT NULL — giờ server |
| `paused_total_sec` | INTEGER | NOT NULL DEFAULT 0 — tổng thời gian đã tạm dừng |
| `paused_at` | TEXT | Nullable — mốc bắt đầu lần tạm dừng hiện tại (NULL = đang chạy) |
| `ended_at` | TEXT | Nullable — giờ server khi kết thúc |
| `overtime_sec` | INTEGER | NOT NULL DEFAULT 0 — thời gian lố tại thời điểm kết thúc |
| `created_at`, `updated_at` | TEXT | |

Index: `INDEX(performance_id)`.

Công thức kiểm tra: `thời gian thi thực = (ended_at − started_at) − paused_total_sec`; `overtime_sec = max(0, thời gian thi thực − standard_duration_sec)` khi đã kết thúc. Trong lúc đang chạy, server tính các giá trị này động để phát sự kiện `timer.sync` (API.md).

### 3.13. `audit_logs` (bất biến)

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | Chỉ tăng, không tái sử dụng |
| `actor_id` | INTEGER FK→users | NOT NULL — người thực hiện |
| `action` | TEXT | NOT NULL — mã hành động, ví dụ: `score.edit_after_submit`, `score.override`, `performance.lock`, `performance.unlock`, `penalty.approve`, `penalty.reject`, `contest.activate`, `contest.finalize`, `contest.unfinalize`, `contest.reset_scores`, `user.reset_pin`, `assignment.shuffle_order`, `backup.create`, `backup.restore` |
| `entity_type` | TEXT | NOT NULL — ví dụ `score`, `score_detail`, `performance`, `contest`, `user` |
| `entity_id` | INTEGER | NOT NULL — id của thực thể bị tác động |
| `criterion_id` | INTEGER FK→criteria | Nullable — có khi hành động liên quan một tiêu chí |
| `old_value` | TEXT | Nullable — giá trị trước (với thay đổi điểm: điểm cũ dạng text của số) |
| `new_value` | TEXT | Nullable — giá trị sau |
| `reason` | TEXT | Nullable ở schema, nhưng **bắt buộc ở tầng API** cho các hành động sửa điểm/override/mở khóa/chốt sổ/restore |
| `created_at` | TEXT | NOT NULL — giờ server |

Bất biến: ứng dụng không cung cấp UPDATE/DELETE trên bảng này; chỉ INSERT và SELECT. Index: `INDEX(entity_type, entity_id)`, `INDEX(actor_id, created_at)`, `INDEX(created_at)`.

### 3.14. `backups`

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `id` | INTEGER PK | |
| `contest_id` | INTEGER FK→contests | Nullable — NULL nếu là snapshot toàn hệ thống trước khi restore |
| `file_name` | TEXT | NOT NULL — tên file trong thư mục `backups/` |
| `file_size` | INTEGER | NOT NULL — byte |
| `kind` | TEXT | NOT NULL, CHECK `IN ('manual','auto_lock','auto_completed','pre_restore')` |
| `created_by` | INTEGER FK→users | Nullable — NULL khi hệ thống tự tạo (auto) thì ghi Admin đang vận hành nếu xác định được, không thì NULL |
| `created_at` | TEXT | NOT NULL |

File backup được tạo bằng `VACUUM INTO '<backups>/<file_name>'` từ kết nối server — không copy file `contest.db` đang sống (PRD Mục 11).

### 3.15. `settings`

| Cột | Kiểu | Ràng buộc / ghi chú |
|---|---|---|
| `key` | TEXT PK | Ví dụ: `schema_version`, `login_max_failed`, `login_lock_minutes`, `session_ttl_hours`, `server_port` |
| `value` | TEXT | NOT NULL — lưu dạng text; tầng ứng dụng ép kiểu |
| `updated_at` | TEXT | |

Giá trị mặc định khi khởi tạo: `schema_version = 1`, `login_max_failed = 5`, `login_lock_minutes = 15`, `session_ttl_hours = 12`, `server_port = 8080` — khớp PRD Mục 12–13.

## 4. Quy tắc toàn vẹn xuyên bảng (thực thi ở tầng ứng dụng + kiểm tra khi kích hoạt)

1. **Tổng trọng số và tổng điểm:** `total_max_score = Σ(criteria.max_score × criteria.weight)` trên các tiêu chí có `is_admin_only = 0` thuộc contest. Hiển thị trực tiếp ở màn hình cấu hình; nếu `contests.require_total_match = 1` thì checklist kích hoạt yêu cầu `total_max_score = contests.target_total_score`.
2. **Trạng thái hợp lệ:** mọi chuyển trạng thái của `contests.status`, `performances.status`, `scores.status` phải theo đúng PRD Mục 4; tầng ứng dụng từ chối chuyển nhảy cóc/quay lui ngoài hai ngoại lệ có audit (mở khóa performance, mở chốt sổ).
3. **Khóa cấu hình:** khi `contests.status = 'active'`, từ chối INSERT/UPDATE/DELETE trên `criteria_groups`, `criteria`, `criteria_options`, `assignments` thuộc contest đó và từ chối sửa các cột luật trừ điểm của contest.
4. **Điểm hợp lệ:** `score_details.raw_score` phải nằm trong `[criteria.min_score, criteria.max_score]`; với `range` phải khớp `step` (sai số thập phân cho phép 1e-9); với `discrete` phải tồn tại `option_id` thuộc đúng tiêu chí và `raw_score = criteria_options.score_value`.
5. **Phân công và nộp điểm:** chỉ tạo/cập nhật `scores` (phiếu thường) khi tồn tại `assignments(performance_id, judge_id)`; phiếu của performance đã `locked`/`revealed` không sửa được trừ đường override của Admin (ghi audit).
6. **Admin-Only:** `score_details` thuộc tiêu chí `is_admin_only = 1` chỉ được nằm trong phiếu `is_admin_sheet = 1`; API của judge không đọc/ghi các dòng này.
7. **Trừ giờ:** `performances.penalty_applied` chỉ có giá trị khi `penalty_status = 'approved'`, và khi đó tồn tại đúng một score detail của tiêu chí `is_penalty_criterion = 1` trong phiếu Admin-Only với `raw_score = −penalty_applied`.
8. **Tính điểm nhất quán:** phiếu Admin-Only (`is_admin_sheet = 1`) không tham gia trung bình giám khảo; khoản của nó cộng một lần vào điểm đội theo PRD Mục 6.6.5.
9. **Session** (đăng nhập) không lưu trong schema nghiệp vụ trên: dùng bảng phụ `sessions(token_hash TEXT PK, user_id FK, expires_at, created_at)` do tầng bảo mật quản lý; token rõ không lưu, chỉ lưu băm. Bảng này là bảng kỹ thuật, không chứa dữ liệu nghiệp vụ.

## 5. Ghi chú về versioning và đồng bộ offline

- `scores.version` là số phiên bản của **nội dung phiếu**; client gửi `base_version` khi cập nhật, server so khớp trước khi ghi (optimistic locking). Tăng version trong cùng transaction với thay đổi `score_details`.
- `scores.client_uuid` UNIQUE là điểm tựa idempotency: retry cùng `client_uuid` trả về chính phiếu đã tồn tại, không tạo mới (API.md Mục 4).
- Xung đột draft/submitted xử lý theo PRD Mục 10.3 — dữ liệu cần cho luật đó nằm ở `scores.status` + `scores.version` + `scores.updated_at`, không cần thêm bảng riêng ở MVP. Log đồng bộ chi tiết (nếu cần debug) ghi ra log vận hành của server, không ghi vào `audit_logs` trừ khi xung đột liên quan bản đã nộp.
