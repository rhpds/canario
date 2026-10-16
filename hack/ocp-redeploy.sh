#!/usr/bin/env bash
# Rebuild canario from the local tree into OpenShift, redeploy the operator,
# optionally wipe / recreate a CanarioRun.
#
# Defaults target the workshop-style install used in development.
# Override with env vars — never commit cluster-specific secrets here.
#
# Guest boot defaults (install-time ConfigMap; change anytime + restart manager):
#   GUEST_DATASOURCE / GUEST_DATASOURCE_NS  — or GUEST_REGISTRY
#   Pick a Ready DataSource on THIS cluster: oc get datasource -A
#
# Usage:
#   ./hack/ocp-redeploy.sh              # build + deploy + restart
#   ./hack/ocp-redeploy.sh --run        # above + delete/recreate SAMPLE_RUN
#   ./hack/ocp-redeploy.sh --destroy    # delete SAMPLE_RUN (+ owned VMs via GC)
#   ./hack/ocp-redeploy.sh --build-only
#   ./hack/ocp-redeploy.sh --deploy-only

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

NAMESPACE="${NAMESPACE:-canario-operator}"
BUILD_NAME="${BUILD_NAME:-canario}"
IMG="${IMG:-image-registry.openshift-image-registry.svc:5000/${NAMESPACE}/${BUILD_NAME}:latest}"
SAMPLE_RUN="${SAMPLE_RUN:-bench}"
# Comma-separated StorageClasses for storage=list (default: one RBD SC).
STORAGE_CLASSES="${STORAGE_CLASSES:-ocs-external-storagecluster-ceph-rbd}"
# Lab example only — not a product default. Override if this cluster differs.
GUEST_DATASOURCE="${GUEST_DATASOURCE:-fedora}"
GUEST_DATASOURCE_NS="${GUEST_DATASOURCE_NS:-openshift-virtualization-os-images}"
GUEST_REGISTRY="${GUEST_REGISTRY:-}"
PROFILE="${PROFILE:-small}"
WORKERS="${WORKERS:-busiest}"
# Placement ceilings (fraction or percent). Override per environment.
NODE_USAGE_CEILING="${NODE_USAGE_CEILING:-0.85}"
STORAGE_USAGE_CEILING="${STORAGE_USAGE_CEILING:-0.75}"
OC="${OC:-oc}"
KUBECTL="${KUBECTL:-$OC}"

DO_BUILD=1
DO_DEPLOY=1
DO_RUN=0
DO_DESTROY=0

usage() {
	sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
	exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--run) DO_RUN=1 ;;
		--destroy) DO_DESTROY=1; DO_BUILD=0; DO_DEPLOY=0 ;;
		--build-only) DO_DEPLOY=0; DO_RUN=0 ;;
		--deploy-only) DO_BUILD=0 ;;
		-h|--help) usage 0 ;;
		*) echo "unknown arg: $1" >&2; usage 1 ;;
	esac
	shift
done

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }

need() { command -v "$1" >/dev/null || { echo "missing: $1" >&2; exit 1; }; }
need "$OC"
need make

if [[ "$DO_DESTROY" -eq 1 ]]; then
	log "Destroying CanarioRun/${SAMPLE_RUN} in ${NAMESPACE}"
	"$OC" delete canariorun "$SAMPLE_RUN" -n "$NAMESPACE" --ignore-not-found --wait=true
	# Safety net if GC lags
	"$OC" delete vm,dv,pvc -n "$NAMESPACE" -l "canario.rhpds.com/canariorun=${SAMPLE_RUN}" --ignore-not-found --wait=false || true
	log "Done destroy"
	exit 0
fi

if [[ "$DO_BUILD" -eq 1 ]]; then
	if ! "$OC" get buildconfig "$BUILD_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
		log "Creating binary BuildConfig/${BUILD_NAME}"
		"$OC" new-build --binary --strategy=docker --name="$BUILD_NAME" -n "$NAMESPACE"
	fi
	log "Building ${BUILD_NAME} from ${ROOT}"
	"$OC" start-build "$BUILD_NAME" --from-dir=. --follow -n "$NAMESPACE"
fi

apply_operator_guest_defaults() {
	# Install-time ConfigMap (namePrefix → canario-operator-config). Lab values only.
	log "Applying operator guest defaults (DataSource=${GUEST_DATASOURCE}/${GUEST_DATASOURCE_NS} registry=${GUEST_REGISTRY:-none})"
	"$OC" apply -n "$NAMESPACE" -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: canario-operator-config
  labels:
    app.kubernetes.io/name: canario
data:
  CANARIO_DEFAULT_GUEST_DATASOURCE_NAME: "${GUEST_DATASOURCE}"
  CANARIO_DEFAULT_GUEST_DATASOURCE_NAMESPACE: "${GUEST_DATASOURCE_NS}"
  CANARIO_DEFAULT_GUEST_REGISTRY: "${GUEST_REGISTRY}"
  CANARIO_NODE_USAGE_CEILING: "${NODE_USAGE_CEILING}"
  CANARIO_STORAGE_USAGE_CEILING: "${STORAGE_USAGE_CEILING}"
EOF
}

if [[ "$DO_DEPLOY" -eq 1 ]]; then
	# OpenShift overlay: base + SCC kubevirt-controller use (virt-launcher guest-agent exec).
	KUSTOMIZE_DIR="${KUSTOMIZE_DIR:-config/overlays/openshift}"
	log "Deploying IMG=${IMG} KUSTOMIZE_DIR=${KUSTOMIZE_DIR}"
	make deploy KUBECTL="$KUBECTL" IMG="$IMG" KUSTOMIZE_DIR="$KUSTOMIZE_DIR"
	apply_operator_guest_defaults
	# :latest often needs an explicit restart to pull the new digest / reload envFrom
	"$OC" rollout restart "deploy/canario-controller-manager" -n "$NAMESPACE"
	"$OC" rollout status "deploy/canario-controller-manager" -n "$NAMESPACE" --timeout=180s
fi

if [[ "$DO_RUN" -eq 1 ]]; then
	log "Recreating CanarioRun/${SAMPLE_RUN} (uses operator guest defaults; no spec.guestImage)"
	"$OC" delete canariorun "$SAMPLE_RUN" -n "$NAMESPACE" --ignore-not-found --wait=true
	# Expand STORAGE_CLASSES (comma-separated) into a YAML list.
	sc_yaml=""
	IFS=',' read -r -a _scs <<<"${STORAGE_CLASSES}"
	for sc in "${_scs[@]}"; do
		sc="$(echo "$sc" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
		[[ -z "$sc" ]] && continue
		sc_yaml+="    - ${sc}"$'\n'
	done
	"$OC" apply -n "$NAMESPACE" -f - <<EOF
apiVersion: canario.rhpds.com/v1alpha1
kind: CanarioRun
metadata:
  name: ${SAMPLE_RUN}
spec:
  type: guest
  profile: ${PROFILE}
  workers: ${WORKERS}
  storage: list
  storageClasses:
${sc_yaml}
EOF
	sleep 5
	"$OC" get canariorun,vm,vmi -n "$NAMESPACE"
	log "Watch: oc get canariorun,vm -n ${NAMESPACE} -w"
fi

log "OK"
