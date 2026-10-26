<script setup>
import { ref, watch, onMounted } from 'vue'
import { store, apiFetch, formatTL } from '../store'

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
            <th class="p-2">ID</th>
            <th class="p-2">Kullanıcı</th>
            <th class="p-2">İşlem</th>
            <th class="p-2">Varlık</th>
            <th class="p-2">Yeni Değer (JSONB)</th>
          </tr>
        </thead>
        <tbody class="divide-y">
          <tr v-for="log in auditLogs" :key="log.id">
            <td class="p-2 font-mono font-bold">#{{ log.id }}</td>
            <td class="p-2 font-mono">User #{{ log.user_id || '-' }}</td>
            <td class="p-2 font-bold text-indigo-700">{{ log.action }}</td>
            <td class="p-2 font-mono">{{ log.entity_type }} #{{ log.entity_id }}</td>
            <td class="p-2 font-mono text-[11px] text-slate-600 truncate max-w-md">{{ JSON.stringify(log.new_data) }}</td>
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