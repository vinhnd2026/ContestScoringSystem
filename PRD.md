# PRD v2 — Contest Scoring System (Hệ thống chấm điểm cuộc thi)

| Hạng mục | Giá trị |
|---|---|
| Phiên bản tài liệu | v2.0 — bản hoàn chỉnh, thay thế bản ý tưởng v1 |
| Repo dự kiến | `vinhnd2026/ContestScoringSystem` (chưa tạo — tạo ở bước triển khai) |
| Ngôn ngữ tài liệu | Tiếng Việt; thuật ngữ kỹ thuật và tên trường dữ liệu giữ tiếng Anh |
| Đối tượng đọc | Chủ dự án, lập trình viên backend (Go), lập trình viên frontend (SPA), người kiểm thử |
| Trạng thái | Đã chốt thiết kế ở mức dev cầm là code được; các giá trị mặc định có ghi rõ "mặc định" và đều cấu hình được |

Tài liệu liên quan: [DATABASE.md](DATABASE.md) — schema SQLite đầy đủ; [API.md](API.md) — REST + WebSocket; [README.md](README.md) — mục lục repo.

---

## 1. Tổng quan

### 1.1. Bài toán

Các cuộc thi tại hiện trường (thi diễn, thi trình bày, thi tài năng, thi học thuật…) cần chấm điểm trực tiếp bởi nhiều giám khảo, công bố kết quả ngay trong sự kiện, và phải chạy được trong điều kiện hội trường không có internet ổn định — nhiều khi không có internet. Hệ thống đám mây phụ thuộc mạng ngoài là rủi ro lớn nhất: mất mạng giữa phần thi là mất khả năng chấm và công bố.

Contest Scoring System giải quyết bài toán đó bằng kiến trúc **offline-first trên mạng LAN**: một máy của Ban tổ chức (máy Admin) chạy toàn bộ hệ thống, các thiết bị của giám khảo và màn hình khán giả kết nối vào cùng mạng nội bộ, chấm điểm đồng bộ theo thời gian thực, và vẫn chấm tiếp được khi thiết bị của giám khảo tạm mất kết nối với máy chủ nội bộ.

### 1.2. Mục tiêu

1. Chấm điểm realtime tại hiện trường với độ trễ hiển thị trạng thái dưới 1 giây trong mạng LAN.
2. Không phụ thuộc internet ở bất kỳ khâu vận hành nào trong ngày thi.
3. Triển khai gọn: backend là **1 file binary duy nhất**, không cần cài đặt runtime, database server hay dịch vụ phụ trợ trên máy Admin.
4. Minh bạch và có thể kiểm chứng: mọi lần sửa điểm, ghi đè điểm đều có vết (audit trail) bất biến; kết quả xuất được ra Excel để lưu hồ sơ.
5. Linh hoạt cho nhiều loại cuộc thi: tiêu chí chấm định nghĩa được (thang khoảng, mức độ định sẵn, trọng số, điểm phạt/bonus của Ban tổ chức), nhiều vòng thi và hạng mục, phân công giám khảo theo đội.
6. Song ngữ Việt–Anh ở dữ liệu hiển thị: người dùng chuyển ngôn ngữ trên thiết bị của mình và thấy nội dung ngay, không reload, không query lại.

### 1.3. Nguyên tắc thiết kế (bắt buộc tuân thủ khi code)

1. **Server là nguồn sự thật duy nhất.** Mọi trạng thái chính thức (trạng thái cuộc thi, trạng thái phần thi, đồng hồ, điểm đã nộp) nằm ở server. Client chỉ giữ nháp.
2. **Giờ server làm chuẩn.** Mọi mốc thời gian thi và đếm giờ tính theo đồng hồ của server, không theo đồng hồ thiết bị của giám khảo/MC.
3. **Không mất điểm vì mất mạng.** Nháp điểm lưu ở IndexedDB trên thiết bị giám khảo; mất kết nối không mất nháp, có kết nối lại thì tự đồng bộ theo luật xung đột ở Mục 10.
4. **Không sửa cấu hình giữa cuộc thi.** Cấu hình chấm (tiêu chí, phân công, luật trừ điểm) bị khóa khi cuộc thi chuyển sang Active (xem Mục 6.3.5).
5. **Mọi ghi đè đều có lý do và có vết.** Sửa điểm sau khi nộp, Admin override, áp điểm phạt thời gian, mở khóa chốt sổ — tất cả bắt buộc nhập lý do (trừ các thao tác có ghi chú riêng) và ghi vào audit log bất biến.
6. **Công bố là hành động có chủ đích.** Không có điểm nào tự lộ ra màn hình khán giả; công bố chỉ xảy ra khi Admin bật cờ reveal cho đúng phần thi/bảng xếp hạng (xem Mục 6.7).

### 1.4. Phạm vi

#### Trong phạm vi MVP (Phase 1)

- Quản lý cuộc thi, vòng thi/hạng mục, đội thi (CRUD + import Excel), nhân bản cấu hình, reset điểm, chế độ tổng duyệt (Dry Run).
- Quản lý tài khoản, 3 vai trò (Admin, Giám khảo, MC/Public View), reset mật khẩu/PIN tại hiện trường, ma trận phân công giám khảo–đội.
- Bộ máy tiêu chí: nhóm tiêu chí, tiêu chí nhập theo khoảng (Range) hoặc theo mức định sẵn (Discrete), trọng số cho mọi tiêu chí, tiêu chí Admin-Only (phạt/bonus).
- Điều khiển thời gian thi: đếm lùi, tạm dừng/tiếp tục, đếm lố giờ, cảnh báo mốc, đề xuất trừ điểm quá giờ chờ Admin duyệt.
- Không gian giám khảo: tự đồng bộ theo phần thi đang diễn ra, nháp theo từng đội, nộp/sửa điểm theo quyền cấu hình, theo dõi tiến độ cá nhân.
- Bảng điều hành Admin realtime: ma trận đội × giám khảo, điều khiển luồng thi, override điểm, audit trail, bảng xếp hạng, xử lý đồng điểm, xuất Excel.
- Màn hình MC/khán giả: đồng hồ, đội đang thi, và phần vinh danh/công bố khi được reveal.
- Offline cho giám khảo (IndexedDB), đồng bộ lại tự động, chống nộp trùng.
- Backup an toàn và restore; triển khai LAN bằng QR code.

#### Ngoài phạm vi (không làm ở Phase 1, xem Roadmap Phase 2 ở Mục 14)

- Chấm điểm qua internet / nhiều địa điểm thi đồng thời (multi-site).
- Ứng dụng di động native (iOS/Android) — MVP dùng web responsive/PWA trên trình duyệt.
- Thanh toán, bán vé, quản lý khán giả, livestream.
- Chấm bằng AI, nhận diện hình ảnh/giọng nói.
- Hơn 2 ngôn ngữ hiển thị (schema hiện tại chỉ gồm Việt và Anh — xem Mục 3.3).
- Tích hợp hệ thống ngoài (CRM, LMS, mạng xã hội).

### 1.5. Thuật ngữ dùng thống nhất trong cả 4 tài liệu

| Thuật ngữ | Nghĩa |
|---|---|
| Contest | Một cuộc thi (sự kiện chấm điểm) |
| Round | Một vòng thi hoặc hạng mục trong contest; mọi đội thuộc đúng 1 round |
| Team | Một đội/thí sinh dự thi, thuộc 1 round |
| Performance | Một lượt thi của 1 đội trong 1 round — đơn vị được điều khiển luồng, bấm giờ và chấm điểm. Một đội có đúng 1 performance trong round của mình ở MVP |
| Criteria group | Nhóm tiêu chí (ví dụ: Nội dung, Hình thức) |
| Criterion (tiêu chí) | Một tiêu chí chấm; có cách nhập điểm, điểm tối đa và trọng số |
| Score (phiếu điểm) | Bộ điểm của 1 giám khảo cho 1 performance, gồm nhiều score detail |
| Score detail | Điểm của 1 tiêu chí trong 1 phiếu điểm |
| Assignment | Phân công: giám khảo nào được chấm performance/đội nào |
| Reveal | Hành động Admin cho phép hiển thị điểm/xếp hạng ra Public View |
| Finalize (chốt sổ) | Khóa cứng kết quả của contest đã Completed, không ai sửa được nữa trừ khi Admin mở chốt có lý do |
| Dry Run | Chế độ/đợt tổng duyệt: dùng cấu hình thật, điểm tạo ra sẽ bị reset trước thi thật |

---

## 2. Người dùng và phân quyền

### 2.1. Vai trò

| Vai trò | Mã (`role`) | Mô tả |
|---|---|---|
| Admin / Trưởng ban | `admin` | Toàn quyền: cấu hình, điều khiển luồng thi, xem toàn bộ điểm, override điểm, backup/restore, chốt sổ |
| Giám khảo | `judge` | Chỉ xem và chấm các đội được phân công cho mình; không thấy điểm của giám khảo khác |
| MC / Màn hình khán giả | `mc` | Tài khoản hiển thị chỉ đọc (read-only): đồng hồ, đội đang thi, nội dung đã được reveal. Không có form chấm điểm, không thấy điểm chưa reveal |

Ghi chú: Public View dùng chung tài khoản `mc` hoặc một liên kết hiển thị chỉ đọc do Admin mở trên máy chiếu; trong mọi trường hợp Public View không có quyền ghi dữ liệu.

### 2.2. Ma trận quyền (tóm tắt — chi tiết thực thi ở API.md Mục 2)

