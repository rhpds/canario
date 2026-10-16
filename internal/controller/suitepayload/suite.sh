#!/bin/bash
# Canario in-guest suite (adapted from ocp-virt-ux-bench).
# Modular: CANARIO_SUITE_SECTIONS=comma list or "all".
# Sections: storage, fileops, responsive, packages, compile, cpu, memory, network, git
# Default: all
#
# Disk-bound (per data disk): storage, fileops
# Guest-bound (once per VM): responsive, packages, compile, cpu, memory, network, git
#
# Network: CANARIO_NETWORK=auto|on|off (default auto).
#   auto — probe internet; skip network+git if unreachable (not a suite failure).
# Env (optional): CANARIO_PING_TARGET, CANARIO_DNS_NAME, CANARIO_DOWNLOAD_URL, CANARIO_GIT_URL

set -euo pipefail

SECTIONS="${CANARIO_SUITE_SECTIONS:-all}"
NODE_NAME="${CANARIO_NODE_NAME:-}"
DATA_MOUNT="${CANARIO_DATA_MOUNT:-/mnt/canario-data}"
RESULTS_DIR="${CANARIO_RESULTS_DIR:-/var/lib/canario}"
FIO_RUNTIME="${CANARIO_FIO_RUNTIME:-15}"
STRESS_TIMEOUT="${CANARIO_STRESS_TIMEOUT:-15}"
NETWORK_MODE="${CANARIO_NETWORK:-auto}"
PING_TARGET="${CANARIO_PING_TARGET:-1.1.1.1}"
DNS_NAME="${CANARIO_DNS_NAME:-dns.google}"
# ~1MiB — large enough for a real Mbps sample (2-byte hotspot files always read as 0.00).
DOWNLOAD_URL="${CANARIO_DOWNLOAD_URL:-http://speedtest.tele2.net/1MB.zip}"
GIT_URL="${CANARIO_GIT_URL:-}"
DISKS_JSON="${CANARIO_DISKS_JSON:-}"
if [[ -z "$DISKS_JSON" && -n "${CANARIO_DISKS_JSON_FILE:-}" && -f "${CANARIO_DISKS_JSON_FILE}" ]]; then
	DISKS_JSON="$(cat "${CANARIO_DISKS_JSON_FILE}")"
	export CANARIO_DISKS_JSON="$DISKS_JSON"
fi

log() { printf '[canario-suite %s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }

mkdir -p "$RESULTS_DIR"
STATUS_FILE="${RESULTS_DIR}/status"
RESULTS_JSON="${RESULTS_DIR}/results.json"
DONE_FILE="${RESULTS_DIR}/done"

echo "running" >"$STATUS_FILE"
rm -f "$DONE_FILE"

DISK_JSON_PARTS=()
declare -A GUEST=()
HAVE_INTERNET=""

section_enabled() {
	local want="$1" IFS=','
	local s
	for s in $SECTIONS; do
		s="${s#"${s%%[![:space:]]*}"}"
		s="${s%"${s##*[![:space:]]}"}"
		[[ "$s" == "$want" || "$s" == "all" ]] && return 0
	done
	return 1
}

# Returns 0 if external network appears usable.
probe_internet() {
	if ping -c 1 -W 3 "$PING_TARGET" >/dev/null 2>&1; then
		return 0
	fi
	if command -v curl >/dev/null 2>&1; then
		# Tiny connectivity probe (not the throughput URL).
		curl -fsS --connect-timeout 5 --max-time 10 -o /dev/null \
			"https://fedoraproject.org/static/hotspot.txt" 2>/dev/null && return 0
		curl -fsS --connect-timeout 5 --max-time 10 -o /dev/null "$DOWNLOAD_URL" 2>/dev/null && return 0
	fi
	return 1
}

resolve_network_mode() {
	case "$NETWORK_MODE" in
		on|off|auto) ;;
		*) NETWORK_MODE=auto ;;
	esac
	if [[ "$NETWORK_MODE" == "off" ]]; then
		HAVE_INTERNET=0
		return
	fi
	if [[ "$NETWORK_MODE" == "on" ]]; then
		HAVE_INTERNET=1
		return
	fi
	# auto
	if probe_internet; then
		HAVE_INTERNET=1
		log "network auto: internet reachable"
	else
		HAVE_INTERNET=0
		log "network auto: no internet — skipping network/git"
	fi
}

