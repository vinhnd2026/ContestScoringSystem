# Contest Scoring System — Hệ thống chấm điểm cuộc thi

Hệ thống chấm điểm cuộc thi **chạy offline trên mạng LAN tại hiện trường**: một máy của Ban tổ chức chạy toàn bộ hệ thống (backend Go đóng gói thành 1 file binary + database SQLite), giám khảo chấm điểm bằng tablet/điện thoại theo thời gian thực, điểm được điều hành, khóa và công bố ngay trong sự kiện — **không cần internet**.

> Trạng thái tài liệu: **PRD v2 — bản hoàn chỉnh** (thay thế bản ý tưởng v1). **Chương trình MVP Phase 1 đã được viết và kiểm chứng chạy thật** — xem mục "Chương trình (code)" ngay dưới đây; bộ tài liệu thiết kế vẫn là đặc tả chuẩn cho mọi thay đổi tiếp theo.

## Chương trình (code) — MVP Phase 1

Mã nguồn nằm cùng cấp với tài liệu trong repo này: backend Go (9 file `*.go`), frontend Vue 3 SPA trong `web/` (Vue được vendor tại `web/vendor/vue.global.prod.js` để chạy offline hoàn toàn), toàn bộ web assets được `go:embed` vào binary.

### Cách build và chạy

```bash
# Cần Go >= 1.23 (không cần CGO)
go build -o contest-scoring .
./contest-scoring -port 8080 -db contest.db
# Mở http://localhost:8080 — trên LAN dùng địa chỉ server in ra ở console
```

Binary tự tạo file SQLite (WAL) + thư mục `backups/` cạnh file db, tự seed dữ liệu mẫu khi DB trống.

### Build offline bằng thư viện đóng kèm (vendor)

Thư mục `vendor/` trong dự án chứa **toàn bộ thư viện Go** mà chương trình cần (SQLite thuần Go, WebSocket, bcrypt…), đóng gói bằng `go mod vendor`. Nhờ vậy việc build **không cần internet**: máy build không tải được thư viện từ proxy Go vẫn build bình thường, và khi build bằng vendor thì **không cần tới `go.sum`** (Go đối chiếu thư viện theo `vendor/modules.txt`).

Cách build offline:

- **Windows:** chạy `build.bat` — file này tự đặt `GOFLAGS=-mod=vendor` và `GOPROXY=off` rồi `go build -o contest-scoring.exe .`
- **Linux/macOS:** chạy `./build.sh` — tương tự, cho ra file `contest-scoring`.
- **Gõ tay:** `go build -mod=vendor .` ngay trong thư mục dự án.

Yêu cầu duy nhất còn lại là máy đã cài sẵn Go >= 1.23. Lưu ý: đừng xóa hay sửa tay thư mục `vendor/` và file `vendor/modules.txt` — chúng phải khớp với `go.mod` thì Go mới cho build bằng vendor.

### Tài khoản mẫu (seed khi DB trống) — PHẢI đổi trước khi thi thật

| Tài khoản | Mật khẩu/PIN | Vai trò |
|---|---|---|
| `admin` | `admin123` | Admin / Trưởng ban |
| `judge1` / `judge2` / `judge3` | `1111` / `2222` / `3333` | Giám khảo (đăng nhập bằng PIN cũng là mật khẩu) |
| `mc` | `mc123` | MC / màn hình khán giả |

Kèm 1 contest mẫu ở trạng thái `pending`: 1 vòng chung kết, 4 đội, nhóm tiêu chí mẫu (Range 0–10; Discrete có weight ×2; 1 tiêu chí Admin-Only; 1 tiêu chí trừ giờ), phân công all-to-all cho 3 giám khảo — mở lên là tổng duyệt (Dry Run) được ngay. Tổng điểm tối đa của contest mẫu: 40.

### Phạm vi đã làm (đã kiểm chứng đầu-cuối bằng server thật)

