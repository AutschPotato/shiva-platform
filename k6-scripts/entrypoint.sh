#!/bin/sh
# --- Fetch scripts from controller (Kubernetes mode) ---
if [ "${SHIVA_FETCH_SCRIPTS_FROM_CONTROLLER:-}" = "true" ] || [ "${SHIVA_FETCH_SCRIPTS_FROM_CONTROLLER:-}" = "1" ]; then
  CONTROLLER_URL="${CONTROLLER_URL:-http://controller:8080}"
  mkdir -p /scripts

  attempt=1
  while [ "$attempt" -le 30 ]; do
    if wget -q -O /scripts/current-test.js "$CONTROLLER_URL/api/internal/scripts/current-test.js"; then
      break
    fi
    if [ "$attempt" -eq 30 ]; then
      echo "failed to fetch current-test.js from $CONTROLLER_URL after 30 attempts" >&2
      exit 1
    fi
    sleep 2
    attempt=$((attempt + 1))
  done

  rm -f /scripts/config.json /scripts/k6-env.sh
  wget -q -O /scripts/config.json "$CONTROLLER_URL/api/internal/scripts/config.json" || true
  wget -q -O /scripts/k6-env.sh "$CONTROLLER_URL/api/internal/scripts/k6-env.sh" || true
fi
# --- End fetch ---

mkdir -p /output

SCRIPT="/scripts/current-test.js"
CONFIG="/scripts/config.json"
ENV_FILE="/scripts/k6-env.sh"

K6_ENV_FLAGS=""
if [ -f "$ENV_FILE" ]; then
  . "$ENV_FILE"
fi

# Include p(99) in handleSummary output (env var avoids shell escaping issues with parentheses)
export K6_SUMMARY_TREND_STATS="avg,min,med,max,p(90),p(95),p(99)"

# Enable the built-in k6 dashboard when configured by the worker runtime.
# The controller later proxies these live worker-local dashboards for admins.
if [ "${K6_WEB_DASHBOARD:-}" = "true" ] || [ "${K6_WEB_DASHBOARD:-}" = "1" ]; then
  export K6_WEB_DASHBOARD="true"
  export K6_WEB_DASHBOARD_HOST="${K6_WEB_DASHBOARD_HOST:-0.0.0.0}"
  export K6_WEB_DASHBOARD_PORT="${K6_WEB_DASHBOARD_PORT:-5665}"
fi

CMD="k6 run --paused --quiet --address 0.0.0.0:6565"

if [ -f "$CONFIG" ]; then
  CMD="$CMD --config $CONFIG"
fi

CMD="$CMD $K6_ENV_FLAGS $SCRIPT"

upload_artifact() {
  artifact_type="$1"
  file_path="$2"
  content_type="$3"

  if [ ! -f "$file_path" ]; then
    return 0
  fi

  if [ ! -s "$file_path" ]; then
    return 0
  fi

  if [ -z "${SHIVA_ARTIFACT_TEST_ID:-}" ] || [ -z "${WORKER_ID:-}" ] || [ -z "${SHIVA_ARTIFACT_UPLOAD_TOKEN:-}" ]; then
    return 0
  fi

  base_url="${SHIVA_ARTIFACT_UPLOAD_URL:-${CONTROLLER_URL:-http://controller:8080}}"
  upload_url="${base_url%/}/api/internal/runs/${SHIVA_ARTIFACT_TEST_ID}/workers/${WORKER_ID}/${artifact_type}"

  attempt=1
  while [ "$attempt" -le 5 ]; do
    if curl -fsS -X POST \
      -H "X-Shiva-Artifact-Token: ${SHIVA_ARTIFACT_UPLOAD_TOKEN}" \
      -H "Content-Type: ${content_type}" \
      --data-binary "@${file_path}" \
      "$upload_url" >/dev/null; then
      return 0
    fi
    if [ "$attempt" -eq 5 ]; then
      echo "failed to upload ${artifact_type} for ${WORKER_ID} after 5 attempts" >&2
      return 1
    fi
    sleep 2
    attempt=$((attempt + 1))
  done
}

run_exit=0
eval "$CMD" || run_exit=$?

if [ "${SHIVA_ARTIFACT_UPLOAD_ENABLED:-}" = "true" ] || [ "${SHIVA_ARTIFACT_UPLOAD_ENABLED:-}" = "1" ]; then
  upload_artifact "summary" "/output/summary-${WORKER_ID}.json" "application/json" || true
  upload_artifact "auth-summary" "/output/auth-summary-${WORKER_ID}.json" "application/json" || true
  upload_artifact "payload" "/output/payload-${WORKER_ID}.json" "application/json" || true
fi

exit "$run_exit"
