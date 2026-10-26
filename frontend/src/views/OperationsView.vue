<script setup>
import { ref, computed, onMounted } from 'vue'
import { apiFetch, store, hasRole, flattenEquipments, formatTL, formatNumber, formatDate } from '../store'

const props = defineProps({
  section: { type: String, required: true }
})

const loading = ref(false)
const error = ref('')
const actionError = ref('')
const actionSuccess = ref('')
const submitting = ref(false)

const equipments = ref([])
const items = ref([])
const nextCursor = ref(null)
const pageSize = 25

const equipmentOptions = computed(() => flattenEquipments(equipments.value))

const sectionMeta = {
  workorders: {
    title: 'İş Emirleri',
    code: 'F-3',
    description: 'Keyset (cursor) sayfalama ile listelenir; durum geçişleri sunucuda doğrulanır.'
  },
  failures: {
    title: 'Arıza Bildirimleri',
    code: 'F-2',
    description: 'Sahadan gelen arıza bildirimleri; iş emrine dönüştürülerek izlenir.'
  },
  pm: {
    title: 'Önleyici Bakım Planları',
    code: 'F-4 / T-1',
    description: 'Süre ve sayaç bazlı planlar. calc_basis planned/completed davranışını belirler.'
  },
  workload: {
    title: 'Teknisyen İş Yükü',
    code: 'F-6',
    description: 'Aktif ve tamamlanmış iş emri dağılımı.'
  }
}

const meta = computed(() => sectionMeta[props.section] || {
  title: props.section,
  code: '—',
  description: ''
})

const canCreateWorkOrder = computed(() => hasRole('planlama', 'yonetici'))
const canCompleteWorkOrder = computed(() => hasRole('teknisyen', 'planlama', 'yonetici'))
const canCreateFailure = computed(() => hasRole('uretim', 'teknisyen', 'planlama', 'yonetici'))
const canCreatePM = computed(() => hasRole('planlama', 'yonetici'))

// ── F-3: Durum geçişi yetkileri ────────────────────────────────────────────
// Sunucu tarafı da aynı rolleri kontrol eder (403). Buradaki kontrol yalnızca
// butonu göstermek/gizlemek için; yetkilendirme backend'de.
const canTransitionWorkOrder = computed(() => hasRole('teknisyen', 'planlama', 'yonetici'))
// F-6: Atama yalnızca planlama ve yöneticiye açık.
const canAssignWorkOrder = computed(() => hasRole('planlama', 'yonetici'))

// Teknisyen listesi: atama modalındaki seçim kutusu için.
// /technicians/workload zaten teknisyenleri döndürüyor, ayrı uç eklemedik.
const technicians = ref([])

// ── Atama modalı ───────────────────────────────────────────────────────────
const assignTarget = ref(null)     // iş emri satırı
const assignTechID = ref('')
const assignNote = ref('')
const assignRunning = ref(false)

// ── Durum geçmişi paneli ───────────────────────────────────────────────────
const eventsTarget = ref(null)     // iş emri satırı
const events = ref([])
const eventsLoading = ref(false)
const eventsError = ref('')

// Aynı iş emrinin iki kez açılmasını engellemek için işlem sürerken satırı kilitle.
const rowBusy = ref(null)

// --- Formlar ---
const workOrderForm = ref({
  equipment_id: '',
  wo_type: 'corrective',
  priority: 'normal',
  title: '',
  labor_cost_kurus: 0
})
const failureForm = ref({ equipment_id: '', title: '', description: '', severity: 'orta' })
const pmForm = ref({
  equipment_id: '',
  name: '',
  plan_type: 'time',
  interval_days: 30,
  interval_hours: '',
  calc_basis: 'completed',
  next_due_at: '',
  next_due_meter: ''
})

const statusBadge = {
  acik: 'bg-slate-100 text-slate-700 border-slate-200',
  atandi: 'bg-blue-100 text-blue-700 border-blue-200',
  devam_ediyor: 'bg-amber-100 text-amber-800 border-amber-200',
  tamamlandi: 'bg-emerald-100 text-emerald-700 border-emerald-200',
  iptal: 'bg-rose-100 text-rose-700 border-rose-200',
  is_emrine_donustu: 'bg-indigo-100 text-indigo-700 border-indigo-200',
  cozuldu: 'bg-emerald-100 text-emerald-700 border-emerald-200',
  reddedildi: 'bg-rose-100 text-rose-700 border-rose-200'
}

const priorityBadge = {
  dusuk: 'bg-slate-100 text-slate-600',
  normal: 'bg-blue-50 text-blue-700',
  yuksek: 'bg-amber-100 text-amber-800',
  acil: 'bg-rose-100 text-rose-700'
}

const severityBadge = {
  dusuk: 'bg-slate-100 text-slate-600',
  orta: 'bg-blue-50 text-blue-700',
  yuksek: 'bg-amber-100 text-amber-800',
  kritik: 'bg-rose-100 text-rose-700'
}