| Hành động | Admin | Judge | MC |
|---|:---:|:---:|:---:|
| Tạo/sửa/xóa contest, round, team, tiêu chí | ✓ | — | — |
| Phân công giám khảo | ✓ | — | — |
| Bắt đầu/tạm dừng/kết thúc phần thi, khóa điểm, reveal | ✓ | — | — |
| Nhập điểm cho đội được phân công | ✓ (qua override) | ✓ | — |
| Xem điểm của giám khảo khác | ✓ | — | — |
| Xem điểm đã reveal | ✓ | ✓ | ✓ |
| Override điểm, mở khóa phiếu điểm | ✓ | — | — |
| Backup / restore | ✓ | — | — |
| Xuất Excel | ✓ | — | — |
| Đổi mật khẩu/PIN của chính mình | ✓ | ✓ | ✓ |
| Reset mật khẩu/PIN của người khác | ✓ | — | — |

### 2.3. Quy tắc phân quyền khi chấm

- Giám khảo chỉ mở được form chấm của performance mà mình có assignment.
- Performance không được phân công: thiết bị giám khảo chỉ hiện thông tin phần thi ở chế độ read-only, không render form nhập điểm (kiểm tra ở cả frontend và backend — backend từ chối ghi điểm khi không có assignment, xem API.md).
- Admin không có phiếu điểm thông thường; Admin tác động lên điểm của giám khảo thông qua override (Mục 6.6.3) hoặc qua tiêu chí Admin-Only do chính Admin nhập.

---

## 3. Kiến trúc và tech stack

### 3.1. Tech stack

| Tầng | Lựa chọn | Ghi chú |
|---|---|---|
| Backend | Go (một module duy nhất), build ra **1 file binary** | Chạy trên máy Admin (Windows/macOS/Linux); không cần cài runtime |
| Database | SQLite, **WAL mode** (`PRAGMA journal_mode=WAL`) | Một file `contest.db` đi kèm binary; xem DATABASE.md |
| Frontend | SPA (React hoặc Vue — chọn một khi bắt đầu code, không trộn), responsive, ưu tiên tablet/mobile cho giám khảo | Build tĩnh và được backend phục vụ trực tiếp (embed vào binary hoặc thư mục `web/` cạnh binary) |
| Realtime | WebSocket (một endpoint `/ws`) | Đồng bộ trạng thái, đồng hồ, ma trận điểm, reveal; chi tiết ở API.md Mục 5 |
| Offline (client) | IndexedDB | Lưu nháp điểm và hàng đợi đồng bộ trên thiết bị giám khảo; **không** dùng LocalStorage cho điểm (dung lượng nhỏ, dễ mất) |
| Xuất báo cáo | Excel `.xlsx` phía server | Điểm chi tiết + tổng hợp + audit log (Mục 6.6.5) |
| Backup | File snapshot SQLite tạo bằng `VACUUM INTO` | Xem Mục 11 |

### 3.2. Kiến trúc triển khai tại hiện trường

```
                 ┌─────────────────────────────────────────┐
   Mạng LAN      │  Máy Admin (chạy binary Go + SQLite)    │
   (Wi-Fi/       │  - HTTP server: phục vụ SPA + REST API  │
   switch của    │  - WebSocket hub: trạng thái realtime   │
   BTC, không    │  - Timer engine: giờ server làm chuẩn   │
   cần internet) │  - File contest.db + thư mục backups/   │
                 └───────▲───────────────▲─────────────────┘
                         │               │
        ┌────────────────┴───┐   ┌───────┴──────────────┐   ┌──────────────────┐
        │ Tablet/điện thoại  │   │ Màn hình MC / máy    │   │ Laptop Admin     │
        │ của giám khảo (SPA)│   │ chiếu (Public View)  │   │ (Admin Dashboard)│
        └────────────────────┘   └──────────────────────┘   └──────────────────┘
```

- Chỉ có **một tiến trình server duy nhất** trong một sự kiện. Không chạy 2 bản sao của binary cùng trỏ vào một file db.
- SQLite ở WAL mode cho phép nhiều kết nối đọc đồng thời và 1 kết nối ghi; backend phải gom các thao tác ghi qua một connection pool giới hạn (`maxOpenConns = 1` cho ghi) hoặc hàng đợi ghi tương đương, để tránh lỗi `database is locked` khi nhiều giám khảo nộp điểm cùng lúc.
- Frontend là SPA thuần: mọi điều hướng không cần server ngoài; API và WebSocket cùng origin với trang.

### 3.3. Song ngữ (bilingual)

- Các bảng chứa nội dung hiển thị do BTC nhập (contests, rounds, teams, criteria_groups, criteria, criteria_options) có sẵn cột đôi: `name_vi` / `name_en`, `desc_vi` / `desc_en` (và `label_vi` / `label_en` cho lựa chọn điểm).
- API trả cả hai bản ngôn ngữ; frontend chọn bản theo cờ ngôn ngữ trên thiết bị của từng người dùng (lưu ở LocalStorage — đây là công dụng duy nhất được phép của LocalStorage trong hệ thống; điểm số không lưu ở đây) và render ngay, không query lại.
- Quy tắc fallback: nếu bản ngôn ngữ đang chọn trống thì hiển thị bản còn lại; không bao giờ hiển thị chuỗi trống cho tên bắt buộc.
- Phạm vi chốt: chỉ 2 ngôn ngữ Việt–Anh. Thêm ngôn ngữ thứ ba là việc Phase 2 và sẽ cần đổi schema (bảng bản dịch riêng) — không hứa hẹn trong MVP.

### 3.4. Đồng hồ hệ thống

- Timer engine chạy trên server. Server phát sự kiện đồng bộ đồng hồ qua WebSocket (`timer.sync`, xem API.md) ít nhất mỗi 1 giây trong lúc có performance đang ở trạng thái Đang thi.
- Client hiển thị dựa trên mốc server (`ends_at` / thời lượng lố) cộng hiệu số đã đồng bộ, không tự đếm độc lập từ 0; khi mất WebSocket, client giữ nguyên hiển thị cuối cùng và đánh dấu "mất kết nối", không tự suy diễn trạng thái mới.

---

## 4. State machine (dùng thống nhất trong PRD, DATABASE và API)

### 4.1. Trạng thái contest

```
pending ──(Admin kích hoạt, qua kiểm tra cấu hình)──▶ active ──(Admin kết thúc)──▶ completed
                                                                                     │
                                                              (Admin chốt sổ) ──▶ completed + is_finalized = 1
```

| Trạng thái (`status`) | Nghĩa | Hành động được phép nổi bật |
|---|---|---|
| `pending` | Sắp diễn ra — đang cấu hình | Sửa mọi cấu hình, clone, dry run, reset |
| `active` | Đang thi | Điều khiển luồng, chấm điểm; **khóa cấu hình chấm** (Mục 6.3.5); vẫn sửa được thông tin mô tả không ảnh hưởng điểm (tên hiển thị, địa điểm) |
| `completed` | Đã kết thúc | Xem, xuất Excel, reveal bổ sung; không chấm mới; Admin vẫn override được khi contest chưa chốt sổ |
| `completed` + `is_finalized = 1` | Đã chốt sổ | Không ai sửa điểm được nữa. Muốn sửa phải "mở chốt sổ" (chỉ Admin, bắt buộc lý do, ghi audit) — mở chốt đưa `is_finalized` về 0 nhưng contest giữ nguyên `completed` |

### 4.2. Trạng thái performance (một lượt thi của một đội)

```
pending ──▶ performing ──▶ scoring ──▶ locked ──▶ revealed
(Chờ thi)    (Đang thi)     (Đang chấm)   (Đã khóa điểm)   (Đã công bố)
```

| Trạng thái (`status`) | Nghĩa | Chuyển trạng thái hợp lệ |
|---|---|---|
| `pending` | Chờ thi — chưa bắt đầu | → `performing` khi Admin bấm Bắt đầu thi |
| `performing` | Đang thi — timer đang chạy (hoặc đang tạm dừng trong trạng thái này, xem `timer_status`) | → `scoring` khi Admin bấm Kết thúc phần thi |
| `scoring` | Đang chấm — phần thi xong, giám khảo hoàn thiện và nộp điểm | → `locked` khi Admin bấm Khóa điểm (điều kiện ở Mục 6.6.2) |
| `locked` | Đã khóa điểm — điểm không sửa được nữa (trừ override) | → `revealed` khi Admin bật reveal |
| `revealed` | Đã công bố — điểm của performance này hiển thị ở Public View | Trạng thái cuối |

Quy tắc cứng:

- Không nhảy cóc trạng thái; không quay lui, trừ hai ngoại lệ có audit: Admin mở khóa một performance `locked` → `scoring` (bắt buộc lý do), và Admin mở chốt sổ ở cấp contest.
- Một round tại một thời điểm chỉ có tối đa **1 performance** ở trạng thái `performing`. Một contest có thể chạy song song nhiều round nếu Admin cấu hình cho phép; mặc định `settings.allow_parallel_rounds = false` (một performance diễn ra tại một thời điểm trên toàn contest).
- Trạng thái timer con (`timer_status`): `idle | running | paused | finished | overtime`. `overtime` là trường hợp của `running/paused` sau khi hết giờ chuẩn — performance vẫn ở `performing` cho tới khi Admin kết thúc.

### 4.3. Trạng thái phiếu điểm (score)

```
draft ──(Giám khảo bấm Gửi)──▶ submitted ──(Admin khóa điểm performance)──▶ locked
  ▲                                │
  └────(Giám khảo sửa, nếu contest cho phép sửa sau khi gửi)────┘
```

- `draft`: nháp — tồn tại ở IndexedDB của giám khảo và/hoặc đã đồng bộ lên server dạng nháp.
- `submitted`: đã nộp chính thức lên server.
- `locked`: đã khóa cùng performance; giám khảo không sửa được, Admin chỉ sửa bằng override có lý do.
- Mỗi lần phiếu điểm thay đổi nội dung, trường `version` tăng 1 (xem Mục 10 và DATABASE.md).

