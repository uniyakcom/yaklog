#!/usr/bin/env bash
# fuzz.sh — yaklog 本地逐目标 Fuzz + 崩溃自动检测
#
# 用法：
#   ./fuzz.sh                           全部目标，每个 5m
#   ./fuzz.sh 2m                        全部目标，自定义时长
#   ./fuzz.sh FuzzJSONEncoder           单目标，5m
#   ./fuzz.sh FuzzJSONEncoder 2m        单目标，自定义时长
#   FUZZ_TIME=10m ./fuzz.sh             环境变量指定时长
#
# Ctrl+C：中断当前目标并继续下一个；再按一次退出整个脚本。
# 日志：所有输出（含崩溃信息）自动保存到 fuzz_logs/fuzz_<时间戳>.log
#
# Fuzz 目标（当前包）：
#   FuzzJSONEncoder     — jsonEncoder appendStr/appendAttr/finalize
#   FuzzTextEncoder     — textEncoder appendStr/finalize
#   FuzzAppendJSONStr   — appendJSONStr 往返验证

set -uo pipefail

# ── 自动发现当前包下所有 Fuzz 目标 ────────────────────────────────────────────
mapfile -t ALL_TARGETS < <(go test -list '^Fuzz' . 2>/dev/null | grep '^Fuzz')
if [[ ${#ALL_TARGETS[@]} -eq 0 ]]; then
  echo "当前目录没有找到任何 Fuzz 测试函数，退出。" >&2
  exit 1
fi

# ── 参数解析 ──────────────────────────────────────────────────────────────────
FUZZ_TIME="${FUZZ_TIME:-5m}"
TARGETS=()

for arg in "$@"; do
  case "$arg" in
    Fuzz*) TARGETS+=("$arg") ;;
    *[0-9][smh]) FUZZ_TIME="$arg" ;;
    -h|--help)
      sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) echo "未知参数: $arg  （运行 ./fuzz.sh --help 查看用法）" >&2; exit 1 ;;
  esac
done

