#!/bin/sh
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