---

## 5. Luồng nghiệp vụ chính (end-to-end)

1. **Trước sự kiện:** Admin tạo contest → tạo round/hạng mục → nhập đội (tay hoặc import Excel) → định nghĩa nhóm + tiêu chí → tạo tài khoản giám khảo → lập ma trận phân công → cấu hình thời gian và luật trừ điểm → chạy Dry Run với đội giả/dữ liệu thử → Reset điểm về trạng thái sạch.
2. **Mở sự kiện:** Admin kiểm tra cấu hình (hệ thống chặn kích hoạt nếu còn lỗi cấu hình — Mục 6.1.4) → chuyển contest sang `active` (khóa cấu hình chấm) → mở Public View lên máy chiếu → giám khảo quét QR code để vào hệ thống trên mạng LAN và đăng nhập.
3. **Mỗi phần thi:** Admin bấm Bắt đầu thi cho đội → timer chạy trên mọi màn hình, form chấm mở trên thiết bị của giám khảo được phân công → Admin kết thúc phần thi → giám khảo hoàn thiện, bấm Gửi → Admin theo dõi ma trận, nhắc giám khảo chậm → (nếu có trừ điểm giờ) duyệt đề xuất trừ điểm → Admin khóa điểm → Admin reveal khi MC sẵn sàng → Public View hiện điểm và hiệu ứng vinh danh.
4. **Kết thúc:** Admin kết thúc contest → xem bảng xếp hạng cuối, xử lý đồng điểm → xuất Excel lưu hồ sơ → bấm Chốt sổ → tải file backup cuối cùng.

---

## 6. Các module chức năng

### 6.1. Module 1 — Admin & Setup

#### 6.1.1. Quản lý cuộc thi (Contests)

- Trường thông tin: tên cuộc thi (song ngữ `name_vi`/`name_en`), mô tả (song ngữ), thời gian tổ chức, địa điểm, tổng điểm tối đa hiển thị (tự tính từ tiêu chí — Mục 6.3.4).
- Trạng thái theo Mục 4.1: `pending` / `active` / `completed` (+ `is_finalized`).
- Cấu hình thuộc contest:
  - `allow_judge_edit_after_submit` (bool): cho phép giám khảo sửa điểm sau khi đã bấm Gửi hay không.
  - `drop_highest_lowest` (bool) và `min_judges_for_trim` (int, mặc định 5, giá trị hợp lệ nhỏ nhất là 4): cấu hình bỏ điểm cao nhất/thấp nhất khi tính điểm cuối — xem luật đầy đủ ở Mục 6.6.5.
  - `tie_breaker_criterion_id` (nullable): tiêu chí dùng làm luật ưu tiên tự động khi đồng điểm (Mục 6.6.4); để trống nghĩa là đồng điểm chỉ xử lý thủ công.
  - Cấu hình thời gian và luật trừ điểm: thuộc Module Time Controller (Mục 6.4), lưu theo contest.
- Mỗi contest có một mã tham gia (join code) phục vụ QR code (Mục 12).

#### 6.1.2. Quản lý tài khoản và phân quyền

- Admin tạo mới, sửa thông tin, vô hiệu hóa tài khoản người dùng. Không xóa cứng tài khoản đã có điểm/audit — chỉ vô hiệu hóa (`is_active = 0`).
- Đăng nhập bằng tên đăng nhập + mật khẩu, hoặc mã PIN ngắn cho giám khảo tại hiện trường (cấu hình theo tài khoản: cho phép đăng nhập PIN hay không). Bảo mật mật khẩu/PIN: xem Mục 13.
- **Reset mật khẩu/PIN:** Admin reset hoặc cấp lại mật khẩu/PIN cho bất kỳ người dùng nào từ màn hình quản trị, không cần biết mật khẩu cũ. Thao tác reset được ghi audit (ai reset cho ai, lúc nào — không ghi giá trị mật khẩu/PIN vào log). Đây là tính năng bắt buộc vì giám khảo quên mật khẩu tại hiện trường.
- Người dùng tự đổi mật khẩu/PIN của mình sau khi đăng nhập.
- Vai trò: theo Mục 2.1. Một tài khoản có đúng 1 vai trò ở MVP.

#### 6.1.3. Nhân bản (Clone) và Reset

- **Clone contest:** nhân bản toàn bộ cấu hình của một contest (round, đội, nhóm + tiêu chí, phân công, cấu hình thời gian/trừ điểm) sang contest mới ở trạng thái `pending`, **không** nhân bản điểm, timer session và audit log nghiệp vụ. Tài khoản người dùng là dùng chung toàn hệ thống, không nhân bản.
- **Clone để tổng duyệt:** cùng cơ chế clone, dùng khi muốn giữ nguyên bản thi thật.
- **Reset điểm:** xóa toàn bộ phiếu điểm, score detail, timer session và trạng thái performance của một contest, đưa mọi performance về `pending`, giữ nguyên cấu hình và danh sách đội. Reset yêu cầu Admin xác nhận lần hai và ghi audit. Reset là thao tác dọn sau Dry Run hoặc trước khi thi thật.
- Phân biệt rõ: Clone tạo contest mới; Reset dọn điểm trong contest hiện tại; Dry Run (Mục 6.1.5) là quy trình vận hành dùng hai thao tác trên.

#### 6.1.4. Kiểm tra cấu hình trước khi kích hoạt (activation checklist)

Hệ thống không cho chuyển contest `pending` → `active` nếu còn lỗi chặn, gồm tối thiểu:

- Chưa có round nào, hoặc có round không có đội nào.
- Chưa có tiêu chí chấm thường nào, hoặc tổng điểm tối đa tính được bằng 0 (Mục 6.3.4).
- Có đội không có bất kỳ giám khảo nào được phân công.
- Có giám khảo được phân công nhưng tài khoản đã bị vô hiệu hóa.
- Cấu hình trừ điểm giờ không hợp lệ (ví dụ trần trừ điểm lớn hơn tổng điểm tối đa).
- Màn hình kích hoạt liệt kê từng lỗi kèm liên kết tới đúng chỗ cần sửa; cảnh báo không chặn (ví dụ: thiếu bản dịch tiếng Anh của một tiêu chí) hiển thị riêng và cho phép kích hoạt sau khi Admin xác nhận.

#### 6.1.5. Dry Run (tổng duyệt)

- Admin có thể chuyển một contest ở `pending` sang chế độ tổng duyệt (`is_dry_run = 1` trên các phiếu điểm phát sinh, hoặc vận hành trên bản clone tổng duyệt — chọn clone là cách khuyến nghị và là mặc định trong hướng dẫn vận hành).
- Điểm và kết quả của Dry Run không được tính vào kết quả chính thức; trước thi thật, Admin dùng Reset điểm (Mục 6.1.3) và kiểm tra lại checklist (Mục 6.1.4).

#### 6.1.6. Tiêu chí nghiệm thu — Module 1

- [ ] Tạo/sửa/vô hiệu hóa được contest và tài khoản; tài khoản đã có điểm thì không xóa cứng được.
- [ ] Admin reset được PIN cho giám khảo đang bị khóa đăng nhập, giám khảo đăng nhập lại được ngay, và audit log có bản ghi reset (không chứa giá trị PIN).
- [ ] Clone một contest cho ra contest mới đầy đủ cấu hình, 0 phiếu điểm, trạng thái `pending`.
- [ ] Reset điểm đưa mọi performance về `pending` và giữ nguyên cấu hình/đội/phân công.
- [ ] Kích hoạt contest thiếu tiêu chí hoặc có đội không có giám khảo bị chặn, kèm danh sách lỗi cụ thể.

### 6.2. Module 2 — Teams & Rounds (ĐỘI THI VÀ VÒNG THI) — MODULE MỚI Ở V2

Module này không có trong bản v1 (v1 nhắc tới "đội thi" ở khắp nơi nhưng không có nơi quản lý đội và vòng thi). Đây là bổ sung bắt buộc: không có module này thì phân công, bấm giờ và tính điểm không có đối tượng để bám vào.

#### 6.2.1. Round (vòng thi / hạng mục)

- Trường: contest, tên song ngữ, thứ tự hiển thị (`sort_order`), thời lượng chuẩn riêng của vòng (ghi đè thời lượng chuẩn của contest nếu có), ghi chú.
- Một contest có 1 hoặc nhiều round. Cuộc thi chỉ có một vòng vẫn tạo đúng 1 round (ví dụ "Vòng chung kết") — không có chế độ "đội không thuộc vòng nào".
- Xếp hạng được tính **trong từng round**; MVP không có xếp hạng gộp xuyên round.

#### 6.2.2. Team (đội thi)

- Trường: round, tên đội (song ngữ nếu cần), số báo danh (SBD — `code`, duy nhất trong contest, ví dụ `01`, `A03`), tên đơn vị/trường (nếu có), thành viên/ghi chú, thứ tự thi (`sort_order`), trạng thái hoạt động.
- CRUD đầy đủ trên giao diện Admin: thêm, sửa, vô hiệu hóa đội (đội đã thi không xóa cứng được).
- **Import Excel:** tải lên file `.xlsx` theo mẫu do hệ thống cung cấp (cột: SBD, Tên đội, Đơn vị, Round, Thứ tự thi). Hệ thống kiểm tra trước khi nhập: trùng SBD, thiếu tên, round không tồn tại — báo cáo theo dòng (dòng nào lỗi, lỗi gì) và **không nhập một phần trong im lặng**: Admin chọn "nhập các dòng hợp lệ" hoặc "hủy toàn bộ". Có file mẫu tải xuống từ đúng màn hình import.
- **Thứ tự thi và bốc thăm:** Admin sắp xếp thứ tự thi bằng kéo–thả hoặc nhập số thứ tự; hệ thống có nút "Bốc thăm ngẫu nhiên" xáo trộn thứ tự thi trong một round (ghi audit; chỉ dùng khi round chưa có performance nào rời `pending`).
- Khi tạo team, hệ thống tự tạo 1 performance tương ứng ở trạng thái `pending`. Xóa/vô hiệu hóa team trước khi thi sẽ vô hiệu hóa performance tương ứng.

