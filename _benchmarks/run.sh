#!/usr/bin/env bash
# yaklog _benchmarks — 多库横向对比基准测试运行脚本
#
# 用法：
#   ./run.sh                    # 全部基准（3s/项 × 3 次）
#   BENCHTIME=5s COUNT=5 ./run.sh
#   GOMAXPROCS=4 ./run.sh       # 限制并发度
#
# 输出：
#   终端实时显示 + 同时写入 results_<os>_<cores>c<threads>t_<timestamp>.txt
#   该文件作为双语 README 和测试文档的数据引用源。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

BENCHTIME="${BENCHTIME:-3s}"
COUNT="${COUNT:-3}"
EXTRA_FLAGS=("$@")

# ── 系统信息 ──────────────────────────────────────────────────────────────────
TIMESTAMP="$(date '+%Y-%m-%d %H:%M:%S %Z')"
KERNEL="$(uname -srm)"
GOVERSION="$(go version | awk '{print $3}')"
CPU="$(grep 'model name' /proc/cpuinfo 2>/dev/null | head -1 | sed 's/.*: //' \
    || sysctl -n machdep.cpu.brand_string 2>/dev/null \
    || echo 'unknown')"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in mingw*|msys*|cygwin*) OS="windows" ;; esac
CORES="$(grep '^core id' /proc/cpuinfo 2>/dev/null | sort -u | wc -l \
    || sysctl -n hw.physicalcpu 2>/dev/null \
    || nproc --all 2>/dev/null || echo '?')"
THREADS="$(grep -c '^processor' /proc/cpuinfo 2>/dev/null \
    || sysctl -n hw.ncpu 2>/dev/null || echo "$CORES")"

# ── 输出文件 ──────────────────────────────────────────────────────────────────
DATESTAMP="$(date '+%Y%m%d_%H%M%S')"
OUTFILE="$SCRIPT_DIR/results_${OS}_${CORES}c${THREADS}t_${DATESTAMP}.txt"

HEADER="# yaklog vs mainstream Go logging libraries — benchmark results
# Generated: $TIMESTAMP
# $KERNEL
# $GOVERSION
# CPU: $CPU
# GOMAXPROCS: ${GOMAXPROCS:-$(nproc)}
# benchtime: ${BENCHTIME}  count: ${COUNT}
# Output: io.Discard (no I/O noise)
# yaklog: Send() synchronous path + TimeOff (no timestamp)
# zerolog: New(io.Discard) default (no timestamp) — equal-payload comparison
#
# Reference: _benchmarks/README.md
# ──────────────────────────────────────────────────────────────────────────────"

echo "======================================================"
echo " yaklog 多库横向对比基准测试"
echo " 时间: $(date '+%Y-%m-%d %H:%M')"
echo " GOMAXPROCS: ${GOMAXPROCS:-$(nproc)}"
echo " benchtime: ${BENCHTIME}  count: ${COUNT}"
echo " 输出文件: $OUTFILE"
echo "======================================================"
echo ""

# 写头部到输出文件（同时打印到终端）
echo "$HEADER" | tee "$OUTFILE"
echo "" | tee -a "$OUTFILE"

# 运行基准测试，同时输出到终端和文件
go test \
    -bench="." \
    -benchmem \
    -benchtime="${BENCHTIME}" \
    -count="${COUNT}" \
    "${EXTRA_FLAGS[@]+"${EXTRA_FLAGS[@]}"}" \
    -run='^$' \
    "./bench/" 2>&1 | tee -a "$OUTFILE"

echo "" | tee -a "$OUTFILE"
echo "# Done: $(date '+%Y-%m-%d %H:%M:%S')" | tee -a "$OUTFILE"
echo ""
echo "======================================================"
echo " 完成 → $OUTFILE"
echo "======================================================"

# ── 自动软链接最新结果 ────────────────────────────────────────────────────────
# latest.txt 始终指向本次运行产生的结果文件，方便 README / CI 引用固定路径。
ln -sf "$(basename "$OUTFILE")" "$SCRIPT_DIR/latest.txt"
echo " latest.txt → $(basename "$OUTFILE")"
