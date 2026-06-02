#!/usr/bin/env bash
set -euo pipefail

ACTION="${1:-up}"

PROJECT="${PROJECT:-dozzle-load-smoke}"
NETWORK="${NETWORK:-${PROJECT}-net}"
DOZZLE_IMAGE="${DOZZLE_IMAGE:-dozzle-gaia:dashboard-current}"
AGENT_IMAGE="${AGENT_IMAGE:-dozzle-gaia:agent-older}"
GENERATOR_IMAGE="${GENERATOR_IMAGE:-golang:1.26-alpine}"
DASHBOARD_NAME="${DASHBOARD_NAME:-${PROJECT}-dashboard}"
AGENT_NAME="${AGENT_NAME:-${PROJECT}-agent}"
PORT="${PORT:-5000}"
CONTAINERS="${CONTAINERS:-40}"
LOG_MIN_DELAY="${LOG_MIN_DELAY:-10}"
LOG_MAX_DELAY="${LOG_MAX_DELAY:-20}"
REMOTE_AGENTS="${REMOTE_AGENTS:-}"
DIND_HOSTS="${DIND_HOSTS:-0}"
NTFY_URL="${NTFY_URL:-http://${PROJECT}-ntfy:8080}"
NTFY_PUBLIC_URL="${NTFY_PUBLIC_URL:-http://127.0.0.1:${NTFY_PORT:-8099}}"
NTFY_TOPIC="${NTFY_TOPIC:-dozzle-load-smoke}"
DOZZLE_URL="${DOZZLE_URL:-http://127.0.0.1:${PORT}}"
LABEL="dozzle.load.smoke=${PROJECT}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

docker_run() {
  docker run "$@"
}

cleanup_project() {
  docker rm -f "$DASHBOARD_NAME" "$AGENT_NAME" "${PROJECT}-ntfy" >/dev/null 2>&1 || true
  docker rm -f $(docker ps -aq --filter "label=$LABEL") >/dev/null 2>&1 || true
  docker rm -f $(docker ps -aq --filter "label=dozzle.load.smoke.dind=${PROJECT}") >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
}

ensure_network() {
  docker network create "$NETWORK" >/dev/null 2>&1 || true
}

start_generators() {
  for raw in $(seq 1 "$CONTAINERS"); do
    idx="$(printf "%02d" "$raw")"
    name="${PROJECT}-load-${idx}"
    delay=$((LOG_MIN_DELAY + (raw % (LOG_MAX_DELAY - LOG_MIN_DELAY + 1))))
    group=$((raw % 4))
    docker rm -f "$name" >/dev/null 2>&1 || true
    docker_run -d \
      --name "$name" \
      --label "$LABEL" \
      --label "dozzle.load.group=g${group}" \
      --memory 64m \
      --cpus 0.05 \
      "$GENERATOR_IMAGE" \
      sh -lc "n=0; while true; do n=\$((n+1)); echo '{\"level\":\"info\",\"service\":\"load-${idx}\",\"group\":\"g${group}\",\"seq\":'\$n',\"message\":\"LOGIC_TEST dozzle low-rate load sample\"}'; if [ \$((n % 12)) -eq 0 ]; then echo 'WARN LOGIC_TEST load-${idx} periodic checkpoint group=g${group}' >&2; fi; sleep ${delay}; done" >/dev/null
  done
}

start_ntfy_dump() {
  docker rm -f "${PROJECT}-ntfy" >/dev/null 2>&1 || true
  local tmpdir
  tmpdir="$(mktemp -d)"
  cat > "${tmpdir}/ntfy-dump.go" <<'EOF'
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Printf("%s %s %s %s\n%s\n", time.Now().Format(time.RFC3339), r.Method, r.URL.Path, r.RemoteAddr, string(body))
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok\n"))
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}
EOF
  docker_run -d \
    --name "${PROJECT}-ntfy" \
    --network "$NETWORK" \
    --network-alias "${PROJECT}-ntfy" \
    --label "$LABEL" \
    -p "${NTFY_PORT:-8099}:8080" \
    -v "${tmpdir}/ntfy-dump.go:/ntfy-dump.go:ro" \
    "$GENERATOR_IMAGE" \
    go run /ntfy-dump.go >/dev/null
}

