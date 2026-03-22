package scriptgen

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
)

const scriptFileName = "current-test.js"

var completionBufferSeconds = 30

// ExecutorType identifies the k6 executor category for control-flow decisions.
type ExecutorType string

const (
	ExecutorExternallyControlled ExecutorType = "externally-controlled"
	ExecutorConstantVUs          ExecutorType = "constant-vus"
	ExecutorRampingVUs           ExecutorType = "ramping-vus"
	ExecutorConstantArrivalRate  ExecutorType = "constant-arrival-rate"
	ExecutorRampingArrivalRate   ExecutorType = "ramping-arrival-rate"
)

const (
	DefaultHTTPMethod              = "GET"
	DefaultContentType             = "application/json"
	payloadPaddingField            = "_shiva_padding"
	AuthClientSecretEnvVar         = "AUTH_CLIENT_SECRET"
	AuthEnabledEnvVar              = "AUTH_ENABLED"
	AuthModeEnvVar                 = "AUTH_MODE"
	AuthTokenURLEnvVar             = "AUTH_TOKEN_URL"
	AuthClientIDEnvVar             = "AUTH_CLIENT_ID"
	AuthClientAuthMethodEnvVar     = "AUTH_CLIENT_AUTH_METHOD"
	AuthRefreshSkewEnvVar          = "AUTH_REFRESH_SKEW_SECONDS"
	AuthRetryLimitEnvVar           = "AUTH_RETRY_LIMIT"
	AuthRetryableStatusCodesEnvVar = "AUTH_RETRYABLE_STATUS_CODES"
	AuthMaxJitterMsEnvVar          = "AUTH_MAX_JITTER_MS"
	AuthTokenTimeoutEnvVar         = "AUTH_TOKEN_TIMEOUT"
	HTTPMethodEnvVar               = "HTTP_METHOD"
	ContentTypeEnvVar              = "CONTENT_TYPE"
	PayloadSourceJSONEnvVar        = "PAYLOAD_SOURCE_JSON"
	PayloadSourceJSONB64EnvVar     = "PAYLOAD_SOURCE_JSON_B64"
	PayloadTargetBytesEnvVar       = "PAYLOAD_TARGET_BYTES"
	TargetURLEnvVar                = "TARGET_URL"
)

// IsControllable returns true if the executor supports Pause/Resume/Scale via REST API.
// VU-based executors (constant-vus, ramping-vus) are transformed to externally-controlled.
// Rate-based executors (arrival-rate) cannot be transformed and remain native.
func (e ExecutorType) IsControllable() bool {
	switch e {
	case ExecutorExternallyControlled, ExecutorConstantVUs, ExecutorRampingVUs:
		return true
	default:
		return false
	}
}

func SetCompletionBufferSeconds(seconds int) {
	if seconds < 0 {
		seconds = 0
	}
	completionBufferSeconds = seconds
}

// --- Builder templates per executor type ---

const builderRequestSharedTemplate = `
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

const BASE_URL = envString('` + TargetURLEnvVar + `', '');
const HTTP_METHOD = envString('` + HTTPMethodEnvVar + `', '` + DefaultHTTPMethod + `').toUpperCase();
const CONTENT_TYPE = envString('` + ContentTypeEnvVar + `', '` + DefaultContentType + `');
const PAYLOAD_SOURCE_JSON = decodePayloadSourceJSON(
  envString('` + PayloadSourceJSONEnvVar + `', ''),
  envString('` + PayloadSourceJSONB64EnvVar + `', ''),
);
const PAYLOAD_TARGET_BYTES = Math.max(0, envNumber('` + PayloadTargetBytesEnvVar + `', 0));
const PAYLOAD_PADDING_KEY = '_shiva_padding';
const AUTH_ENABLED = envBool('` + AuthEnabledEnvVar + `', false);
const AUTH_MODE = envString('` + AuthModeEnvVar + `', 'oauth_client_credentials');
const AUTH_TOKEN_URL = envString('` + AuthTokenURLEnvVar + `', '');
const AUTH_CLIENT_ID = envString('` + AuthClientIDEnvVar + `', '');
const AUTH_CLIENT_AUTH_METHOD = envString('` + AuthClientAuthMethodEnvVar + `', 'basic');
const AUTH_REFRESH_SKEW_SECONDS = Math.max(1, envNumber('` + AuthRefreshSkewEnvVar + `', 30));
const AUTH_CLIENT_SECRET = envString('` + AuthClientSecretEnvVar + `', '');
const AUTH_RETRY_LIMIT = Math.max(0, envNumber('` + AuthRetryLimitEnvVar + `', 1));
const AUTH_RETRYABLE_STATUS_CODES = envString('` + AuthRetryableStatusCodesEnvVar + `', '408,429,502,503,504');
const AUTH_MAX_JITTER_MS = Math.max(0, envNumber('` + AuthMaxJitterMsEnvVar + `', 5000));
const AUTH_TOKEN_TIMEOUT = envString('` + AuthTokenTimeoutEnvVar + `', '10s');

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
  const tags = {
    cause: String(cause || ''),
    retryable: retryable ? 'true' : 'false',
  };
  const numericStatusCode = Number(statusCode || 0);
  if (Number.isInteger(numericStatusCode) && numericStatusCode > 0) {
    tags.status_code = String(numericStatusCode);
  }
  authAbortEvents.add(1, tags);
  authAbortTriggered.add(1);
  const causeKey = Object.prototype.hasOwnProperty.call(authAbortCauseCounters, String(cause || ''))
    ? String(cause || '')
    : 'unknown';
  authAbortCauseCounters[causeKey].add(1);
  const abortStatusCounter = authAbortStatusCounters[numericStatusCode];
  if (abortStatusCounter) {
    abortStatusCounter.add(1);
  }
  if (retryable) {
    authAbortRetryableTrue.add(1);
  } else {
    authAbortRetryableFalse.add(1);
  }
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
      authTokenResponses.add(1, { status_code: String(res.status) });
      const responseStatusCounter = authResponseStatusCounters[Number(res.status || 0)];
      if (responseStatusCounter) {
        responseStatusCounter.add(1);
      }
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
`

const builderMetricDeclarations = `
const errorRate = new Rate('errors');
const successRate = new Rate('success_rate');
const status4xx = new Counter('status_4xx');
const status5xx = new Counter('status_5xx');
const authTokenRequests = new Counter('auth_token_requests_total');
const authTokenSuccess = new Counter('auth_token_success_total');
const authTokenFailure = new Counter('auth_token_failure_total');
const authTokenResponses = new Counter('auth_token_responses_total');
const authAbortEvents = new Counter('auth_abort_events_total');
const authAbortTriggered = new Counter('auth_abort_triggered_total');
const authAbortRetryableTrue = new Counter('auth_abort_retryable_true_total');
const authAbortRetryableFalse = new Counter('auth_abort_retryable_false_total');
const authAbortCauseCounters = {
  http_status: new Counter('auth_abort_cause_http_status_total'),
  timeout: new Counter('auth_abort_cause_timeout_total'),
  network_error: new Counter('auth_abort_cause_network_error_total'),
  config_error: new Counter('auth_abort_cause_config_error_total'),
  invalid_response: new Counter('auth_abort_cause_invalid_response_total'),
  unknown: new Counter('auth_abort_cause_unknown_total'),
};
const authResponseStatusCounters = {};
const authAbortStatusCounters = {};
for (let code = 100; code <= 599; code += 1) {
  authResponseStatusCounters[code] = new Counter('auth_token_response_status_' + String(code) + '_total');
  authAbortStatusCounters[code] = new Counter('auth_abort_status_' + String(code) + '_total');
}
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
`

const builderRequestExecutionTemplate = `
export default function () {
  let requestParams;
  try {
    requestParams = buildRequestParams(ensureAuthorizationHeader());
  } catch (err) {
    errorRate.add(true);
    successRate.add(false);
{{ if gt .Sleep 0.0 }}    sleep({{ .Sleep }});
{{ end }}    return;
  }

  const res = http.request(HTTP_METHOD, BASE_URL, REQUEST_BODY, requestParams);
  businessHttpRequests.add(1);

  if (!res || res.error) {
    errorRate.add(true);
    successRate.add(false);
    businessHttpFailure.add(1);
    businessTransportFailures.add(1);
{{ if gt .Sleep 0.0 }}    sleep({{ .Sleep }});
{{ end }}    return;
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
{{ if gt .Sleep 0.0 }}
  sleep({{ .Sleep }});
{{ end }}}
`

