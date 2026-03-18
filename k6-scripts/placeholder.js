import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';

const errorRate = new Rate('errors');
const successRate = new Rate('success_rate');

export const options = {
  stages: [
    { duration: '30s', target: 5 },
    { duration: '1m', target: 10 },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    http_req_duration: ['p(95)<1500'],
    errors: ['rate<0.1'],
    success_rate: ['rate>0.95'],
  },
};

const BASE_URL = __ENV.TARGET_URL || 'http://dummy-service:8090';

const endpoints = [
  { method: 'GET',  path: '/api/users' },
  { method: 'GET',  path: '/api/users/1' },
  { method: 'GET',  path: '/api/products' },
  { method: 'GET',  path: '/api/orders' },
  { method: 'POST', path: '/api/users',  body: JSON.stringify({ name: 'LoadTest', email: 'test@example.com' }) },
  { method: 'POST', path: '/api/orders', body: JSON.stringify({ user_id: 1, total: 19.99 }) },
];

export default function () {
  // Pick a random endpoint each iteration
  const ep = endpoints[Math.floor(Math.random() * endpoints.length)];
  const url = `${BASE_URL}${ep.path}`;
  const params = { headers: { 'Content-Type': 'application/json' } };

  let res;
  if (ep.method === 'POST') {
    res = http.post(url, ep.body, params);
  } else {
    res = http.get(url, params);
  }

  const passed = check(res, {
    'status is 2xx': (r) => r.status >= 200 && r.status < 300,
    'response time < 500ms': (r) => r.timings.duration < 500,
  });

  errorRate.add(!passed);
  successRate.add(passed);

  sleep(0.5);
}