function resetMessages() {
  actionError.value = ''
  actionSuccess.value = ''
}

async function loadEquipments() {
  try {
    const res = await apiFetch('/equipments/tree')
    equipments.value = res.tree || []
  } catch (_) {
    equipments.value = []
  }
}

async function loadList(append = false) {
  loading.value = true
  error.value = ''
  try {
    if (props.section === 'workorders') {
      const params = new URLSearchParams({ limit: String(pageSize) })
      if (append && nextCursor.value) params.set('cursor', String(nextCursor.value))
      const res = await apiFetch(`/work-orders?${params.toString()}`)
      const page = res.items || []
      items.value = append ? [...items.value, ...page] : page
      nextCursor.value = page.length === pageSize ? res.next_cursor : null
      return
    }

    if (props.section === 'failures') {
      const res = await apiFetch('/failure-reports')
      items.value = res.items || []
      return
    }

    if (props.section === 'pm') {
      const res = await apiFetch('/preventive-plans')
      items.value = res.items || []
      return
    }

    if (props.section === 'workload') {
      const res = await apiFetch('/technicians/workload')
      items.value = res.items || []
      return
    }

    items.value = []
  } catch (err) {
    error.value = err.message
    if (!append) items.value = []
  } finally {
    loading.value = false
  }
}

async function refresh() {
  await loadEquipments()
  // Atama modalı için teknisyen listesi (yalnızca iş emri ekranında gerekli).
  if (props.section === 'workorders' && canAssignWorkOrder.value) {
    await loadTechnicians()
  }
  nextCursor.value = null
  await loadList(false)
}

async function submitWorkOrder() {
  resetMessages()
  submitting.value = true
  try {
    const res = await apiFetch('/work-orders', {
      method: 'POST',
      body: JSON.stringify({
        equipment_id: Number(workOrderForm.value.equipment_id),
        wo_type: workOrderForm.value.wo_type,
        priority: workOrderForm.value.priority,
        title: workOrderForm.value.title,
        labor_cost_kurus: Number(workOrderForm.value.labor_cost_kurus) || 0
      })
    })
    actionSuccess.value = `İş emri açıldı: ${res.code ?? '#' + res.id} (${res.status})`
    workOrderForm.value = { equipment_id: '', wo_type: 'corrective', priority: 'normal', title: '', labor_cost_kurus: 0 }
    nextCursor.value = null
    await loadList(false)
  } catch (err) {
    actionError.value = err.message
  } finally {
    submitting.value = false
  }
}

async function completeWorkOrder(row) {
  resetMessages()
  try {
    const res = await apiFetch(`/work-orders/${row.id}/complete`, {
      method: 'POST',
      body: JSON.stringify({ labor_cost_kurus: row.labor_cost_kurus })
    })
    actionSuccess.value = `İş emri #${row.id} tamamlandı.` +
      (res.next_pm_scheduled_at ? ` Sonraki PM: ${formatDate(res.next_pm_scheduled_at)}` : '')
    await loadList(false)
  } catch (err) {
    actionError.value = err.message
  }
}

// ── F-6: Teknisyen atama ───────────────────────────────────────────────────
// /technicians/workload yanıtı { technician_id, full_name, ... } döner.
async function loadTechnicians() {
  try {
    const res = await apiFetch('/technicians/workload')
    technicians.value = res.items || []
  } catch (_) {
    technicians.value = []
  }
}

function openAssign(row) {
  resetMessages()
  assignTarget.value = row
  // Mevcut atama varsa onu önseçili getir (yeniden atama senaryosu).
  assignTechID.value = row.assigned_to ? String(row.assigned_to) : ''
  assignNote.value = ''
}

function closeAssign() {
  assignTarget.value = null
  assignTechID.value = ''
  assignNote.value = ''
}

async function submitAssign() {
  if (!assignTarget.value || !assignTechID.value) return
  resetMessages()
  assignRunning.value = true
  try {
    const res = await apiFetch(`/work-orders/${assignTarget.value.id}/assign`, {
      method: 'POST',
      body: JSON.stringify({
        technician_id: Number(assignTechID.value),
        note: assignNote.value || undefined
      })
    })
    actionSuccess.value = `İş emri #${res.work_order_id} → teknisyen #${res.assigned_to} atandı (durum: ${res.status}).`
    closeAssign()
    await loadList(false)
  } catch (err) {
    actionError.value = err.message
  } finally {
    assignRunning.value = false
  }
}

async function unassignWorkOrder(row) {
  resetMessages()
  rowBusy.value = row.id
  try {
    const res = await apiFetch(`/work-orders/${row.id}/assign`, { method: 'DELETE' })
    actionSuccess.value = `İş emri #${res.work_order_id} ataması kaldırıldı (durum: ${res.status}).`
    await loadList(false)
  } catch (err) {
    actionError.value = err.message
  } finally {
    rowBusy.value = null
  }
}