network_wanted() {
	section_enabled network || return 1
	[[ "$HAVE_INTERNET" == "1" ]]
}

git_wanted() {
	section_enabled git || return 1
	[[ "$HAVE_INTERNET" == "1" ]]
	[[ -n "$GIT_URL" ]]
}

prepare_data_disk() {
	local dev="$1" mount="$2"
	log "Preparing data disk ${dev} -> ${mount}"
	if ! lsblk -nlo NAME,TYPE "$dev" >/dev/null 2>&1; then
		log "FATAL: data device ${dev} not found"
		return 1
	fi
	if mountpoint -q "$mount"; then
		umount "$mount" || true
	fi
	if ! blkid -o value -s TYPE "$dev" >/dev/null 2>&1; then
		mkfs.xfs -f "$dev"
	fi
	mkdir -p "$mount"
	mount "$dev" "$mount"
	df -h "$mount" | tee "${RESULTS_DIR}/df-$(basename "$dev").txt"
}

teardown_data_disk() {
	local mount="$1"
	if mountpoint -q "$mount"; then
		umount "$mount" || true
	fi
}

run_storage() {
	local fio_file="$1"
	local -n _m=$2
	if ! command -v fio >/dev/null 2>&1; then
		log "fio missing"
		_m[fio_error]="missing"
		return 1
	fi
	local out
	out=$(fio --name=seqwrite --ioengine=libaio --direct=1 --bs=1M --iodepth=4 \
		--size=512M --rw=write --runtime="${FIO_RUNTIME}" --time_based \
		--group_reporting --filename="${fio_file}" 2>&1) || true
	_m[fio_seqwrite_bw]=$(echo "$out" | grep -oE 'BW=[0-9.]+[GMKT]iB/s' | head -1 || echo N/A)

	out=$(fio --name=seqread --ioengine=libaio --direct=1 --bs=1M --iodepth=4 \
		--size=512M --rw=read --runtime="${FIO_RUNTIME}" --time_based \
		--group_reporting --filename="${fio_file}" 2>&1) || true
	_m[fio_seqread_bw]=$(echo "$out" | grep -oE 'BW=[0-9.]+[GMKT]iB/s' | head -1 || echo N/A)

	out=$(fio --name=randread --ioengine=libaio --direct=1 --bs=4k --iodepth=4 \
		--size=512M --rw=randread --runtime="${FIO_RUNTIME}" --time_based \
		--group_reporting --filename="${fio_file}" 2>&1) || true
	_m[fio_randread_iops]=$(echo "$out" | grep -oE 'IOPS=[0-9.kKmM]+' | head -1 || echo N/A)

	out=$(fio --name=randwrite --ioengine=libaio --direct=1 --bs=4k --iodepth=4 \
		--size=512M --rw=randwrite --runtime="${FIO_RUNTIME}" --time_based \
		--group_reporting --filename="${fio_file}" 2>&1) || true
	_m[fio_randwrite_iops]=$(echo "$out" | grep -oE 'IOPS=[0-9.kKmM]+' | head -1 || echo N/A)

	log "seqwrite=${_m[fio_seqwrite_bw]} seqread=${_m[fio_seqread_bw]} randread=${_m[fio_randread_iops]} randwrite=${_m[fio_randwrite_iops]}"
}

run_fileops() {
	local mount="$1"
	local -n _m=$2
	local s e
	rm -rf "${mount}/filetest" "${mount}/filetest.tar.gz" "${mount}/filetest-extracted"
	mkdir -p "${mount}/filetest"

	s=$(date +%s%N)
	for i in $(seq 1 1000); do echo "test content $i" >"${mount}/filetest/file_${i}.txt"; done
	e=$(date +%s%N)
	_m[fileops_create_1000_ms]=$(( (e - s) / 1000000 ))

	s=$(date +%s%N)
	tar czf "${mount}/filetest.tar.gz" -C "${mount}" filetest
	e=$(date +%s%N)
	_m[fileops_tar_create_ms]=$(( (e - s) / 1000000 ))

	mkdir -p "${mount}/filetest-extracted"
	s=$(date +%s%N)
	tar xzf "${mount}/filetest.tar.gz" -C "${mount}/filetest-extracted"
	e=$(date +%s%N)
	_m[fileops_tar_extract_ms]=$(( (e - s) / 1000000 ))

	log "create=${_m[fileops_create_1000_ms]}ms tar=${_m[fileops_tar_create_ms]}ms extract=${_m[fileops_tar_extract_ms]}ms"
}