#### 6.2.3. Tiêu chí nghiệm thu — Module 2

- [ ] Tạo round và đội; SBD trùng trong cùng contest bị chặn với thông báo rõ.
- [ ] Import file Excel 30 đội: file có 2 dòng lỗi cho ra báo cáo đúng 2 dòng đó, không nhập lặng dữ liệu sai.
- [ ] Bốc thăm ngẫu nhiên đổi thứ tự thi và ghi audit; nút bị khóa khi đã có đội bắt đầu thi.
- [ ] Mỗi đội tạo ra đúng 1 performance ở trạng thái `pending`.

### 6.3. Module 3 — Criteria Engine (Hệ thống tiêu chí) — LÀM LẠI Ở V2

#### 6.3.1. Nguyên tắc làm lại

Bản v1 liệt kê 4 "loại" tiêu chí: Range, Discrete, Multiplier, Admin-Only. Cách phân loại này sai và là lỗi thiết kế lớn nhất của v1: **Multiplier (nhân hệ số) không phải cách nhập điểm — nó là trọng số, một thuộc tính mà tiêu chí Range hay Discrete nào cũng có thể có.** Nếu code theo v1, hệ thống sẽ không biểu diễn được "tiêu chí chọn mức độ nhưng hệ số 2".

Mô hình đúng ở v2 — mỗi tiêu chí có ba chiều độc lập:

| Chiều | Trường | Giá trị |
|---|---|---|
| Cách nhập điểm | `input_type` | `range` (thang khoảng) hoặc `discrete` (mức định sẵn) |
| Thuộc tính điểm | `max_score`, `min_score`, `step`, `weight` | Áp dụng cho **mọi** tiêu chí (Mục 6.3.3) |
| Quyền nhìn thấy/nhập | `is_admin_only` | `true`: chỉ Admin thấy và nhập; `false`: giám khảo được phân công thấy và nhập |

#### 6.3.2. Cách nhập điểm

- **Range (`range`):** giám khảo nhập bằng thanh trượt và/hoặc ô số, từ `min_score` đến `max_score`, theo `step` (ví dụ 0–10, bước 0.5). Frontend làm tròn theo step; backend kiểm tra lại và từ chối giá trị ngoài khoảng hoặc lệch step.
- **Discrete (`discrete`):** giám khảo chọn đúng 1 mức trong danh sách lựa chọn định sẵn của tiêu chí (`criteria_options`: nhãn song ngữ + giá trị điểm, ví dụ Kém = 2, Khá = 5, Tốt = 8, Xuất sắc = 10). Giá trị điểm của mọi lựa chọn phải nằm trong `[min_score, max_score]` của tiêu chí.

#### 6.3.3. Thuộc tính áp dụng cho mọi tiêu chí

| Thuộc tính | Ý nghĩa | Ràng buộc |
|---|---|---|
| `max_score` | Điểm tối đa của tiêu chí (trước trọng số) | > 0 |
| `min_score` | Điểm tối thiểu (mặc định 0) | ≥ 0 và < `max_score` |
| `step` | Bước nhảy (chỉ áp cho `range`; với `discrete` bỏ qua) | > 0 và ≤ `max_score - min_score` |
| `weight` | Trọng số (hệ số nhân) của tiêu chí — đây chính là "Multiplier" của v1, chuyển thành thuộc tính | > 0; mặc định 1 |
| `is_admin_only` | Tiêu chí đặc quyền của Admin | Xem Mục 6.3.6 |
| `allow_comment` | Giám khảo có được/bị bắt buộc ghi chú cho tiêu chí này không (cấu hình: không hiện / tùy chọn / bắt buộc) | Mặc định: tùy chọn ở cấp phiếu điểm |

Điểm của một tiêu chí trong một phiếu điểm: `weighted = raw_score × weight`. Điểm một phiếu điểm (của một giám khảo cho một performance) là tổng `weighted` của mọi score detail thuộc phiếu đó.

#### 6.3.4. Nhóm tiêu chí và validation tổng

- Tiêu chí thuộc về nhóm (`criteria_groups`: tên song ngữ, thứ tự). Nhóm dùng để trình bày form chấm cho dễ theo dõi và để báo cáo điểm theo nhóm.
- **Tổng điểm tối đa của contest được hệ thống tự tính:** `total_max_score = Σ (max_score × weight)` trên mọi tiêu chí **không** phải Admin-Only. Admin-Only không tính vào tổng tối đa vì nó là khoản cộng/trừ điều chỉnh (Mục 6.3.6).
- Validation khi lưu cấu hình và khi kích hoạt contest:
  - Tổng trọng số của từng nhóm và của toàn bộ tiêu chí được hiển thị trực tiếp trên màn hình cấu hình (số liệu sống, cập nhật khi Admin sửa) để Admin tự cân đối.
  - Nếu Admin khai báo một **tổng điểm mục tiêu** (ví dụ muốn tổng là 100), hệ thống cảnh báo khi `total_max_score` tính được khác tổng mục tiêu; đây là cảnh báo, không chặn lưu, nhưng là lỗi chặn ở checklist kích hoạt nếu Admin đã bật "bắt buộc khớp tổng mục tiêu" (`settings.require_total_match`, mặc định tắt).
  - Trọng số ≤ 0, `max_score` ≤ 0, lựa chọn discrete trùng giá trị hoặc ngoài khoảng — chặn lưu và báo đúng trường lỗi.
- Báo cáo và bảng xếp hạng luôn trình bày điểm kèm tổng tối đa (ví dụ 87,5 / 100) lấy từ `total_max_score` đã tính, không nhập tay.

#### 6.3.5. Khóa cấu hình khi contest Active

- Khi contest chuyển sang `active`, toàn bộ cấu hình chấm bị khóa: không thêm/sửa/xóa nhóm, tiêu chí, lựa chọn điểm, trọng số, phân công, luật trừ điểm giờ.
- Lý do: sửa tiêu chí giữa cuộc thi làm điểm đã chấm mất ý nghĩa và không thể tính lại công bằng.
- Muốn đổi cấu hình sau khi đã Active, Admin phải: (a) dùng Clone để tạo contest mới, hoặc (b) chuyển contest về lại `pending` — thao tác này chỉ được phép khi **chưa có phiếu điểm `submitted` nào**, bắt buộc lý do và ghi audit. Đã có điểm nộp thì không mở khóa cấu hình được nữa.

#### 6.3.6. Tiêu chí Admin-Only

- Chỉ Admin thấy và nhập; giám khảo không thấy tiêu chí này trên form và API không trả nó cho tài khoản judge.
- Dùng cho: điểm phạt, điểm cộng chuyên cần, điểm hội đồng độc lập, và **điểm trừ quá giờ đã được duyệt** (Module 6.4 ghi vào một tiêu chí Admin-Only dành riêng cho trừ giờ — hệ thống tự tạo tiêu chí này khi Admin bật luật trừ điểm, tên mặc định "Trừ điểm quá giờ / Overtime penalty", không hiển thị cho giám khảo).
- Điểm Admin-Only nhập dạng số có dấu (cho phép âm đối với khoản trừ), vẫn có `weight` (thường = 1) và ghi chú bắt buộc khi nhập.

#### 6.3.7. Tiêu chí nghiệm thu — Module 3

- [ ] Tạo được tiêu chí Discrete có weight = 2 và điểm tính ra đúng gấp đôi giá trị mức đã chọn.
- [ ] Màn hình cấu hình hiển thị đúng `total_max_score = Σ(max_score × weight)` (không tính Admin-Only) ngay khi sửa trọng số.
- [ ] Lưu tiêu chí có weight = 0 hoặc lựa chọn discrete ngoài khoảng bị chặn với thông báo đúng trường.
- [ ] Sau khi contest `active`, mọi thao tác sửa tiêu chí/phân công bị từ chối ở cả giao diện và API.
- [ ] Tài khoản judge gọi API lấy form chấm không nhận được bất kỳ tiêu chí Admin-Only nào.

### 6.4. Module 4 — Time Controller (Kiểm soát thời gian)

#### 6.4.1. Cấu hình thời gian theo contest/round

- Thời lượng chuẩn (`standard_duration_sec`): theo contest, round có thể ghi đè.
- Các mốc cảnh báo: mặc định còn 60 giây (đổi màu + âm thanh trên Public View và màn hình Admin), hết giờ (đổi trạng thái hiển thị sang "lố giờ").
- Timer hiển thị toàn màn hình trên Public View (đưa ra máy chiếu/LED) và thu nhỏ trên Admin Dashboard + thiết bị giám khảo.

#### 6.4.2. Vận hành timer

- Hành động: **Bắt đầu** (gắn với Bắt đầu thi, Mục 6.6.2), **Tạm dừng (Pause)**, **Tiếp tục (Resume)**, **Kết thúc** (gắn với Kết thúc phần thi), **Đặt lại** (chỉ khi performance còn `pending`).
- Khi hết thời lượng chuẩn mà phần thi chưa kết thúc, timer **không tự dừng**: chuyển sang **đếm lố giờ** (đếm tăng thời gian quá, hiển thị nổi bật) cho tới khi Admin bấm Kết thúc. Thời gian lố được ghi vào timer session (`overtime_sec`).
- Tổng thời gian tạm dừng (`paused_total_sec`) được ghi riêng và **không** tính vào thời gian thi cũng như thời gian lố.
- Mọi mốc timer lấy giờ server (`started_at`, `paused_at`, `ended_at` do server ghi) — client không tự tính thời gian lố để trừ điểm.

