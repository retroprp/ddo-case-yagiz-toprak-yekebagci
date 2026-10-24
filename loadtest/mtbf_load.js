import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  vus: 25,
  duration: '15s',
  thresholds: {
    http_req_duration: ['p(95)<250'], // %95 yanıt süresi 250ms altında olmalı
    http_req_failed: ['rate<0.01'],
  },
};

const BASE_URL = __ENV.API_URL || 'http://localhost:8080';

export function setup() {
  const res = http.post(`${BASE_URL}/api/v1/auth/login`, JSON.stringify({
    email: 'yonetici@bakimplan.local',
    password: 'Password123!',
  }), { headers: { 'Content-Type': 'application/json' } });

  return { token: res.json('access_token') };
}

export default function (data) {
  const params = {
    headers: {
      Authorization: `Bearer ${data.token}`,
    },
  };

  // Görev 4: MTBF, MTTR ve En Problemli 10 Ekipman Sorgusu Yük Testi
  const relRes = http.get(`${BASE_URL}/api/v1/metrics/reliability`, params);
  check(relRes, {
    'reliability status is 200': (r) => r.status === 200,
    'top 10 returned': (r) => r.json('top_problematic_10').length === 10,
  });

  // Görev 2: WITH RECURSIVE 2000 Ekipman Ağacı ve Roll-up Sorgusu
  const treeRes = http.get(`${BASE_URL}/api/v1/equipments/tree`, params);
  check(treeRes, {
    'tree status is 200': (r) => r.status === 200,
  });

  sleep(0.2);
}