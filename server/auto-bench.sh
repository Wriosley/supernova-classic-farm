#!/bin/bash
# 自动化压测 + 火焰图采集脚本
# 用法: ./auto-bench.sh <scenario> <concurrency_list> [run_id]
# 例如: ./auto-bench.sh snapshot 1,10,50,100,200,500
#       ./auto-bench.sh snapshot 1,10,50,100,200,500 20260817_071649

set -euo pipefail

cd /data/workspace/supernova-classic-farm/server

SCENARIO="${1:-snapshot}"
CONCURRENCY="${2:-1,10,50,100,200,500}"
RUN_ID="${3:-$(echo ${SCENARIO}$(date +%H%M%S))}"
PPROF_DIR="/data/workspace/supernova-classic-farm/result/pprof-${RUN_ID}"
LOG="/tmp/bench-${RUN_ID}.log"

GO=/usr/local/go/bin/go
GATE_PPROF="http://127.0.0.1:6060/internal/debug/pprof"
ZONE_PPROF="http://127.0.0.1:6061/internal/debug/pprof"

mkdir -p "$PPROF_DIR"

# --- 函数定义 ---
capture_profile() {
  local label="$1"
  local ts=$(date +%H%M%S)

  for target in "gate:$GATE_PPROF" "zone:$ZONE_PPROF"; do
    local name="${target%%:*}"
    local url="${target##*:}"

    # CPU (30秒采样，与压测并行)
    $GO tool pprof -seconds=30 -png \
      -output "$PPROF_DIR/${label}_${ts}_${name}_cpu.png" \
      "$url/profile" > /dev/null 2>&1 &

    # Heap (即时快照)
    $GO tool pprof -png \
      -output "$PPROF_DIR/${label}_${ts}_${name}_heap.png" \
      "$url/heap" > /dev/null 2>&1 &

    # Goroutine (即时快照)
    $GO tool pprof -png \
      -output "$PPROF_DIR/${label}_${ts}_${name}_goroutine.png" \
      "$url/goroutine" > /dev/null 2>&1 &

    # 原始数据 (供后续交互分析)
    $GO tool pprof -seconds=30 \
      -output "$PPROF_DIR/${label}_${ts}_${name}_cpu.pb.gz" \
      "$url/profile" > /dev/null 2>&1 &
    $GO tool pprof \
      -output "$PPROF_DIR/${label}_${ts}_${name}_heap.pb.gz" \
      "$url/heap" > /dev/null 2>&1 &
  done

  wait
  echo "  [$label] 采集完成"
}

echo "=========================================="
echo " 自动压测 + 火焰图采集"
echo " 场景: $SCENARIO"
echo " 并发: $CONCURRENCY"
echo " RunID: $RUN_ID"
echo " 火焰图: $PPROF_DIR"
echo "=========================================="

# --- 1. 确保 pprof 端口转发就绪 ---
echo "[1/4] 检查 pprof 端口转发..."
if ! curl -sS -m 2 "$GATE_PPROF/" -o /dev/null 2>/dev/null; then
  echo "  建立 gate pprof 端口转发..."
  nohup kubectl -n classic-farm port-forward pod/gate-0 6060:8081 > /tmp/pf-gate-pprof.log 2>&1 &
  sleep 3
fi
if ! curl -sS -m 2 "$ZONE_PPROF/" -o /dev/null 2>/dev/null; then
  echo "  建立 zone pprof 端口转发..."
  nohup kubectl -n classic-farm port-forward pod/zone-pool-0 6061:8082 > /tmp/pf-zone-pprof.log 2>&1 &
  sleep 3
fi
echo "  pprof 就绪 ✓"

# --- 2. 采集压测前基线 ---
echo "[2/4] 采集基线火焰图..."
capture_profile "baseline"
echo "  基线采集完成 ✓"

# --- 3. 启动 benchrunner (后台) ---
echo "[3/4] 启动 benchrunner..."
nohup ./benchrunner \
  -scenario "$SCENARIO" \
  -concurrency "$CONCURRENCY" \
  -warmup 10s \
  -duration 60s \
  -run-id "$RUN_ID" \
  -login-url http://127.0.0.1:8080 \
  -gate-url ws://127.0.0.1:8081/ws \
  -connect-workers 64 \
  > "$LOG" 2>&1 &

BENCH_PID=$!
echo "  benchrunner PID: $BENCH_PID"
echo "  日志: $LOG"

# --- 4. 监控 benchrunner，在高并发档位自动采集 ---
echo "[4/4] 监控压测进度，自动采集火焰图..."
LAST_LINE=""
while kill -0 $BENCH_PID 2>/dev/null; do
  CURRENT_LINE=$(tail -1 "$LOG" 2>/dev/null)

  # 检测新档位开始 (含 concurrency= 字样)
  if [[ "$CURRENT_LINE" == *"concurrency="* && "$CURRENT_LINE" != "$LAST_LINE" ]]; then
    LAST_LINE="$CURRENT_LINE"
    # 提取并发数
    CONC=$(echo "$CURRENT_LINE" | grep -oP 'concurrency=\K[0-9]+')

    # 并发 >= 100 时自动采集 CPU 火焰图 (30秒采样)
    if [[ -n "$CONC" && "$CONC" -ge 100 ]]; then
      echo "  >>> 并发 $CONC 触发火焰图采集..."
      capture_profile "c${CONC}" &
    fi
  fi

  sleep 5
done

echo ""
echo "=== 压测完成 ==="
# 采集压测后火焰图
capture_profile "after"
echo ""
echo "=========================================="
echo " 压测结果: $LOG"
echo " summary: $(dirname $(dirname $0))/../benchmark/results/${RUN_ID}/summary.json"
echo " 火焰图: $PPROF_DIR/"
echo "=========================================="
echo ""
ls -lh "$PPROF_DIR/"*.png 2>/dev/null