#### 6.4.3. Auto-Penalty (trừ điểm quá giờ) — QUY TRÌNH DUYỆT, KHÔNG TỰ ÁP

Đây là điểm sửa quan trọng so với v1. V1 viết "hệ thống tự trừ thẳng vào điểm tổng" — v2 **không cho phép tự trừ trong im lặng**, vì timer có thể bị bấm nhầm hoặc đội được cộng giờ do sự cố kỹ thuật của BTC; trừ điểm oan không có đường sửa là rủi ro tranh cãi cao nhất của hệ thống.

Quy trình bắt buộc:

1. Admin cấu hình luật trừ trước khi thi (chỉ sửa được khi contest `pending`):
   - `interval_sec`: độ dài một khoảng lố (ví dụ 30 giây).
   - `first_interval_points`: điểm trừ cho khoảng lố đầu tiên (ví dụ 1,0).
   - `subsequent_interval_points`: điểm trừ cho mỗi khoảng lố tiếp theo (ví dụ 0,5).
   - `max_penalty_points`: **trần trừ điểm** — tổng trừ cho một performance không vượt quá giá trị này (bắt buộc khai báo, > 0).
   - Công thức: số khoảng lố `n = ceil(overtime_sec / interval_sec)`; nếu `n = 0` thì trừ 0; nếu `n ≥ 1` thì `penalty = first_interval_points + (n − 1) × subsequent_interval_points`, sau đó `penalty = min(penalty, max_penalty_points)`.
   - Ví dụ kiểm chứng: lố 30 giây với cấu hình trên → n = 1 → trừ 1,0. Lố 75 giây → n = 3 → trừ 1,0 + 2 × 0,5 = 2,0.
2. Khi Admin bấm Kết thúc phần thi, hệ thống **tính và đề xuất** mức trừ (`penalty_proposed`), hiển thị rõ trên Admin Dashboard kèm thời gian lố và cách tính.
3. Admin chọn **Duyệt áp dụng** (có thể sửa mức trừ trước khi duyệt — mức đã sửa và lý do sửa đều ghi audit) hoặc **Không áp dụng** (ghi lý do, ví dụ sự cố kỹ thuật).
4. Chỉ khi được duyệt, hệ thống mới ghi khoản trừ vào tiêu chí Admin-Only dành cho trừ giờ (Mục 6.3.6) của performance đó, với `penalty_status = approved`. Chưa duyệt thì `penalty_status = proposed` và **không** ảnh hưởng điểm tổng hiển thị chính thức.
5. Mọi bước (đề xuất, sửa mức, duyệt, từ chối) đều ghi audit log.

#### 6.4.4. Tiêu chí nghiệm thu — Module 4

- [ ] Pause/Resume hoạt động; thời gian tạm dừng không bị tính vào thời gian lố của một phần thi thử.
- [ ] Hết giờ chuẩn, timer chuyển sang đếm lố và không tự kết thúc phần thi.
- [ ] Phần thi lố 75 giây với luật mẫu (30s / 1,0 / 0,5 / trần 5) cho đề xuất trừ đúng 2,0 điểm, và điểm tổng **không đổi** cho tới khi Admin bấm Duyệt.
- [ ] Admin sửa mức trừ khi duyệt hoặc từ chối trừ: cả hai trường hợp đều có bản ghi audit kèm lý do.
- [ ] Mức trừ đề xuất không bao giờ vượt trần `max_penalty_points` đã cấu hình.

### 6.5. Module 5 — Judge Workspace (Không gian giám khảo)

#### 6.5.1. Đồng bộ ngữ cảnh (Context Sync) và kiểm tra quyền

- Khi Admin kích hoạt một performance (Bắt đầu thi), thiết bị của mọi giám khảo đang trực tuyến tự chuyển hiển thị sang performance đó qua WebSocket (`performance.started`, API.md Mục 5).
- Giám khảo **có** assignment với performance đó: form chấm mở với đúng các tiêu chí không phải Admin-Only.
- Giám khảo **không có** assignment: chỉ hiện thông tin phần thi ở chế độ read-only (tên đội, vòng, thời gian), không có form chấm.
- Giám khảo mở ứng dụng trễ (sau khi phần thi đã bắt đầu) vẫn nhận đúng ngữ cảnh hiện tại ngay khi tải trang — trạng thái lấy từ server, không phụ thuộc việc có bắt được sự kiện WebSocket hay không.

#### 6.5.2. Form chấm điểm (Scoring UI)

- Tối giản thao tác, nút bấm và thanh trượt đủ lớn cho tablet/điện thoại: Range dùng slider + ô số đồng bộ; Discrete dùng nút chọn lớn, chọn một mức.
- Tiêu chí hiển thị theo nhóm, kèm điểm tối đa và trọng số (hiển thị dạng "×2" khi weight ≠ 1) để giám khảo hiểu thang điểm.
- Ô ghi chú ở cấp phiếu điểm và (theo cấu hình tiêu chí) ở cấp tiêu chí: ghi lý do cho điểm, đặc biệt khi cho điểm ở hai đầu thang.
- Form luôn hiển thị: tên đội, SBD, vòng thi, trạng thái timer của phần thi, và trạng thái phiếu điểm của chính giám khảo (nháp / đã nộp / đã khóa).

#### 6.5.3. Nháp theo từng đội, tự lưu (Auto-save)

- Điểm thay đổi đến đâu, hệ thống lưu nháp đến đó vào IndexedDB, **theo khóa (giám khảo × performance)** — nháp của đội này không bao giờ ghi đè nháp của đội khác.
- Tình huống bắt buộc xử lý đúng: Admin đã chuyển sang đội tiếp theo trong khi giám khảo đang chấm dở đội trước — nháp của đội trước phải còn nguyên khi giám khảo quay lại (từ Progress Tracker), và form của đội mới mở ở trạng thái nháp riêng của đội mới.
- Khi có mạng nội bộ, nháp được đồng bộ lên server theo cơ chế ở Mục 10; khi mất mạng, giám khảo vẫn chấm bình thường trên nháp local.

#### 6.5.4. Trạng thái đồng bộ (Sync status) — luôn nhìn thấy

Mỗi phiếu điểm trên giao diện giám khảo luôn hiển thị đúng một trong các trạng thái:

| Trạng thái hiển thị | Nghĩa |
|---|---|
| `Đã lưu trên máy này` | Nháp mới nhất chỉ có ở IndexedDB, chưa lên server |
| `Đã đồng bộ` | Bản trên server trùng bản local mới nhất |
| `Đã nộp` | Phiếu ở trạng thái `submitted` trên server |
| `Có xung đột` | Server đã có bản `submitted`/`locked` khác với bản local — xử lý theo Mục 10.3, giám khảo được thông báo rõ và không bị mất bản local (hiển thị để đối chiếu, việc chốt thuộc Admin) |

#### 6.5.5. Nộp và sửa điểm

- Bấm **Gửi** (`submit`): phiếu chuyển `draft → submitted` trên server. Trước khi gửi, form kiểm tra: mọi tiêu chí bắt buộc đã có điểm, giá trị hợp lệ theo step/khoảng — lỗi chỉ đúng tiêu chí thiếu.
- **Sửa điểm sau khi gửi:** chỉ khi contest bật `allow_judge_edit_after_submit` và performance chưa bị khóa điểm. Sửa điểm đã nộp **bắt buộc nhập lý do**; phiếu giữ trạng thái `submitted`, `version` tăng, và audit log ghi: người sửa, thời gian, tiêu chí, điểm cũ → điểm mới, lý do.
- Sau khi performance đã `locked`, giám khảo không sửa được nữa dưới mọi cấu hình; chỉ Admin override (Mục 6.6.3).

#### 6.5.6. Progress Tracker (tiến độ cá nhân)

- Danh sách các đội thuộc phạm vi phân công của giám khảo, kèm trạng thái từng đội đối với giám khảo đó: **Đã nộp** (xanh), **Chưa chấm** (đỏ), **Đang chấm dở** (vàng — có nháp chưa nộp).
- Đầu danh sách hiện tổng tiến độ dạng "Đã nộp: 4/10 đội".
- Bấm vào một đội trong tracker để mở lại đúng nháp/phiếu của đội đó (trong giới hạn trạng thái: đội đã khóa chỉ xem).

#### 6.5.7. Tiêu chí nghiệm thu — Module 5

- [ ] Admin bắt đầu thi đội B khi giám khảo đang chấm dở đội A: nháp đội A còn nguyên khi mở lại từ Progress Tracker.
- [ ] Giám khảo không được phân công cho đội đang thi chỉ thấy thông tin read-only, và API từ chối mọi yêu cầu ghi điểm của tài khoản đó cho performance đó.
- [ ] Ngắt mạng LAN trên tablet, chấm tiếp 1 đội, nộp thử: điểm được giữ ở trạng thái "Đã lưu trên máy này"; nối mạng lại, phiếu tự đồng bộ và chuyển "Đã nộp" mà giám khảo không phải thao tác lại.
- [ ] Sửa điểm đã nộp khi contest tắt quyền sửa: nút sửa không hiện và API từ chối. Khi contest bật quyền sửa: sửa không nhập lý do bị chặn; sửa có lý do tạo đúng bản ghi audit điểm cũ → mới.
- [ ] Progress Tracker đếm đúng số đội đã nộp / chưa chấm / chấm dở của giám khảo đang đăng nhập.