// ── F-3: Durum geçişi ──────────────────────────────────────────────────────
// Geçiş matrisi sunucuda; burada yalnızca geçerli hedefler için buton gösterilir.
const nextStatuses = {
  acik: ['atandi', 'iptal'],
  atandi: ['devam_ediyor', 'acik', 'iptal'],
  devam_ediyor: ['tamamlandi', 'iptal'],
  tamamlandi: [],
  iptal: []
}

// Satırdaki duruma göre gösterilecek butonlar.
// 'tamamlandi' burada YOK: yan etkileri (stok tüketimi + PM yeniden planlama)
// nedeniyle ayrı uçtan (/complete) yapılır.
// 'atandi' hedefi teknik seçimi gerektirdiği için yalnızca atama yetkisi
// olanlara gösterilir; aksi hâlde teknisyen "Ata"ya basıp 403 alırdı.
function actionsFor(row) {
  const allowed = nextStatuses[row.status] || []
  return allowed.filter((s) => {
    if (s === 'tamamlandi') return false
    if (s === 'atandi' && !canAssignWorkOrder.value) return false
    return true
  })
}

const statusActionLabel = {
  atandi: 'Ata',
  devam_ediyor: 'Başlat',
  acik: 'Havuza Dön',
  iptal: 'İptal'
}

const statusActionClass = {
  atandi: 'bg-blue-600 hover:bg-blue-700 text-white',
  devam_ediyor: 'bg-amber-600 hover:bg-amber-700 text-white',
  acik: 'bg-slate-200 hover:bg-slate-300 text-slate-700',
  iptal: 'bg-rose-100 hover:bg-rose-200 text-rose-700'
}

async function changeStatus(row, target) {
  resetMessages()
  if (target === 'iptal' && !window.confirm(`İş emri ${row.code} iptal edilsin mi? Bu işlem geri alınamaz.`)) {
    return
  }
  rowBusy.value = row.id
  try {
    const res = await apiFetch(`/work-orders/${row.id}/status`, {
      method: 'PATCH',
      body: JSON.stringify({ status: target })
    })
    actionSuccess.value = `#${res.work_order_id}: ${res.from_status} → ${res.to_status}` +
      (res.allowed_next?.length ? ` (sonraki: ${res.allowed_next.join(', ')})` : '')
    await loadList(false)
  } catch (err) {
    actionError.value = err.message
  } finally {
    rowBusy.value = null
  }
}

// 'Ata' butonu hem durum geçişi (acik→atandi) hem de teknisyen seçimi gerektirir,
// bu yüzden modal açar. Diğer geçişler tek tıkla yapılır.
function onStatusAction(row, target) {
  if (target === 'atandi') return openAssign(row)
  return changeStatus(row, target)
}

// ── F-3: Durum geçmişi ─────────────────────────────────────────────────────
async function openEvents(row) {
  resetMessages()
  eventsTarget.value = row
  events.value = []
  eventsError.value = ''
  eventsLoading.value = true
  try {
    const res = await apiFetch(`/work-orders/${row.id}/events`)
    events.value = res.items || []
  } catch (err) {
    eventsError.value = err.message
  } finally {
    eventsLoading.value = false
  }
}

function closeEvents() {
  eventsTarget.value = null
  events.value = []
  eventsError.value = ''
}

async function submitFailure() {
  resetMessages()
  submitting.value = true
  try {
    const res = await apiFetch('/failure-reports', {
      method: 'POST',
      body: JSON.stringify({
        equipment_id: Number(failureForm.value.equipment_id),
        title: failureForm.value.title,
        description: failureForm.value.description,
        severity: failureForm.value.severity
      })
    })
    actionSuccess.value = `Arıza bildirimi #${res.id} oluşturuldu (${res.status}).`
    failureForm.value = { equipment_id: '', title: '', description: '', severity: 'orta' }
    await loadList(false)
  } catch (err) {
    actionError.value = err.message
  } finally {
    submitting.value = false
  }
}

async function submitPM() {
  resetMessages()
  submitting.value = true
  try {
    const payload = {
      equipment_id: Number(pmForm.value.equipment_id),
      name: pmForm.value.name,
      plan_type: pmForm.value.plan_type,
      calc_basis: pmForm.value.calc_basis
    }
    if (pmForm.value.interval_days !== '' && pmForm.value.interval_days !== null) {
      payload.interval_days = Number(pmForm.value.interval_days)
    }
    if (pmForm.value.interval_hours !== '' && pmForm.value.interval_hours !== null) {
      payload.interval_hours = Number(pmForm.value.interval_hours)
    }
    if (pmForm.value.next_due_at) {
      payload.next_due_at = new Date(pmForm.value.next_due_at).toISOString()
    }
    if (pmForm.value.next_due_meter !== '' && pmForm.value.next_due_meter !== null) {
      payload.next_due_meter = Number(pmForm.value.next_due_meter)
    }

    const res = await apiFetch('/preventive-plans', { method: 'POST', body: JSON.stringify(payload) })
    actionSuccess.value = `PM planı #${res.id} oluşturuldu (calc_basis: ${res.calc_basis}).`
    pmForm.value = {
      equipment_id: '',
      name: '',
      plan_type: 'time',
      interval_days: 30,
      interval_hours: '',
      calc_basis: 'completed',
      next_due_at: '',
      next_due_meter: ''
    }
    await loadList(false)
  } catch (err) {
    actionError.value = err.message
  } finally {
    submitting.value = false
  }
}

