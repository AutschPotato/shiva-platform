import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import encoding from 'k6/encoding';
import { Rate, Counter, Trend } from 'k6/metrics';

const errorRate = new Rate('errors');
const successRate = new Rate('success_rate');
const status4xx = new Counter('status_4xx');
const status5xx = new Counter('status_5xx');
const authTokenRequests = new Counter('auth_token_requests_total');
const authTokenSuccess = new Counter('auth_token_success_total');
const authTokenFailure = new Counter('auth_token_failure_total');
const authTokenRefresh = new Counter('auth_token_refresh_total');
const authTokenReuseHits = new Counter('auth_token_reuse_hits_total');
const authTokenRequestDuration = new Trend('auth_token_request_duration_ms', true);
const businessHttpRequests = new Counter('business_http_requests_total');
const businessHttpSuccess = new Counter('business_http_success_total');
const businessHttpFailure = new Counter('business_http_failure_total');
const businessStatus2xx = new Counter('business_status_2xx');
const businessStatus4xx = new Counter('business_status_4xx');
const businessStatus5xx = new Counter('business_status_5xx');
const businessTransportFailures = new Counter('business_transport_failures_total');
const businessHttpDuration = new Trend('business_http_duration_ms', true);
const businessHttpBlocked = new Trend('business_http_blocked_ms', true);
const businessHttpWaiting = new Trend('business_http_waiting_ms', true);
const businessHttpSending = new Trend('business_http_sending_ms', true);
const businessHttpReceiving = new Trend('business_http_receiving_ms', true);
const businessHttpConnecting = new Trend('business_http_connecting_ms', true);
const businessHttpTLSHandshaking = new Trend('business_http_tls_handshaking_ms', true);

function envString(name, fallback) {
  const value = __ENV[name];
  return value === undefined || value === null ? fallback : String(value);
}

function envNumber(name, fallback) {
  const parsed = Number(envString(name, ''));
  return Number.isFinite(parsed) ? parsed : fallback;
}

function envBool(name, fallback) {
  const raw = envString(name, fallback ? 'true' : 'false').trim().toLowerCase();
  return raw === '1' || raw === 'true' || raw === 'yes' || raw === 'on';
}

function decodePayloadSourceJSON(rawValue, encodedValue) {
  if (encodedValue) {
    try {
      return encoding.b64decode(encodedValue, 'std', 's');
    } catch (error) {
      throw new Error('failed to decode payload JSON from base64 env: ' + String(error));
    }
  }
  return rawValue;
}

const BASE_URL = envString('TARGET_URL', '');
const HTTP_METHOD = envString('HTTP_METHOD', 'GET').toUpperCase();
const CONTENT_TYPE = envString('CONTENT_TYPE', 'application/json');
const PAYLOAD_SOURCE_JSON = decodePayloadSourceJSON(
  envString('PAYLOAD_SOURCE_JSON', ''),
  envString('PAYLOAD_SOURCE_JSON_B64', ''),
);
const PAYLOAD_TARGET_BYTES = Math.max(0, envNumber('PAYLOAD_TARGET_BYTES', 0));
const PAYLOAD_PADDING_KEY = '_shiva_padding';
const AUTH_ENABLED = envBool('AUTH_ENABLED', false);
const AUTH_MODE = envString('AUTH_MODE', 'oauth_client_credentials');
const AUTH_TOKEN_URL = envString('AUTH_TOKEN_URL', '');
const AUTH_CLIENT_ID = envString('AUTH_CLIENT_ID', '');
const AUTH_CLIENT_AUTH_METHOD = envString('AUTH_CLIENT_AUTH_METHOD', 'basic');
const AUTH_REFRESH_SKEW_SECONDS = Math.max(1, envNumber('AUTH_REFRESH_SKEW_SECONDS', 30));
const AUTH_CLIENT_SECRET = envString('AUTH_CLIENT_SECRET', '');
const AUTH_RETRY_LIMIT = Math.max(0, envNumber('AUTH_RETRY_LIMIT', 1));
const AUTH_RETRYABLE_STATUS_CODES = envString('AUTH_RETRYABLE_STATUS_CODES', '408,429,502,503,504');
const AUTH_MAX_JITTER_MS = Math.max(0, envNumber('AUTH_MAX_JITTER_MS', 5000));
const AUTH_TOKEN_TIMEOUT = envString('AUTH_TOKEN_TIMEOUT', '10s');