### 6.6. Module 6 — Admin Dashboard (Màn hình điều hành realtime)

#### 6.6.1. Live Matrix (ma trận điểm trực tiếp)

- Lưới: hàng = performance/đội (theo thứ tự thi), cột = giám khảo.
- Mỗi ô thể hiện đúng một trạng thái: **đã nộp** / **chưa chấm** / **đang chấm dở** / **không được phân công** / **đã khóa** — cập nhật realtime qua WebSocket, Admin nhìn là biết ai đang chậm mà không cần hỏi.
- Ô của giám khảo mất kết nối (không còn heartbeat — API.md Mục 5) hiển thị thêm dấu hiệu mất kết nối.
- Bấm vào một ô đã nộp để xem chi tiết phiếu điểm của giám khảo đó cho đội đó (chỉ Admin).

#### 6.6.2. Flow Control (điều khiển luồng thi)

Các nút điều khiển theo đúng state machine Mục 4.2, luôn hiển thị theo performance đang chọn:

- **Bắt đầu thi [Đội]:** `pending → performing`, khởi động timer, đồng bộ mọi màn hình.
- **Tạm dừng / Tiếp tục:** tác động timer trong lúc `performing`.
- **Kết thúc phần thi:** `performing → scoring`, dừng timer, chốt thời gian lố và (nếu bật luật trừ) tạo đề xuất trừ điểm (Mục 6.4.3).
- **Khóa điểm:** `scoring → locked`. Điều kiện: mọi giám khảo được phân công đã `submitted`, hoặc Admin xác nhận khóa dù còn thiếu (bắt buộc lý do; phiếu thiếu được ghi nhận là không có điểm và bị loại khỏi tập tính trung bình — xem Mục 6.6.5). Sau khi khóa, mọi phiếu thuộc performance chuyển `locked`.
- **Công bố (Reveal):** `locked → revealed`, bật hiển thị điểm của performance này ở Public View (Mục 6.7).
- **Mở khóa (ngoại lệ):** `locked → scoring`, chỉ Admin, bắt buộc lý do, ghi audit; dùng khi phát hiện sai sót ngay trong sự kiện.

#### 6.6.3. Score Override và Audit Trail

- **Admin Override:** Admin sửa trực tiếp điểm của bất kỳ tiêu chí nào trong phiếu của bất kỳ giám khảo nào, ở trạng thái phiếu `submitted` hoặc `locked`. Bắt buộc nhập lý do. Điểm gốc của giám khảo không bị xóa — hệ thống lưu cả giá trị trước và sau override.
- **Audit Trail (bất biến):** bảng `audit_logs` chỉ cho phép **thêm** (append-only) ở tầng ứng dụng: không có chức năng sửa/xóa bản ghi audit trên giao diện và API; các thao tác tối thiểu phải ghi audit:
  - Mọi thay đổi điểm: nộp lần đầu không cần audit chi tiết (đã có phiếu điểm), nhưng **sửa sau khi nộp, override, mở khóa phiếu/performance, áp/từ chối trừ điểm giờ** đều bắt buộc ghi: `[người sửa] + [thời gian server] + [performance] + [tiêu chí] + [điểm cũ → điểm mới] + [lý do]`.
  - Thao tác hệ thống nhạy cảm: reset PIN, reset điểm, clone, bốc thăm lại, chuyển trạng thái contest, chốt sổ/mở chốt sổ, restore backup.
- Màn hình Audit Log: lọc theo người, theo đội, theo khoảng thời gian; xuất kèm trong file Excel (Mục 6.6.5).

#### 6.6.4. Bảng xếp hạng và xử lý đồng điểm (Tie-breaker)

- **Live Leaderboard:** xếp hạng trong từng round theo điểm cuối của đội (Mục 6.6.5), cập nhật realtime khi có phiếu nộp/khóa mới. Leaderboard trên Admin Dashboard luôn hiển thị đầy đủ; Leaderboard trên Public View chỉ hiện khi Admin reveal (Mục 6.7).
- **Đồng điểm:** hệ thống tự phát hiện và làm nổi bật (highlight) các đội bằng điểm cuối trong cùng round.
- Xử lý đồng điểm theo một trong hai cách (Admin chọn):
  - **Luật ưu tiên tự động:** nếu contest đã cấu hình `tie_breaker_criterion_id`, đội có điểm trung bình của tiêu chí đó cao hơn xếp trên. Nếu sau luật này vẫn bằng nhau, hệ thống báo "vẫn đồng điểm" chứ không tự bịa thêm luật.
  - **Chốt thủ công:** Admin bấm chọn thứ hạng cuối cùng cho các đội đồng điểm; quyết định thủ công ghi audit kèm lý do và được đánh dấu rõ trên báo cáo xuất ra là thứ hạng do Admin chốt.

#### 6.6.5. Tính điểm, xuất báo cáo

- **Điểm của một phiếu:** tổng `raw_score × weight` của mọi score detail thường, cộng các khoản Admin-Only đã nhập cho performance đó (khoản Admin-Only thuộc về performance, không thuộc riêng giám khảo nào — nó cộng một lần vào điểm đội, không cộng theo từng giám khảo; riêng khoản trừ giờ chỉ tính khi `penalty_status = approved`).
- Cách tính điểm cuối của đội (chốt, dùng thống nhất ở mọi màn hình và file xuất):
  1. Với mỗi giám khảo được phân công **và** đã nộp phiếu (phiếu ở `submitted`/`locked`): tính điểm phiếu theo công thức trên (chưa gồm Admin-Only).
  2. Nếu `drop_highest_lowest = true` **và** số phiếu hợp lệ `≥ min_judges_for_trim` (mặc định 5, tối thiểu cấu hình được là 4): loại 1 phiếu cao nhất và 1 phiếu thấp nhất khỏi bước 3. Nếu số phiếu ít hơn ngưỡng này, **không** bỏ phiếu nào và giao diện phải ghi rõ "không áp dụng bỏ cao/thấp nhất — không đủ số giám khảo" để tránh hiểu nhầm.
  3. Điểm giám khảo của đội = trung bình cộng các phiếu còn lại.
  4. Điểm cuối của đội = điểm giám khảo + tổng các khoản Admin-Only của performance (gồm trừ giờ đã duyệt, mang dấu âm).
- Làm tròn: điểm hiển thị và xếp hạng dùng 2 chữ số thập phân; tính toán nội bộ giữ nguyên độ chính xác của dữ liệu nhập (step có thể là 0,5 hoặc 0,25).
- **Export Excel (.xlsx):** một file cho một contest, tối thiểu gồm các sheet: (1) Tổng hợp xếp hạng theo round, (2) Điểm chi tiết đội × giám khảo × tiêu chí, (3) Điểm Admin-Only và trừ giờ, (4) Audit log các lần sửa điểm. Tên file chứa tên contest và thời gian xuất.

#### 6.6.6. Tiêu chí nghiệm thu — Module 6

- [ ] Live Matrix đổi trạng thái ô trong dưới 1 giây sau khi một giám khảo bấm Gửi (cùng mạng LAN).
- [ ] Khóa điểm khi còn 1 giám khảo chưa nộp: hệ thống cảnh báo, Admin khóa tiếp phải nhập lý do, và phiếu thiếu không xuất hiện trong tập tính trung bình.
- [ ] Override một điểm: giá trị gốc vẫn xem lại được, audit log có đủ người/thời gian/tiêu chí/cũ → mới/lý do.
- [ ] Tính điểm: 5 phiếu 8, 9, 7, 10, 6 với cấu hình bỏ cao/thấp nhất và ngưỡng 5 cho kết quả trung bình đúng của {8, 9, 7} = 8,00; với chỉ 4 phiếu, hệ thống không bỏ phiếu nào và hiển thị ghi chú không đủ ngưỡng.
- [ ] Hai đội bằng điểm được highlight; áp luật tiêu chí ưu tiên đã cấu hình xếp đúng đội có điểm tiêu chí đó cao hơn lên trên; chốt thủ công ghi audit.
- [ ] File Excel xuất ra đủ 4 sheet, số liệu khớp với Leaderboard tại thời điểm xuất.

### 6.7. Module 7 — Public / MC View (Màn hình khán giả và MC)

#### 6.7.1. Nội dung hiển thị

- Tên cuộc thi, vòng thi, đội đang thi (tên + SBD), đồng hồ đếm ngược/lố giờ toàn màn hình — đưa ra máy chiếu/LED.
- Tiến trình sự kiện ở mức khán giả cần biết: đội tiếp theo (nếu Admin bật hiển thị), không kèm bất kỳ điểm nào chưa reveal.
- Khi được reveal: điểm của đội vừa công bố, và/hoặc bảng xếp hạng đã reveal, kèm hiệu ứng vinh danh (animation CSS nhẹ, chạy được offline, không tải tài nguyên ngoài).

#### 6.7.2. Reveal là cờ riêng do Admin bật — nguyên tắc cứng

- Điểm của một performance chỉ xuất hiện trên Public View khi performance đó ở trạng thái `revealed` (Mục 4.2). Không có ngoại lệ "Admin thấy nên khán giả cũng thấy".
- Bảng xếp hạng Public View là một cờ reveal riêng (`leaderboard_revealed` theo round): Admin có thể reveal điểm từng đội mà chưa reveal bảng xếp hạng tổng, và ngược lại chỉ reveal top theo cấu hình hiển thị.
- Tài khoản MC/Public View gọi API cũng bị chặn như giao diện: API không trả điểm chưa reveal cho role `mc`.
- Thu hồi reveal: Admin có thể tắt reveal của một performance/bảng xếp hạng (ghi audit); Public View cập nhật ngay qua WebSocket.

#### 6.7.3. Tiêu chí nghiệm thu — Module 7

