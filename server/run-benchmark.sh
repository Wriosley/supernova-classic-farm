#!/bin/bash
# benchrunner 阶梯压测脚本
# 用法: ./run-benchmark.sh [scenario]
# scenario: snapshot (默认) | player_loop | connect_hold | mixed | friend_interaction | mail_operations

set -euo pipefail

cd /data/workspace/supernova-classic-farm/server

SCENARIO="${1:-snapshot}"
RUN_ID="${SCENARIO}_$(date +%Y%m%d_%H%M%S)"
TIMESTAMP=$(date -u +%Y%m%d-%H%M%S)

echo "=========================================="
echo " benchrunner 阶梯压测"
echo " 场景: $SCENARIO"
echo " RunID: $RUN_ID"
echo "=========================================="
echo ""

# 阶梯配置 (concurrency 列表)
if [ "$SCENARIO" = "connect_hold" ]; then
  # 长连接场景用较小并发
  CONCURRENCY="10,50,100,200,500"
  DURATION="120s"
elif [ "$SCENARIO" = "friend_interaction" ] || [ "$SCENARIO" = "mail_operations" ]; then
  # 需要偶数并发
  CONCURRENCY="2,10,20,50,100"
  DURATION="120s"
else
  # snapshot / player_loop / mixed
  CONCURRENCY="1,10,25,50,100,200,500"
  DURATION="60s"
fi

echo "并发阶梯: $CONCURRENCY"
echo "每档时长: $DURATION"
echo "预热: 10s"
echo ""

# 确认 login 服务可达
if ! curl -sS -m 3 http://127.0.0.1:8080/v1/auth/csrf -o /dev/null 2>/dev/null; then
  echo "[!] login:8080 不可达，尝试建立 port-forward..."
  nohup kubectl -n classic-farm port-forward svc/login 8080:8080 > /tmp/pf-login.log 2>&1 &
  sleep 3
fi

echo "[*] 开始执行 benchrunner..."
echo ""

# 绑核 14-15 (留给压测工具的 2 个核)
taskset -c 14,15 ./benchrunner \
  -scenario "$SCENARIO" \
  -concurrency "$CONCURRENCY" \
  -warmup 10s \
  -duration "$DURATION" \
  -run-id "$RUN_ID" \
  -connect-workers 64 \
  2>&1 | tee "/tmp/bench-${RUN_ID}.log"

EXIT_CODE=${PIPESTATUS[0]}

echo ""
echo "=========================================="
if [ $EXIT_CODE -eq 0 ]; then
  echo " [✓] 压测完成: $RUN_ID"
  echo " 结果目录: benchmark/results/$RUN_ID/"
  echo ""
  echo " 关键指标:"
  cat "benchmark/results/$RUN_ID/summary.json" 2>/dev/null | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(f\"{'并发':>6} {'QPS':>8} {'P50(ms)':>8} {'P95(ms)':>8} {'P99(ms)':>8} {'错误':>6}\")
    print('-'*46)
    for r in d.get('runs', d if isinstance(d, list) else []):
        c = r.get('concurrency', '?')
        q = r.get('qps', r.get('throughput', 0))
        p50 = r.get('p50_ms', r.get('latency_p50_ms', 0))
        p95 = r.get('p95_ms', r.get('latency_p95_ms', 0))
        p99 = r.get('p99_ms', r.get('latency_p99_ms', 0))
        err = r.get('error_count', r.get('errors', 0))
        print(f'{c:>6} {q:>8.1f} {p50:>8.1f} {p95:>8.1f} {p99:>8.1f} {err:>6}')
" 2>/dev/null || echo "(无法解析 summary.json，请查看原始文件)"
  echo ""
  echo " 完整报告: benchmark/results/$RUN_ID/report.md"
else
  echo " [✗] 压测失败 (exit=$EXIT_CODE)"
  echo " 日志: /tmp/bench-${RUN_ID}.log"
fi
echo "=========================================="
