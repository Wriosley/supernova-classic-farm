#!/bin/bash
# 压测期间火焰图采集脚本
# 用法:
#   1. 压测前先运行: ./capture-pprof.sh start
#   2. 压测中运行: ./capture-pprof.sh capture <label>
#   3. 压测后运行: ./capture-pprof.sh stop

set -euo pipefail

GO=/usr/local/go/bin/go
OUT_DIR="/data/workspace/supernova-classic-farm/result/pprof-$(date +%Y%m%d_%H%M%S)"
mkdir -p "$OUT_DIR"

# pprof 端口映射
# gate-0 -> 6060, zone-pool-0 -> 6061
GATE_PPROF="http://127.0.0.1:6060/internal/debug/pprof"
ZONE_PPROF="http://127.0.0.1:6061/internal/debug/pprof"

cmd="${1:-help}"
label="${2:-default}"

case "$cmd" in
  start)
    echo "=== 建立 pprof 端口转发 ==="
    # 清理旧的
    ps -ef | grep "port-forward.*606[01]" | grep -v grep | awk '{print $2}' | xargs -r kill 2>/dev/null || true
    sleep 1
    # gate-0
    nohup kubectl -n classic-farm port-forward pod/gate-0 6060:8081 > /tmp/pf-gate-pprof.log 2>&1 &
    echo "gate-0 pprof -> 6060 (PID $!)"
    # zone-pool-0
    nohup kubectl -n classic-farm port-forward pod/zone-pool-0 6061:8082 > /tmp/pf-zone-pprof.log 2>&1 &
    echo "zone-pool-0 pprof -> 6061 (PID $!)"
    sleep 3
    echo "=== 验证 ==="
    curl -sS -m 3 "$GATE_PPROF/" -o /dev/null -w "gate pprof: HTTP %{http_code}\n"
    curl -sS -m 3 "$ZONE_PPROF/" -o /dev/null -w "zone pprof: HTTP %{http_code}\n"
    echo "输出目录: $OUT_DIR"
    echo "$OUT_DIR" > /tmp/pprof_out_dir
    ;;

  capture)
    OUT_DIR=$(cat /tmp/pprof_out_dir 2>/dev/null || echo "/data/workspace/supernova-classic-farm/result/pprof-capture")
    mkdir -p "$OUT_DIR"
    TS=$(date +%H%M%S)
    echo "=== 采集 [${label}] 到 $OUT_DIR/${label}_${TS} ==="

    for target in "gate:$GATE_PPROF" "zone:$ZONE_PPROF"; do
      name="${target%%:*}"
      url="${target##*:}"
      echo "--- $name ---"

      # CPU profile (30 秒采样)
      echo "  CPU profile (30s)..."
      $GO tool pprof -seconds=30 -png -output "$OUT_DIR/${label}_${TS}_${name}_cpu.png" "$url/profile" 2>&1 | tail -1

      # Heap
      echo "  Heap..."
      $GO tool pprof -png -output "$OUT_DIR/${label}_${TS}_${name}_heap.png" "$url/heap" 2>&1 | tail -1

      # Goroutine
      echo "  Goroutine..."
      $GO tool pprof -png -output "$OUT_DIR/${label}_${TS}_${name}_goroutine.png" "$url/goroutine" 2>&1 | tail -1

      # 保存原始 pb.gz 供后续交互分析
      $GO tool pprof -seconds=30 -output "$OUT_DIR/${label}_${TS}_${name}_cpu.pb.gz" "$url/profile" 2>&1 | tail -1
      $GO tool pprof -output "$OUT_DIR/${label}_${TS}_${name}_heap.pb.gz" "$url/heap" 2>&1 | tail -1
    done
    echo "=== 采集完成 ==="
    echo "文件列表:"
    ls -lh "$OUT_DIR/${label}_${TS}_"* 2>/dev/null
    ;;

  stop)
    echo "=== 清理 pprof 端口转发 ==="
    ps -ef | grep "port-forward.*606[01]" | grep -v grep | awk '{print $2}' | xargs -r kill 2>/dev/null || true
    echo "已清理"
    ;;

  web)
    # 交互式查看某个 profile
    target="${2:-gate}"
    profile="${3:-heap}"
    case "$target" in
      gate) url="$GATE_PPROF/$profile" ;;
      zone) url="$ZONE_PPROF/$profile" ;;
    esac
    echo "=== 打开交互式 pprof ($target/$profile) ==="
    echo "在 pprof 提示符下输入: web (浏览器), top, list <函数名>"
    $GO tool pprof "$url"
    ;;

  *)
    echo "用法:"
    echo "  $0 start                    # 压测前: 建立 pprof 端口转发"
    echo "  $0 capture <label>           # 压测中: 采集 CPU/heap/goroutine 火焰图"
    echo "  $0 stop                      # 压测后: 清理端口转发"
    echo "  $0 web gate|zone heap|cpu|goroutine  # 交互式查看"
    echo ""
    echo "典型压测流程:"
    echo "  ./capture-pprof.sh start"
    echo "  # 启动 benchrunner..."
    echo "  ./capture-pprof.sh capture peak      # 峰值时采集"
    echo "  ./capture-pprof.sh capture after     # 压测后采集"
    echo "  ./capture-pprof.sh stop"
    ;;
esac