function utf8ByteLength(value) {
  return unescape(encodeURIComponent(value)).length;
}

function canonicalizePayloadValue(value) {
  if (Array.isArray(value)) {
    return value.map(canonicalizePayloadValue);
  }
  if (value && typeof value === 'object') {
    const canonical = {};
    for (const key of Object.keys(value).sort()) {
      canonical[key] = canonicalizePayloadValue(value[key]);
    }
    return canonical;
  }
  return value;
}

function serializePayload(value) {
  return JSON.stringify(canonicalizePayloadValue(value));
}

function prepareRequestPayload() {
  if (HTTP_METHOD === 'GET') {
    return {
      sourceJSON: PAYLOAD_SOURCE_JSON || '',
      content: '',
      targetBytes: PAYLOAD_TARGET_BYTES,
      actualBytes: 0,
    };
  }

  const hasExplicitPayload = PAYLOAD_SOURCE_JSON !== '';
  if (!hasExplicitPayload && PAYLOAD_TARGET_BYTES === 0) {
    return {
      sourceJSON: '',
      content: '',
      targetBytes: 0,
      actualBytes: 0,
    };
  }

  const parsed = hasExplicitPayload ? JSON.parse(PAYLOAD_SOURCE_JSON) : {};
  let content = serializePayload(parsed);
  let actualBytes = utf8ByteLength(content);

  if (PAYLOAD_TARGET_BYTES === 0) {
    return {
      sourceJSON: PAYLOAD_SOURCE_JSON || '',
      content,
      targetBytes: 0,
      actualBytes,
    };
  }

  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
    if (actualBytes !== PAYLOAD_TARGET_BYTES) {
      throw new Error('payload_target_kib requires an object payload unless the serialized JSON already matches the exact target size');
    }
    return {
      sourceJSON: PAYLOAD_SOURCE_JSON || '',
      content,
      targetBytes: PAYLOAD_TARGET_BYTES,
      actualBytes,
    };
  }

  if (actualBytes > PAYLOAD_TARGET_BYTES) {
    throw new Error('payload_target_kib is smaller than the minimum serialized JSON payload size');
  }

  const payload = canonicalizePayloadValue(parsed);
  if (Object.prototype.hasOwnProperty.call(payload, PAYLOAD_PADDING_KEY) && typeof payload[PAYLOAD_PADDING_KEY] !== 'string') {
    throw new Error('reserved payload padding field must be a string when present');
  }

  if (actualBytes < PAYLOAD_TARGET_BYTES) {
    const basePadding = typeof payload[PAYLOAD_PADDING_KEY] === 'string' ? payload[PAYLOAD_PADDING_KEY] : '';
    payload[PAYLOAD_PADDING_KEY] = basePadding + 'x'.repeat(PAYLOAD_TARGET_BYTES - actualBytes);
    content = serializePayload(payload);
    actualBytes = utf8ByteLength(content);
  }

  let guard = 0;
  while (actualBytes !== PAYLOAD_TARGET_BYTES && guard < 8) {
    const diff = PAYLOAD_TARGET_BYTES - actualBytes;
    const currentPadding = typeof payload[PAYLOAD_PADDING_KEY] === 'string' ? payload[PAYLOAD_PADDING_KEY] : '';
    if (diff > 0) {
      payload[PAYLOAD_PADDING_KEY] = currentPadding + 'x'.repeat(diff);
    } else {
      payload[PAYLOAD_PADDING_KEY] = currentPadding.slice(0, currentPadding.length + diff);
    }
    content = serializePayload(payload);
    actualBytes = utf8ByteLength(content);
    guard++;
  }

  if (actualBytes !== PAYLOAD_TARGET_BYTES) {
    throw new Error('failed to size JSON payload to the exact target byte length');
  }

  return {
    sourceJSON: PAYLOAD_SOURCE_JSON || '',
    content,
    targetBytes: PAYLOAD_TARGET_BYTES,
    actualBytes,
  };
}

