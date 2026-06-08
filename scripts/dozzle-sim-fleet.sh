#!/usr/bin/env bash
# Simulate a multi-host fleet: N hosts, each a real gRPC agent (distinct host ID
# via DOZZLE_HOST_ID) showing only its own containers (via --filter). All run on
# a single Docker daemon but appear in the dashboard as separate remote agents.
set -euo pipefail

ACTION="${1:-up}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PROJECT="${PROJECT:-dozzle-sim}"
NETWORK="${NETWORK:-${PROJECT}-net}"
PORT="${PORT:-5000}"
HOSTS="${HOSTS:-10}"
PER_HOST="${PER_HOST:-5}"
TOTAL_CONTAINERS="${TOTAL_CONTAINERS:-}"
DOZZLE_IMAGE="${DOZZLE_IMAGE:-dozzle-gaia:dashboard-current}"
AGENT_IMAGE="${AGENT_IMAGE:-dozzle-gaia:agent-sim}"
GENERATOR_IMAGE="${GENERATOR_IMAGE:-golang:1.26-alpine}"
SOCK="${SOCK:-/run/user/1000/docker.sock}"
DATA_DIR="${DATA_DIR:-}"
SEED_CACHE="${SEED_CACHE:-0}"
CACHE_DAYS="${CACHE_DAYS:-4}"
CACHE_LINES_PER_DAY="${CACHE_LINES_PER_DAY:-250}"
INCLUDE_EXPIRED_CACHE="${INCLUDE_EXPIRED_CACHE:-1}"
LABEL="dozzle.sim=${PROJECT}"

# host index -> "display-name|Group"
host_meta() {
  case "$1" in
    1) echo "prod-eu-web-01|Production EU" ;;
    2) echo "prod-eu-web-02|Production EU" ;;
    3) echo "prod-eu-db-01|Production EU" ;;
    4) echo "prod-us-web-01|Production US" ;;
    5) echo "prod-us-web-02|Production US" ;;
    6) echo "prod-us-db-01|Production US" ;;
    7) echo "staging-app-01|Staging" ;;
    8) echo "staging-app-02|Staging" ;;
    9) echo "staging-worker-01|Staging" ;;
    10) echo "staging-cache-01|Staging" ;;
    *) echo "sim-host-$1|Simulated" ;;
  esac
}

svc_name() {
  case "$1" in
    1) echo "nginx" ;; 2) echo "api" ;; 3) echo "postgres" ;; 4) echo "redis" ;; 5) echo "worker" ;;
    *) echo "svc$1" ;;
  esac
}

ensure_network() { docker network create "$NETWORK" >/dev/null 2>&1 || true; }

containers_for_host() {
  if [ -n "$TOTAL_CONTAINERS" ]; then
    local base=$((TOTAL_CONTAINERS / HOSTS))
    local extra=$((TOTAL_CONTAINERS % HOSTS))
    if [ "$1" -le "$extra" ]; then
      echo $((base + 1))
    else
      echo "$base"
    fi
  else
    echo "$PER_HOST"
  fi
}

cleanup() {
  docker rm -f "${PROJECT}-dashboard" >/dev/null 2>&1 || true
  docker rm -f $(docker ps -aq --filter "label=$LABEL") >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
}

start_containers() {
  for h in $(seq 1 "$HOSTS"); do
    hid="$(printf "h%02d" "$h")"
    count="$(containers_for_host "$h")"
    for c in $(seq 1 "$count"); do
      svc="$(svc_name "$c")"
      name="${PROJECT}-${hid}-${svc}"
      docker rm -f "$name" >/dev/null 2>&1 || true
      docker run -d \
        --name "$name" \
        --label "$LABEL" \
        --label "dozzle.simhost=${hid}" \
        --label "dev.dozzle.group=${svc}" \
        --memory 32m --cpus 0.03 \
        "$GENERATOR_IMAGE" \
        sh -lc "n=0; while true; do n=\$((n+1));
          echo '{\"level\":\"info\",\"svc\":\"${svc}\",\"host\":\"${hid}\",\"seq\":'\$n',\"msg\":\"request handled\"}';
          if [ \$((n % 7)) -eq 0 ]; then echo 'WARN ${svc} slow query detected seq='\$n; fi;
          if [ \$((n % 23)) -eq 0 ]; then echo '{\"level\":\"error\",\"svc\":\"${svc}\",\"msg\":\"connection reset\",\"seq\":'\$n'}' >&2; fi;
          sleep \$(( (n % 5) + 3 )); done" >/dev/null
    done
  done
}