run_responsive() {
	local s e
	log "--- responsive ---"
	s=$(date +%s%N)
	ls -laR /etc >/dev/null 2>&1 || true
	e=$(date +%s%N)
	GUEST[cmd_ls_etc_ms]=$(( (e - s) / 1000000 ))

	s=$(date +%s%N)
	find / -name '*.conf' 2>/dev/null | wc -l >/dev/null || true
	e=$(date +%s%N)
	GUEST[cmd_find_conf_ms]=$(( (e - s) / 1000000 ))

	s=$(date +%s%N)
	ps auxww | wc -l >/dev/null || true
	e=$(date +%s%N)
	GUEST[cmd_ps_ms]=$(( (e - s) / 1000000 ))

	log "ls_etc=${GUEST[cmd_ls_etc_ms]}ms find=${GUEST[cmd_find_conf_ms]}ms ps=${GUEST[cmd_ps_ms]}ms"
}

run_packages() {
	log "--- packages: dnf install httpd ---"
	local s e
	s=$(date +%s%N)
	if dnf install -y httpd >/tmp/canario-dnf.log 2>&1; then
		GUEST[dnf_install_status]=ok
	else
		GUEST[dnf_install_status]=failed
		log "dnf install failed (see /tmp/canario-dnf.log)"
		return 1
	fi
	e=$(date +%s%N)
	GUEST[dnf_install_ms]=$(( (e - s) / 1000000 ))
	log "dnf_install=${GUEST[dnf_install_ms]}ms"
}

run_compile() {
	log "--- compile ---"
	cat >/tmp/canario-bench.c <<'CEOF'
#include <stdio.h>
#include <math.h>
int main() {
    double sum = 0;
    for (long i = 0; i < 10000000; i++) {
        sum += sin(i) * cos(i);
    }
    printf("Result: %f\n", sum);
    return 0;
}
CEOF
	local s e
	s=$(date +%s%N)
	if ! gcc -O2 -o /tmp/canario-bench /tmp/canario-bench.c -lm 2>/tmp/canario-gcc.log; then
		GUEST[compile_gcc_ms]=failed
		return 1
	fi
	e=$(date +%s%N)
	GUEST[compile_gcc_ms]=$(( (e - s) / 1000000 ))

	s=$(date +%s%N)
	/tmp/canario-bench >/dev/null
	e=$(date +%s%N)
	GUEST[compile_run_ms]=$(( (e - s) / 1000000 ))
	log "gcc=${GUEST[compile_gcc_ms]}ms run=${GUEST[compile_run_ms]}ms"
}

run_cpu() {
	log "--- cpu: stress-ng matrix ---"
	if ! command -v stress-ng >/dev/null 2>&1; then
		GUEST[cpu_matrix_bogo_ops]=missing
		return 1
	fi
	local cores out
	cores=$(nproc 2>/dev/null || echo 1)
	out=$(stress-ng --matrix "${cores}" --timeout "${STRESS_TIMEOUT}s" --metrics 2>&1) || true
	echo "$out" >"${RESULTS_DIR}/cpu.txt"
	GUEST[cpu_matrix_bogo_ops]=$(echo "$out" | grep -E 'matrix[[:space:]]+[0-9]' | awk '{print $(NF-1)}' | head -1 || echo N/A)
	log "matrix_bogo=${GUEST[cpu_matrix_bogo_ops]}"
}

run_memory() {
	log "--- memory: stress-ng memcpy ---"
	if ! command -v stress-ng >/dev/null 2>&1; then
		GUEST[mem_memcpy_bogo_ops]=missing
		return 1
	fi
	local out
	out=$(stress-ng --memcpy 1 --timeout "${STRESS_TIMEOUT}s" --metrics 2>&1) || true
	echo "$out" >"${RESULTS_DIR}/memory.txt"
	GUEST[mem_memcpy_bogo_ops]=$(echo "$out" | grep -E 'memcpy[[:space:]]+[0-9]' | awk '{print $(NF-1)}' | head -1 || echo N/A)
	log "memcpy_bogo=${GUEST[mem_memcpy_bogo_ops]}"
}

