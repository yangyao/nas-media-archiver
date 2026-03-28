# archive Usage Guide

## 1. 目的

`archive` 是一個直接運行在 NAS 或其他 Linux 主機上的單機 Go CLI，用來把輸入目錄中的照片和視頻歸檔到指定輸出目錄。

它的特點是：

- 不需要 Web 前端
- 不需要常駐 server
- 直接在 NAS 本地執行
- 支持 dry run、狀態查詢、失敗重試與結果導出

---

## 2. 文件位置

建議自行決定部署位置，例如：

- 二進制：`./archive`
- 狀態目錄：`/path/to/archive-state`

本地開發輸出：

- 本機可執行文件：`archive`
- Linux ARM64 二進制：`dist/archive-linux-arm64`

---

## 3. 部署

從本地重新編譯：

```bash
go build -o archive .
make build-linux-arm64
```

上傳到 NAS：

```bash
scp dist/archive-linux-arm64 user@your-nas:/path/to/archive
ssh user@your-nas 'chmod +x /path/to/archive'
```

---

## 4. 基本命令

### 掃描目錄

```bash
./archive scan \
  --path /path/to/input \
  --state-dir /path/to/archive-state
```

輸出示例：

```text
Job: job-20260327-163345
Files: 721
Status: created
ScannedAt: 2026-03-27T16:33:45+08:00
```

### 查詢所有 job

```bash
./archive jobs \
  --state-dir /path/to/archive-state
```

### 查詢單個 job 狀態

```bash
./archive status \
  --job job-20260327-163345 \
  --state-dir /path/to/archive-state
```

### 查看文件列表

```bash
./archive files \
  --job job-20260327-163345 \
  --state-dir /path/to/archive-state
```

只看失敗文件：

```bash
./archive files \
  --job job-20260327-163345 \
  --state-dir /path/to/archive-state \
  --status failed
```

### 觀察事件日誌

```bash
./archive watch \
  --job job-20260327-163345 \
  --state-dir /path/to/archive-state
```

---

## 5. dry run

正式歸檔前，建議先 dry run。

```bash
./archive run \
  --job job-20260327-163345 \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state \
  --dry-run
```

dry run 會做：

- 時間提取
- 目標路徑規劃
- 衝突檢查
- 事件和狀態記錄

dry run 不會做：

- 創建目標文件
- 移動源文件

---

## 6. 真實歸檔

### 推薦方式

QNAP 的 ACL 和權限模型比較特殊，真實寫入共享目錄時，建議用 `admin` 權限執行。

```bash
sudo -u admin \
  ./archive run \
  --job job-20260327-163345 \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state
```

### 說明

程序已支持跨文件系統安全移動：

- 如果 `rename` 成功，直接完成
- 如果 `rename` 因跨設備失敗，則會走：
  - 複製到目標臨時文件
  - 落盤
  - rename 到正式目標
  - 刪除源文件

---

## 7. 推薦性能參數

根據目前在 QNAP 上的 dry run 基準，推薦：

```bash
--workers 8
--snapshot-every 100
```

示例：

```bash
./archive run \
  --job job-20260327-163345 \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state \
  --dry-run \
  --workers 8 \
  --snapshot-every 100
```

說明：

- `--workers` 控制 metadata/plan 並發
- `--snapshot-every` 控制每處理多少個文件寫一次 `job.json`

---

## 8. 失敗重試

把失敗文件重置為可重新處理狀態：

```bash
./archive retry \
  --job job-20260327-163345 \
  --state-dir /path/to/archive-state
```

重試後再執行：

```bash
./archive run \
  --job job-20260327-163345 \
  --archive-base /path/to/archive \
  --state-dir /path/to/archive-state
```

---

## 9. 導出報告

導出失敗文件為 CSV：

```bash
./archive export \
  --job job-20260327-163345 \
  --state-dir /path/to/archive-state \
  --status failed \
  --format csv \
  --output failed.csv
```

導出為 JSON：

```bash
./archive export \
  --job job-20260327-163345 \
  --state-dir /path/to/archive-state \
  --status failed \
  --format json \
  --output failed.json
```

---

## 10. 日常操作建議

建議每次按下面流程跑：

1. 先 `scan`
2. 再 `run --dry-run`
3. 看 `status`、`files --status failed`
4. 確認目標路徑和時間來源沒問題
5. 再用 `sudo -u admin` 跑真實歸檔
6. 最後導出結果或錯誤列表

---

## 11. 文檔對照

- 架構與設計原則：`docs/design.md`
- 性能結論與最佳參數：`docs/benchmarks.md`
- 實際歸檔樣本測試：`docs/archive-run-report.md`
