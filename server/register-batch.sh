#!/bin/bash
# 健壮的分批注册脚本
# 每批 50 个，connect-workers=16，失败自动重启 login 重试

set -uo pipefail

cd /data/workspace/supernova-classic-farm/server

RUN_ID="reg1000"
TARGET=1000
BATCH_SIZE=50
LOG="/tmp/register-${RUN_ID}.log"
> "$LOG"

echo "=== 分批注册 $TARGET 个账号，每批 $BATCH_SIZE ==="

REGISTERED=0
ATTEMPT=0

while [ $REGISTERED -lt $TARGET ]; do
  REMAIN=$((TARGET - REGISTERED))
  CUR=$((REGISTERED + BATCH_SIZE))
  if [ $CUR -gt $TARGET ]; then
    CUR=$TARGET
  fi

  ATTEMPT=$((ATTEMPT + 1))
  echo -n "[$(date +%H:%M:%S)] 尝试 $ATTEMPT: 注册到 $CUR (新建 $((CUR - REGISTERED)) 个)... "

  # 用 timeout 包裹，10 分钟超时
  OUTPUT=$(timeout 600 ./benchrunner \
    -scenario snapshot \
    -concurrency "$CUR" \
    -warmup 0s \
    -duration 1s \
    -run-id "$RUN_ID" \
    -login-url http://127.0.0.1:8080 \
    -gate-url ws://127.0.0.1:8081/ws \
    -connect-workers 16 \
    -timeout 60s 2>&1) || true

  if echo "$OUTPUT" | grep -qE "concurrency=.*qps=|connected"; then
    REGISTERED=$CUR
    QPS=$(echo "$OUTPUT" | grep -oP 'qps=\K[0-9.]+' | tail -1)
    echo "✓ 成功 (累计 $REGISTERED/$TARGET, qps=$QPS)"
    echo "[$(date +%H:%M:%S)] attempt $ATTEMPT: cur=$CUR OK qps=$QPS" >> "$LOG"
  else
    ERR=$(echo "$OUTPUT" | grep -iE 'error|500|deadline|refused|EOF' | tail -1)
    echo "✗ 失败: $ERR"
    echo "[$(date +%H:%M:%S)] attempt $ATTEMPT: cur=$CUR FAIL: $ERR" >> "$LOG"

    # 重启 login 恢复 tcaplus
    echo "  重启 login..."
    kubectl -n classic-farm rollout restart deploy/login > /dev/null 2>&1
    kubectl -n classic-farm rollout status deploy/login --timeout=60s > /dev/null 2>&1
    sleep 2

    # 重建 port-forward
    ps -ef | grep "port-forward.*8080" | grep -v grep | awk '{print $2}' | xargs -r kill 2>/dev/null
    sleep 1
    LOGIN_POD=$(kubectl -n classic-farm get pod -l app.kubernetes.io/name=login -o name | head -1)
    nohup kubectl -n classic-farm port-forward "$LOGIN_POD" 8080:8080 > /tmp/pf-login.log 2>&1 &
    sleep 3

    # 确认 tcaplus 恢复
    TCAP=$(kubectl -n classic-farm logs deploy/login --tail=3 2>&1 | grep -c 'init success')
    if [ $TCAP -eq 0 ]; then
      echo "  tcaplus 仍未恢复，等待 10 秒后重试..."
      sleep 10
    fi
    echo "  login 已恢复，继续重试"
  fi
done

echo ""
echo "=========================================="
echo " 注册完成: $REGISTERED 个账号"
echo " run-id: $RUN_ID"
echo " 账号: bench_${RUN_ID}_001 ~ bench_${RUN_ID}_${REGISTERED}"
echo "=========================================="