run_network() {
	log "--- network ---"
	local out s e

	out=$(ping -c 10 -q "$PING_TARGET" 2>&1) || true
	echo "$out" >"${RESULTS_DIR}/net_ping.txt"
	GUEST[net_ping_avg_ms]=$(echo "$out" | grep -oE '[0-9.]+/[0-9.]+/[0-9.]+/[0-9.]+' | head -1 | cut -d/ -f2 || echo "")
	if [[ -z "${GUEST[net_ping_avg_ms]}" ]] || echo "$out" | grep -qiE '100% packet loss|Network unreachable|unknown host'; then
		GUEST[net_ping_status]=failed
		GUEST[net_ping_avg_ms]=failed
	else
		GUEST[net_ping_status]=ok
	fi

	s=$(date +%s%N)
	if command -v nslookup >/dev/null 2>&1; then
		if nslookup "$DNS_NAME" >/tmp/canario-dns.out 2>&1 && grep -qE 'Address:|Name:' /tmp/canario-dns.out; then
			GUEST[net_dns_status]=ok
		else
			GUEST[net_dns_status]=failed
		fi
	elif getent hosts "$DNS_NAME" >/dev/null 2>&1; then
		GUEST[net_dns_status]=ok
	else
		GUEST[net_dns_status]=failed
	fi
	e=$(date +%s%N)
	GUEST[net_dns_ms]=$(( (e - s) / 1000000 ))

	if command -v curl >/dev/null 2>&1; then
		out=$(curl -so /dev/null -w '%{speed_download} %{time_starttransfer} %{http_code}' \
			--connect-timeout 10 --max-time 60 "$DOWNLOAD_URL" 2>/dev/null || echo "0 0 000")
		local speed_bps http_code
		speed_bps=$(echo "$out" | awk '{print $1}')
		http_code=$(echo "$out" | awk '{print $3}')
		GUEST[net_download_mbps]=$(awk "BEGIN{printf \"%.2f\", ${speed_bps:-0}/1048576}" 2>/dev/null || echo "0.00")
		GUEST[net_ttfb_ms]=$(echo "$out" | awk '{printf "%.0f", $2*1000}' 2>/dev/null || echo "0")
		if [[ "$http_code" == "200" ]]; then
			GUEST[net_download_status]=ok
		else
			GUEST[net_download_status]=failed
		fi
	else
		GUEST[net_download_status]=missing
	fi

	GUEST[network_status]=ok
	if [[ "${GUEST[net_ping_status]}" == "failed" && "${GUEST[net_dns_status]}" == "failed" && "${GUEST[net_download_status]}" == "failed" ]]; then
		GUEST[network_status]=failed
	fi
	log "ping=${GUEST[net_ping_avg_ms]} dns=${GUEST[net_dns_ms]}ms dl=${GUEST[net_download_mbps]}Mbps status=${GUEST[network_status]}"
	# Soft: network failures do not fail the suite
	return 0
}

run_git() {
	log "--- git clone ${GIT_URL} ---"
	local s e
	rm -rf /tmp/canario-git
	s=$(date +%s%N)
	if git clone --depth 1 "$GIT_URL" /tmp/canario-git >/tmp/canario-git.log 2>&1; then
		GUEST[git_clone_status]=ok
	else
		GUEST[git_clone_status]=failed
		log "git clone failed (soft)"
	fi
	e=$(date +%s%N)
	GUEST[git_clone_ms]=$(( (e - s) / 1000000 ))
	return 0
}

json_escape() {
	printf '%s' "$1" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()), end="")' 2>/dev/null \
		|| printf '"%s"' "${1//\"/\\\"}"
}