- Đăng nhập/session cookie HttpOnly, 3 vai trò, khóa đăng nhập sau 5 lần sai, Admin tạo/vô hiệu hóa/reset mật khẩu-PIN (ghi audit), đổi mật khẩu của chính mình.
- Contest CRUD + state machine `pending → active → completed` + chốt sổ/mở chốt (có lý do), checklist kích hoạt, khóa cấu hình khi active, Clone (cấu hình, không điểm), Reset điểm.
- Round/Team CRUD (SBD duy nhất trong contest, tạo team tự sinh performance `pending`), **import đội bằng CSV** có báo lỗi theo dòng, bốc thăm thứ tự thi.
- Criteria Engine: nhóm + tiêu chí `range`/`discrete`, `weight` là thuộc tính, options cho discrete, `total_max_score = Σ(max × weight)` (không tính Admin-Only) hiển thị trực tiếp.
- Assignment matrix: all-to-all và gán chọn lọc.
- Flow control + timer server: bắt đầu/tạm dừng/tiếp tục/kết thúc, đếm lố giờ, đề xuất trừ điểm theo công thức khoảng lố có trần — Admin duyệt/sửa/từ chối có lý do mới áp vào tiêu chí trừ giờ; khóa điểm (khóa thiếu phải có lý do), mở khóa, reveal/unreveal từng đội và reveal bảng xếp hạng riêng.
- Judge Workspace: tự đồng bộ đội đang thi (WebSocket + poll), chỉ giám khảo được phân công mới ghi điểm được (API trả `403 NOT_ASSIGNED`), nháp IndexedDB theo giám khảo × performance + đồng bộ draft lên server, nộp/sửa điểm (sửa sau nộp bắt buộc lý do, ghi audit), progress tracker, chống nộp trùng bằng `client_uuid` + optimistic locking `version` (xung đột trả `409`).
- Admin Dashboard: Live Matrix realtime, xem chi tiết phiếu, override có lý do (giữ giá trị cũ trong audit), audit log append-only có lọc/phân trang.
- Leaderboard theo round đúng PRD 6.6.5: trung bình phiếu hợp lệ, bỏ cao nhất/thấp nhất chỉ khi đủ `min_judges_for_trim` (mặc định 5, tối thiểu 4) và có ghi chú khi không áp dụng, cộng Admin-Only một lần, highlight đồng điểm, tie-breaker bằng tiêu chí cấu hình hoặc Admin chốt thủ công có lý do.
- Public/MC View: chỉ thấy timer, đội đang thi và điểm/BXH **đã reveal** — chặn ở API (`public-state`), không chỉ ẩn giao diện.
- Backup bằng snapshot `VACUUM INTO` + tải xuống; restore từ file tải lên (tự tạo snapshot `pre_restore` trước, chặn khi đang có đội `performing`); auto-backup khi khóa điểm và khi kết thúc contest. Xuất Excel `.xlsx` thật gồm 4 sheet (Tổng hợp, Chi tiết, Admin-Only, Audit).

### Để Phase 2 (chưa làm, theo đúng PRD)

- **Import đội bằng Excel `.xlsx`** (Phase 1 dùng CSV + có file mẫu template .xlsx để tải xuống), xuất PDF biên bản, QR code join trên dashboard (hiện server in địa chỉ LAN ra console), PWA/native, ngôn ngữ thứ ba, multi-site, thống kê sau sự kiện.

## Tóm tắt dự án

| Hạng mục | Giá trị |
|---|---|
| Backend | Go — build ra 1 file binary duy nhất, không cần cài runtime |
| Database | SQLite (WAL mode), một file `contest.db`, backup bằng snapshot |
| Frontend | SPA (React hoặc Vue), responsive, ưu tiên tablet cho giám khảo |
| Realtime | WebSocket — đồng bộ trạng thái thi, đồng hồ (giờ server làm chuẩn), ma trận điểm, công bố |
| Offline | Nháp điểm lưu IndexedDB trên thiết bị giám khảo, tự đồng bộ lại khi có mạng, chống nộp trùng |
| Ngôn ngữ hiển thị | Song ngữ Việt–Anh, chuyển đổi ngay trên từng thiết bị |
| Triển khai | Máy Admin làm server LAN; giám khảo quét QR code để vào, không gõ địa chỉ IP |

**Bài toán cốt lõi mà hệ thống giải quyết khác với các hệ thống chấm điểm online:** hội trường thi thường không có internet ổn định. Hệ thống này được thiết kế để mất internet hoàn toàn vẫn thi bình thường, mất kết nối tạm thời trên một thiết bị giám khảo vẫn không mất điểm, và mọi lần sửa điểm đều có vết để bảo vệ tính minh bạch của kết quả.

## Tính năng chính (theo PRD v2)

1. **Quản trị & Setup** — quản lý cuộc thi, tài khoản 3 vai trò (Admin / Giám khảo / MC), reset PIN tại hiện trường, nhân bản cấu hình, reset điểm, tổng duyệt Dry Run, checklist chặn kích hoạt khi cấu hình chưa đạt.
2. **Đội thi & Vòng thi** — CRUD đội, import Excel có báo cáo lỗi theo dòng, số báo danh, sắp xếp thứ tự thi và bốc thăm ngẫu nhiên, nhiều vòng thi/hạng mục.
3. **Bộ máy tiêu chí** — tiêu chí nhập theo khoảng (Range) hoặc mức định sẵn (Discrete); trọng số là thuộc tính của mọi tiêu chí; nhóm tiêu chí có hiển thị tổng trọng số và tổng điểm tối đa tự tính; tiêu chí Admin-Only cho điểm phạt/bonus; toàn bộ cấu hình chấm bị khóa khi cuộc thi đang diễn ra.
4. **Kiểm soát thời gian** — đếm lùi/tạm dừng/tiếp tục, đếm lố giờ, cảnh báo mốc; điểm trừ quá giờ được hệ thống đề xuất theo luật đã cấu hình (có trần trừ) và **chỉ áp dụng khi Admin duyệt**, mọi quyết định đều ghi log.
5. **Không gian giám khảo** — tự đồng bộ theo đội đang thi, chỉ chấm được đội được phân công, nháp lưu theo từng đội kể cả khi mất mạng, trạng thái đồng bộ luôn nhìn thấy, sửa điểm sau khi nộp phải có lý do.
6. **Bảng điều hành Admin** — ma trận đội × giám khảo cập nhật realtime, điều khiển luồng thi (bắt đầu → kết thúc → khóa điểm → công bố), override điểm có lý do, audit trail bất biến, bảng xếp hạng, xử lý đồng điểm, xuất Excel 4 sheet.
7. **Màn hình MC/khán giả** — đồng hồ toàn màn hình cho máy chiếu/LED, đội đang thi, và công bố điểm/vinh danh **chỉ khi Admin bật reveal** cho đúng nội dung đó.