[[ ${#TARGETS[@]} -eq 0 ]] && TARGETS=("${ALL_TARGETS[@]}")

# ── 日志文件 ──────────────────────────────────────────────────────────────────
LOG_DIR="fuzz_logs"
mkdir -p "$LOG_DIR"
LOG_FILE="${LOG_DIR}/fuzz_$(date '+%Y%m%d_%H%M%S').log"
exec > >(tee >(sed 's/\x1b\[[0-9;]*m//g' >> "$LOG_FILE")) 2>&1

# ── 颜色 ─────────────────────────────────────────────────────────────────────
if [[ -t 1 ]]; then
  RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
  CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'
else
  RED=''; GREEN=''; YELLOW=''; CYAN=''; BOLD=''; RESET=''
fi

log()  { echo -e "${CYAN}[fuzz]${RESET} $*"; }
ok()   { echo -e "  ${GREEN}✓${RESET} $*"; }
warn() { echo -e "  ${YELLOW}⚠${RESET} $*"; }
err()  { echo -e "  ${RED}✗${RESET} $*"; }

# ── 编译检查 ──────────────────────────────────────────────────────────────────
log "日志：${LOG_FILE}"
log "编译检查…"
if ! go test -c -o /dev/null . 2>&1; then
  err "编译失败，终止。"
  echo "  → 详细错误见 ${LOG_FILE}"
  exit 1
fi
ok "编译通过"
echo ""

# ── Ctrl+C 处理 ───────────────────────────────────────────────────────────────
SKIP_CURRENT=0
ABORT=0
FUZZ_PID=0

_on_sigint() {
  if [[ $SKIP_CURRENT -eq 1 ]]; then
    ABORT=1
    echo -e "\n${YELLOW}  已请求退出，正在停止…${RESET}"
  else
    SKIP_CURRENT=1
    echo -e "\n${YELLOW}  ↩ Ctrl+C — 中断当前目标，继续下一个（再按一次退出）${RESET}"
  fi
  [[ $FUZZ_PID -gt 0 ]] && kill "$FUZZ_PID" 2>/dev/null || true
}
trap '_on_sigint' INT

# ── 崩溃检测 ──────────────────────────────────────────────────────────────────
CORPUS_ROOT="testdata/fuzz"

_check_crashes() {
  local target="$1"
  local dir="${CORPUS_ROOT}/${target}"
  [[ -d "$dir" ]] || return 0

  local raw
  raw=$(git status --porcelain -- "$dir" 2>/dev/null | awk '$1=="??" {print $2}')
  [[ -z "$raw" ]] && return 0

  local new_files=""
  while IFS= read -r entry; do
    if [[ -d "$entry" ]]; then
      while IFS= read -r f; do
        new_files+="${f}"$'\n'
      done < <(find "$entry" -type f | sort)
    else
      new_files+="${entry}"$'\n'
    fi
  done <<< "$raw"
  new_files="${new_files%$'\n'}"
  [[ -z "$new_files" ]] && return 0

  echo -e "  ${RED}${BOLD}崩溃种子：${RESET}"
  while IFS= read -r f; do
    echo -e "    ${RED}${f}${RESET}"
  done <<< "$new_files"
  echo ""
  echo -e "  ${BOLD}复现（逐文件）：${RESET}"
  while IFS= read -r f; do
    local seed
    seed=$(basename "$f")
    echo -e "    go test -run '^${target}/${seed}$' -v ."
  done <<< "$new_files"
  echo ""
  echo -e "  ${BOLD}提交到仓库（下次 CI 自动回归）：${RESET}"
  while IFS= read -r f; do
    echo -e "    git add '${f}'"
  done <<< "$new_files"
  echo -e "    git commit -m 'fuzz: add crash seed for ${target}'"
  echo ""
}

# ── 主循环 ────────────────────────────────────────────────────────────────────
TOTAL=${#TARGETS[@]}
CRASHED=()
SKIPPED=0

log "目标 ${BOLD}${TOTAL}${RESET} 个，每个 ${BOLD}${FUZZ_TIME}${RESET}"
log "Ctrl+C 中断当前目标，继续下一个；再按退出"
echo ""

for i in "${!TARGETS[@]}"; do
  [[ $ABORT -eq 1 ]] && break
  SKIP_CURRENT=0

  target="${TARGETS[$i]}"
  n=$((i + 1))

  echo -e "${CYAN}[${n}/${TOTAL}]${RESET} ${BOLD}${target}${RESET}  (${FUZZ_TIME})"

  go test -run="^${target}$" -fuzz="^${target}$" -fuzztime="${FUZZ_TIME}" . &
  FUZZ_PID=$!
  wait $FUZZ_PID
  exit_code=$?
  FUZZ_PID=0

  [[ $ABORT -eq 1 ]] && { warn "已中断，退出。"; break; }

  if [[ $SKIP_CURRENT -eq 1 ]]; then
    warn "已跳过"
    ((SKIPPED++)) || true
  elif [[ $exit_code -ne 0 ]]; then
    err "退出码 ${exit_code}（可能发现崩溃）"
    CRASHED+=("$target")
    _check_crashes "$target"
  else
    ok "完成（无崩溃）"
  fi

  echo ""
done

# ── 汇总 ──────────────────────────────────────────────────────────────────────
echo -e "${BOLD}── 汇总 ──────────────────────────────────────────${RESET}"
ran=$((TOTAL - SKIPPED))
echo -e "  跑了 ${ran} / ${TOTAL} 个目标，每个 ${FUZZ_TIME}"

if [[ ${#CRASHED[@]} -gt 0 ]]; then
  echo -e "  ${RED}${BOLD}发现崩溃：${CRASHED[*]}${RESET}"
  echo -e "  ${YELLOW}修复后运行 ${BOLD}go test -run '^Fuzz' .${RESET}${YELLOW} 确认种子通过后提交。${RESET}"
  echo -e "  详细信息（含复现命令）：${BOLD}${LOG_FILE}${RESET}"
  exit 1
else
  echo -e "  ${GREEN}✓ 未发现崩溃${RESET}"
fi
echo -e "  完整日志：${LOG_FILE}"
