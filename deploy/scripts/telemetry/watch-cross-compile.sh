#!/bin/bash
# Runs cross-compile-openobserve-pi.sh beside a memory monitor in tmux (macOS),
# logging both to ./o2-logs/<timestamp>/ in the current directory so the
# last lines before a crash and reboot survive. Build env vars pass through.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SELF="$SCRIPT_DIR/$(basename "$0")"
BUILD_SCRIPT="$SCRIPT_DIR/cross-compile-openobserve-pi.sh"
REPO="$(cd "$SCRIPT_DIR/../../.." && pwd)"
SESSION="${SESSION:-o2-build}"

run_build() {
    local log="$1"
    {
        echo "== started $(date) on $(sysctl -n hw.model), $(sysctl -n hw.ncpu) CPUs, $(( $(sysctl -n hw.memsize) / 1073741824 )) GB"
        echo "== O2_BUILD_JOBS=${O2_BUILD_JOBS:-<script default>} O2_VERSION=${O2_VERSION:-<default>} SKIP_WEB=${SKIP_WEB:-0}"
    } | tee -a "$log"
    cd "$REPO"
    set +e
    "$BUILD_SCRIPT" 2>&1 | perl -MPOSIX -pe '$|=1; $_ = strftime("%T ", localtime) . $_' | tee -a "$log"
    echo "== build exited with ${PIPESTATUS[0]} at $(date +%T)" | tee -a "$log"
    sync
}

# sync after each sample so the last lines reach disk before a panic.
run_monitor() {
    local log="$1"
    # One failed probe (grep with no match, ps hit by SIGPIPE) mustn't stop it.
    set +e +o pipefail
    echo "Logging memory every 5s to $log"
    while true; do
        {
            echo "== $(date +%T) level=$(sysctl -n kern.memorystatus_vm_pressure_level) $(memory_pressure -Q | tail -1)"
            echo "swap: $(sysctl -n vm.swapusage)"
            vm_stat | grep -E 'compressor|Swapouts|Pages free'
            df -h /System/Volumes/Data | tail -1
            pmset -g therm | grep -i limit || echo "thermal: no limit reported"
            ps -Amo rss=,pid=,comm= | head -5 | awk '{printf "  %6.0f MB %s %s\n", $1/1024, $2, $3}'
        } >> "$log" 2>&1
        sync
        printf '\r%s  %s' "$(date +%T)" "$(sysctl -n vm.swapusage)"
        sleep 5
    done
}

case "${1:-}" in
    build) run_build "$2"; exit ;;
    monitor) run_monitor "$2"; exit ;;
    "") ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
esac

[ "$(uname -s)" = "Darwin" ] || { echo "The monitor uses macOS tools; run on macOS" >&2; exit 1; }
command -v tmux >/dev/null 2>&1 || { echo "tmux is not installed (brew install tmux)" >&2; exit 1; }
if tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "tmux session $SESSION already exists: tmux attach -t $SESSION (or tmux kill-session -t $SESSION)" >&2
    exit 1
fi

LOG_DIR="$PWD/o2-logs/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$LOG_DIR"

# tmux panes don't inherit this shell's env, so pass the build knobs on.
ENV_ARGS=""
for var in O2_BUILD_JOBS O2_VERSION SKIP_WEB; do
    if [ -n "${!var:-}" ]; then
        ENV_ARGS="$ENV_ARGS $var=$(printf '%q' "${!var}")"
    fi
done

# The long-lived monitor pane comes first, so a build that exits at once
# can't end the session before remain-on-exit keeps its pane around.
tmux new-session -d -s "$SESSION" -x 220 -y 50 \
    "$(printf '%q' "$SELF") monitor $(printf '%q' "$LOG_DIR/mem.log")"
tmux set-option -t "$SESSION" remain-on-exit on
tmux split-window -t "$SESSION" -v -b -l 44 \
    "env$ENV_ARGS $(printf '%q' "$SELF") build $(printf '%q' "$LOG_DIR/build.log")"

echo "Logs: $LOG_DIR"
echo "Detach with Ctrl-b d; stop everything with: tmux kill-session -t $SESSION"
if [ -n "${TMUX:-}" ]; then
    tmux switch-client -t "$SESSION"
else
    tmux attach -t "$SESSION"
fi