const PREPARED_REQUEST_PAYLOAD = prepareRequestPayload();
const REQUEST_BODY = PREPARED_REQUEST_PAYLOAD.content || null;
const REQUEST_PAYLOAD_ARTIFACT = REQUEST_BODY || '';

const AUTH_STATE = {
  initialized: false,
  accessToken: '',
  tokenType: 'Bearer',
  expiresAtEpochMs: 0,
  refreshAtEpochMs: 0,
  jitterMs: 0,
  tokenRequestCount: 0,
  tokenSuccessCount: 0,
  tokenFailureCount: 0,
  tokenRefreshCount: 0,
  tokenReuseHitCount: 0,
  responseStatusCounts: {},
  lastAuthSuccess: false,
  lastAuthError: '',
  abortTriggered: false,
  abortCause: '',
  abortReason: '',
  abortHTTPStatusCode: 0,
  abortRetryable: false,
};

function ensureAuthStateInitialized() {
  if (!AUTH_STATE.initialized) {
    const vuId = Number((exec.vu && exec.vu.idInTest) || 0);
    const jitterBuckets = Math.max(1, Math.floor(AUTH_MAX_JITTER_MS / 1000) + 1);
    AUTH_STATE.jitterMs = Math.min((vuId % jitterBuckets) * 1000, AUTH_MAX_JITTER_MS);
    AUTH_STATE.initialized = true;
  }
  return AUTH_STATE;
}

function effectiveRefreshSkewMs(expiresInSeconds) {
  const configuredSkewMs = Math.max(0, Number(AUTH_REFRESH_SKEW_SECONDS) || 0) * 1000;
  const expiresInMs = Math.max(0, Number(expiresInSeconds) || 0) * 1000;
  return Math.min(configuredSkewMs, Math.max(5000, Math.floor(expiresInMs * 0.2)));
}

function refreshLeadMs(expiresInSeconds, jitterMs) {
  const expiresInMs = Math.max(0, Number(expiresInSeconds) || 0) * 1000;
  const desiredLeadMs = effectiveRefreshSkewMs(expiresInSeconds) + jitterMs;
  const maxLeadMs = Math.max(0, expiresInMs - 1000);
  return Math.min(desiredLeadMs, maxLeadMs);
}

function parseRetryableStatusCodes(raw) {
  return String(raw || '')
    .split(',')
    .map((value) => Number(String(value).trim()))
    .filter((value) => Number.isInteger(value) && value >= 100 && value <= 599);
}

const AUTH_RETRYABLE_STATUS_CODE_LIST = parseRetryableStatusCodes(AUTH_RETRYABLE_STATUS_CODES);

function recordAuthResponseStatus(statusCode) {
  const numeric = Number(statusCode || 0);
  if (!Number.isInteger(numeric) || numeric < 100 || numeric > 599) {
    return;
  }
  const state = ensureAuthStateInitialized();
  const key = String(numeric);
  const existing = Number(state.responseStatusCounts[key] || 0);
  state.responseStatusCounts[key] = existing + 1;
}

function authResponseStatusEntries(state) {
  if (!state || !state.responseStatusCounts) {
    return [];
  }
  return Object.keys(state.responseStatusCounts)
    .map((key) => Number(key))
    .filter((code) => Number.isInteger(code) && code >= 100 && code <= 599)
    .sort((a, b) => a - b)
    .map((code) => ({
      code,
      count: Number(state.responseStatusCounts[String(code)] || 0),
    }))
    .filter((entry) => entry.count > 0);
}

function isRetryableTokenStatus(status) {
  const numeric = Number(status || 0);
  if (!Number.isInteger(numeric) || numeric < 100 || numeric > 599) {
    return false;
  }
  return AUTH_RETRYABLE_STATUS_CODE_LIST.indexOf(numeric) !== -1;
}

function shouldRetryTokenRequest(res) {
  if (!res || res.error) {
    return true;
  }
  return isRetryableTokenStatus(res.status);
}

function tokenRetryBackoffSeconds() {
  const state = ensureAuthStateInitialized();
  return 0.25 + Math.min(state.jitterMs / 1000, 5) * 0.05;
}

