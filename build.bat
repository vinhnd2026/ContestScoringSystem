@echo off
chcp 65001 >nul
rem ============================================================================
rem  Contest Scoring System — Build OFFLINE bằng thư viện đóng kèm (vendor)
rem  Toàn bộ thư viện Go nằm sẵn trong thư mục vendor\ của dự án,
rem  nên build KHÔNG cần internet và KHÔNG cần go.sum.
rem  Yêu cầu duy nhất: máy đã cài Go >= 1.23.
rem ============================================================================
setlocal
set GOFLAGS=-mod=vendor
set GOPROXY=off
go build -o contest-scoring.exe .
if errorlevel 1 (
    echo [LỖI] Build thất bại — xem thông báo lỗi ở trên.
    pause
    exit /b 1
)
echo [OK] Đã build xong: contest-scoring.exe
echo Chạy chương trình:  contest-scoring.exe -port 8080 -db contest.db
pause
endlocal
