#!/bin/bash
# =============================================================================
# RAgent Router 压测脚本
#
# 使用 vegeta 进行 HTTP 压测，输出 QPS / P50 / P99 / 错误率
#
# 安装 vegeta:
#   go install github.com/tsenart/vegeta@latest
#
# 使用方式:
#   chmod +x benchmark.sh
#   ./benchmark.sh              # 默认压测 Mock 模式
#   ./benchmark.sh http://localhost:15722  # 指定目标
# =============================================================================

set -e

TARGET=${1:-"http://localhost:15722"}
DURATION=${2:-"30s"}
RATE=${3:-"1000"}  # QPS

echo "╔══════════════════════════════════════════════════════════╗"
echo "║          RAgent Router 压测报告                          ║"
echo "╠══════════════════════════════════════════════════════════╣"
echo "║  目标:    $TARGET"
echo "║  时长:    $DURATION"
echo "║  目标QPS: $RATE"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

# 检查 vegeta 是否安装
if ! command -v vegeta &> /dev/null; then
    echo "错误: 请先安装 vegeta"
    echo "  go install github.com/tsenart/vegeta@latest"
    exit 1
fi

# 检查目标是否可达
echo "[1/4] 检查目标可达性..."
if ! curl -s -o /dev/null -w "%{http_code}" "$TARGET/healthz" | grep -q "200"; then
    echo "警告: $TARGET/healthz 未返回 200，继续压测..."
fi

# 构造请求体
BODY='{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hello, what is 1+1?"}]}'

# 生成压测目标文件
echo "[2/4] 生成压测目标..."
cat > /tmp/vegeta-targets.txt << EOF
POST $TARGET/v1/messages
Content-Type: application/json
@body.json
EOF

echo "$BODY" > /tmp/body.json

# 执行压测
echo "[3/4] 执行压测 (${DURATION}, ${RATE} QPS)..."
RESULTS=/tmp/vegeta-results.bin

vegeta attack \
    -targets=/tmp/vegeta-targets.txt \
    -body=/tmp/body.json \
    -duration="$DURATION" \
    -rate="$RATE" \
    -connections=100 \
    -timeout=30s \
    > "$RESULTS" 2>/dev/null

# 生成报告
echo "[4/4] 生成报告..."
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "  压测结果"
echo "═══════════════════════════════════════════════════════════"

vegeta report -type=text < "$RESULTS"

echo ""
echo "═══════════════════════════════════════════════════════════"
echo "  延迟分布"
echo "═══════════════════════════════════════════════════════════"

vegeta report -type='hist[0,1ms,5ms,10ms,50ms,100ms,500ms,1s]' < "$RESULTS"

echo ""
echo "═══════════════════════════════════════════════════════════"
echo "  JSON 报告（可保存用于简历数据）"
echo "═══════════════════════════════════════════════════════════"

vegeta report -type=json < "$RESULTS" | python -m json.tool 2>/dev/null || vegeta report -type=json < "$RESULTS"

# 清理
rm -f /tmp/vegeta-targets.txt /tmp/body.json "$RESULTS"

echo ""
echo "压测完成！"
echo ""
echo "简历话术示例："
echo "  基于 go-zero 微服务架构，单机 Mock 模式压测达 XXXX QPS"
echo "  P50 延迟 XXms，P99 延迟 XXms"
echo "  熔断器在连续 N 次失败后 Xms 内触发状态转换"