- [ ] Trước reveal, Public View và API của role `mc` không chứa điểm của bất kỳ đội nào (kiểm tra cả dữ liệu trả về, không chỉ giao diện).
- [ ] Bấm Công bố cho một đội: trong dưới 1 giây Public View hiện đúng điểm cuối của đội đó; các đội chưa công bố vẫn không lộ điểm.
- [ ] Reveal điểm từng đội không tự kéo theo reveal bảng xếp hạng khi cờ bảng xếp hạng đang tắt.
- [ ] Tắt reveal: Public View ẩn điểm ngay và audit log có bản ghi thu hồi.

---

## 7. Ma trận phân công (Assignment Matrix)

- Màn hình dạng lưới: hàng = đội/performance, cột = giám khảo; Admin tích chọn từng ô, hoặc dùng thao tác nhanh: gán tất cả giám khảo cho tất cả đội (All-to-All), gán theo round, bỏ gán hàng loạt.
- Assignment quyết định: giám khảo thấy form của đội nào (Mục 6.5.1), ma trận Live Matrix hiển thị ô "không được phân công" cho cặp nào, và phiếu của giám khảo nào được tính cho đội nào.
- Assignment bị khóa cùng cấu hình chấm khi contest `active` (Mục 6.3.5).
- Hệ thống cảnh báo ở checklist kích hoạt (Mục 6.1.4) khi có đội không có giám khảo nào, hoặc giám khảo không được gán cho đội nào.

**Tiêu chí nghiệm thu:** gán giám khảo A chỉ cho đội 1–2 thì tài khoản A mở đội 3 chỉ thấy read-only; All-to-All gán đúng cho mọi cặp đội × giám khảo đang hoạt động; sửa phân công sau khi contest `active` bị từ chối.

---

## 8. Song ngữ trên giao diện

Ngoài dữ liệu song ngữ ở Mục 3.3, giao diện hệ thống (nhãn nút, thông báo) dùng file ngôn ngữ của frontend cho 2 ngôn ngữ Việt–Anh. Mỗi người dùng (Admin, từng giám khảo, MC) chuyển ngôn ngữ độc lập trên thiết bị của mình; lựa chọn không ảnh hưởng người khác và được nhớ trên thiết bị đó.

**Tiêu chí nghiệm thu:** giám khảo A chuyển sang tiếng Anh thì toàn bộ nhãn và nội dung cuộc thi trên máy A hiển thị tiếng Anh ngay (không reload), trong khi máy giám khảo B vẫn là tiếng Việt; tiêu chí thiếu bản dịch tiếng Anh hiển thị bản tiếng Việt thay vì để trống.

---

## 9. Các yêu cầu xuyên suốt — giới thiệu

Luồng điều khiển thi nằm ở Mục 6.6.2, timer ở Mục 6.4, đồng bộ ngữ cảnh ở Mục 6.5.1. Các mục tiếp theo (Mục 10–13) là các yêu cầu xuyên suốt (cross-cutting), áp dụng đồng thời cho mọi module ở Mục 6 và là một phần bắt buộc của MVP, không phải tính năng phụ: offline & đồng bộ, backup/restore, triển khai tại hiện trường và bảo mật.

---

## 10. Offline và đồng bộ (Offline & Sync)

### 10.1. Mô hình

- Thiết bị giám khảo là **online-first, offline-tolerant**: có LAN thì đồng bộ liên tục; mất LAN thì mọi thao tác chấm tiếp tục trên dữ liệu local.
- Nơi lưu local: **IndexedDB**, gồm: bản sao cấu hình cần cho form chấm (tiêu chí, assignment của chính giám khảo), nháp phiếu điểm theo từng performance, và hàng đợi thao tác chờ gửi (outbox).
- Khi mất kết nối, giao diện báo rõ trạng thái mất kết nối; giám khảo không bị đăng xuất hay mất phiên chỉ vì rớt mạng.

### 10.2. Chống nộp trùng (idempotency)

- Mỗi phiếu điểm được client tạo kèm một `client_uuid` (UUID v4) ngay từ lần lưu nháp đầu tiên và giữ nguyên suốt vòng đời phiếu. Server có ràng buộc duy nhất trên `client_uuid` (DATABASE.md).
- Mọi yêu cầu tạo/cập nhật/nộp phiếu đều mang `client_uuid` và số `version` mà client biết. Gửi lại cùng một yêu cầu (do sync lặp, do giám khảo bấm lại, do mạng chập chờn) **không** tạo phiếu thứ hai và **không** áp thay đổi hai lần — server nhận diện theo `client_uuid` + `version` và trả về kết quả đã ghi nhận (chi tiết ở API.md Mục 4).
- Nút Gửi bị khóa trong lúc một yêu cầu gửi đang bay, nhưng cơ chế chống trùng chính nằm ở server, không dựa vào giao diện.

### 10.3. Luật xung đột (chốt — không thay đổi khi code)

Mỗi phiếu điểm có `version` nguyên, tăng 1 sau mỗi lần thay đổi nội dung được server chấp nhận. Luật xử lý khi bản local và bản server lệch nhau:

1. **Bản `submitted`/`locked` trên server luôn thắng bản `draft` ở local.** Nếu giám khảo có nháp local mới hơn về thời gian nhưng server đã có bản đã nộp (do chính giám khảo nộp từ thiết bị khác, hoặc Admin đã khóa), bản local không được ghi đè lên; client nhận phản hồi xung đột, hiển thị trạng thái "Có xung đột" (Mục 6.5.4) và giữ bản local để đối chiếu.
2. **Hai bản cùng là draft:** bản có `version` cao hơn thắng; client gửi kèm `base_version` — nếu `base_version` khác version hiện tại của server thì server từ chối ghi (409) và trả bản server về để client hợp nhất bằng cách tải lại, không ghi đè mù.
3. **Admin override là đường ghi riêng**, không tham gia luật trên: override áp trực tiếp lên bản server hiện tại, tăng `version`, ghi audit; client của giám khảo liên quan nhận sự kiện cập nhật và phải tải lại phiếu (nháp local dựa trên version cũ bị đánh dấu xung đột nếu còn tồn tại).
4. Mọi trường hợp xung đột đều được ghi nhận (audit hoặc log đồng bộ) — không có xung đột nào bị nuốt trong im lặng.

### 10.4. Đồng bộ lại

- Khi kết nối trở lại, client tự gửi outbox theo thứ tự thời gian, sau đó tải lại trạng thái chính thức của các performance liên quan và đối chiếu `version`.
- Giám khảo được thông báo tóm tắt sau khi đồng bộ lại nếu có mục nào xung đột; các mục đồng bộ thành công không cần thông báo.

**Tiêu chí nghiệm thu (Mục 10):** gửi cùng một yêu cầu nộp phiếu 3 lần liên tiếp (mô phỏng sync lặp) chỉ tạo đúng 1 phiếu trên server; thiết bị có nháp local trong khi Admin đã khóa performance — khi nối mạng lại, điểm chính thức là bản server đã khóa, nháp local không ghi đè được và giám khảo thấy thông báo xung đột.

---

## 11. Backup và Restore

### 11.1. Backup an toàn (sửa lỗi của v1)

V1 viết "tải trực tiếp file `contest.db` về máy" — **không được copy file db đang sống** trong lúc hệ thống đang ghi, vì có thể nhận file hỏng/thiếu dữ liệu trong WAL. Quy trình đúng ở v2:

- Mọi bản backup đều là **snapshot do server tạo bằng `VACUUM INTO`** (SQLite) ra một file riêng trong thư mục `backups/`, đã nhất quán tại một thời điểm, rồi mới cho Admin tải xuống hoặc lưu tại chỗ.
- Tên file backup chứa tên contest + mốc thời gian server, ví dụ `backup-contest-3-20261109-143005.db`.
- **Backup thủ công:** nút "Tải file Backup" trên Admin Dashboard — tạo snapshot mới rồi tải.
- **Auto-backup:** hệ thống tự tạo snapshot sau mỗi lần một performance chuyển sang `locked` (kết thúc một đội), và một snapshot khi contest chuyển sang `completed`. Giữ lại tối thiểu các snapshot của sự kiện hiện tại trong thư mục `backups/`; việc dọn dẹp chỉ do Admin làm thủ công ngoài giờ thi.
- Mỗi lần tạo backup ghi một dòng trong bảng `backups` (đường dẫn, kích thước, thời điểm, loại: thủ công/tự động/trước-khi-restore).

### 11.2. Restore

- Restore chỉ thực hiện từ màn hình Admin, chọn một snapshot trong danh sách hoặc tải lên một file backup hợp lệ.
- Trước khi ghi đè dữ liệu hiện tại, hệ thống **tự tạo một snapshot "trước khi restore"** của dữ liệu hiện tại, để luôn quay lui được nếu chọn nhầm file.
- Restore yêu cầu Admin nhập xác nhận (gõ lại tên contest hoặc mật khẩu Admin — chọn một, chốt khi code) và ghi audit.
- Sau restore, server yêu cầu mọi client tải lại trạng thái (phát sự kiện `contest.state` mới); phiên chấm đang dở trên client bị đánh dấu cần đối chiếu theo luật xung đột Mục 10.3.
- Restore không thực hiện được trong lúc có performance đang `performing` — Admin phải kết thúc/tạm dừng phần thi hiện tại trước (hệ thống chặn và báo rõ).

**Tiêu chí nghiệm thu (Mục 11):** tạo backup giữa lúc nhiều giám khảo đang nộp điểm — file backup mở được độc lập và chứa đủ các phiếu đã nộp trước thời điểm backup; restore về một snapshot cũ đưa dữ liệu về đúng trạng thái snapshot đó, đồng thời tồn tại snapshot "trước khi restore" của dữ liệu vừa bị thay thế; nút restore bị chặn khi đang có đội thi.