function retryDelaySeconds(res) {
  if (res && res.status === 429 && res.headers) {
    const retryAfter = res.headers['Retry-After'] || res.headers['retry-after'];
    const raw = Array.isArray(retryAfter) ? retryAfter[0] : retryAfter;
    const parsed = Number(raw);
    if (Number.isFinite(parsed) && parsed > 0) {
      return parsed;
    }
  }
  return tokenRetryBackoffSeconds();
}

function buildTokenRequest() {
  const headers = {
    'Accept': 'application/json',
    'Content-Type': 'application/x-www-form-urlencoded',
  };

  let body = 'grant_type=client_credentials';
  if (AUTH_CLIENT_AUTH_METHOD === 'basic') {
    headers['Authorization'] = 'Basic ' + encoding.b64encode(AUTH_CLIENT_ID + ':' + AUTH_CLIENT_SECRET);
  } else {
    body += '&client_id=' + encodeURIComponent(AUTH_CLIENT_ID);
    body += '&client_secret=' + encodeURIComponent(AUTH_CLIENT_SECRET);
  }

  return { body, params: { headers, timeout: AUTH_TOKEN_TIMEOUT } };
}

function markAuthAbort(cause, reason, statusCode, retryable) {
  const state = ensureAuthStateInitialized();
  state.abortTriggered = true;
  state.abortCause = cause || '';
  state.abortReason = reason || '';
  state.abortHTTPStatusCode = Number.isInteger(Number(statusCode)) ? Number(statusCode) : 0;
  state.abortRetryable = Boolean(retryable);
  state.lastAuthSuccess = false;
  state.lastAuthError = state.abortReason || reason || '';
}

function abortAuthRun(cause, reason, statusCode, retryable) {
  markAuthAbort(cause, reason, statusCode, retryable);
  exec.test.abort('Authentication aborted test run: ' + String(reason || 'token request failed'));
}

function tokenFailureDetails(res, fallbackError) {
  if (res && !res.error) {
    return {
      cause: 'http_status',
      reason: 'token endpoint returned HTTP ' + String(res.status),
      statusCode: Number(res.status || 0),
      retryable: isRetryableTokenStatus(res.status),
    };
  }
  if (res && res.error) {
    const message = String(res.error || fallbackError || 'token request failed');
    const lower = message.toLowerCase();
    return {
      cause: lower.indexOf('timeout') !== -1 ? 'timeout' : 'network_error',
      reason: message,
      statusCode: 0,
      retryable: true,
    };
  }
  return {
    cause: 'network_error',
    reason: String(fallbackError || 'token request failed without response'),
    statusCode: 0,
    retryable: true,
  };
}

