<script setup>
import { ref, watch, onMounted } from 'vue'
import { store, apiFetch, formatTL, formatDate } from '../store'

const props = defineProps({ section: { type: String, default: 'inventory' } })

// Sunucu varsayılanı 50; arayüzde 25 tutup "daha fazla yükle" ile büyütüyoruz.
const PAGE_SIZE = 25

const partsList = ref([])
const auditLogs = ref([])

// Keyset sayfalama durumu: her liste kendi imlecini taşır.
const partsCursor = ref(null)
const auditCursor = ref(null)
const partsHasMore = ref(false)
const auditHasMore = ref(false)

const loading = ref(false)
const loadingMore = ref(false)
const loadError = ref('')

const reserveForm = ref({ work_order_id: 1, part_id: 1, warehouse_id: 1, quantity: 1 })
const stockForm = ref({ part_id: 1, warehouse_id: 1, quantity_change: 10, movement_type: 'purchase' })

// append=false → ilk sayfa (listeyi sıfırla)
// append=true  → sonraki sayfa (mevcut listenin sonuna ekle)
//
// Neden birleştirme? Kullanıcı kaydırdıkça liste büyümeli; her sayfada
// öncekileri kaybetmek "geri gitme" hissi verir.
async function loadData(append = false) {
  if (append) {
    loadingMore.value = true
  } else {
    loading.value = true
    loadError.value = ''
  }

  try {
    if (props.section === 'inventory') {
      const params = new URLSearchParams({ limit: String(PAGE_SIZE) })
      const cursor = append ? partsCursor.value : null
      if (cursor) params.set('cursor', String(cursor))

      const res = await apiFetch(`/parts?${params.toString()}`)
      const page = res.items || []
      partsList.value = append ? [...partsList.value, ...page] : page
      partsCursor.value = res.next_cursor ?? null
      partsHasMore.value = Boolean(res.has_more)
    } else if (props.section === 'audit') {
      const params = new URLSearchParams({ limit: String(PAGE_SIZE) })
      const cursor = append ? auditCursor.value : null
      if (cursor) params.set('cursor', String(cursor))

      const res = await apiFetch(`/audit-logs?${params.toString()}`)
      const page = res.items || []
      auditLogs.value = append ? [...auditLogs.value, ...page] : page
      auditCursor.value = res.next_cursor ?? null
      auditHasMore.value = Boolean(res.has_more)
    }
  } catch (err) {
    loadError.value = err.message
    if (!append) {
      partsList.value = []
      auditLogs.value = []
    }
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

async function submitReservePart() {
  try {
    const res = await apiFetch(`/work-orders/${reserveForm.value.work_order_id}/reserve-part`, {
      method: 'POST',
      body: JSON.stringify({
        part_id: Number(reserveForm.value.part_id),
        warehouse_id: Number(reserveForm.value.warehouse_id),
        quantity: Number(reserveForm.value.quantity)
      })
    })
    store.successBanner = `Rezervasyon #${res.reservation_id} başarılı! Kalan kullanılabilir stok: ${res.remaining_available}`
    await loadData()
  } catch (_) {}
}

async function submitStockMovement() {
  try {
    await apiFetch('/stock-movements', {
      method: 'POST',
      body: JSON.stringify({
        part_id: Number(stockForm.value.part_id),
        warehouse_id: Number(stockForm.value.warehouse_id),
        quantity_change: Number(stockForm.value.quantity_change),
        movement_type: stockForm.value.movement_type
      })
    })
    store.successBanner = 'Stok hareketi kaydedildi ve stok toplamı güncellendi.'
    await loadData()
  } catch (_) {}
}

watch(() => props.section, () => loadData())
onMounted(() => loadData())

// ── Denetim kaydı: JSONB okunabilir gösterim ────────────────────────────────
// Ham JSON.stringify çıktısı ekranda okunmuyor ('{"path":"/api/v1/..."}').
// Bunun yerine anahtar/değer çiftleri gösteriyoruz; bilinen alanlara Türkçe
// etiket veriyoruz.
const auditFieldLabels = {
  path: 'Uç',
  status: 'Durum',
  status_code: 'HTTP',
  code: 'Kod',
  name: 'Ad',
  title: 'Başlık',
  level_type: 'Seviye',
  parent_id: 'Üst',
  is_critical: 'Kritik',
  shift_hours_per_day: 'Vardiya (sa)',
  equipment_id: 'Ekipman',
  equipment_id_for_bom: 'BOM ekipmanı',
  part_id: 'Parça',
  warehouse_id: 'Depo',
  quantity: 'Miktar',
  quantity_change: 'Miktar',
  reservation_id: 'Rezervasyon',
  available_after: 'Kalan stok',
  available_before: 'Önceki stok',
  work_order_id: 'İş emri',
  assigned_to: 'Teknisyen',
  previous_assignee: 'Önceki teknisyen',
  wo_type: 'İş emri tipi',
  movement_type: 'Hareket tipi',
  plan_type: 'Plan tipi',
  start_time: 'Başlangıç',
  end_time: 'Bitiş',
  reason: 'Sebep',
  severity: 'Önem',
  priority: 'Öncelik',
  failure_code: 'Arıza kodu',
  calc_basis: 'Hesap tabanı',
  interval_days: 'Periyot (gün)',
  interval_hours: 'Periyot (saat)',
  next_due_at: 'Sonraki tarih',
  next_due_meter: 'Sonraki sayaç',
  completed_at: 'Tamamlanma',
  next_pm_scheduled_at: 'Sonraki bakım',
  labor_cost_kurus: 'İşçilik',
  parts_cost_kurus: 'Parça maliyeti',
  note: 'Not',
  row_count: 'Satır'
}

// Değeri insan-okunur biçime çevirir.
// Tarih alanları ISO string gelir; ekranda "26.10.2026 15:12" görmek
// "2026-10-26T15:12:32Z" görmekten iyidir.
function auditValue(key, value) {
  if (value === null || value === undefined) return '—'
  if (typeof value === 'boolean') return value ? 'evet' : 'hayır'
  if (typeof value === 'object') return JSON.stringify(value)
  if (typeof value === 'string' && /^\d{4}-\d{2}-\d{2}T/.test(value)) {
    return formatDate(value)
  }
  // Kuruş alanları: 150000 → "1.500,00 ₺"
  if (typeof value === 'number' && key.endsWith('_kurus')) return formatTL(value)
  return String(value)
}

function auditRows(data) {
  if (!data || typeof data !== 'object') return []
  const rows = Object.entries(data).map(([key, value]) => ({
    key,
    label: auditFieldLabels[key] || key,
    value: auditValue(key, value),
    empty: value === null || value === undefined
  }))
  // Boş alanları sona at: okunurken anlamlı bilgiler önce gelsin.
  // (Sıralama stabil: aynı değere sahip alanlar orijinal sırasını korur.)
  return rows.sort((a, b) => Number(a.empty) - Number(b.empty))
}

// "post /api/v1/work-orders" → "POST /work-orders"
// Ham eylem adları (UPDATE_DOWNTIME) olduğu gibi kalır.
function auditAction(action) {
  if (!action) return '—'
  const m = action.match(/^(get|post|patch|put|delete)\s+(.+)$/i)
  if (!m) return action
  return m[1].toUpperCase() + ' ' + m[2].replace('/api/v1', '')
}
</script>

<template>
  <div v-if="section === 'inventory'" class="grid grid-cols-1 lg:grid-cols-3 gap-4">
    <div class="lg:col-span-2 bg-white rounded-xl border border-slate-200 shadow-sm p-4">
      <h2 class="text-base font-bold text-slate-800 mb-1">Yedek Parça Stoğu (Stok = Hareketlerin Toplamı - T-3)</h2>
      <p class="text-xs text-slate-500 mb-3">Eşzamanlı rezervasyonlar <code>SELECT ... FOR UPDATE</code> satır kilidiyle korunur; kritik parçada sıfır stok uyarısı verilir.</p>

      <!-- Yükleniyor / hata: sayfalama eklendiği için liste boş görünümü
           "yükleniyor" ile karışmasın diye ayrı durumlar gösteriliyor. -->
      <div v-if="loading" class="flex items-center gap-2 p-6 text-xs text-slate-500">
        <span class="animate-spin inline-block w-4 h-4 border-2 border-emerald-600 border-t-transparent rounded-full"></span>
        Parçalar yükleniyor...
      </div>
      <div v-else-if="loadError" class="p-4 text-xs text-red-800 bg-red-50 border border-red-200 rounded-lg">
        <p class="font-bold mb-1">Liste alınamadı</p>
        <p class="font-mono">{{ loadError }}</p>
        <button @click="loadData()" class="mt-2 px-3 py-1.5 rounded bg-red-600 text-white font-semibold">Tekrar dene</button>
      </div>
      <div v-else-if="partsList.length === 0" class="p-6 text-center text-slate-400 text-xs">Parça bulunamadı.</div>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-xs text-left">
          <thead class="bg-slate-50 text-slate-600 uppercase border-b">
            <tr>
              <th class="p-2">ID / SKU</th>
              <th class="p-2">Parça Adı</th>
              <th class="p-2 text-right">Birim Maliyet</th>
              <th class="p-2 text-right">Fiziksel</th>
              <th class="p-2 text-right">Rezerve</th>
              <th class="p-2 text-right">Kullanılabilir</th>
              <th class="p-2">Uyarı</th>
            </tr>
          </thead>
          <tbody class="divide-y">
            <tr v-for="p in partsList" :key="p.id" class="hover:bg-slate-50">
              <td class="p-2 font-mono font-bold">#{{ p.id }} ({{ p.sku }})</td>
              <td class="p-2">
                {{ p.name }}
                <span v-if="p.is_critical" class="ml-1 text-[10px] bg-red-100 text-red-700 font-bold px-1.5 py-0.5 rounded">KRİTİK</span>
              </td>
              <td class="p-2 text-right font-mono">{{ formatTL(p.unit_cost_kurus) }}</td>
              <td class="p-2 text-right font-mono">{{ p.physical_stock }}</td>
              <td class="p-2 text-right font-mono text-amber-600">{{ p.reserved_stock }}</td>
              <td class="p-2 text-right font-mono font-extrabold">{{ p.available_stock }}</td>
              <td class="p-2">
                <span v-if="p.critical_zero_warning" class="px-2 py-0.5 rounded bg-red-600 text-white text-[10px] font-bold">SIFIR STOK UYARISI!</span>
                <span v-else-if="p.needs_reorder" class="px-2 py-0.5 rounded bg-amber-100 text-amber-800 text-[10px] font-bold">Yeniden Sipariş (&le;{{ p.reorder_point }})</span>
                <span v-else class="text-emerald-600 text-[11px] font-semibold">Yeterli</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Keyset sayfalama: sunucu "daha var mı" bilgisini has_more ile verir,
           arayüz tahmin etmez. -->
      <div v-if="partsHasMore" class="pt-3 text-center">
        <button
          @click="loadData(true)"
          :disabled="loadingMore"
          class="text-xs px-3 py-1.5 rounded-lg border border-slate-300 font-semibold text-slate-700 hover:bg-slate-50 disabled:opacity-50"
        >
          {{ loadingMore ? 'Yükleniyor...' : 'Daha fazla parça' }}
        </button>
      </div>
      <p class="pt-2 text-[10px] text-slate-400 text-center">
        {{ partsList.length }} parça gösteriliyor
        <span v-if="partsHasMore"> · devamı var (cursor: {{ partsCursor }})</span>
        <span v-else> · liste sonu</span>
      </p>
    </div>

    <div class="space-y-4">
      <div class="bg-white rounded-xl border border-slate-200 shadow-sm p-4">
        <h3 class="text-sm font-bold text-slate-800 mb-1">İş Emrine Parça Rezerve Et (T-3)</h3>
        <form @submit.prevent="submitReservePart" class="space-y-2 text-xs mt-2">
          <input v-model.number="reserveForm.work_order_id" type="number" required placeholder="İş Emri ID" class="w-full px-2.5 py-1.5 border rounded" />
          <input v-model.number="reserveForm.part_id" type="number" required placeholder="Parça ID" class="w-full px-2.5 py-1.5 border rounded" />
          <input v-model.number="reserveForm.warehouse_id" type="number" required placeholder="Depo ID (1..3)" class="w-full px-2.5 py-1.5 border rounded" />
          <input v-model.number="reserveForm.quantity" type="number" required placeholder="Miktar" class="w-full px-2.5 py-1.5 border rounded" />
          <button type="submit" class="w-full py-2 bg-emerald-600 hover:bg-emerald-700 text-white font-semibold rounded">Rezerve Et</button>
        </form>
      </div>

      <div class="bg-white rounded-xl border border-slate-200 shadow-sm p-4">
        <h3 class="text-sm font-bold text-slate-800 mb-2">Depo Stok Giriş / Çıkış Hareketi</h3>
        <form @submit.prevent="submitStockMovement" class="space-y-2 text-xs">
          <input v-model.number="stockForm.part_id" type="number" placeholder="Parça ID" required class="w-full px-2.5 py-1.5 border rounded" />
          <input v-model.number="stockForm.warehouse_id" type="number" placeholder="Depo ID (1..3)" required class="w-full px-2.5 py-1.5 border rounded" />
          <input v-model.number="stockForm.quantity_change" type="number" placeholder="Miktar (+ Giriş / - Çıkış)" required class="w-full px-2.5 py-1.5 border rounded" />
          <button type="submit" class="w-full py-2 bg-slate-900 text-white font-semibold rounded">Hareket Ekle</button>
        </form>
      </div>
    </div>
  </div>

  <div v-else-if="section === 'audit'" class="bg-white rounded-xl border border-slate-200 shadow-sm p-4">
    <h2 class="text-base font-bold text-slate-800 mb-1">Denetim Kaydı (Append-Only Audit Logs)</h2>
    <p class="text-xs text-slate-500 mb-3">Durum değiştiren tüm işlemlerde kim, ne zaman, hangi kaydı, eski ve yeni JSONB değerleriyle değiştirdi kaydedilir.</p>
    <div v-if="loading" class="flex items-center gap-2 p-6 text-xs text-slate-500">
      <span class="animate-spin inline-block w-4 h-4 border-2 border-emerald-600 border-t-transparent rounded-full"></span>
      Denetim kayıtları yükleniyor...
    </div>
    <div v-else-if="loadError" class="p-4 text-xs text-red-800 bg-red-50 border border-red-200 rounded-lg">
      <p class="font-bold mb-1">Liste alınamadı</p>
      <p class="font-mono">{{ loadError }}</p>
      <button @click="loadData()" class="mt-2 px-3 py-1.5 rounded bg-red-600 text-white font-semibold">Tekrar dene</button>
    </div>
    <div v-else-if="auditLogs.length === 0" class="p-6 text-center text-slate-400 text-xs">Henüz denetim kaydı yok.</div>
    <div v-else class="overflow-x-auto">
      <table class="w-full text-xs text-left">
        <thead class="bg-slate-50 text-slate-600 uppercase border-b">
          <tr>
            <th class="p-2 w-16">ID</th>
            <th class="p-2 w-24">Kullanıcı</th>
            <th class="p-2">İşlem</th>
            <th class="p-2">Varlık</th>
            <th class="p-2">Zaman</th>
            <th class="p-2">Değişiklik</th>
          </tr>
        </thead>
        <tbody class="divide-y">
          <tr v-for="log in auditLogs" :key="log.id" class="align-top hover:bg-slate-50">
            <td class="p-2 font-mono font-bold text-slate-500">#{{ log.id }}</td>
            <td class="p-2 font-mono text-slate-600">User #{{ log.user_id || '-' }}</td>
            <td class="p-2">
              <!-- Ham eylem adı okunur biçime çevrilir; teknik ayrıntı tooltip'te -->
              <span
                class="font-semibold text-indigo-700 font-mono text-[11px]"
                :title="log.action"
              >{{ auditAction(log.action) }}</span>
            </td>
            <td class="p-2 font-mono text-[11px] text-slate-600">
              {{ log.entity_type }}<span v-if="log.entity_id"> #{{ log.entity_id }}</span>
            </td>
            <td class="p-2 text-[11px] text-slate-500 whitespace-nowrap">
              {{ formatDate(log.created_at) }}
            </td>
            <td class="p-2">
              <!--
                JSONB'yi ham metin yerine anahtar/değer çiftleri olarak göster:
                ekranda okunabilir, alan adları Türkçe etiketli.
                Eski değer de varsa (güncellemeler) iki sütun olarak yan yana.
              -->
              <div v-if="!log.old_data && !log.new_data" class="text-slate-300">—</div>
              <div v-else class="flex flex-wrap gap-x-6 gap-y-2">
                <div v-if="log.old_data" class="min-w-[180px]">
                  <div class="text-[10px] uppercase tracking-wide text-slate-400 mb-1">Önce</div>
                  <dl class="space-y-0.5">
                    <div v-for="row in auditRows(log.old_data)" :key="'o-' + row.key" class="flex gap-1.5 leading-tight">
                      <dt class="text-[10px] text-slate-400 shrink-0">{{ row.label }}:</dt>
                      <dd class="text-[11px] text-slate-500 line-through" :class="row.empty ? 'opacity-40' : ''">{{ row.value }}</dd>
                    </div>
                  </dl>
                </div>
                <div v-if="log.new_data" class="min-w-[180px]">
                  <div v-if="log.old_data" class="text-[10px] uppercase tracking-wide text-slate-400 mb-1">Sonra</div>
                  <dl class="space-y-0.5">
                    <div v-for="row in auditRows(log.new_data)" :key="'n-' + row.key" class="flex gap-1.5 leading-tight">
                      <dt class="text-[10px] text-slate-400 shrink-0">{{ row.label }}:</dt>
                      <dd class="text-[11px] text-slate-700 font-medium break-all" :class="row.empty ? 'opacity-40' : ''">{{ row.value }}</dd>
                    </div>
                  </dl>
                </div>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="auditHasMore" class="pt-3 text-center">
      <button
        @click="loadData(true)"
        :disabled="loadingMore"
        class="text-xs px-3 py-1.5 rounded-lg border border-slate-300 font-semibold text-slate-700 hover:bg-slate-50 disabled:opacity-50"
      >
        {{ loadingMore ? 'Yükleniyor...' : 'Daha fazla kayıt' }}
      </button>
    </div>
    <p v-if="!loading && !loadError" class="pt-2 text-[10px] text-slate-400 text-center">
      {{ auditLogs.length }} kayıt gösteriliyor
      <span v-if="auditHasMore"> · devamı var (cursor: {{ auditCursor }})</span>
      <span v-else> · liste sonu</span>
    </p>
  </div>
</template>