append_disk_result() {
	local sc="$1" dev="$2" volmode="$3" access="$4" prov="$5" disk_status="$6"
	local -n _m=$7
	local msg="${8:-}"
	DISK_JSON_PARTS+=("$(cat <<EOF
{
  "storage_class": $(json_escape "$sc"),
  "data_dev": $(json_escape "$dev"),
  "volume_mode": $(json_escape "$volmode"),
  "access_mode": $(json_escape "$access"),
  "provisioner": $(json_escape "$prov"),
  "suite_status": $(json_escape "$disk_status"),
  "sections": $(json_escape "$SECTIONS"),
  "node_name": $(json_escape "$NODE_NAME"),
  "fio_seqwrite_bw": $(json_escape "${_m[fio_seqwrite_bw]:-}"),
  "fio_seqread_bw": $(json_escape "${_m[fio_seqread_bw]:-}"),
  "fio_randread_iops": $(json_escape "${_m[fio_randread_iops]:-}"),
  "fio_randwrite_iops": $(json_escape "${_m[fio_randwrite_iops]:-}"),
  "fileops_create_1000_ms": $(json_escape "${_m[fileops_create_1000_ms]:-}"),
  "fileops_tar_create_ms": $(json_escape "${_m[fileops_tar_create_ms]:-}"),
  "fileops_tar_extract_ms": $(json_escape "${_m[fileops_tar_extract_ms]:-}"),
  "message": $(json_escape "$msg")
}
EOF
)")
}

run_one_disk() {
	local sc="$1" dev="$2" volmode="$3" access="$4" prov="$5"
	local mount="${DATA_MOUNT}-${sc//\//-}"
	local fio_file="${mount}/fio-test"
	declare -A METRICS=()
	local disk_rc=0
	local disk_status="succeeded"
	local msg=""

	log "=== disk ${sc} on ${dev} ==="
	if ! prepare_data_disk "$dev" "$mount"; then
		disk_status="failed"
		msg="prepare failed"
		append_disk_result "$sc" "$dev" "$volmode" "$access" "$prov" "$disk_status" METRICS "$msg"
		return 1
	fi

	if section_enabled storage; then
		run_storage "$fio_file" METRICS || disk_rc=1
	fi
	if section_enabled fileops; then
		run_fileops "$mount" METRICS || disk_rc=1
	fi

	teardown_data_disk "$mount"

	if [[ "$disk_rc" -ne 0 ]]; then
		disk_status="failed"
		msg="section failed"
	fi
	append_disk_result "$sc" "$dev" "$volmode" "$access" "$prov" "$disk_status" METRICS "$msg"
	return "$disk_rc"
}

load_disks() {
	if [[ -n "$DISKS_JSON" ]]; then
		python3 - <<'PY' || true
import json, os
raw = os.environ.get("CANARIO_DISKS_JSON", "[]")
try:
    disks = json.loads(raw)
except Exception:
    disks = []
for d in disks:
    print("|".join([
        d.get("storage_class", ""),
        d.get("data_dev", ""),
        d.get("volume_mode", ""),
        d.get("access_mode", ""),
        d.get("provisioner", ""),
    ]))
PY
		return
	fi
	printf '%s|%s|%s|%s|%s\n' \
		"${CANARIO_STORAGE_CLASS:-}" \
		"${CANARIO_DATA_DEV:-/dev/vdb}" \
		"${CANARIO_VOLUME_MODE:-}" \
		"${CANARIO_ACCESS_MODE:-ReadWriteOnce}" \
		"${CANARIO_PROVISIONER:-}"
}

run_guest_sections() {
	local rc=0
	resolve_network_mode

	if section_enabled responsive; then
		run_responsive || rc=1
	fi
	if section_enabled packages; then
		run_packages || rc=1
	fi
	if section_enabled compile; then
		run_compile || rc=1
	fi
	if section_enabled cpu; then
		run_cpu || rc=1
	fi
	if section_enabled memory; then
		run_memory || rc=1
	fi

	if section_enabled network; then
		if [[ "$HAVE_INTERNET" != "1" ]]; then
			GUEST[network_status]=skipped
			GUEST[net_ping_status]=skipped
			GUEST[net_dns_status]=skipped
			GUEST[net_download_status]=skipped
			log "network skipped (no internet / CANARIO_NETWORK=off)"
		else
			run_network || true
		fi
	fi

	if section_enabled git; then
		if [[ "$HAVE_INTERNET" != "1" ]]; then
			GUEST[git_clone_status]=skipped
			log "git skipped (no internet)"
		elif [[ -z "$GIT_URL" ]]; then
			GUEST[git_clone_status]=skipped
			log "git skipped (CANARIO_GIT_URL empty)"
		else
			run_git || true
		fi
	fi

	return "$rc"
}

