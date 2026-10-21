#!/bin/bash
# Runs a build on this Linux host beside a memory monitor that stops when the
# build exits, logging both to ./o2-logs/<timestamp>/ and syncing every sample.

# Usage, in tmux: ./watch-build.sh ./cross-compile-openobserve-pi.sh

set -uo pipefail

usage() {
    echo "Usage: $0 <build command> [args...]" >&2
    echo "Run it on the Linux host doing the build, in tmux:" >&2
    echo "  ./watch-build.sh ./cross-compile-openobserve-pi.sh" >&2
    echo "On macOS, use watch-cross-compile.sh instead." >&2
    exit 1
}
[ "$(uname -s)" = "Linux" ] || usage
[ $# -gt 0 ] || usage

INTERVAL="${WATCH_INTERVAL:-10}"
LOG_DIR="$PWD/o2-logs/$(date +%Y%m%d-%H%M%S)"
BUILD_LOG="$LOG_DIR/build.log"
MEM_LOG="$LOG_DIR/mem.log"
SUMMARY="$LOG_DIR/summary.txt"
EXIT_FILE="$LOG_DIR/.exit"
mkdir -p "$LOG_DIR"

kb() { awk -v k="$1" '$1 == k":" {print $2}' /proc/meminfo; }
mb() { echo $(( $1 / 1024 )); }

host_info() {
    echo "host:    $(tr -d '\0' </proc/device-tree/model 2>/dev/null || uname -n), $(nproc) CPUs"
    echo "memory:  RAM $(mb "$(kb MemTotal)") MB, swap $(mb "$(kb SwapTotal)") MB"
    awk 'NR > 1 {printf "         swap %s %d MB\n", $1, $3 / 1024}' /proc/swaps
    echo "disk:    $(df -h "$PWD" | awk 'NR == 2 {print $4 " free on " $6}')"
    echo "command: $*"
    echo "started: $(date '+%F %T')"
}

# Memory "used" counts RAM not available plus swap in use, i.e. what the build
# would need without swap. Also tracks the crate the biggest rustc is building.
sample() {
    local total avail swap_total swap_free used swap_used top crate
    total="$(kb MemTotal)"; avail="$(kb MemAvailable)"
    swap_total="$(kb SwapTotal)"; swap_free="$(kb SwapFree)"
    used=$(( (total - avail) / 1024 )); swap_used=$(( (swap_total - swap_free) / 1024 ))
    echo "== $(date +%T) ram_used=${used}MB swap_used=${swap_used}MB total_used=$((used + swap_used))MB avail=$(mb "$avail")MB"
    [ -r /proc/pressure/memory ] && sed 's/^/   psi_mem /' /proc/pressure/memory
    if [ -r /sys/block/zram0/mm_stat ]; then
        awk '{printf "   zram orig=%dMB compressed=%dMB\n", $1/1048576, $2/1048576}' /sys/block/zram0/mm_stat
    fi
    echo "   load $(cut -d' ' -f1-3 /proc/loadavg)  temp $(( $(cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null || echo 0) / 1000 ))C  $(vcgencmd get_throttled 2>/dev/null || true)"
    echo "   disk $(df -h "$PWD" | awk 'NR == 2 {print $4 " free"}')"
    ps -eo rss=,pid=,comm= --sort=-rss | head -5 | awk '{printf "   %6d MB %s %s\n", $1/1024, $2, $3}'
    top="$(pgrep -x rustc | xargs -r ps -o rss=,pid= -p 2>/dev/null | sort -rn | head -1 | awk '{print $2}')"
    if [ -n "$top" ]; then
        crate="$(tr '\0' ' ' <"/proc/$top/cmdline" 2>/dev/null | grep -o -- '--crate-name [^ ]*' | cut -d' ' -f2)"
        echo "   biggest rustc: ${crate:-?} (pid $top)"
    fi
    PEAK_LINE="$(printf '%s\n%s' "$PEAK_LINE" "$((used + swap_used)) $(date +%T) ${crate:-}" | sort -rn | head -1)"
}

{ echo "== watch-build"; host_info "$@"; } | tee "$SUMMARY"
cp "$SUMMARY" "$BUILD_LOG"
cp "$SUMMARY" "$MEM_LOG"
START=$SECONDS

# The build runs in the background; its exit code lands in EXIT_FILE.
(
    "$@" 2>&1 | while IFS= read -r line; do printf '%(%T)T %s\n' -1 "$line"; done | tee -a "$BUILD_LOG"
    echo "${PIPESTATUS[0]}" > "$EXIT_FILE"
) &
BUILD_PID=$!
echo "Logs: $LOG_DIR (monitor samples every ${INTERVAL}s while the build runs)"

PEAK_LINE="0"
next_sample=0
while kill -0 "$BUILD_PID" 2>/dev/null; do
    if [ "$SECONDS" -ge "$next_sample" ]; then
        sample >> "$MEM_LOG" 2>&1
        sync
        next_sample=$((SECONDS + INTERVAL))
    fi
    sleep 2
done
wait "$BUILD_PID" 2>/dev/null

ELAPSED=$((SECONDS - START))
EXIT_CODE="$(cat "$EXIT_FILE" 2>/dev/null || echo "?")"
rm -f "$EXIT_FILE"
read -r peak_mb peak_at peak_crate <<<"$PEAK_LINE"
{
    echo ""
    echo "finished: $(date '+%F %T')"
    echo "exit:     ${EXIT_CODE}"
    printf 'duration: %dh %02dm %02ds\n' $((ELAPSED / 3600)) $((ELAPSED % 3600 / 60)) $((ELAPSED % 60))
    echo "peak:     ${peak_mb:-0} MB RAM+swap in use at ${peak_at:-?}${peak_crate:+ (biggest rustc: $peak_crate)}"
    echo "oom kills since start (kernel log):"
    journalctl -k --since "@$(( $(date +%s) - ELAPSED ))" --no-pager 2>/dev/null \
        | grep -iE 'out of memory|oom-kill|killed process' | sed 's/^/   /' || true
} | tee -a "$SUMMARY" | tee -a "$MEM_LOG" >/dev/null
sed -n "/^finished:/,\$p" "$SUMMARY"
sync