start_dind_hosts() {
  if [ "$DIND_HOSTS" -le 0 ]; then
    return
  fi

  for host_idx in $(seq 1 "$DIND_HOSTS"); do
    dind="${PROJECT}-dind-${host_idx}"
    agent="${PROJECT}-dind-agent-${host_idx}"
    docker rm -f "$agent" "$dind" >/dev/null 2>&1 || true
    if ! docker_run -d \
      --name "$dind" \
      --network "$NETWORK" \
      --network-alias "$dind" \
      --label "dozzle.load.smoke.dind=${PROJECT}" \
      --privileged \
      -e DOCKER_TLS_CERTDIR= \
      docker:29-dind >/dev/null; then
      echo "failed to start ${dind}. DIND_HOSTS requires a rootful Docker daemon with --privileged support." >&2
      echo "Use REMOTE_AGENTS=host1:7007,host2:7007 for real production hosts, or rerun with DIND_HOSTS=0." >&2
      exit 1
    fi

    echo "waiting for ${dind} Docker daemon..."
    for _ in $(seq 1 60); do
      if docker exec "$dind" docker info >/dev/null 2>&1; then
        break
      fi
      sleep 1
    done

    docker save "$GENERATOR_IMAGE" | docker exec -i "$dind" docker load >/dev/null
    for raw in $(seq 1 5); do
      idx="$(printf "%02d" "$raw")"
      docker exec "$dind" docker rm -f "nested-load-${host_idx}-${idx}" >/dev/null 2>&1 || true
      docker exec "$dind" docker run -d \
        --name "nested-load-${host_idx}-${idx}" \
        --label "$LABEL" \
        "$GENERATOR_IMAGE" \
        sh -lc "n=0; while true; do n=\$((n+1)); echo '{\"level\":\"info\",\"service\":\"nested-${host_idx}-${idx}\",\"message\":\"LOGIC_TEST nested host load sample\",\"seq\":'\$n'}'; sleep 15; done" >/dev/null
    done

    docker_run -d \
      --name "$agent" \
      --network "$NETWORK" \
      --network-alias "$agent" \
      --label "dozzle.load.smoke.dind=${PROJECT}" \
      -e DOCKER_HOST="tcp://${dind}:2375" \
      -e DOZZLE_HOSTNAME="dind-host-${host_idx}" \
      -e DOZZLE_HOST_GROUP="Load Smoke" \
      "$AGENT_IMAGE" agent >/dev/null

    REMOTE_AGENTS="${REMOTE_AGENTS:+${REMOTE_AGENTS},}${agent}:7007|dind-host-${host_idx}|Load Smoke"
  done
}

start_agent_and_dashboard() {
  ensure_network
  docker rm -f "$AGENT_NAME" "$DASHBOARD_NAME" >/dev/null 2>&1 || true

  docker_run -d \
    --name "$AGENT_NAME" \
    --network "$NETWORK" \
    --network-alias "$AGENT_NAME" \
    --label "$LABEL" \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -e DOZZLE_HOSTNAME="${AGENT_HOSTNAME:-load-host-1}" \
    -e DOZZLE_HOST_GROUP="${AGENT_GROUP:-Load Smoke}" \
    "$AGENT_IMAGE" agent >/dev/null

  local agents="${REMOTE_AGENTS:+${REMOTE_AGENTS},}${AGENT_NAME}:7007|${AGENT_HOSTNAME:-load-host-1}|${AGENT_GROUP:-Load Smoke}"
  IFS=',' read -r -a agent_array <<< "$agents"
  local args=()
  for endpoint in "${agent_array[@]}"; do
    [ -n "$endpoint" ] && args+=(--remote-agent "$endpoint")
  done

  docker_run -d \
    --name "$DASHBOARD_NAME" \
    --network "$NETWORK" \
    --label "$LABEL" \
    -p "${PORT}:8080" \
    -v "${PROJECT}-data:/data" \
    "$DOZZLE_IMAGE" "${args[@]}" >/dev/null
}