---

## 12. Triển khai tại hiện trường (LAN Deployment)

- **Máy chủ là máy Admin:** chạy file binary trên laptop của BTC, nối vào mạng LAN của sự kiện (router/switch riêng của BTC được khuyến nghị, không phụ thuộc Wi-Fi chung của địa điểm).
- **Địa chỉ truy cập:** khi khởi động, server in ra màn hình console và trên Admin Dashboard: địa chỉ LAN (ví dụ `http://192.168.1.10:8080`), kèm **QR code** để giám khảo và MC quét bằng camera tablet/điện thoại là vào đúng địa chỉ, không phải gõ IP. QR code cũng chứa join code của contest đang active (Mục 6.1.1) để thiết bị vào thẳng đúng cuộc thi.
- **Không cần internet:** toàn bộ tài nguyên (SPA, font, hiệu ứng) phục vụ từ server nội bộ; hệ thống không gọi bất kỳ dịch vụ ngoài nào trong lúc vận hành.
- **Hướng dẫn vận hành tối thiểu** (đi kèm repo, viết trong README hoặc tài liệu vận hành Phase 1): cắm mạng → chạy binary → kiểm tra địa chỉ LAN → mở Public View → giám khảo quét QR → chạy checklist kích hoạt → bắt đầu.
- Cổng lắng nghe cấu hình được (mặc định 8080); nếu cổng bận, server báo lỗi rõ ràng thay vì tắt lặng.
- Giám khảo dùng trình duyệt hiện đại trên tablet/điện thoại/máy tính; giao diện responsive, ưu tiên tablet.

**Tiêu chí nghiệm thu (Mục 12):** trên một mạng LAN không có internet, một tablet chưa từng truy cập hệ thống quét QR và vào được đúng contest đang active trong tối đa 3 thao tác; toàn bộ luồng chấm một đội (bắt đầu → chấm → nộp → khóa → reveal) chạy hoàn toàn không cần internet.

---

## 13. Bảo mật

Phạm vi bảo mật tương xứng với bối cảnh LAN tại hiện trường (không phải hệ thống công khai trên internet), nhưng các điểm sau là bắt buộc:

- **Mật khẩu và PIN được băm** (bcrypt hoặc argon2id — chọn một khi code) kèm salt riêng; tuyệt đối không lưu mật khẩu/PIN dạng rõ ở bất kỳ đâu, kể cả log và audit.
- **Giới hạn đăng nhập sai:** sau 5 lần đăng nhập sai liên tiếp cho một tài khoản, khóa đăng nhập của tài khoản đó 15 phút (thông số cấu hình được ở `settings`); Admin mở khóa sớm được từ màn hình quản trị (đây là đường xử lý khi giám khảo quên PIN: reset PIN theo Mục 6.1.2 thay vì chờ).
- **Session:** đăng nhập thành công cấp session token qua cookie `HttpOnly`; session hết hạn sau 12 giờ (bao trọn một ngày sự kiện) hoặc khi Admin thu hồi; đăng xuất xóa session phía server.
- **Phân quyền kiểm tra ở server** cho mọi API ghi dữ liệu (không chỉ ẩn nút ở frontend) — đặc biệt: ghi điểm phải có assignment, tiêu chí Admin-Only không trả cho judge, điểm chưa reveal không trả cho role `mc`.
- **Audit log bất biến** ở tầng ứng dụng (Mục 6.6.3): không API sửa/xóa.
- File backup chứa toàn bộ dữ liệu điểm — Admin được nhắc trên giao diện backup rằng file này là hồ sơ nhạy cảm, cần lưu giữ như biên bản thi.

**Tiêu chí nghiệm thu (Mục 13):** kiểm tra trực tiếp file db không tìm thấy mật khẩu/PIN dạng rõ của bất kỳ tài khoản nào; nhập sai PIN 5 lần liên tiếp bị khóa đúng thời gian cấu hình và Admin mở khóa/reset được; tài khoản judge gọi API override điểm bị từ chối 403.

---

## 14. Yêu cầu phi chức năng (Non-functional Requirements)

| Nhóm | Yêu cầu |
|---|---|
| Hiệu năng | Quy mô thiết kế: 1 contest ≤ 20 giám khảo, ≤ 100 đội, ≤ 20 tiêu chí/đội, ≤ 10 round. Thao tác nộp điểm phản hồi < 1 giây trên LAN; Live Matrix cập nhật < 1 giây |
| Độ tin cậy | Mất điện/rớt mạng ở client không mất điểm đã nộp lên server và không mất nháp local; server khởi động lại tự phục vụ tiếp từ file db hiện tại, timer của performance đang `performing` được khôi phục theo mốc giờ server đã lưu (thời gian server tắt vẫn tính là thời gian thi trừ phần đã ghi nhận tạm dừng — Admin được cảnh báo khi khôi phục để quyết định tạm dừng bù nếu cần) |
| Tính sẵn sàng vận hành | Một người không biết kỹ thuật (được hướng dẫn 15 phút) khởi động được hệ thống theo hướng dẫn Mục 12 |
| Tương thích | Trình duyệt hiện đại (Chrome/Edge/Safari/Firefox bản ổn định gần nhất) trên Windows/macOS/Android/iOS; giao diện dùng tốt ở bề rộng 360px (điện thoại) tới màn hình LED lớn |
| Dữ liệu | Toàn bộ dữ liệu sự kiện nằm trong 1 file SQLite + thư mục `backups/`; chuyển máy dự phòng = mang binary + file db + backups sang máy khác và chạy |
| Quan sát lỗi | Server ghi log vận hành ra console/file log (không chứa mật khẩu/PIN/điểm chi tiết ở mức log thường); lỗi API trả về theo format thống nhất ở API.md Mục 3 |
| Khả năng kiểm thử | Mọi công thức tính điểm (Mục 6.3.4, 6.4.3, 6.6.5) phải kiểm chứng được bằng test tự động với các ví dụ số đã nêu trong tài liệu này |

---

## 15. Roadmap

### Phase 1 — MVP (phạm vi của PRD này)

Toàn bộ Mục 6 (7 module), Mục 10 (offline & sync), Mục 11 (backup/restore), Mục 12 (LAN + QR), Mục 13 (bảo mật cơ bản). Thứ tự triển khai đề xuất ở README.md.

### Phase 2 — Sau khi MVP vận hành ổn định ở ít nhất một sự kiện thật

- Thêm ngôn ngữ thứ ba trở đi (đổi sang bảng bản dịch riêng).
- Nhiều sự kiện đồng thời trên một server / multi-site qua internet (đồng bộ đám mây tùy chọn).
- Ứng dụng native hoặc PWA cài đặt đầy đủ có thông báo đẩy.
- Mẫu hiệu ứng vinh danh và giao diện Public View tùy biến theo bộ nhận diện của BTC.
- Nhập đội từ CSV/Google Sheets, xuất PDF biên bản kết quả có chữ ký số.
- Thống kê sau sự kiện: phân bố điểm theo giám khảo, độ lệch giữa các giám khảo, báo cáo cho từng đội.

Các hạng mục Phase 2 **không** được làm tắt trong Phase 1 và không ảnh hưởng schema/API của MVP ngoài các điểm đã ghi chú rõ là "Phase 2" trong DATABASE.md và API.md.

---

## 16. Quyết định thiết kế đã chốt trong v2 (so với v1)

| # | Vấn đề ở v1 | Quyết định ở v2 |
|---|---|---|
| 1 | Multiplier là một "loại tiêu chí" | Multiplier là thuộc tính `weight` của mọi tiêu chí; cách nhập chỉ còn Range/Discrete (Mục 6.3.1) |
| 2 | Không có module đội/vòng thi | Thêm Module 2: Teams & Rounds, import Excel, SBD, bốc thăm (Mục 6.2) |
| 3 | Auto-Penalty tự trừ thẳng vào điểm | Đề xuất trừ → Admin duyệt/sửa/từ chối có lý do → mới áp vào tiêu chí Admin-Only; có trần trừ (Mục 6.4.3) |
| 4 | Backup = tải trực tiếp file db đang sống | Snapshot bằng `VACUUM INTO`, auto-backup khi khóa mỗi đội, restore có snapshot bảo vệ + xác nhận (Mục 11) |
| 5 | Offline nói chung chung "lưu LocalStorage" | IndexedDB + outbox, `client_uuid` chống nộp trùng, `version` phiếu và luật xung đột 4 điều (Mục 10) |
| 6 | Leaderboard realtime, không nói về lộ điểm | Reveal là cờ riêng cho từng performance và cho bảng xếp hạng, chặn ở cả API (Mục 6.7.2) |
| 7 | Không có trạng thái đầy đủ cho đội/phần thi | State machine 5 trạng thái cho performance + trạng thái phiếu điểm + trạng thái contest có chốt sổ (Mục 4) |
| 8 | Bỏ điểm cao/thấp nhất không có điều kiện | Chỉ áp khi số phiếu ≥ `min_judges_for_trim` (mặc định 5, tối thiểu 4), ghi rõ khi không áp dụng (Mục 6.6.5) |
| 9 | Không khóa cấu hình khi đang thi | Khóa toàn bộ cấu hình chấm khi contest `active`; mở lại chỉ khi chưa có điểm nộp (Mục 6.3.5) |
| 10 | Vào hệ thống bằng địa chỉ IP tự gõ | QR code + join code trên máy Admin (Mục 12) |
| 11 | Không nhắc đồng hồ server/WAL | Giờ server làm chuẩn cho timer và audit; SQLite bắt buộc WAL mode + ghi qua 1 kết nối (Mục 3.2, 3.4) |
