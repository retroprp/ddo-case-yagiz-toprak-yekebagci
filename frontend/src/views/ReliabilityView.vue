<script setup>
import { ref, onMounted } from 'vue'
import { store, apiFetch, formatTL } from '../store'

const metricsData = ref(null)
const metricEqFilter = ref('')
const metricDays = ref(30)
const downtimeForm = ref({
  equipment_id: 31,
  start_time: '2026-10-20T09:00:00Z',
  end_time: '2026-10-20T12:00:00Z',
  reason: 'Örtüşen Duruş Test Kaydı'
})

async function loadMetrics() {
  try {
    const params = new URLSearchParams({ days: String(metricDays.value || 30) })
    if (metricEqFilter.value) params.set('equipment_id', String(metricEqFilter.value))
    metricsData.value = await apiFetch(`/metrics/reliability?${params.toString()}`)
  } catch (_) {}
}

async function submitDowntime() {
  try {
    await apiFetch('/downtime-events', {
      method: 'POST',
      body: JSON.stringify(downtimeForm.value)
    })
    store.successBanner = 'Duruş olayı kaydedildi, MTBF/MTTR/Availability yeniden hesaplandı!'
    await loadMetrics()
  } catch (_) {}
}

onMounted(loadMetrics)
</script>

<template>
  <div v-if="metricsData" class="space-y-4">
    <div class="bg-white rounded-xl shadow-sm border border-slate-200 p-4 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-base font-bold text-slate-800">Güvenilirlik Göstergeleri (MTBF, MTTR, Kullanılabilirlik)</h2>
        <p class="text-xs text-slate-500">Seçilen zaman penceresinde ({{ metricsData.window_days || 30 }} gün) örtüşen duruşlar birleştirilir ve vardiya dışı saatler düşülür.</p>
      </div>
      <div class="flex items-center gap-2">
        <input v-model="metricEqFilter" type="number" placeholder="Ekipman ID (Örn: 31)" class="px-3 py-1.5 border rounded-lg text-xs w-40" />
        <input v-model.number="metricDays" type="number" min="1" max="1095" placeholder="Gün" class="px-3 py-1.5 border rounded-lg text-xs w-20" />
        <button @click="loadMetrics" class="px-3 py-1.5 bg-slate-900 text-white rounded-lg text-xs font-semibold">Hesapla</button>
      </div>
    </div>

    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
      <div class="bg-white p-4 rounded-xl border border-slate-200 shadow-sm">
        <div class="text-xs text-slate-400 font-medium">MTBF (Arızalar Arası Süre)</div>
        <div class="text-2xl font-extrabold text-slate-800 mt-1">{{ metricsData.mtbf_hours }} <span class="text-xs font-normal">saat</span></div>
        <div class="text-[11px] text-slate-500 mt-1">Net Çalışma: {{ metricsData.operating_hours }} s</div>
      </div>
      <div class="bg-white p-4 rounded-xl border border-slate-200 shadow-sm">
        <div class="text-xs text-slate-400 font-medium">MTTR (Ortalama Onarım Süresi)</div>
        <div class="text-2xl font-extrabold text-amber-600 mt-1">{{ metricsData.mttr_hours }} <span class="text-xs font-normal">saat</span></div>
        <div class="text-[11px] text-slate-500 mt-1">Net Duruş: {{ metricsData.downtime_hours }} s</div>
      </div>
      <div class="bg-white p-4 rounded-xl border border-slate-200 shadow-sm">
        <div class="text-xs text-slate-400 font-medium">Kullanılabilirlik (Availability)</div>
        <div class="text-2xl font-extrabold text-emerald-600 mt-1">%{{ metricsData.availability_percent }}</div>
        <div class="text-[11px] text-slate-500 mt-1">Vardiya: {{ metricsData.shift_hours_per_day }} saat/gün</div>
      </div>
      <div class="bg-white p-4 rounded-xl border border-slate-200 shadow-sm">
        <div class="text-xs text-slate-400 font-medium">Örtüşen Duruş Birleştirme</div>
        <div class="text-2xl font-extrabold text-indigo-600 mt-1">{{ metricsData.raw_events_count }} → {{ metricsData.merged_failures_count }}</div>
        <div class="text-[11px] text-slate-500 mt-1">Ham Olay → Birleşmiş Arıza</div>
      </div>
    </div>

    <div class="grid grid-cols-1 lg:grid-cols-3 gap-4">
      <div class="lg:col-span-2 bg-white rounded-xl border border-slate-200 shadow-sm p-4">
        <h3 class="text-sm font-bold text-slate-800 mb-1">En Problemli 10 Ekipman Raporu (Composite Index Optimize)</h3>
        <p class="text-xs text-slate-500 mb-3">Arıza iş emri sayısı ve toplam bakım maliyetine göre sıralanır.</p>
        <div v-if="!metricsData.top_problematic_10?.length" class="p-6 text-center text-slate-400 text-xs">Veri bulunamadı.</div>
        <div v-else class="overflow-x-auto">
          <table class="w-full text-xs text-left">
            <thead class="bg-slate-50 text-slate-600 uppercase border-b">
              <tr>
                <th class="p-2">Kod</th>
                <th class="p-2">Ekipman Adı</th>
                <th class="p-2">Seviye</th>
                <th class="p-2 text-right">Arıza Sayısı</th>
                <th class="p-2 text-right">Toplam Maliyet</th>
              </tr>
            </thead>
            <tbody class="divide-y">
              <tr v-for="item in metricsData.top_problematic_10" :key="item.equipment_id" class="hover:bg-slate-50">
                <td class="p-2 font-mono font-bold text-slate-700">{{ item.code }}</td>
                <td class="p-2">{{ item.name }}</td>
                <td class="p-2 uppercase text-[10px] font-bold text-slate-500">{{ item.level_type }}</td>
                <td class="p-2 text-right font-bold text-red-600">{{ item.failure_wo_count }}</td>
                <td class="p-2 text-right font-mono">{{ formatTL(item.total_cost_kurus) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="bg-white rounded-xl border border-slate-200 shadow-sm p-4">
        <h3 class="text-sm font-bold text-slate-800 mb-2">Yeni Duruş Olayı Kaydet (T-4)</h3>
        <form @submit.prevent="submitDowntime" class="space-y-2.5 text-xs">
          <div>
            <label class="block font-semibold text-slate-600 mb-1">Ekipman ID</label>
            <input v-model.number="downtimeForm.equipment_id" type="number" required class="w-full px-2.5 py-1.5 border rounded" />
          </div>
          <div>
            <label class="block font-semibold text-slate-600 mb-1">Başlangıç (ISO UTC)</label>
            <input v-model="downtimeForm.start_time" type="text" required class="w-full px-2.5 py-1.5 border rounded font-mono" />
          </div>
          <div>
            <label class="block font-semibold text-slate-600 mb-1">Bitiş (ISO UTC)</label>
            <input v-model="downtimeForm.end_time" type="text" required class="w-full px-2.5 py-1.5 border rounded font-mono" />
          </div>
          <div>
            <label class="block font-semibold text-slate-600 mb-1">Duruş Nedeni</label>
            <input v-model="downtimeForm.reason" type="text" required class="w-full px-2.5 py-1.5 border rounded" />
          </div>
          <button type="submit" class="w-full py-2 bg-indigo-600 hover:bg-indigo-700 text-white font-semibold rounded">
            Duruş Ekle & Metrikleri Güncelle
          </button>
        </form>
      </div>
    </div>
  </div>
</template>