function requestAccessToken(isRefresh) {
  const state = ensureAuthStateInitialized();

  if (!AUTH_CLIENT_SECRET) {
    state.tokenFailureCount += 1;
    state.lastAuthSuccess = false;
    state.lastAuthError = 'auth client secret is missing';
    authTokenFailure.add(1);
    abortAuthRun('config_error', state.lastAuthError, 0, false);
    throw new Error(state.lastAuthError);
  }
  if (!AUTH_TOKEN_URL) {
    state.tokenFailureCount += 1;
    state.lastAuthSuccess = false;
    state.lastAuthError = 'auth token url is missing';
    authTokenFailure.add(1);
    abortAuthRun('config_error', state.lastAuthError, 0, false);
    throw new Error(state.lastAuthError);
  }

  const tokenRequest = buildTokenRequest();
  let attempts = 0;
  let lastError = 'token request failed';
  let lastFailure = { cause: 'unknown', reason: lastError, statusCode: 0, retryable: false };

  while (attempts <= AUTH_RETRY_LIMIT) {
    attempts += 1;
    state.tokenRequestCount += 1;
    authTokenRequests.add(1);

    const startedAt = Date.now();
    const res = http.post(AUTH_TOKEN_URL, tokenRequest.body, tokenRequest.params);
    authTokenRequestDuration.add(Date.now() - startedAt);
    if (res && !res.error) {
      recordAuthResponseStatus(res.status);
    }

    if (res && !res.error && res.status >= 200 && res.status < 300) {
      let payload;
      try {
        payload = JSON.parse(res.body || '{}');
      } catch (err) {
        lastError = 'token endpoint returned invalid JSON';
        lastFailure = { cause: 'invalid_response', reason: lastError, statusCode: Number(res.status || 0), retryable: false };
        break;
      }

      const accessToken = typeof payload.access_token === 'string' ? payload.access_token : '';
      const tokenType = typeof payload.token_type === 'string' ? payload.token_type : 'Bearer';
      const expiresIn = Number(payload.expires_in || 0);
      if (!accessToken || expiresIn <= 0) {
        lastError = 'token endpoint response is missing access_token or expires_in';
        lastFailure = { cause: 'invalid_response', reason: lastError, statusCode: Number(res.status || 0), retryable: false };
        break;
      }

      const nowMs = Date.now();
      state.accessToken = accessToken;
      state.tokenType = tokenType;
      state.expiresAtEpochMs = nowMs + expiresIn * 1000;
      state.refreshAtEpochMs = state.expiresAtEpochMs - refreshLeadMs(expiresIn, state.jitterMs);
      state.lastAuthSuccess = true;
      state.lastAuthError = '';
      state.tokenSuccessCount += 1;
      authTokenSuccess.add(1);
      if (isRefresh) {
        state.tokenRefreshCount += 1;
        authTokenRefresh.add(1);
      }
      return state.tokenType + ' ' + state.accessToken;
    }

    lastFailure = tokenFailureDetails(res, lastError);
    lastError = lastFailure.reason;

    if (attempts <= AUTH_RETRY_LIMIT && shouldRetryTokenRequest(res)) {
      sleep(retryDelaySeconds(res));
      continue;
    }
    break;
  }

  state.tokenFailureCount += 1;
  state.lastAuthSuccess = false;
  state.lastAuthError = lastError;
  authTokenFailure.add(1);
  abortAuthRun(lastFailure.cause, lastFailure.reason, lastFailure.statusCode, lastFailure.retryable);
  throw new Error(lastError);
}

function ensureAuthorizationHeader() {
  if (!AUTH_ENABLED) {
    return '';
  }

  const state = ensureAuthStateInitialized();
  const nowMs = Date.now();
  if (state.accessToken && nowMs < state.refreshAtEpochMs) {
    state.tokenReuseHitCount += 1;
    authTokenReuseHits.add(1);
    return state.tokenType + ' ' + state.accessToken;
  }

  return requestAccessToken(Boolean(state.accessToken));
}

function buildRequestParams(authHeader) {
  const headers = {};
  if (REQUEST_BODY) {
    headers['Content-Type'] = CONTENT_TYPE;
  }
  if (authHeader) {
    headers['Authorization'] = authHeader;
  }
  return Object.keys(headers).length > 0 ? { headers } : undefined;
}

export default function () {
  let requestParams;
  try {
    requestParams = buildRequestParams(ensureAuthorizationHeader());
  } catch (err) {
    errorRate.add(true);
    successRate.add(false);
    sleep(0.5);
    return;
  }

  const res = http.request(HTTP_METHOD, BASE_URL, REQUEST_BODY, requestParams);
  businessHttpRequests.add(1);

  if (!res || res.error) {
    errorRate.add(true);
    successRate.add(false);
    businessHttpFailure.add(1);
    businessTransportFailures.add(1);
    sleep(0.5);
    return;
  }

  if (res.timings) {
    businessHttpDuration.add(Number(res.timings.duration || 0));
    businessHttpBlocked.add(Number(res.timings.blocked || 0));
    businessHttpWaiting.add(Number(res.timings.waiting || 0));
    businessHttpSending.add(Number(res.timings.sending || 0));
    businessHttpReceiving.add(Number(res.timings.receiving || 0));
    businessHttpConnecting.add(Number(res.timings.connecting || 0));
    businessHttpTLSHandshaking.add(Number(res.timings.tls_handshaking || 0));
  }

  const passed = check(res, {
    'status is 2xx': (r) => r.status >= 200 && r.status < 300,
  });

  errorRate.add(!passed);
  successRate.add(passed);
  if (passed) {
    businessHttpSuccess.add(1);
    businessStatus2xx.add(1);
  } else {
    businessHttpFailure.add(1);
  }
  if (res.status >= 400 && res.status < 500) status4xx.add(1);
  if (res.status >= 500) status5xx.add(1);
  if (res.status >= 400 && res.status < 500) businessStatus4xx.add(1);
  if (res.status >= 500) businessStatus5xx.add(1);

  sleep(0.5);
}