const externallyControlledTemplate = `import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import encoding from 'k6/encoding';
import { Rate, Counter, Trend } from 'k6/metrics';
` + builderMetricDeclarations + `
export const options = {
  scenarios: {
    controller_managed: {
      executor: 'externally-controlled',
      vus: {{ .StartVUs }},
      maxVUs: {{ .MaxVUs }},
      duration: '{{ .TotalDuration }}',
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<1500'],
    errors: ['rate<0.1'],
    success_rate: ['rate>0.95'],
  },
};` + builderRequestSharedTemplate + builderRequestExecutionTemplate

const constantArrivalRateTemplate = `import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import encoding from 'k6/encoding';
import { Rate, Counter, Trend } from 'k6/metrics';
` + builderMetricDeclarations + `
export const options = {
  scenarios: {
    fixed_throughput: {
      executor: 'constant-arrival-rate',
      rate: {{ .Rate }},
      timeUnit: '{{ .TimeUnit }}',
      duration: '{{ .Duration }}',
      preAllocatedVUs: {{ .PreAllocatedVUs }},
      maxVUs: {{ .MaxVUs }},
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<1500'],
    errors: ['rate<0.1'],
    success_rate: ['rate>0.95'],
  },
};` + builderRequestSharedTemplate + builderRequestExecutionTemplate

const rampingArrivalRateTemplate = `import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import encoding from 'k6/encoding';
import { Rate, Counter, Trend } from 'k6/metrics';
` + builderMetricDeclarations + `
export const options = {
  scenarios: {
    ramping_throughput: {
      executor: 'ramping-arrival-rate',
      startRate: 0,
      timeUnit: '{{ .TimeUnit }}',
      preAllocatedVUs: {{ .PreAllocatedVUs }},
      maxVUs: {{ .MaxVUs }},
      stages: [
{{- range .Stages }}
        { duration: '{{ .Duration }}', target: {{ .Target }} },
{{- end }}
      ],
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<1500'],
    errors: ['rate<0.1'],
    success_rate: ['rate>0.95'],
  },
};` + builderRequestSharedTemplate + builderRequestExecutionTemplate

// BuilderResult contains the generated script and the executor type used.
type BuilderResult struct {
	Script       string
	ExecutorType ExecutorType
}

type BuilderPayloadArtifacts struct {
	SourceJSON  string
	Content     string
	HTTPMethod  string
	ContentType string
	TargetBytes int
	TargetKiB   float64
	TargetKB    float64
	ActualBytes int
	ActualKiB   float64
	ActualKB    float64
}

type builderTemplateData struct {
	URL                    string
	StartVUs               int
	MaxVUs                 int
	TotalDuration          string
	VUs                    int
	Duration               string
	Rate                   int
	TimeUnit               string
	PreAllocatedVUs        int
	Stages                 any
	Sleep                  float64
	HTTPMethod             string
	ContentType            string
	PayloadSourceJSON      string
	PayloadTargetBytes     int
	AuthEnabled            bool
	AuthMode               string
	AuthTokenURL           string
	AuthClientID           string
	AuthClientAuthMethod   string
	AuthRefreshSkewSeconds int
}

const defaultSleepSeconds = 0.5

// sleepVal returns the configured think-time or the default.
func sleepVal(req *model.TestRequest) float64 {
	if req.SleepSeconds != nil {
		return *req.SleepSeconds
	}
	if req.Executor == string(ExecutorConstantArrivalRate) || req.Executor == string(ExecutorRampingArrivalRate) {
		return 0
	}
	return defaultSleepSeconds
}

func NormalizeHTTPMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return DefaultHTTPMethod
	}
	return method
}

func NormalizeContentType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return DefaultContentType
	}
	return contentType
}

