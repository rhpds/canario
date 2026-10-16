#!/bin/bash
# Canario in-guest suite (extracted/adapted from ocp-virt-ux-bench guest benches).
# Modular: set CANARIO_SUITE_SECTIONS to a comma-separated list.
# Supported sections (v1): storage, fileops
# Default: storage
#
# Env:
#   CANARIO_DISKS_JSON   JSON array of disks (preferred for multi-SC)
#   CANARIO_DATA_DEV     single-disk fallback (default /dev/vdb)
#   CANARIO_STORAGE_CLASS single-disk fallback SC name
#   CANARIO_DATA_MOUNT   mount point (default /mnt/canario-data)
#   CANARIO_RESULTS_DIR  results dir (default /var/lib/canario)
#   CANARIO_FIO_RUNTIME  seconds per fio job (default 15)

set -euo pipefail

SECTIONS="${CANARIO_SUITE_SECTIONS:-storage}"
NODE_NAME="${CANARIO_NODE_NAME:-}"
DATA_MOUNT="${CANARIO_DATA_MOUNT:-/mnt/canario-data}"
RESULTS_DIR="${CANARIO_RESULTS_DIR:-/var/lib/canario}"
FIO_RUNTIME="${CANARIO_FIO_RUNTIME:-15}"
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

# Disk rows accumulated as JSON objects (no trailing commas).
DISK_JSON_PARTS=()

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
		log "fio missing — install via cloud-init packages"
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
	# Prints lines: storage_class|data_dev|volume_mode|access_mode|provisioner
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
	# Single-disk fallback
	printf '%s|%s|%s|%s|%s\n' \
		"${CANARIO_STORAGE_CLASS:-}" \
		"${CANARIO_DATA_DEV:-/dev/vdb}" \
		"${CANARIO_VOLUME_MODE:-}" \
		"${CANARIO_ACCESS_MODE:-ReadWriteOnce}" \
		"${CANARIO_PROVISIONER:-}"
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
	log "Starting suite sections=${SECTIONS}"
	local rc=0
	local line sc dev volmode access prov

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