// --- Auto-injected by controller: export summary with real percentiles ---
export function handleSummary(data) {
  const wid = __ENV.WORKER_ID || 'unknown';
  const artifacts = {
    ['/output/summary-' + wid + '.json']: JSON.stringify(data),
  };
  const payloadArtifact = typeof REQUEST_PAYLOAD_ARTIFACT !== 'undefined' ? REQUEST_PAYLOAD_ARTIFACT : '';
  if (payloadArtifact) {
    artifacts['/output/payload-' + wid + '.json'] = payloadArtifact;
  }
  if (typeof AUTH_ENABLED !== 'undefined' && AUTH_ENABLED) {
    const metricValues = (name) =>
      data &&
      data.metrics &&
      data.metrics[name] &&
      data.metrics[name].values
        ? data.metrics[name].values
        : {};
    const tokenDurationMetric =
      metricValues('auth_token_request_duration_ms');
    const authState = typeof AUTH_STATE !== 'undefined' ? AUTH_STATE : null;
    const tokenRequestsTotal = Number(metricValues('auth_token_requests_total').count || 0);
    const tokenSuccessTotal = Number(metricValues('auth_token_success_total').count || 0);
    const tokenFailureTotal = Number(metricValues('auth_token_failure_total').count || 0);
    const tokenSuccessRate = tokenRequestsTotal > 0 ? tokenSuccessTotal / tokenRequestsTotal : 0;
    const tokenRefreshTotal = Number(metricValues('auth_token_refresh_total').count || 0);
    const tokenReuseHitsTotal = Number(metricValues('auth_token_reuse_hits_total').count || 0);

    artifacts['/output/auth-summary-' + wid + '.json'] = JSON.stringify({
      status: authState && authState.abortTriggered ? 'aborted' : 'complete',
      message: authState && authState.abortTriggered
        ? String(authState.abortReason || authState.lastAuthError || 'Authentication aborted the test run.')
        : (tokenRequestsTotal === 0 ? 'Authentication was enabled but no token request was required during this run.' : ''),
      mode: typeof AUTH_MODE !== 'undefined' ? AUTH_MODE : '',
      token_url: typeof AUTH_TOKEN_URL !== 'undefined' ? AUTH_TOKEN_URL : '',
      client_auth_method: typeof AUTH_CLIENT_AUTH_METHOD !== 'undefined' ? AUTH_CLIENT_AUTH_METHOD : '',
      refresh_skew_seconds: typeof AUTH_REFRESH_SKEW_SECONDS !== 'undefined' ? Number(AUTH_REFRESH_SKEW_SECONDS || 0) : 0,
      metrics: {
        token_requests_total: tokenRequestsTotal,
        token_success_total: tokenSuccessTotal,
        token_failure_total: tokenFailureTotal,
        token_success_rate: tokenSuccessRate,
        token_request_avg_ms: Number(tokenDurationMetric.avg || 0),
        token_request_p95_ms: Number(tokenDurationMetric['p(95)'] || 0),
        token_request_p99_ms: Number(tokenDurationMetric['p(99)'] || 0),
        token_request_max_ms: Number(tokenDurationMetric.max || 0),
        token_refresh_total: tokenRefreshTotal,
        token_reuse_hits_total: tokenReuseHitsTotal,
        response_status_codes: authResponseStatusEntries(authState),
        abort_triggered: Boolean(authState && authState.abortTriggered),
        abort_cause: authState && authState.abortCause ? String(authState.abortCause) : '',
        abort_reason: authState && authState.abortReason ? String(authState.abortReason) : '',
        abort_http_status_codes: authState && Number(authState.abortHTTPStatusCode || 0) > 0
          ? [Number(authState.abortHTTPStatusCode)]
          : [],
        abort_retryable: Boolean(authState && authState.abortRetryable),
      },
    });
  }
  return artifacts;
}
