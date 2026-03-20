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
eval exec $CMD