publish_results() {
	local disks_joined="" first=1 part
	for part in "${DISK_JSON_PARTS[@]+"${DISK_JSON_PARTS[@]}"}"; do
		if [[ "$first" -eq 1 ]]; then
			disks_joined="$part"
			first=0
		else
			disks_joined+=",$part"
		fi
	done
	cat >"$RESULTS_JSON" <<EOF
{
  "sections": $(json_escape "$SECTIONS"),
  "node_name": $(json_escape "$NODE_NAME"),
  "guest": {
    "cmd_ls_etc_ms": $(json_escape "${GUEST[cmd_ls_etc_ms]:-}"),
    "cmd_find_conf_ms": $(json_escape "${GUEST[cmd_find_conf_ms]:-}"),
    "cmd_ps_ms": $(json_escape "${GUEST[cmd_ps_ms]:-}"),
    "dnf_install_ms": $(json_escape "${GUEST[dnf_install_ms]:-}"),
    "dnf_install_status": $(json_escape "${GUEST[dnf_install_status]:-}"),
    "compile_gcc_ms": $(json_escape "${GUEST[compile_gcc_ms]:-}"),
    "compile_run_ms": $(json_escape "${GUEST[compile_run_ms]:-}"),
    "cpu_matrix_bogo_ops": $(json_escape "${GUEST[cpu_matrix_bogo_ops]:-}"),
    "mem_memcpy_bogo_ops": $(json_escape "${GUEST[mem_memcpy_bogo_ops]:-}"),
    "network_status": $(json_escape "${GUEST[network_status]:-}"),
    "net_ping_avg_ms": $(json_escape "${GUEST[net_ping_avg_ms]:-}"),
    "net_ping_status": $(json_escape "${GUEST[net_ping_status]:-}"),
    "net_dns_ms": $(json_escape "${GUEST[net_dns_ms]:-}"),
    "net_dns_status": $(json_escape "${GUEST[net_dns_status]:-}"),
    "net_download_mbps": $(json_escape "${GUEST[net_download_mbps]:-}"),
    "net_ttfb_ms": $(json_escape "${GUEST[net_ttfb_ms]:-}"),
    "net_download_status": $(json_escape "${GUEST[net_download_status]:-}"),
    "git_clone_ms": $(json_escape "${GUEST[git_clone_ms]:-}"),
    "git_clone_status": $(json_escape "${GUEST[git_clone_status]:-}")
  },
  "disks": [${disks_joined}]
}
EOF
	log "Results: ${RESULTS_JSON}"
	cat "$RESULTS_JSON"
	mkdir -p /tmp/canario
	cp -f "$RESULTS_JSON" /tmp/canario/results.json
	cp -f "$STATUS_FILE" /tmp/canario/status
	cp -f "$DONE_FILE" /tmp/canario/done 2>/dev/null || true
	chmod -R a+rX /tmp/canario
	if command -v chcon >/dev/null 2>&1; then
		chcon -R -t virt_qemu_ga_tmp_t /tmp/canario 2>/dev/null \
			|| chcon -R -t tmp_t /tmp/canario 2>/dev/null \
			|| true
	fi
}

main() {
	log "Starting suite sections=${SECTIONS} network=${NETWORK_MODE}"
	local rc=0
	local line sc dev volmode access prov

	if ! run_guest_sections; then
		rc=1
	fi

	while IFS= read -r line; do
		[[ -z "$line" ]] && continue
		IFS='|' read -r sc dev volmode access prov <<<"$line"
		if ! run_one_disk "$sc" "$dev" "$volmode" "$access" "$prov"; then
			rc=1
		fi
	done < <(load_disks)

	if [[ "$rc" -eq 0 ]]; then
		echo "succeeded" >"$STATUS_FILE"
	else
		echo "failed" >"$STATUS_FILE"
	fi
	date -u +%Y-%m-%dT%H:%M:%SZ >"$DONE_FILE"
	publish_results
	log "Suite finished status=$(cat "$STATUS_FILE")"
	exit "$rc"
}

main "$@"