## Tài liệu trong repo

| File | Nội dung | Ai nên đọc |
|---|---|---|
| [PRD.md](PRD.md) | Tài liệu yêu cầu sản phẩm hoàn chỉnh: mục tiêu, phạm vi MVP, kiến trúc, state machine, 7 module chức năng, offline & sync, backup/restore, bảo mật, triển khai LAN, yêu cầu phi chức năng, tiêu chí nghiệm thu từng module, roadmap | Mọi người — đây là tài liệu gốc |
| [DATABASE.md](DATABASE.md) | Schema SQLite đầy đủ: 15 bảng, cột, khóa ngoại, index, quy tắc toàn vẹn, WAL mode, versioning và khóa chống nộp trùng | Lập trình viên backend |
| [API.md](API.md) | REST endpoints theo module, WebSocket events hai chiều, format lỗi thống nhất, cơ chế idempotency cho việc nộp điểm | Lập trình viên backend + frontend |

### Cách đọc

- **Lần đầu:** đọc PRD.md Mục 1–5 (tổng quan, phân quyền, kiến trúc, state machine, luồng nghiệp vụ) để nắm toàn cảnh, sau đó đọc module mình phụ trách ở Mục 6 và các mục xuyên suốt 10–13.
- **Khi code backend:** PRD.md + DATABASE.md + API.md đọc song song; các công thức tính điểm nằm ở PRD Mục 6.3.4 (tổng điểm tối đa), 6.4.3 (trừ điểm quá giờ) và 6.6.5 (điểm cuối của đội) — cả ba đều có ví dụ số để viết test.
- **Khi code frontend:** PRD Mục 6.5 (không gian giám khảo), 6.6 (dashboard), 6.7 (public view) + API.md Mục 5 (WebSocket) và Mục 6.7–6.9.
- **Khi kiểm thử:** tiêu chí nghiệm thu ở cuối mỗi module trong PRD.md là checklist kiểm thử chấp nhận.

## Thứ tự triển khai đề xuất (Phase 1 — MVP)

1. Khung server Go + SQLite WAL + migration + auth/session + phân quyền.
2. Contests, Rounds, Teams (CRUD + import Excel) và Criteria Engine kèm validation tổng điểm.
3. Assignments (ma trận phân công) + checklist kích hoạt + khóa cấu hình.
4. Flow control + Timer engine (giờ server) + WebSocket (`contest.state`, `timer.sync`).
5. Judge Workspace: form chấm, IndexedDB nháp/outbox, nộp điểm idempotent, luật xung đột.
6. Admin Dashboard: Live Matrix, khóa/mở khóa, override + audit log.
7. Tính điểm cuối, Leaderboard, tie-breaker, xuất Excel.
8. Public/MC View + cơ chế reveal.
9. Backup/Restore (`VACUUM INTO`, auto-backup khi khóa điểm) + QR join LAN + tài liệu vận hành hiện trường.
10. Kiểm thử theo toàn bộ tiêu chí nghiệm thu trong PRD.md, chạy thử một sự kiện Dry Run trọn vẹn trước khi dùng thật.

Phase 2 (sau khi MVP chạy ổn định ở ít nhất một sự kiện thật): thêm ngôn ngữ thứ ba, nhiều sự kiện đồng thời/multi-site, PWA/native, xuất PDF biên bản, thống kê sau sự kiện — chi tiết ở PRD.md Mục 15.

## Nguyên tắc không được phá khi triển khai

- Server là nguồn sự thật duy nhất; giờ server làm chuẩn cho mọi mốc thi.
- Không sửa cấu hình chấm khi cuộc thi đang `active`.
- Không tự trừ điểm quá giờ khi chưa có Admin duyệt.
- Không copy trực tiếp file database đang chạy để backup — luôn dùng snapshot.
- Điểm chưa được Admin reveal thì không xuất hiện ở bất kỳ đâu phía khán giả, kể cả trong dữ liệu API trả về.
- Mọi sửa điểm sau khi nộp, override, mở khóa đều bắt buộc có lý do và ghi audit.

---

*Tài liệu được viết và chốt ở phiên bản v2 dựa trên bản ý tưởng v1 cùng phần rà soát thiết kế: sửa mô hình tiêu chí (trọng số là thuộc tính, không phải một loại tiêu chí), bổ sung module Đội thi & Vòng thi, quy trình duyệt trừ điểm quá giờ, backup bằng snapshot và restore an toàn, luật đồng bộ offline chống nộp trùng/xung đột, cơ chế reveal cho màn hình khán giả và QR code tham gia qua LAN.*