func MethodAllowsBody(method string) bool {
	switch NormalizeHTTPMethod(method) {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

func HasPayloadConfiguration(method, contentType, payloadJSON string, targetKiB int) bool {
	if strings.TrimSpace(payloadJSON) != "" || targetKiB > 0 {
		return true
	}
	if method = NormalizeHTTPMethod(method); method != DefaultHTTPMethod {
		return true
	}
	if contentType = NormalizeContentType(contentType); contentType != DefaultContentType {
		return true
	}
	return false
}

func BuildBuilderPayloadArtifacts(req *model.TestRequest) (*BuilderPayloadArtifacts, error) {
	method := NormalizeHTTPMethod(req.HTTPMethod)
	contentType := NormalizeContentType(req.ContentType)
	sourceJSON := strings.TrimSpace(req.PayloadJSON)
	targetBytes := payloadTargetBytes(req.PayloadTargetKiB)

	if err := validateBuilderHTTPMethod(method); err != nil {
		return nil, err
	}

	artifacts := &BuilderPayloadArtifacts{
		SourceJSON:  sourceJSON,
		HTTPMethod:  method,
		ContentType: contentType,
		TargetBytes: targetBytes,
		TargetKiB:   kibFromBytes(targetBytes),
		TargetKB:    kbFromBytes(targetBytes),
	}

	if !MethodAllowsBody(method) {
		if sourceJSON != "" || targetBytes > 0 {
			return nil, fmt.Errorf("%s does not support a JSON request body in builder mode", method)
		}
		return artifacts, nil
	}

	if sourceJSON == "" && targetBytes == 0 {
		return artifacts, nil
	}

	parsedSource := sourceJSON
	if parsedSource == "" {
		parsedSource = "{}"
	}

	var parsed any
	if err := json.Unmarshal([]byte(parsedSource), &parsed); err != nil {
		return nil, fmt.Errorf("payload_json must be valid JSON: %w", err)
	}

	content, err := buildExactPayloadContent(parsed, targetBytes)
	if err != nil {
		return nil, err
	}

	artifacts.Content = content
	artifacts.ActualBytes = len([]byte(content))
	artifacts.ActualKiB = kibFromBytes(artifacts.ActualBytes)
	artifacts.ActualKB = kbFromBytes(artifacts.ActualBytes)
	return artifacts, nil
}

func validateBuilderHTTPMethod(method string) error {
	switch NormalizeHTTPMethod(method) {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return nil
	default:
		return fmt.Errorf("http_method must be one of GET, POST, PUT, PATCH, DELETE")
	}
}

func buildExactPayloadContent(parsed any, targetBytes int) (string, error) {
	content, err := marshalCanonicalJSON(parsed)
	if err != nil {
		return "", fmt.Errorf("serialize payload: %w", err)
	}

	actualBytes := len([]byte(content))
	if targetBytes == 0 {
		return content, nil
	}
	if actualBytes == targetBytes {
		return content, nil
	}
	if actualBytes > targetBytes {
		return "", fmt.Errorf("payload_target_kib is smaller than the minimum serialized JSON payload size")
	}

	objectPayload, ok := parsed.(map[string]any)
	if !ok {
		return "", fmt.Errorf("payload_target_kib requires an object payload unless the serialized JSON already matches the exact target size")
	}

	padded, err := sizeObjectPayload(objectPayload, targetBytes)
	if err != nil {
		return "", err
	}
	return padded, nil
}

func sizeObjectPayload(payload map[string]any, targetBytes int) (string, error) {
	if existing, ok := payload[payloadPaddingField]; ok {
		if _, ok := existing.(string); !ok {
			return "", fmt.Errorf("reserved payload padding field must be a string when present")
		}
	}

	content, err := marshalCanonicalJSON(payload)
	if err != nil {
		return "", fmt.Errorf("serialize payload: %w", err)
	}
	actualBytes := len([]byte(content))
	if actualBytes == targetBytes {
		return content, nil
	}
	if actualBytes > targetBytes {
		return "", fmt.Errorf("payload_target_kib is smaller than the minimum serialized JSON payload size")
	}

	currentPadding, _ := payload[payloadPaddingField].(string)
	payload[payloadPaddingField] = currentPadding + strings.Repeat("x", targetBytes-actualBytes)
	content, err = marshalCanonicalJSON(payload)
	if err != nil {
		return "", fmt.Errorf("serialize payload: %w", err)
	}
	actualBytes = len([]byte(content))

	for guard := 0; actualBytes != targetBytes && guard < 8; guard++ {
		diff := targetBytes - actualBytes
		currentPadding, _ := payload[payloadPaddingField].(string)
		if diff > 0 {
			payload[payloadPaddingField] = currentPadding + strings.Repeat("x", diff)
		} else {
			payload[payloadPaddingField] = currentPadding[:len(currentPadding)+diff]
		}
		content, err = marshalCanonicalJSON(payload)
		if err != nil {
			return "", fmt.Errorf("serialize payload: %w", err)
		}
		actualBytes = len([]byte(content))
	}

	if actualBytes != targetBytes {
		return "", fmt.Errorf("failed to size JSON payload to the exact target byte length")
	}
	return content, nil
}

func marshalCanonicalJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func payloadTargetBytes(targetKiB int) int {
	if targetKiB <= 0 {
		return 0
	}
	return targetKiB * 1024
}

func normalizeAuthMode(mode string) string {
	trimmed := strings.TrimSpace(mode)
	if trimmed == "" {
		return "oauth_client_credentials"
	}
	return trimmed
}

func normalizeAuthClientMethod(method string) string {
	trimmed := strings.TrimSpace(method)
	if trimmed == "" {
		return "basic"
	}
	return trimmed
}

func normalizeAuthRefreshSkew(skew int) int {
	if skew <= 0 {
		return 30
	}
	return skew
}

func buildBuilderEnvContract(req *model.TestRequest) map[string]string {
	env := map[string]string{
		HTTPMethodEnvVar:         NormalizeHTTPMethod(req.HTTPMethod),
		ContentTypeEnvVar:        NormalizeContentType(req.ContentType),
		PayloadSourceJSONEnvVar:  strings.TrimSpace(req.PayloadJSON),
		PayloadTargetBytesEnvVar: strconv.Itoa(payloadTargetBytes(req.PayloadTargetKiB)),
		AuthEnabledEnvVar:        strconv.FormatBool(req.Auth.Enabled),
	}
	if strings.TrimSpace(req.URL) != "" {
		env[TargetURLEnvVar] = strings.TrimSpace(req.URL)
	}
	if req.Auth.Enabled {
		env[AuthModeEnvVar] = normalizeAuthMode(req.Auth.Mode)
		env[AuthTokenURLEnvVar] = strings.TrimSpace(req.Auth.TokenURL)
		env[AuthClientIDEnvVar] = strings.TrimSpace(req.Auth.ClientID)
		env[AuthClientSecretEnvVar] = strings.TrimSpace(req.Auth.ClientSecret)
		env[AuthClientAuthMethodEnvVar] = normalizeAuthClientMethod(req.Auth.ClientAuthMethod)
		env[AuthRefreshSkewEnvVar] = strconv.Itoa(normalizeAuthRefreshSkew(req.Auth.RefreshSkewSeconds))
		env[AuthRetryLimitEnvVar] = "1"
		env[AuthRetryableStatusCodesEnvVar] = "408,429,502,503,504"
		env[AuthMaxJitterMsEnvVar] = "5000"
		env[AuthTokenTimeoutEnvVar] = "10s"
	}
	return env
}

func mergeEnvIntoConfigContent(content string, env map[string]string) (string, error) {
	config := map[string]any{}
	if strings.TrimSpace(content) != "" {
		if err := json.Unmarshal([]byte(content), &config); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}
	envBlock := map[string]any{}
	if existing, ok := config["env"]; ok && existing != nil {
		existingMap, ok := existing.(map[string]any)
		if !ok {
			return "", fmt.Errorf(`"env" must be a JSON object with string values`)
		}
		for key, value := range existingMap {
			envBlock[key] = fmt.Sprintf("%v", value)
		}
	}
	for key, value := range env {
		envBlock[key] = value
	}
	if len(envBlock) > 0 {
		config["env"] = envBlock
	}
	merged, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(merged), nil
}

func BuildBuilderConfig(req *model.TestRequest) (string, error) {
	executor := req.Executor
	if executor == "" {
		executor = "ramping-vus"
	}

	scenario := map[string]any{
		"executor": executor,
	}
	switch executor {
	case "constant-vus":
		scenario["vus"] = req.VUs
		scenario["duration"] = req.Duration
	case "ramping-vus":
		scenario["startVUs"] = 0
		scenario["stages"] = filterBuilderStages(req.Stages)
	case "constant-arrival-rate":
		scenario["rate"] = req.Rate
		scenario["timeUnit"] = req.TimeUnit
		scenario["duration"] = req.Duration
		scenario["preAllocatedVUs"] = req.PreAllocatedVUs
		scenario["maxVUs"] = req.MaxVUs
	case "ramping-arrival-rate":
		scenario["startRate"] = req.Rate
		scenario["timeUnit"] = req.TimeUnit
		scenario["preAllocatedVUs"] = req.PreAllocatedVUs
		scenario["maxVUs"] = req.MaxVUs
		scenario["stages"] = filterBuilderStages(req.Stages)
	}

	config := map[string]any{
		"scenarios": map[string]any{
			"default": scenario,
		},
		"thresholds": map[string]any{
			"http_req_duration": []string{"p(95)<500", "p(99)<1000"},
			"errors":            []string{"rate<0.01"},
			"success_rate":      []string{"rate>0.99"},
		},
	}

	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal builder config: %w", err)
	}
	return mergeEnvIntoConfigContent(string(data), buildBuilderEnvContract(req))
}

func EnrichBuilderConfig(content string, req *model.TestRequest) (string, error) {
	return mergeEnvIntoConfigContent(content, buildBuilderEnvContract(req))
}

func filterBuilderStages(stages []model.Stage) []map[string]any {
	filtered := make([]map[string]any, 0, len(stages))
	for _, stage := range stages {
		if strings.TrimSpace(stage.Duration) == "" {
			continue
		}
		filtered = append(filtered, map[string]any{
			"duration": stage.Duration,
			"target":   stage.Target,
		})
	}
	return filtered
}

func kibFromBytes(bytes int) float64 {
	return float64(bytes) / 1024
}

func kbFromBytes(bytes int) float64 {
	return float64(bytes) / 1000
}

func newBuilderTemplateData(req *model.TestRequest) builderTemplateData {
	return builderTemplateData{
		URL:                    req.URL,
		Sleep:                  sleepVal(req),
		HTTPMethod:             NormalizeHTTPMethod(req.HTTPMethod),
		ContentType:            NormalizeContentType(req.ContentType),
		PayloadSourceJSON:      strings.TrimSpace(req.PayloadJSON),
		PayloadTargetBytes:     payloadTargetBytes(req.PayloadTargetKiB),
		AuthEnabled:            req.Auth.Enabled,
		AuthMode:               req.Auth.Mode,
		AuthTokenURL:           req.Auth.TokenURL,
		AuthClientID:           req.Auth.ClientID,
		AuthClientAuthMethod:   req.Auth.ClientAuthMethod,
		AuthRefreshSkewSeconds: req.Auth.RefreshSkewSeconds,
	}
}

// GenerateFromBuilder creates a k6 script based on the request's executor type.
// workerCount is used to divide VUs/rate across workers (each worker runs the script independently).
// VU-based executors in builder mode with stages use externally-controlled for Pause/Resume/Scale.
// Rate-based and native VU executors generate their own k6-native scripts.
func GenerateFromBuilder(req *model.TestRequest, workerCount int) (*BuilderResult, error) {
	if workerCount < 1 {
		workerCount = 1
	}
	executor := req.Executor
	if executor == "" {
		// Legacy default: stages → externally-controlled
		executor = "ramping-vus"
	}

	switch executor {
	case "constant-vus":
		return generateConstantVUs(req, workerCount)
	case "ramping-vus":
		return generateRampingVUs(req, workerCount)
	case "constant-arrival-rate":
		return generateConstantArrivalRate(req, workerCount)
	case "ramping-arrival-rate":
		return generateRampingArrivalRate(req, workerCount)
	default:
		return nil, fmt.Errorf("unsupported executor: %s", executor)
	}
}

func generateRampingVUs(req *model.TestRequest, workerCount int) (*BuilderResult, error) {
	// Transform to externally-controlled for Pause/Resume/Scale support
	tmpl, err := template.New("k6").Parse(externallyControlledTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	maxVUs := 0
	totalDuration := 0
	for _, s := range req.Stages {
		if s.Target > maxVUs {
			maxVUs = s.Target
		}
		totalDuration += parseDuration(s.Duration)
	}
	if maxVUs < 1 {
		maxVUs = 1
	}
	// maxVUs per worker: ScaleVUs divides total across workers, so each worker
	// needs enough headroom. Use ceiling division to avoid truncation.
	perWorkerMaxVUs := (maxVUs + workerCount - 1) / workerCount
	totalDuration += completionBufferSeconds // buffer for graceful shutdown

	data := newBuilderTemplateData(req)
	data.StartVUs = 0
	data.MaxVUs = perWorkerMaxVUs
	data.TotalDuration = fmt.Sprintf("%ds", totalDuration)

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	return &BuilderResult{Script: buf.String(), ExecutorType: ExecutorExternallyControlled}, nil
}

func generateConstantVUs(req *model.TestRequest, workerCount int) (*BuilderResult, error) {
	// Transform to externally-controlled for Pause/Resume/Scale support.
	// The controller holds VUs constant (no ramping) for the specified duration.
	tmpl, err := template.New("k6").Parse(externallyControlledTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	vus := max(req.VUs, 1)
	perWorkerVUs := divideAcrossWorkers(vus, workerCount)
	totalDuration := parseDuration(req.Duration)
	if totalDuration < 1 {
		totalDuration = 60 // default 1m
	}
	totalDuration += completionBufferSeconds // buffer for graceful shutdown

	data := newBuilderTemplateData(req)
	data.StartVUs = perWorkerVUs
	data.MaxVUs = perWorkerVUs
	data.TotalDuration = fmt.Sprintf("%ds", totalDuration)

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	return &BuilderResult{Script: buf.String(), ExecutorType: ExecutorConstantVUs}, nil
}

func generateConstantArrivalRate(req *model.TestRequest, workerCount int) (*BuilderResult, error) {
	tmpl, err := template.New("k6").Parse(constantArrivalRateTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	rate := max(req.Rate, 1)
	// Divide rate across workers
	rate = divideAcrossWorkers(rate, workerCount)
	timeUnit := req.TimeUnit
	if timeUnit == "" {
		timeUnit = "1s"
	}
	duration := req.Duration
	if duration == "" {
		duration = "1m"
	}
	preAlloc := req.PreAllocatedVUs
	if preAlloc < 1 {
		preAlloc = rate // sensible default (already divided)
	} else {
		preAlloc = divideAcrossWorkers(preAlloc, workerCount)
	}
	maxVUs := req.MaxVUs
	if maxVUs < preAlloc {
		maxVUs = preAlloc * 2
	} else {
		maxVUs = divideAcrossWorkers(maxVUs, workerCount)
		if maxVUs < preAlloc {
			maxVUs = preAlloc * 2
		}
	}

	data := newBuilderTemplateData(req)
	data.Rate = rate
	data.TimeUnit = timeUnit
	data.Duration = duration
	data.PreAllocatedVUs = preAlloc
	data.MaxVUs = maxVUs

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	return &BuilderResult{Script: buf.String(), ExecutorType: ExecutorConstantArrivalRate}, nil
}

func generateRampingArrivalRate(req *model.TestRequest, workerCount int) (*BuilderResult, error) {
	tmpl, err := template.New("k6").Parse(rampingArrivalRateTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	timeUnit := req.TimeUnit
	if timeUnit == "" {
		timeUnit = "1s"
	}
	maxTarget := 0
	for _, s := range req.Stages {
		if s.Target > maxTarget {
			maxTarget = s.Target
		}
	}
	preAlloc := req.PreAllocatedVUs
	if preAlloc < 1 {
		preAlloc = max(divideAcrossWorkers(maxTarget, workerCount), 1)
	} else {
		preAlloc = divideAcrossWorkers(preAlloc, workerCount)
	}
	maxVUs := req.MaxVUs
	if maxVUs < preAlloc {
		maxVUs = preAlloc * 2
	} else {
		maxVUs = divideAcrossWorkers(maxVUs, workerCount)
		if maxVUs < preAlloc {
			maxVUs = preAlloc * 2
		}
	}

	type stageData struct {
		Duration string
		Target   int
	}
	stagesData := make([]stageData, len(req.Stages))
	for i, s := range req.Stages {
		// Divide each stage target across workers
		stagesData[i] = stageData{Duration: s.Duration, Target: divideAcrossWorkers(s.Target, workerCount)}
	}

	data := newBuilderTemplateData(req)
	data.TimeUnit = timeUnit
	data.PreAllocatedVUs = preAlloc
	data.MaxVUs = maxVUs
	data.Stages = stagesData

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	return &BuilderResult{Script: buf.String(), ExecutorType: ExecutorRampingArrivalRate}, nil
}

// divideAcrossWorkers splits a total value across N workers using ceiling division.
// Returns at least 1 to avoid zero-value configs.
func divideAcrossWorkers(total, workers int) int {
	if workers <= 1 {
		return total
	}
	result := max((total+workers-1)/workers, 1)
	return result
}

// ParseK6Duration converts k6 duration strings like "30s", "2m", "1h" to seconds.
// Exported for use by the scheduler package for duration estimation.
func ParseK6Duration(d string) int {
	return parseDuration(d)
}

// parseDuration converts k6 duration strings like "30s", "2m", "1h" to seconds.
func parseDuration(d string) int {
	d = strings.TrimSpace(d)
	if d == "" {
		return 0
	}
	unit := d[len(d)-1]
	val, err := strconv.Atoi(d[:len(d)-1])
	if err != nil {
		return 0
	}
	switch unit {
	case 's':
		return val
	case 'm':
		return val * 60
	case 'h':
		return val * 3600
	default:
		return val
	}
}

// ValidateUpload performs basic checks on an uploaded k6 script.
func ValidateUpload(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("script is empty")
	}
	if len(content) > 1<<20 { // 1 MB
		return fmt.Errorf("script exceeds 1 MB size limit")
	}
	if !strings.Contains(content, "export default function") &&
		!strings.Contains(content, "export function setup") {
		return fmt.Errorf("script must contain 'export default function'")
	}
	return nil
}

// InjectStatusCounters adds status_4xx and status_5xx Counter definitions to
// an uploaded script if they are not already present. This is safe because
// Counter definitions are additive — they don't interfere with existing logic.
// The counters appear in k6's /v1/metrics endpoint for live status tracking.
func InjectStatusCounters(content string) string {
	if strings.Contains(content, "status_4xx") || strings.Contains(content, "status_5xx") {
		return content // already has status counters
	}

	// Ensure Counter is imported from k6/metrics.
	// Uses regex to safely modify the import without breaking syntax.
	if strings.Contains(content, "from 'k6/metrics'") && !strings.Contains(content, "Counter") {
		re := regexp.MustCompile(`(import\s*\{)([^}]*)(}\s*from\s*'k6/metrics')`)
		content = re.ReplaceAllStringFunc(content, func(match string) string {
			parts := re.FindStringSubmatch(match)
			if len(parts) != 4 {
				return match
			}
			existing := strings.TrimSpace(parts[2])
			if existing != "" && !strings.HasSuffix(existing, ",") {
				existing += ","
			}
			return parts[1] + " " + existing + " Counter " + parts[3]
		})
	} else if !strings.Contains(content, "from 'k6/metrics'") {
		// No k6/metrics import at all — add one
		content = "import { Counter } from 'k6/metrics';\n" + content
	}

	// Add counter definitions after imports (before first export or const)
	counterDefs := "\nconst status4xx = new Counter('status_4xx');\nconst status5xx = new Counter('status_5xx');\n"

	// Insert after the last import line
	lines := strings.Split(content, "\n")
	lastImport := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "import ") {
			lastImport = i
		}
	}
	if lastImport >= 0 {
		lines = append(lines[:lastImport+1], append([]string{counterDefs}, lines[lastImport+1:]...)...)
		content = strings.Join(lines, "\n")
	} else {
		content = counterDefs + content
	}

	return content
}

// InjectSummaryExport appends a handleSummary function to the script that
// writes the k6 end-of-test summary (including real p99) to a JSON file.
// If the script already defines handleSummary, the original is preserved.
func InjectSummaryExport(content string) string {
	if strings.Contains(content, "handleSummary") {
		return content
	}

	if !regexp.MustCompile(`(?m)^\s*import\s+http\b.*from\s+['"]k6/http['"]`).MatchString(content) {
		content = "import http from 'k6/http';\n" + content
	}
	return content + `

// --- Auto-injected by controller: export summary with real percentiles ---
function shivaUploadArtifact(artifactType, content, contentType) {
  const enabled = String(__ENV.SHIVA_ARTIFACT_UPLOAD_ENABLED || '').toLowerCase();
  if (!(enabled === 'true' || enabled === '1')) {
    return;
  }
  if (!content) {
    return;
  }

  const workerId = String(__ENV.WORKER_ID || 'unknown');
  const testId = String(__ENV.SHIVA_ARTIFACT_TEST_ID || '');
  const uploadToken = String(__ENV.SHIVA_ARTIFACT_UPLOAD_TOKEN || '');
  const baseUrl = String(__ENV.SHIVA_ARTIFACT_UPLOAD_URL || '');
  if (!workerId || !testId || !uploadToken || !baseUrl) {
    return;
  }

  const uploadUrl =
    String(baseUrl).replace(/\/+$/, '') +
    '/api/internal/runs/' +
    encodeURIComponent(testId) +
    '/workers/' +
    encodeURIComponent(workerId) +
    '/' +
    encodeURIComponent(artifactType);

  try {
    http.post(uploadUrl, content, {
      headers: {
        'Content-Type': contentType || 'application/json',
        'X-Shiva-Artifact-Token': uploadToken,
      },
      timeout: '10s',
    });
  } catch (error) {
    // Best effort only: the controller still has the shared-volume fallback.
  }
}

export function handleSummary(data) {
  const wid = __ENV.WORKER_ID || 'unknown';
  const summaryContent = JSON.stringify(data);
  const artifacts = {
    ['/output/summary-' + wid + '.json']: summaryContent,
  };
  shivaUploadArtifact('summary', summaryContent, 'application/json');
  const payloadArtifact = typeof REQUEST_PAYLOAD_ARTIFACT !== 'undefined' ? REQUEST_PAYLOAD_ARTIFACT : '';
  if (payloadArtifact) {
    artifacts['/output/payload-' + wid + '.json'] = payloadArtifact;
    shivaUploadArtifact('payload', payloadArtifact, 'application/json');
  }
  if (typeof AUTH_ENABLED !== 'undefined' && AUTH_ENABLED) {
    const allMetrics = data && data.metrics ? data.metrics : {};
    const metricValues = (name) =>
      allMetrics[name] &&
      allMetrics[name].values
        ? allMetrics[name].values
        : {};
    const taggedMetricKeys = (name) =>
      Object.keys(allMetrics).filter((key) => key === name || key.indexOf(name + '{') === 0);
    const metricKeyTags = (metricKey) => {
      const start = metricKey.indexOf('{');
      const end = metricKey.lastIndexOf('}');
      if (start === -1 || end <= start + 1) {
        return {};
      }
      const tags = {};
      for (const part of metricKey.slice(start + 1, end).split(',')) {
        let separator = part.indexOf(':');
        if (separator <= 0) {
          separator = part.indexOf('=');
        }
        if (separator <= 0) {
          continue;
        }
        const key = part.slice(0, separator).trim();
        let value = part.slice(separator + 1).trim();
        if (
          (value.startsWith('"') && value.endsWith('"')) ||
          (value.startsWith("'") && value.endsWith("'"))
        ) {
          value = value.slice(1, -1);
        }
        if (key) {
          tags[key] = value;
        }
      }
      return tags;
    };
    const taggedMetricCount = (metricKey) =>
      Number(allMetrics[metricKey] && allMetrics[metricKey].values ? allMetrics[metricKey].values.count || 0 : 0);
    const authRuntimeState = () =>
      typeof AUTH_STATE !== 'undefined' && AUTH_STATE
        ? AUTH_STATE
        : null;
    const counterMetricCount = (name) => Number(metricValues(name).count || 0);
    const authStatusCodeEntriesFromCounterPrefix = (prefix) => {
      const entries = [];
      for (let code = 100; code <= 599; code += 1) {
        const count = counterMetricCount(prefix + String(code) + '_total');
        if (count > 0) {
          entries.push({ code, count });
        }
      }
      return entries;
    };
    const authResponseStatusEntriesFromSummary = () => {
      const counts = {};
      for (const metricKey of taggedMetricKeys('auth_token_responses_total')) {
        const tags = metricKeyTags(metricKey);
        const statusCode = Number(tags.status_code || 0);
        const count = taggedMetricCount(metricKey);
        if (!Number.isInteger(statusCode) || statusCode < 100 || statusCode > 599 || count <= 0) {
          continue;
        }
        const key = String(statusCode);
        counts[key] = Number(counts[key] || 0) + count;
      }
      return Object.keys(counts)
        .map((key) => Number(key))
        .sort((a, b) => a - b)
        .map((code) => ({ code, count: counts[String(code)] }));
    };
    const authResponseStatusEntriesFromCounters = () => authStatusCodeEntriesFromCounterPrefix('auth_token_response_status_');
    const authResponseStatusEntriesFromRuntime = () => {
      const state = authRuntimeState();
      return authResponseStatusEntries(state);
    };
    const authAbortCauseFromCounters = () => {
      const causeKeys = ['http_status', 'timeout', 'network_error', 'config_error', 'invalid_response', 'unknown'];
      let selected = '';
      let selectedCount = 0;
      for (const causeKey of causeKeys) {
        const count = counterMetricCount('auth_abort_cause_' + causeKey + '_total');
        if (count > selectedCount) {
          selected = causeKey;
          selectedCount = count;
        }
      }
      return selected;
    };
    const authAbortSummaryFromMetrics = () => {
      let selected = null;
      for (const metricKey of taggedMetricKeys('auth_abort_events_total')) {
        const count = taggedMetricCount(metricKey);
        if (count <= 0) {
          continue;
        }
        const tags = metricKeyTags(metricKey);
        const statusCode = Number(tags.status_code || 0);
        const candidate = {
          count,
          cause: tags.cause || '',
          retryable: String(tags.retryable || '').toLowerCase() === 'true',
          statusCode: Number.isInteger(statusCode) && statusCode > 0 ? statusCode : 0,
        };
        if (!selected || candidate.count > selected.count) {
          selected = candidate;
        }
      }
      if (!selected) {
        return {
          triggered: false,
          cause: '',
          reason: '',
          statusCode: 0,
          retryable: false,
        };
      }
      let reason = 'Authentication aborted the test run.';
      if (selected.cause === 'http_status' && selected.statusCode > 0) {
        reason = 'token endpoint returned HTTP ' + String(selected.statusCode);
      } else if (selected.cause === 'timeout') {
        reason = 'token request timed out';
      } else if (selected.cause === 'network_error') {
        reason = 'token request failed';
      } else if (selected.cause === 'config_error') {
        reason = 'authentication configuration is incomplete';
      } else if (selected.cause === 'invalid_response') {
        reason = 'token endpoint returned invalid JSON';
      }
      return {
        triggered: true,
        cause: selected.cause,
        reason,
        statusCode: selected.statusCode,
        retryable: selected.retryable,
      };
    };
    const authAbortSummaryFromCounters = () => {
      const triggered = counterMetricCount('auth_abort_triggered_total') > 0 || counterMetricCount('auth_abort_events_total') > 0;
      if (!triggered) {
        return {
          triggered: false,
          cause: '',
          reason: '',
          statusCode: 0,
          retryable: false,
        };
      }
      const statusEntries = authStatusCodeEntriesFromCounterPrefix('auth_abort_status_');
      const statusCode = statusEntries.length > 0 ? statusEntries[0].code : 0;
      const cause = authAbortCauseFromCounters();
      const retryable = counterMetricCount('auth_abort_retryable_true_total') > 0
        ? true
        : counterMetricCount('auth_abort_retryable_false_total') > 0
          ? false
          : false;
      let reason = 'Authentication aborted the test run.';
      if (cause === 'http_status' && statusCode > 0) {
        reason = 'token endpoint returned HTTP ' + String(statusCode);
      } else if (cause === 'timeout') {
        reason = 'token request timed out';
      } else if (cause === 'network_error') {
        reason = 'token request failed';
      } else if (cause === 'config_error') {
        reason = 'authentication configuration is incomplete';
      } else if (cause === 'invalid_response') {
        reason = 'token endpoint returned invalid JSON';
      }
      return {
        triggered: true,
        cause,
        reason,
        statusCode,
        retryable,
      };
    };
    const authAbortSummaryFromRuntime = () => {
      const state = authRuntimeState();
      if (!state || !state.abortTriggered) {
        return {
          triggered: false,
          cause: '',
          reason: '',
          statusCode: 0,
          retryable: false,
        };
      }
      return {
        triggered: true,
        cause: String(state.abortCause || ''),
        reason: String(state.abortReason || 'Authentication aborted the test run.'),
        statusCode: Number.isInteger(Number(state.abortHTTPStatusCode)) ? Number(state.abortHTTPStatusCode) : 0,
        retryable: Boolean(state.abortRetryable),
      };
    };
    const tokenDurationMetric =
      metricValues('auth_token_request_duration_ms');
    const tokenRequestsTotal = Number(metricValues('auth_token_requests_total').count || 0);
    const tokenSuccessTotal = Number(metricValues('auth_token_success_total').count || 0);
    const tokenFailureTotal = Number(metricValues('auth_token_failure_total').count || 0);
    const tokenSuccessRate = tokenRequestsTotal > 0 ? tokenSuccessTotal / tokenRequestsTotal : 0;
    const tokenRefreshTotal = Number(metricValues('auth_token_refresh_total').count || 0);
    const tokenReuseHitsTotal = Number(metricValues('auth_token_reuse_hits_total').count || 0);
    const responseStatusCodesFromCounters = authResponseStatusEntriesFromCounters();
    const responseStatusCodesFromSummary = authResponseStatusEntriesFromSummary();
    const responseStatusCodesFromRuntime = authResponseStatusEntriesFromRuntime();
    const responseStatusCodes = responseStatusCodesFromCounters.length > 0
      ? responseStatusCodesFromCounters
      : responseStatusCodesFromSummary.length > 0
        ? responseStatusCodesFromSummary
        : responseStatusCodesFromRuntime;
    const abortSummaryFromCounters = authAbortSummaryFromCounters();
    const abortSummaryFromMetrics = authAbortSummaryFromMetrics();
    const abortSummaryFromRuntime = authAbortSummaryFromRuntime();
    const abortSummary = abortSummaryFromCounters.triggered
      ? abortSummaryFromCounters
      : abortSummaryFromMetrics.triggered
        ? {
            triggered: true,
            cause: abortSummaryFromMetrics.cause || abortSummaryFromRuntime.cause,
            reason:
              abortSummaryFromMetrics.cause || abortSummaryFromMetrics.statusCode > 0
                ? abortSummaryFromMetrics.reason
                : (abortSummaryFromRuntime.reason || abortSummaryFromMetrics.reason),
            statusCode: abortSummaryFromMetrics.statusCode > 0
              ? abortSummaryFromMetrics.statusCode
              : abortSummaryFromRuntime.statusCode,
            retryable: abortSummaryFromMetrics.retryable || abortSummaryFromRuntime.retryable,
          }
        : abortSummaryFromRuntime;

    const authSummaryContent = JSON.stringify({
      status: abortSummary.triggered ? 'aborted' : 'complete',
      message: abortSummary.triggered
        ? abortSummary.reason
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
        response_status_codes: responseStatusCodes,
        abort_triggered: abortSummary.triggered,
        abort_cause: abortSummary.cause,
        abort_reason: abortSummary.reason,
        abort_http_status_codes: abortSummary.statusCode > 0
          ? [abortSummary.statusCode]
          : [],
        abort_retryable: abortSummary.retryable,
      },
    });
    artifacts['/output/auth-summary-' + wid + '.json'] = authSummaryContent;
    shivaUploadArtifact('auth-summary', authSummaryContent, 'application/json');
  }
  return artifacts;
}
`
}

// WriteScript writes the script content to the shared volume.
func WriteScript(scriptsDir, content string) error {
	path := filepath.Join(scriptsDir, scriptFileName)
	return os.WriteFile(path, []byte(content), 0644)
}

const configFileName = "config.json"

// ProcessedConfig holds the k6 options JSON (without env) and the extracted env vars.
type ProcessedConfig struct {
	OptionsJSON  string            // JSON without the "env" key — written to config.json
	EnvVars      map[string]string // extracted from "env" — passed as -e flags
	ExecutorType ExecutorType      // detected executor type from scenarios
	Stages       []model.Stage     // extracted from VU-based scenarios (for RampingManager)
}

// ValidateAndProcessConfig validates config JSON, extracts the "env" block,
// and returns the cleaned options JSON plus the env vars separately.
// workerCount is used to divide arrival-rate values across workers so each
// worker runs its fair share of the total target rate.
func ValidateAndProcessConfig(content string, workerCount int) (*ProcessedConfig, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("config is empty")
	}
	if len(content) > 1<<20 {
		return nil, fmt.Errorf("config exceeds 1 MB size limit")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// Extract env vars for -e CLI flags (belt-and-suspenders: env block also
	// stays in config.json so scripts reading options.env still work).
	envVars := make(map[string]string)
	if envObj, ok := parsed["env"]; ok {
		if envMap, ok := envObj.(map[string]any); ok {
			for k, v := range envMap {
				envVars[k] = fmt.Sprintf("%v", v)
			}
		} else {
			return nil, fmt.Errorf("\"env\" must be a JSON object with string values")
		}
	}

	// Schema validation: only allow known k6 option keys
	knownKeys := map[string]bool{
		"scenarios": true, "stages": true, "vus": true, "duration": true,
		"iterations": true, "thresholds": true, "noConnectionReuse": true,
		"userAgent": true, "hosts": true, "insecureSkipTLSVerify": true,
		"throw": true, "batch": true, "batchPerHost": true, "rps": true,
		"httpDebug": true, "noVUConnectionReuse": true, "maxRedirects": true,
		"discardResponseBodies": true, "setupTimeout": true, "teardownTimeout": true,
		"ext": true, "tags": true, "systemTags": true, "summaryTrendStats": true,
		"summaryTimeUnit": true, "dns": true, "tlsCipherSuites": true,
		"tlsVersion": true, "tlsAuth": true, "blacklistIPs": true,
		"blockHostnames": true, "noUsageReport": true, "cloud": true,
		"minIterationDuration": true, "gracefulRampDown": true,
		"gracefulStop": true, "executor": true, "env": true,
	}
	for key := range parsed {
		if !knownKeys[key] {
			return nil, fmt.Errorf("unknown k6 option key: %q", key)
		}
	}

	// Detect executor type and transform VU-based scenarios to externally-controlled
	// so the controller can manage Pause/Resume/Scale.
	detectedExecutor := DetectExecutorFromConfig(parsed)
	var extractedStages []model.Stage

	if detectedExecutor.IsControllable() && detectedExecutor != ExecutorExternallyControlled {
		// Transform VU-based scenarios (ramping-vus, constant-vus) to externally-controlled
		if scenarios, ok := parsed["scenarios"]; ok {
			if scenarioMap, ok := scenarios.(map[string]any); ok {
				extractedStages = transformVUScenarios(scenarioMap)
				parsed["scenarios"] = scenarioMap // write back the transformed map
			}
		}
	}

	// Divide arrival-rate scenario values across workers so each worker
	// runs its fair share. Without this, each worker runs the FULL rate,
	// resulting in N× the intended total throughput.
	if workerCount > 1 {
		if scenarios, ok := parsed["scenarios"]; ok {
			if scenarioMap, ok := scenarios.(map[string]any); ok {
				divideForWorkers(scenarioMap, workerCount)
			}
		}
	}

	// Re-serialize config
	optionsBytes, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to re-serialize config: %w", err)
	}

	return &ProcessedConfig{
		OptionsJSON:  string(optionsBytes),
		EnvVars:      envVars,
		ExecutorType: detectedExecutor,
		Stages:       extractedStages,
	}, nil
}

// transformVUScenarios finds ramping-vus and constant-vus scenarios, extracts their
// stage/VU info, and replaces them with externally-controlled executors so the controller
// can manage VU ramping with Pause/Resume/Scale support.
func transformVUScenarios(scenarioMap map[string]any) []model.Stage {
	var allStages []model.Stage

	for name, sc := range scenarioMap {
		scMap, ok := sc.(map[string]any)
		if !ok {
			continue
		}
		executor, _ := scMap["executor"].(string)

		switch executor {
		case "ramping-vus":
			stagesRaw, ok := scMap["stages"]
			if !ok {
				continue
			}
			stagesSlice, ok := stagesRaw.([]any)
			if !ok {
				continue
			}

			maxVUs := 0
			totalDurationSec := 0
			for _, stageRaw := range stagesSlice {
				stageMap, ok := stageRaw.(map[string]any)
				if !ok {
					continue
				}
				dur, _ := stageMap["duration"].(string)
				target := 0
				if t, ok := stageMap["target"].(float64); ok {
					target = int(t)
				}
				allStages = append(allStages, model.Stage{Duration: dur, Target: target})
				if target > maxVUs {
					maxVUs = target
				}
				totalDurationSec += parseDuration(dur)
			}
			if maxVUs < 1 {
				maxVUs = 1
			}
			totalDurationSec += completionBufferSeconds // buffer

			scenarioMap[name] = map[string]any{
				"executor": "externally-controlled",
				"vus":      0,
				"maxVUs":   maxVUs,
				"duration": fmt.Sprintf("%ds", totalDurationSec),
			}

		case "constant-vus":
			vus := 0
			if v, ok := scMap["vus"].(float64); ok {
				vus = int(v)
			}
			if vus < 1 {
				vus = 1
			}
			dur, _ := scMap["duration"].(string)
			durationSec := parseDuration(dur)
			if durationSec < 1 {
				durationSec = 60
			}
			durationSec += completionBufferSeconds // buffer

			// For constant-vus: instant jump to target, then hold constant.
			// Two stages are required because calculateVUs interpolates linearly
			// from prevTarget=0. Without the "0s" instant-jump stage, VUs would
			// ramp from 0 to target over the entire duration instead of staying constant.
			allStages = append(allStages,
				model.Stage{Duration: "0s", Target: vus},
				model.Stage{Duration: dur, Target: vus},
			)

			scenarioMap[name] = map[string]any{
				"executor": "externally-controlled",
				"vus":      0,   // start at 0; RampingManager sets correct total VU count
				"maxVUs":   vus, // per-worker ceiling
				"duration": fmt.Sprintf("%ds", durationSec),
			}

		case "externally-controlled":
			// Already externally-controlled: extract maxVUs/duration so the
			// controller can build ramping stages to scale VUs up.
			maxVUs := 0
			if v, ok := scMap["maxVUs"].(float64); ok {
				maxVUs = int(v)
			}
			if maxVUs < 1 {
				maxVUs = 50
			}
			dur, _ := scMap["duration"].(string)
			durationSec := parseDuration(dur)
			if durationSec < 1 {
				durationSec = 120
			}

			// Synthetic stage: immediately scale to maxVUs, hold for duration.
			allStages = append(allStages,
				model.Stage{Duration: "0s", Target: maxVUs},
				model.Stage{Duration: fmt.Sprintf("%ds", durationSec), Target: maxVUs},
			)

			// Ensure config has enough duration (add buffer for collection phase)
			scMap["duration"] = fmt.Sprintf("%ds", durationSec+completionBufferSeconds)
			scMap["maxVUs"] = maxVUs
			scMap["vus"] = 0 // start at 0; RampingManager sets correct total VU count
		}
	}

	return allStages
}

// divideForWorkers divides arrival-rate scenario values across workers.
// Each worker runs rate/N, preAllocatedVUs/N, maxVUs/N so the aggregate
// across all workers equals the user's intended total.
// VU-based executors (already transformed to externally-controlled) are NOT
// divided here — the RampingManager distributes VUs via ScaleVUs calls.
func divideForWorkers(scenarioMap map[string]any, workerCount int) {
	for _, sc := range scenarioMap {
		scMap, ok := sc.(map[string]any)
		if !ok {
			continue
		}
		executor, _ := scMap["executor"].(string)

		switch executor {
		case "constant-arrival-rate":
			divideIntVal(scMap, "rate", workerCount)
			divideIntVal(scMap, "preAllocatedVUs", workerCount)
			divideIntVal(scMap, "maxVUs", workerCount)

		case "ramping-arrival-rate":
			divideIntVal(scMap, "startRate", workerCount)
			divideIntVal(scMap, "preAllocatedVUs", workerCount)
			divideIntVal(scMap, "maxVUs", workerCount)
			if stages, ok := scMap["stages"].([]any); ok {
				for _, stageRaw := range stages {
					if stageMap, ok := stageRaw.(map[string]any); ok {
						divideIntVal(stageMap, "target", workerCount)
					}
				}
			}

			// externally-controlled / VU-based: RampingManager distributes via ScaleVUs.
			// Do NOT divide maxVUs — it's a per-worker ceiling that must remain generous
			// so manual scaling and stage targets can be fulfilled.
		}
	}
}

// divideIntVal divides a numeric JSON value by workerCount using ceiling division.
func scenarioDurationSeconds(scMap map[string]any) int {
	executor, _ := scMap["executor"].(string)
	switch executor {
	case "ramping-vus", "ramping-arrival-rate":
		total := 0
		if stages, ok := scMap["stages"].([]any); ok {
			for _, stageRaw := range stages {
				stageMap, ok := stageRaw.(map[string]any)
				if !ok {
					continue
				}
				dur, _ := stageMap["duration"].(string)
				total += parseDuration(dur)
			}
		}
		return total
	default:
		dur, _ := scMap["duration"].(string)
		return parseDuration(dur)
	}
}

// EstimateConfiguredExecutionDuration returns the intended scenario runtime from
// raw k6 options JSON. It intentionally excludes the controller completion
// buffer and is used to guard against premature native-run completion.
func EstimateConfiguredExecutionDuration(content string) time.Duration {
	if strings.TrimSpace(content) == "" {
		return 0
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return 0
	}
	maxSeconds := 0
	if scenariosRaw, ok := parsed["scenarios"].(map[string]any); ok {
		for _, scenarioRaw := range scenariosRaw {
			scMap, ok := scenarioRaw.(map[string]any)
			if !ok {
				continue
			}
			if seconds := scenarioDurationSeconds(scMap); seconds > maxSeconds {
				maxSeconds = seconds
			}
		}
	}
	if maxSeconds == 0 {
		if stages, ok := parsed["stages"].([]any); ok {
			for _, stageRaw := range stages {
				stageMap, ok := stageRaw.(map[string]any)
				if !ok {
					continue
				}
				dur, _ := stageMap["duration"].(string)
				maxSeconds += parseDuration(dur)
			}
		} else if dur, ok := parsed["duration"].(string); ok {
			maxSeconds = parseDuration(dur)
		}
	}
	if maxSeconds <= 0 {
		return 0
	}
	return time.Duration(maxSeconds) * time.Second
}
func divideIntVal(m map[string]any, key string, workers int) {
	v, ok := m[key].(float64)
	if !ok || v <= 0 {
		return
	}
	divided := math.Ceil(v / float64(workers))
	if divided < 1 {
		divided = 1
	}
	m[key] = divided
}

// DetectExecutorFromConfig inspects the parsed config JSON for scenarios and returns the executor type.
// If multiple scenarios use different executors, the first one is returned.
func DetectExecutorFromConfig(parsed map[string]any) ExecutorType {
	scenarios, ok := parsed["scenarios"]
	if !ok {
		// No scenarios — check for top-level stages/vus/duration
		if _, hasStages := parsed["stages"]; hasStages {
			return ExecutorRampingVUs
		}
		if _, hasVUs := parsed["vus"]; hasVUs {
			return ExecutorConstantVUs
		}
		return ExecutorConstantVUs // default
	}

	scenarioMap, ok := scenarios.(map[string]any)
	if !ok {
		return ExecutorConstantVUs
	}

	for _, sc := range scenarioMap {
		scMap, ok := sc.(map[string]any)
		if !ok {
			continue
		}
		executor, _ := scMap["executor"].(string)
		switch executor {
		case "externally-controlled":
			return ExecutorExternallyControlled
		case "constant-vus":
			return ExecutorConstantVUs
		case "ramping-vus":
			return ExecutorRampingVUs
		case "constant-arrival-rate":
			return ExecutorConstantArrivalRate
		case "ramping-arrival-rate":
			return ExecutorRampingArrivalRate
		}
	}
	return ExecutorConstantVUs
}

// DetectExecutorFromScript inspects script content for executor keywords.
func DetectExecutorFromScript(content string) ExecutorType {
	// Check for explicit executor declarations in the script
	executors := []struct {
		keyword string
		typ     ExecutorType
	}{
		{"externally-controlled", ExecutorExternallyControlled},
		{"constant-arrival-rate", ExecutorConstantArrivalRate},
		{"ramping-arrival-rate", ExecutorRampingArrivalRate},
		{"ramping-vus", ExecutorRampingVUs},
		{"constant-vus", ExecutorConstantVUs},
	}
	for _, e := range executors {
		if strings.Contains(content, e.keyword) {
			return e.typ
		}
	}
	return ExecutorConstantVUs // default if no executor found
}

// WriteEnvFile writes env vars as a newline-separated KEY=VALUE file for the entrypoint.
func WriteEnvFile(scriptsDir string, envVars map[string]string) error {
	path := filepath.Join(scriptsDir, "k6-env.sh")
	if len(envVars) == 0 {
		// Remove stale env file
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	var buf bytes.Buffer
	for k, v := range envVars {
		key := k
		value := v
		if k == PayloadSourceJSONEnvVar && strings.TrimSpace(v) != "" {
			key = PayloadSourceJSONB64EnvVar
			value = base64.StdEncoding.EncodeToString([]byte(v))
		}
		// Shell-safe: single-quote values, escape embedded single quotes
		escaped := strings.ReplaceAll(value, "'", "'\\''")
		fmt.Fprintf(&buf, "export %s='%s'\n", key, escaped)
		fmt.Fprintf(&buf, "K6_ENV_FLAGS=\"$K6_ENV_FLAGS -e %s='%s'\"\n", key, escaped)
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// RemoveEnvFile removes the env file if it exists.
func RemoveEnvFile(scriptsDir string) error {
	path := filepath.Join(scriptsDir, "k6-env.sh")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// optionsBlockRE matches the entire `export const options = { ... };` block
// spanning multiple lines. The (?ms) flags enable multiline + dotall so that
// `^` matches line starts and `.` matches newlines.
var optionsBlockRE = regexp.MustCompile(`(?ms)^export\s+(?:const|let|var)\s+options\s*=\s*\{.*?^\};\s*?\n`)

// StripScriptOptions removes the `export const options = { ... };` block from
// a generated script. This is used when a config.json is provided, making the
// script-level options redundant (config takes precedence in k6).
// Custom metric definitions (Rate, Counter) are preserved.
func StripScriptOptions(script string) string {
	return optionsBlockRE.ReplaceAllString(script, "")
}

// CheckConflicts detects potential conflicts between script and config content.
func CheckConflicts(scriptContent, configContent string) []model.ConflictWarning {
	var warnings []model.ConflictWarning

	var config map[string]any
	if err := json.Unmarshal([]byte(configContent), &config); err != nil {
		return warnings
	}

	// 1. Redundancy: options in JS that are also in config
	scriptHasOptions := strings.Contains(scriptContent, "export const options") ||
		strings.Contains(scriptContent, "export let options") ||
		strings.Contains(scriptContent, "export var options")

	if scriptHasOptions {
		redundantKeys := []string{"stages", "vus", "duration", "thresholds", "iterations", "scenarios"}
		for _, key := range redundantKeys {
			if _, ok := config[key]; ok {
				inScript := strings.Contains(scriptContent, key)
				if inScript {
					warnings = append(warnings, model.ConflictWarning{
						Type:    "redundancy",
						Message: fmt.Sprintf("'%s' is defined in both the script options and the config file. The config file takes precedence.", key),
					})
				}
			}
		}
		// General warning if script has options block
		if len(warnings) == 0 {
			for key := range config {
				if key == "stages" || key == "vus" || key == "duration" || key == "thresholds" || key == "iterations" || key == "scenarios" {
					warnings = append(warnings, model.ConflictWarning{
						Type:    "redundancy",
						Message: "The script contains an 'export options' block. Config file values will override matching script options.",
					})
					break
				}
			}
		}
	}

	// 2. Entry-point: config has scenarios with exec functions not in script
	if scenarios, ok := config["scenarios"]; ok {
		if scenarioMap, ok := scenarios.(map[string]any); ok {
			for name, sc := range scenarioMap {
				if scMap, ok := sc.(map[string]any); ok {
					if exec, ok := scMap["exec"].(string); ok && exec != "default" {
						pattern := fmt.Sprintf(`export\s+(function|const|let|var)\s+%s`, regexp.QuoteMeta(exec))
						matched, _ := regexp.MatchString(pattern, scriptContent)
						if !matched && !strings.Contains(scriptContent, "export function "+exec) {
							warnings = append(warnings, model.ConflictWarning{
								Type:    "entry_point",
								Message: fmt.Sprintf("Scenario '%s' references exec function '%s' which is not exported in the script.", name, exec),
							})
						}
					}
				}
			}
		}
	}

	// 3. Metric consistency: thresholds reference custom metrics not in script
	if thresholds, ok := config["thresholds"]; ok {
		if thMap, ok := thresholds.(map[string]any); ok {
			builtins := map[string]bool{
				"http_req_duration": true, "http_req_failed": true, "http_req_waiting": true,
				"http_req_connecting": true, "http_req_tls_handshaking": true, "http_req_sending": true,
				"http_req_receiving": true, "http_req_blocked": true, "http_reqs": true,
				"vus": true, "vus_max": true, "iterations": true, "iteration_duration": true,
				"data_received": true, "data_sent": true, "checks": true, "group_duration": true,
				"status_4xx": true, "status_5xx": true, "errors": true, "success_rate": true,
			}
			for metric := range thMap {
				baseName := strings.Split(metric, "{")[0] // strip tag filters like http_req_duration{expected_response:true}
				if !builtins[baseName] && !strings.Contains(scriptContent, baseName) {
					warnings = append(warnings, model.ConflictWarning{
						Type:    "metric_consistency",
						Message: fmt.Sprintf("Threshold references metric '%s' which does not appear in the script.", baseName),
					})
				}
			}
		}
	}

	return warnings
}

// WriteConfig writes the config JSON to the shared volume.
func WriteConfig(scriptsDir, content string) error {
	path := filepath.Join(scriptsDir, configFileName)
	return os.WriteFile(path, []byte(content), 0644)
}

// RemoveConfig removes the config file if it exists.
func RemoveConfig(scriptsDir string) error {
	path := filepath.Join(scriptsDir, configFileName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// EnsureDefault writes a default script if current-test.js does not exist.
func EnsureDefault(scriptsDir string) error {
	path := filepath.Join(scriptsDir, scriptFileName)
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}

	defaultScript := `import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Counter } from 'k6/metrics';

const errorRate = new Rate('errors');
const successRate = new Rate('success_rate');
const status4xx = new Counter('status_4xx');
const status5xx = new Counter('status_5xx');

export const options = {
  scenarios: {
    controller_managed: {
      executor: 'externally-controlled',
      vus: 5,
      maxVUs: 20,
      duration: '3m',
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<1500'],
    errors: ['rate<0.1'],
    success_rate: ['rate>0.95'],
  },
};

const BASE_URL = __ENV.TARGET_URL || 'http://target-lb:8090';

export default function () {
  const res = http.get(BASE_URL);

  if (!res || res.error) {
    errorRate.add(true);
    successRate.add(false);
    sleep(0.5);
    return;
  }

  const passed = check(res, {
    'status is 2xx': (r) => r.status >= 200 && r.status < 300,
  });

  errorRate.add(!passed);
  successRate.add(passed);
  if (res.status >= 400 && res.status < 500) status4xx.add(1);
  if (res.status >= 500) status5xx.add(1);

  sleep(0.5);
}
`
	return os.WriteFile(path, []byte(defaultScript), 0644)
}