create_alerts() {
  need curl
  need python3

  echo "waiting for Dozzle API at ${DOZZLE_URL}..."
  for _ in $(seq 1 60); do
    if curl -fsS "${DOZZLE_URL}/api/notifications/dispatchers" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done

  python3 - "$DOZZLE_URL" "$NTFY_URL" "$NTFY_TOPIC" <<'PY'
import json
import sys
import urllib.request

base, ntfy_url, ntfy_topic = sys.argv[1:4]

def request(method, path, payload=None):
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(
        base + path,
        data=data,
        method=method,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=10) as res:
        body = res.read().decode()
        return json.loads(body) if body else None

dispatchers = request("GET", "/api/notifications/dispatchers")
dispatcher = next((d for d in dispatchers if d.get("name") == "Load smoke ntfy dump"), None)
if dispatcher is None:
    dispatcher = request("POST", "/api/notifications/dispatchers", {
        "name": "Load smoke ntfy dump",
        "type": "ntfy",
        "url": ntfy_url,
        "topic": ntfy_topic,
        "priority": 3,
        "tags": ["test_tube"],
        "titleTemplate": "Dozzle load smoke: {{ .Container.Name }}",
        "messageTemplate": "{{ .Log.Message }}",
    })

rules = request("GET", "/api/notifications/rules")
existing = {r.get("name") for r in rules}
payloads = [
    {
        "name": "Load smoke LOGIC_TEST info",
        "alertGroup": "Load smoke",
        "enabled": True,
        "dispatcherId": dispatcher["id"],
        "containerExpression": "name startsWith \"dozzle-load\" or name startsWith \"nested-load\" or labels[\"dozzle.load.smoke\"] != nil",
        "logExpression": "string(message) contains \"LOGIC_TEST\"",
        "cooldown": 30,
        "ntfyTopic": ntfy_topic,
        "ntfyPriority": 3,
        "ntfyTags": ["test_tube", "logic"],
        "uniqueKeyRegex": "service\\\\\\\":\\\\\\\"([^\\\\\\\"]+)",
        "uniqueWindow": 60,
        "uniqueThreshold": 1,
    },
    {
        "name": "Load smoke WARN checkpoints",
        "alertGroup": "Load smoke",
        "enabled": True,
        "dispatcherId": dispatcher["id"],
        "containerExpression": "name startsWith \"dozzle-load\" or name startsWith \"nested-load\"",
        "logExpression": "string(message) contains \"WARN\" and string(message) contains \"LOGIC_TEST\"",
        "cooldown": 60,
        "ntfyTopic": ntfy_topic,
        "ntfyPriority": 4,
        "ntfyTags": ["warning", "logic"],
    },
]

for payload in payloads:
    if payload["name"] not in existing:
        created = request("POST", "/api/notifications/rules", payload)
        print(f"created alert {created['id']}: {created['name']}")
    else:
        print(f"alert exists: {payload['name']}")

print(f"ntfy dispatcher URL: {ntfy_url.rstrip('/')}")
print(f"ntfy topic: {ntfy_topic}")
PY
}

status() {
  echo "containers with ${LABEL}:"
  docker ps --filter "label=$LABEL" --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'
  echo
  echo "Dozzle endpoint: ${DOZZLE_URL}"
  echo "ntfy dispatcher URL: ${NTFY_URL}"
  echo "ntfy public URL: ${NTFY_PUBLIC_URL}"
  echo "ntfy topic: ${NTFY_TOPIC}"
}

case "$ACTION" in
  up)
    need docker
    ensure_network
    start_dind_hosts
    start_generators
    start_agent_and_dashboard
    echo "started load smoke on ${DOZZLE_URL}"
    ;;
  alerts)
    create_alerts
    ;;
  ntfy)
    need docker
    ensure_network
    start_ntfy_dump
    echo "started ntfy dump container at ${NTFY_URL}; public port ${NTFY_PUBLIC_URL}; logs: docker logs ${PROJECT}-ntfy"
    ;;
  status)
    status
    ;;
  down)
    cleanup_project
    echo "removed ${PROJECT}"
    ;;
  *)
    echo "usage: $0 {up|alerts|ntfy|status|down}" >&2
    exit 2
    ;;
esac
