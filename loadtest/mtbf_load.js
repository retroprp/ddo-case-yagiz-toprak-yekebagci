import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend } from 'k6/metrics';

/**
 * BakımPlan CMMS — yük testi
 *
 * İki ayrı senaryo var, çünkü iki uç farklı SLO'ya tabi:
 *
 *  1) reliability  → F-7/T-4 raporu. Spec'in hedefi: 1 milyon iş emrinde
 *                    "En Problemli 10 Ekipman" sorgusunun hızlı çalışması.
 *                    Bu yüzden sıkı eşik: p(95) < 250 ms.
 *
 *  2) tree         → F-1/T-2 ekipman ağacı. Tüm 2000 düğümü tek yanıtta
 *                    döndürdüğü için gövde ~570 KB; ağ ve JSON ayrıştırma
 *                    süresi baskın. Bu bilinçli bir tasarım tercihi
 *                    (ağaç tek seferde yüklenir), bu yüzden eşiği ayrı ve
 *                    gevşek tutuyoruz — aksi hâlde test hep kırmızı kalır
 *                    ve gerçek regresyonu gizler.
 *
 * Çalıştırma:
 *   make load                                   # backend:8080 (compose içi)
 *   K6_API_URL=http://localhost:8080 make load  # host üzerinden
 */

const BASE_URL = __ENV.API_URL || 'http://localhost:8080';

const treePayload = new Trend('equipment_tree_payload_bytes');

export const options = {
  scenarios: {
    reliability: {
      executor: 'constant-vus',
      vus: 20,
      duration: '15s',
      exec: 'reliabilityReport',
      tags: { scenario: 'reliability' },
    },
    tree: {
      executor: 'constant-vus',
      vus: 5,
      duration: '15s',
      exec: 'equipmentTree',
      tags: { scenario: 'tree' },
    },
  },
  thresholds: {
    // Spec'in asıl hedefi: rapor ucu 1M satırda hızlı kalmalı.
    'http_req_duration{scenario:reliability}': ['p(95)<250'],
    // Ağaç ucu: 570 KB gövde + 2000 düğüm JSON ayrıştırma.
    'http_req_duration{scenario:tree}': ['p(95)<3000'],
    'http_req_failed': ['rate<0.01'],
    'checks': ['rate>0.99'],
  },
};

export function setup() {
  const res = http.post(
    `${BASE_URL}/api/v1/auth/login`,
    JSON.stringify({ email: 'yonetici@bakimplan.local', password: 'Password123!' }),
    { headers: { 'Content-Type': 'application/json' }, tags: { endpoint: 'login' } },
  );

  check(res, { 'login 200': (r) => r.status === 200 });

  const token = res.json('access_token');
  if (!token) {
    throw new Error('login başarısız: token alınamadı (seed tamamlandı mı?)');
  }
  return { token };
}

function authHeaders(token) {
  return { headers: { Authorization: `Bearer ${token}` } };
}

/** F-7 / T-4: MTBF, MTTR, Availability ve En Problemli 10 Ekipman */
export function reliabilityReport(data) {
  const res = http.get(`${BASE_URL}/api/v1/metrics/reliability?days=30`, authHeaders(data.token));

  check(res, {
    'reliability 200': (r) => r.status === 200,
    'top10 dolu': (r) => Array.isArray(r.json('top_problematic_10')),
    'availability <= 100': (r) => r.json('availability_percent') <= 100,
  });

  sleep(0.2);
}

/** F-1 / T-2: WITH RECURSIVE ekipman ağacı ve roll-up */
export function equipmentTree(data) {
  const res = http.get(`${BASE_URL}/api/v1/equipments/tree`, authHeaders(data.token));

  check(res, {
    'tree 200': (r) => r.status === 200,
    'kök tesis var': (r) => r.json('tree').length > 0,
    'flat_count > 0': (r) => r.json('flat_count') > 0,
  });

  if (res.status === 200 && res.body) {
    treePayload.add(res.body.length);
  }

  sleep(0.5);
}