seed_cache() {
  if [ "$SEED_CACHE" != "1" ]; then
    return
  fi
  if [ -z "$DATA_DIR" ]; then
    echo "SEED_CACHE=1 requires DATA_DIR to be set" >&2
    exit 1
  fi
  mkdir -p "$DATA_DIR"
  python3 "$SCRIPT_DIR/seed_log_cache.py" \
    --project "$PROJECT" \
    --data-dir "$DATA_DIR" \
    --days "$CACHE_DAYS" \
    --lines-per-day "$CACHE_LINES_PER_DAY" \
    --include-expired "$INCLUDE_EXPIRED_CACHE"
}

start_agents_and_dashboard() {
  ensure_network
  local agent_args=()
  for h in $(seq 1 "$HOSTS"); do
    hid="$(printf "h%02d" "$h")"
    meta="$(host_meta "$h")"
    name="${meta%%|*}"; group="${meta##*|}"
    agent="${PROJECT}-agent-${hid}"
    docker rm -f "$agent" >/dev/null 2>&1 || true
    docker run -d \
      --name "$agent" \
      --network "$NETWORK" --network-alias "$agent" \
      --label "$LABEL" \
      -v "${SOCK}:/var/run/docker.sock" \
      -e DOZZLE_HOST_ID="sim-${hid}" \
      -e DOZZLE_HOSTNAME="$name" \
      -e DOZZLE_HOST_GROUP="$group" \
      -e DOZZLE_FILTER="label=dozzle.simhost=${hid}" \
      "$AGENT_IMAGE" agent >/dev/null
    agent_args+=(--remote-agent "${agent}:7007|${name}|${group}")
  done

  docker rm -f "${PROJECT}-dashboard" >/dev/null 2>&1 || true
  local dashboard_mount=()
  local dashboard_env=()
  if [ -n "$DATA_DIR" ]; then
    mkdir -p "$DATA_DIR"
    dashboard_mount=(-v "${DATA_DIR}:/data")
    dashboard_env=(-e DOZZLE_LOG_CACHE_DIR=/data/cache)
  fi
  docker run -d \
    --name "${PROJECT}-dashboard" \
    --network "$NETWORK" \
    --label "$LABEL" \
    -p "${PORT}:8080" \
    "${dashboard_mount[@]}" \
    "${dashboard_env[@]}" \
    "$DOZZLE_IMAGE" "${agent_args[@]}" >/dev/null
}

case "$ACTION" in
  up)
    cleanup
    ensure_network
    if [ -n "$TOTAL_CONTAINERS" ]; then
      echo "starting ${HOSTS} hosts with ${TOTAL_CONTAINERS} total containers ..."
    else
      echo "starting ${HOSTS} hosts x ${PER_HOST} containers ..."
    fi
    start_containers
    seed_cache
    echo "starting ${HOSTS} agents + dashboard ..."
    start_agents_and_dashboard
    total="${TOTAL_CONTAINERS:-$((HOSTS*PER_HOST))}"
    echo "fleet up on http://127.0.0.1:${PORT}  (${HOSTS} hosts, ${total} containers)"
    ;;
  status)
    docker ps --filter "label=$LABEL" --format 'table {{.Names}}\t{{.Status}}' | head -80
    ;;
  down)
    cleanup
    echo "removed ${PROJECT}"
    ;;
  *) echo "usage: $0 {up|status|down}" >&2; exit 2 ;;
esac