async function triggerDue() {
  resetMessages()
  try {
    const res = await apiFetch('/preventive-plans/trigger-due', { method: 'POST', body: JSON.stringify({}) })
    const list = res.generated_work_orders || []
    actionSuccess.value = list.length > 0
      ? `${list.length} iş emri üretildi: #${list.join(', #')}`
      : 'Vadesi gelmiş plan yok veya açık iş emri bulunduğu için yeni kayıt birikmedi.'
    await loadList(false)
  } catch (err) {
    actionError.value = err.message
  }
}

onMounted(refresh)
</script>

<template>
  <div class="space-y-4">
    <header class="bg-white rounded-xl border border-slate-200 shadow-sm p-4">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div class="flex items-center gap-2">
            <h1 class="text-lg font-bold text-slate-800">{{ meta.title }}</h1>
            <span class="text-[10px] px-1.5 py-0.5 rounded font-mono bg-emerald-100 text-emerald-800 font-bold">{{ meta.code }}</span>
          </div>
          <p class="text-xs text-slate-500 mt-1">{{ meta.description }}</p>
        </div>
        <button @click="refresh" class="text-xs px-3 py-1.5 rounded-lg bg-slate-900 text-white font-semibold hover:bg-slate-800">
          Yenile
        </button>
      </div>
    </header>

    <div v-if="actionSuccess" class="p-3 rounded-xl bg-emerald-50 border border-emerald-300 text-emerald-800 text-xs">
      {{ actionSuccess }}
    </div>
    <div v-if="actionError" class="p-3 rounded-xl bg-red-50 border border-red-300 text-red-800 text-xs font-mono">
      {{ actionError }}
    </div>

    <div class="grid grid-cols-1 xl:grid-cols-3 gap-4">
      <section class="xl:col-span-2 bg-white rounded-xl border border-slate-200 shadow-sm p-4">
        <div v-if="loading" class="flex items-center gap-2 py-6 text-xs text-slate-500">
          <span class="animate-spin inline-block w-4 h-4 border-2 border-emerald-600 border-t-transparent rounded-full"></span>
          Kayıtlar yükleniyor...
        </div>

        <div v-else-if="error" class="p-4 text-xs text-red-800 bg-red-50 border border-red-200 rounded-lg">
          <p class="font-bold mb-1">Liste alınamadı</p>
          <p class="font-mono">{{ error }}</p>
          <button @click="loadList(false)" class="mt-2 px-3 py-1.5 rounded bg-red-600 text-white font-semibold">Tekrar dene</button>
        </div>

        <div v-else-if="items.length === 0" class="py-10 text-center text-xs text-slate-400 border border-dashed border-slate-200 rounded-lg">
          Kayıt bulunmuyor. Sağdaki formdan yeni kayıt oluşturabilirsiniz.
        </div>

        <!-- İş emirleri -->
        <div v-else-if="section === 'workorders'" class="space-y-2">
          <div class="overflow-x-auto">
            <table class="w-full text-xs">
              <thead class="text-[11px] uppercase text-slate-400 border-b border-slate-200">
                <tr>
                  <th class="text-left py-2 pr-2 font-semibold">Kod</th>
                  <th class="text-left py-2 pr-2 font-semibold">Ekipman</th>
                  <th class="text-left py-2 pr-2 font-semibold">Tip</th>
                  <th class="text-left py-2 pr-2 font-semibold">Durum</th>
                  <th class="text-right py-2 pr-2 font-semibold">Maliyet</th>
                  <th class="text-right py-2 font-semibold">İşlem</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in items" :key="row.id" class="border-b border-slate-100 last:border-0">
                  <td class="py-2 pr-2">
                    <div class="font-mono text-[11px] text-slate-600">{{ row.code }}</div>
                    <div class="text-[10px] text-slate-400">{{ formatDate(row.created_at) }}</div>
                  </td>
                  <td class="py-2 pr-2">
                    <div class="text-slate-800">{{ row.title }}</div>
                    <div class="text-[10px] text-slate-400">{{ row.equipment_name }}</div>
                  </td>
                  <td class="py-2 pr-2">
                    <span class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 text-slate-600 font-mono">{{ row.wo_type }}</span>
                    <span :class="['ml-1 text-[10px] px-1.5 py-0.5 rounded font-semibold', priorityBadge[row.priority] || 'bg-slate-100 text-slate-600']">{{ row.priority }}</span>
                  </td>
                  <td class="py-2 pr-2">
                    <span :class="['text-[10px] px-1.5 py-0.5 rounded border font-semibold', statusBadge[row.status] || 'bg-slate-100 text-slate-600 border-slate-200']">{{ row.status }}</span>
                  </td>
                  <td class="py-2 pr-2 text-right font-mono text-slate-700">
                    {{ formatTL((row.labor_cost_kurus || 0) + (row.parts_cost_kurus || 0)) }}
                  </td>
                  <td class="py-2">
                    <div class="flex flex-wrap items-center justify-end gap-1">
                      <!--
                        Aksiyonlar duruma göre üretilir (actionsFor).
                        'tamamlandi' listede yok: yan etkileri nedeniyle /complete ucu kullanılır.
                      -->
                      <button
                        v-for="target in canTransitionWorkOrder ? actionsFor(row) : []"
                        :key="target"
                        :disabled="rowBusy === row.id"
                        @click="onStatusAction(row, target)"
                        :class="['text-[10px] px-2 py-1 rounded font-semibold disabled:opacity-50', statusActionClass[target]]"
                      >
                        {{ statusActionLabel[target] || target }}
                      </button>

                      <button
                        v-if="canCompleteWorkOrder && row.status !== 'tamamlandi' && row.status !== 'iptal'"
                        :disabled="rowBusy === row.id"
                        @click="completeWorkOrder(row)"
                        class="text-[10px] px-2 py-1 rounded bg-emerald-600 text-white font-semibold hover:bg-emerald-700 disabled:opacity-50"
                      >
                        Tamamla
                      </button>

                      <button
                        v-if="canAssignWorkOrder && row.assigned_to && (row.status === 'acik' || row.status === 'atandi')"
                        :disabled="rowBusy === row.id"
                        @click="unassignWorkOrder(row)"
                        class="text-[10px] px-2 py-1 rounded bg-slate-100 text-slate-600 font-semibold hover:bg-slate-200 disabled:opacity-50"
                      >
                        Atamayı Kaldır
                      </button>

                      <button
                        @click="openEvents(row)"
                        class="text-[10px] px-2 py-1 rounded text-indigo-600 font-semibold hover:bg-indigo-50"
                      >
                        Geçmiş
                      </button>
                    </div>
                    <div v-if="row.assigned_to" class="text-[10px] text-slate-400 text-right mt-0.5">
                      teknisyen #{{ row.assigned_to }}
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-if="nextCursor" class="pt-2 text-center">
            <button @click="loadList(true)" class="text-xs px-3 py-1.5 rounded-lg border border-slate-300 font-semibold text-slate-700 hover:bg-slate-50">
              Daha fazla yükle
            </button>
          </div>
          <p class="text-[10px] text-slate-400 text-center">
            {{ items.length }} kayıt gösteriliyor · keyset sayfalama (cursor: {{ nextCursor ?? 'son sayfa' }})
          </p>
        </div>

        <!-- Arıza bildirimleri -->
        <div v-else-if="section === 'failures'" class="space-y-2">
          <div v-for="row in items" :key="row.id" class="border border-slate-200 rounded-lg p-3">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-mono text-[11px] text-slate-500">#{{ row.id }}</span>
              <span class="text-xs font-semibold text-slate-800">{{ row.title }}</span>
              <span :class="['text-[10px] px-1.5 py-0.5 rounded font-semibold', severityBadge[row.severity] || 'bg-slate-100 text-slate-600']">{{ row.severity }}</span>
              <span :class="['text-[10px] px-1.5 py-0.5 rounded border font-semibold', statusBadge[row.status] || 'bg-slate-100 text-slate-600 border-slate-200']">{{ row.status }}</span>
              <span class="ml-auto text-[10px] text-slate-400">{{ formatDate(row.created_at) }}</span>
            </div>
            <div class="text-[11px] text-slate-500 mt-1">{{ row.equipment_name }}</div>
          </div>
        </div>

        <!-- PM planları -->
        <div v-else-if="section === 'pm'" class="space-y-2">
          <div class="overflow-x-auto">
            <table class="w-full text-xs">
              <thead class="text-[11px] uppercase text-slate-400 border-b border-slate-200">
                <tr>
                  <th class="text-left py-2 pr-2 font-semibold">Plan</th>
                  <th class="text-left py-2 pr-2 font-semibold">Ekipman</th>
                  <th class="text-left py-2 pr-2 font-semibold">Periyot</th>
                  <th class="text-left py-2 pr-2 font-semibold">calc_basis</th>
                  <th class="text-left py-2 font-semibold">Sonraki</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in items" :key="row.id" class="border-b border-slate-100 last:border-0">
                  <td class="py-2 pr-2">
                    <div class="text-slate-800 font-semibold">{{ row.name }}</div>
                    <div class="text-[10px] text-slate-400 font-mono">#{{ row.id }} · {{ row.plan_type }}</div>
                  </td>
                  <td class="py-2 pr-2 text-slate-600">{{ row.equipment_name }}</td>
                  <td class="py-2 pr-2 font-mono text-[11px] text-slate-600">
                    <span v-if="row.interval_days">{{ row.interval_days }} gün</span>
                    <span v-if="row.interval_hours"> · {{ row.interval_hours }} sa</span>
                  </td>
                  <td class="py-2 pr-2">
                    <span class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 text-slate-600 font-mono">{{ row.calc_basis }}</span>
                  </td>
                  <td class="py-2 text-[11px] text-slate-600">
                    <div v-if="row.next_due_at">{{ formatDate(row.next_due_at) }}</div>
                    <div v-if="row.next_due_meter" class="font-mono text-slate-400">{{ formatNumber(row.next_due_meter, 0) }} sayaç</div>
                    <div v-if="!row.next_due_at && !row.next_due_meter" class="text-slate-300">—</div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-if="canCreatePM" class="pt-2 text-right">
            <button @click="triggerDue" class="text-xs px-3 py-1.5 rounded-lg bg-indigo-600 text-white font-semibold hover:bg-indigo-700">
              Vadesi Gelenleri Üret
            </button>
          </div>
        </div>

        <!-- Teknisyen iş yükü -->
        <div v-else-if="section === 'workload'" class="space-y-2">
          <div v-for="row in items" :key="row.technician_id" class="flex items-center gap-3 border border-slate-200 rounded-lg p-3">
            <div class="w-9 h-9 rounded-full bg-slate-900 text-white flex items-center justify-center text-xs font-bold shrink-0">
              {{ (row.full_name || '?').charAt(0) }}
            </div>
            <div class="min-w-0">
              <div class="text-xs font-semibold text-slate-800 truncate">{{ row.full_name }}</div>
              <div class="text-[10px] text-slate-400 truncate">{{ row.email }}</div>
            </div>
            <div class="ml-auto flex items-center gap-4 shrink-0 text-center">
              <div>
                <div class="text-lg font-extrabold text-amber-600">{{ row.active_tasks }}</div>
                <div class="text-[10px] text-slate-400">aktif</div>
              </div>
              <div>
                <div class="text-lg font-extrabold text-emerald-600">{{ row.completed_tasks }}</div>
                <div class="text-[10px] text-slate-400">tamamlanan</div>
              </div>
            </div>
          </div>
        </div>
      </section>

      <aside class="space-y-4">
        <!-- Yeni iş emri -->
        <div v-if="section === 'workorders' && canCreateWorkOrder" class="bg-white rounded-xl border border-slate-200 shadow-sm p-4">
          <h2 class="text-xs font-bold text-slate-700 mb-3">Yeni İş Emri</h2>
          <form @submit.prevent="submitWorkOrder" class="space-y-2">
            <select v-model="workOrderForm.equipment_id" required class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5 bg-white">
              <option value="" disabled>Ekipman seçin</option>
              <option v-for="eq in equipmentOptions" :key="eq.id" :value="eq.id">{{ eq.code }} · {{ eq.name }}</option>
            </select>
            <input v-model="workOrderForm.title" required placeholder="Başlık" class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5" />
            <div class="grid grid-cols-2 gap-2">
              <select v-model="workOrderForm.wo_type" class="text-xs border border-slate-300 rounded-lg px-2.5 py-1.5 bg-white">
                <option value="corrective">corrective</option>
                <option value="preventive">preventive</option>
              </select>
              <select v-model="workOrderForm.priority" class="text-xs border border-slate-300 rounded-lg px-2.5 py-1.5 bg-white">
                <option value="dusuk">dusuk</option>
                <option value="normal">normal</option>
                <option value="yuksek">yuksek</option>
                <option value="acil">acil</option>
              </select>
            </div>
            <input v-model.number="workOrderForm.labor_cost_kurus" type="number" min="0" placeholder="İşçilik (kuruş)" class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5" />
            <button type="submit" :disabled="submitting" class="w-full text-xs px-3 py-2 rounded-lg bg-emerald-600 text-white font-semibold hover:bg-emerald-700 disabled:opacity-50">
              {{ submitting ? 'Kaydediliyor...' : 'İş Emri Aç' }}
            </button>
          </form>
        </div>

        <!-- Yeni arıza -->
        <div v-if="section === 'failures' && canCreateFailure" class="bg-white rounded-xl border border-slate-200 shadow-sm p-4">
          <h2 class="text-xs font-bold text-slate-700 mb-3">Arıza Bildir</h2>
          <form @submit.prevent="submitFailure" class="space-y-2">
            <select v-model="failureForm.equipment_id" required class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5 bg-white">
              <option value="" disabled>Ekipman seçin</option>
              <option v-for="eq in equipmentOptions" :key="eq.id" :value="eq.id">{{ eq.code }} · {{ eq.name }}</option>
            </select>
            <input v-model="failureForm.title" required placeholder="Arıza başlığı" class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5" />
            <textarea v-model="failureForm.description" rows="3" placeholder="Açıklama" class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5"></textarea>
            <select v-model="failureForm.severity" class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5 bg-white">
              <option value="dusuk">dusuk</option>
              <option value="orta">orta</option>
              <option value="yuksek">yuksek</option>
              <option value="kritik">kritik</option>
            </select>
            <button type="submit" :disabled="submitting" class="w-full text-xs px-3 py-2 rounded-lg bg-rose-600 text-white font-semibold hover:bg-rose-700 disabled:opacity-50">
              {{ submitting ? 'Kaydediliyor...' : 'Bildirimi Gönder' }}
            </button>
          </form>
        </div>

        <!-- Yeni PM planı -->
        <div v-if="section === 'pm' && canCreatePM" class="bg-white rounded-xl border border-slate-200 shadow-sm p-4">
          <h2 class="text-xs font-bold text-slate-700 mb-3">Yeni PM Planı</h2>
          <form @submit.prevent="submitPM" class="space-y-2">
            <select v-model="pmForm.equipment_id" required class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5 bg-white">
              <option value="" disabled>Ekipman seçin</option>
              <option v-for="eq in equipmentOptions" :key="eq.id" :value="eq.id">{{ eq.code }} · {{ eq.name }}</option>
            </select>
            <input v-model="pmForm.name" required placeholder="Plan adı" class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5" />
            <div class="grid grid-cols-2 gap-2">
              <select v-model="pmForm.plan_type" class="text-xs border border-slate-300 rounded-lg px-2.5 py-1.5 bg-white">
                <option value="time">time</option>
                <option value="meter">meter</option>
                <option value="both">both</option>
              </select>
              <select v-model="pmForm.calc_basis" class="text-xs border border-slate-300 rounded-lg px-2.5 py-1.5 bg-white">
                <option value="completed">completed</option>
                <option value="planned">planned</option>
              </select>
            </div>
            <div class="grid grid-cols-2 gap-2">
              <input v-model.number="pmForm.interval_days" type="number" min="1" placeholder="Gün" class="text-xs border border-slate-300 rounded-lg px-2.5 py-1.5" />
              <input v-model.number="pmForm.interval_hours" type="number" min="1" placeholder="Sayaç saati" class="text-xs border border-slate-300 rounded-lg px-2.5 py-1.5" />
            </div>
            <input v-model="pmForm.next_due_at" type="datetime-local" class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5" />
            <input v-model.number="pmForm.next_due_meter" type="number" min="0" placeholder="Sonraki sayaç eşiği" class="w-full text-xs border border-slate-300 rounded-lg px-2.5 py-1.5" />
            <p class="text-[10px] text-slate-400">
              <strong>planned</strong>: sonraki tarih planlanan tarihten, <strong>completed</strong>: fiili tamamlanma tarihinden hesaplanır.
            </p>
            <button type="submit" :disabled="submitting" class="w-full text-xs px-3 py-2 rounded-lg bg-emerald-600 text-white font-semibold hover:bg-emerald-700 disabled:opacity-50">
              {{ submitting ? 'Kaydediliyor...' : 'Plan Oluştur' }}
            </button>
          </form>
        </div>

        <div v-if="section === 'workload'" class="bg-white rounded-xl border border-dashed border-slate-300 p-4 text-[11px] text-slate-400">
          İş yükü dağılımı atanmış iş emirlerinden hesaplanır. Atama işlemleri iş emri oluşturulurken yapılır.
        </div>

        <div
          v-if="(section === 'workorders' && !canCreateWorkOrder) || (section === 'failures' && !canCreateFailure) || (section === 'pm' && !canCreatePM)"
          class="bg-white rounded-xl border border-dashed border-slate-300 p-4 text-[11px] text-slate-400"
        >
          Rolünüz ({{ store.user?.role }}) bu ekranda kayıt oluşturamaz; yalnızca görüntüleme yapabilir.
        </div>
      </aside>
    </div>

    <!-- ── F-6: Teknisyen atama modalı ─────────────────────────────────── -->
    <!--
      Modal (dialog) seçildi çünkü atama, satır içinde tek tıkla yapılamayacak
      kadar çok girdi istiyor (teknisyen + not). Ayrı sayfa açmak ise
      bağlamı kaybettirirdi: kullanıcı hangi iş emrini atadığını unutur.
    -->
    <div
      v-if="assignTarget"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 p-4"
      @click.self="closeAssign"
    >
      <div class="bg-white rounded-xl shadow-xl border border-slate-200 w-full max-w-md p-5">
        <div class="flex items-start justify-between gap-3 mb-3">
          <div>
            <h2 class="text-sm font-bold text-slate-800">Teknisyen Ata</h2>
            <p class="text-[11px] text-slate-500 mt-0.5 font-mono">{{ assignTarget.code }}</p>
          </div>
          <button @click="closeAssign" class="text-slate-400 hover:text-slate-700 font-bold">✕</button>
        </div>

        <form @submit.prevent="submitAssign" class="space-y-3">
          <label class="block">
            <span class="text-[11px] font-semibold text-slate-500 uppercase tracking-wide">Teknisyen</span>
            <select
              v-model="assignTechID"
              required
              class="mt-1 w-full text-xs border border-slate-300 rounded-lg px-2.5 py-2 bg-white"
            >
              <option value="" disabled>Seçin</option>
              <option v-for="tech in technicians" :key="tech.technician_id" :value="tech.technician_id">
                {{ tech.full_name }} — aktif {{ tech.active_tasks }}
              </option>
            </select>
          </label>

          <label class="block">
            <span class="text-[11px] font-semibold text-slate-500 uppercase tracking-wide">Not (isteğe bağlı)</span>
            <input
              v-model="assignNote"
              type="text"
              placeholder="örn. vardiya teknisyeni"
              class="mt-1 w-full text-xs border border-slate-300 rounded-lg px-2.5 py-2"
            />
          </label>

          <p class="text-[10px] text-slate-400">
            Atama, iş emrini <span class="font-mono">acik</span> durumundan
            <span class="font-mono">atandi</span> durumuna geçirir ve geçmişe kayıt düşer.
            Yalnızca <span class="font-mono">teknisyen</span> rolündeki kullanıcılar atanabilir.
          </p>

          <div class="flex gap-2 pt-1">
            <button
              type="button"
              @click="closeAssign"
              class="flex-1 text-xs px-3 py-2 rounded-lg border border-slate-300 font-semibold text-slate-700 hover:bg-slate-50"
            >
              Vazgeç
            </button>
            <button
              type="submit"
              :disabled="assignRunning || !assignTechID"
              class="flex-1 text-xs px-3 py-2 rounded-lg bg-blue-600 text-white font-semibold hover:bg-blue-700 disabled:opacity-50"
            >
              {{ assignRunning ? 'Atanıyor...' : 'Ata' }}
            </button>
          </div>
        </form>
      </div>
    </div>

    <!-- ── F-3: Durum geçmişi modalı ───────────────────────────────────── -->
    <div
      v-if="eventsTarget"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 p-4"
      @click.self="closeEvents"
    >
      <div class="bg-white rounded-xl shadow-xl border border-slate-200 w-full max-w-lg p-5">
        <div class="flex items-start justify-between gap-3 mb-3">
          <div>
            <h2 class="text-sm font-bold text-slate-800">Durum Geçmişi</h2>
            <p class="text-[11px] text-slate-500 mt-0.5 font-mono">
              {{ eventsTarget.code }} · {{ eventsTarget.title }}
            </p>
          </div>
          <button @click="closeEvents" class="text-slate-400 hover:text-slate-700 font-bold">✕</button>
        </div>

        <div v-if="eventsLoading" class="flex items-center gap-2 py-6 text-xs text-slate-500">
          <span class="animate-spin inline-block w-4 h-4 border-2 border-emerald-600 border-t-transparent rounded-full"></span>
          Geçmiş yükleniyor...
        </div>

        <div v-else-if="eventsError" class="p-3 text-xs text-red-800 bg-red-50 border border-red-200 rounded-lg">
          {{ eventsError }}
        </div>

        <div v-else-if="events.length === 0" class="py-8 text-center text-xs text-slate-400 border border-dashed border-slate-200 rounded-lg">
          Bu iş emri için henüz geçmiş kaydı yok.
        </div>

        <ol v-else class="space-y-2 max-h-80 overflow-y-auto pr-1">
          <li
            v-for="ev in events"
            :key="ev.id"
            class="border border-slate-200 rounded-lg p-2.5"
          >
            <div class="flex flex-wrap items-center gap-1.5">
              <span v-if="ev.from_status" class="text-[10px] font-mono px-1.5 py-0.5 rounded bg-slate-100 text-slate-600">
                {{ ev.from_status }}
              </span>
              <span v-if="ev.from_status" class="text-[10px] text-slate-400">→</span>
              <span :class="['text-[10px] font-mono px-1.5 py-0.5 rounded border font-semibold', statusBadge[ev.to_status] || 'bg-slate-100 text-slate-600 border-slate-200']">
                {{ ev.to_status }}
              </span>
              <span class="ml-auto text-[10px] text-slate-400">{{ formatDate(ev.created_at) }}</span>
            </div>
            <div class="text-[11px] text-slate-600 mt-1">
              {{ ev.actor_name || 'sistem' }}
              <span v-if="ev.actor_role" class="text-slate-400">({{ ev.actor_role }})</span>
            </div>
            <div v-if="ev.note" class="text-[10px] text-slate-400 italic mt-0.5">{{ ev.note }}</div>
          </li>
        </ol>

        <div class="mt-4 text-right">
          <button
            @click="closeEvents"
            class="text-xs px-3 py-2 rounded-lg bg-slate-900 text-white font-semibold hover:bg-slate-800"
          >
            Kapat
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
