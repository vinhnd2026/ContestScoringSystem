#!/usr/bin/env bash
# ============================================================================
#  Contest Scoring System — Build OFFLINE bằng thư viện đóng kèm (vendor)
#  Toàn bộ thư viện Go nằm sẵn trong thư mục vendor/ của dự án,
#  nên build KHÔNG cần internet và KHÔNG cần go.sum.
#  Yêu cầu duy nhất: máy đã cài Go >= 1.23.
# ============================================================================
set -e
cd "$(dirname "$0")"
export GOFLAGS=-mod=vendor
export GOPROXY=off
go build -o contest-scoring .
echo "[OK] Đã build xong: contest-scoring"
echo "Chạy chương trình:  ./contest-scoring -port 8080 -db contest.db"
