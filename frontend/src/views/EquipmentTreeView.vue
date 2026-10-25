<script setup>
import { ref, onMounted } from 'vue'
import { store, apiFetch, formatTL } from '../store'

const equipmentTree = ref([])
const flatEquipmentsCount = ref(0)
const selectedRootIndex = ref(0)
const moveForm = ref({ equipmentId: 6, newParentId: 31 })

async function loadTree() {
  try {
    const res = await apiFetch('/equipments/tree')
    equipmentTree.value = res.tree || []
    flatEquipmentsCount.value = res.flat_count || 2000
  } catch (_) {}
}

async function testMoveEquipment() {
  try {
    await apiFetch(`/equipments/${moveForm.value.equipmentId}/move`, {
      method: 'PATCH',
      body: JSON.stringify({ new_parent_id: Number(moveForm.value.newParentId) })
    })
    store.successBanner = `Ekipman #${moveForm.value.equipmentId} başarıyla #${moveForm.value.newParentId} altına taşındı.`
    await loadTree()
  } catch (_) {}
}

onMounted(loadTree)
</script>

<template>
  <div class="grid grid-cols-1 lg:grid-cols-3 gap-4">
    <div class="lg:col-span-2 bg-white rounded-xl border border-slate-200 shadow-sm p-4">
      <div class="flex flex-wrap items-center justify-between gap-2 mb-3">
        <div>
          <h2 class="text-base font-bold text-slate-800">Ekipman Hiyerarşisi (Tesis → Hat → Makine → Bileşen)</h2>
          <p class="text-xs text-slate-500">Toplam {{ flatEquipmentsCount }} ekipman `WITH RECURSIVE` ile sorgulandı; maliyet ve duruşlar yukarı toplandı (Roll-up).</p>
        </div>
        <div class="flex gap-1">
          <button
            v-for="(root, idx) in equipmentTree"
            :key="root.id"
            @click="selectedRootIndex = idx"
            :class="['px-2.5 py-1 rounded text-xs font-bold', selectedRootIndex === idx ? 'bg-slate-900 text-white' : 'bg-slate-100 text-slate-700']"
          >
            {{ root.code }}
          </button>
        </div>
      </div>

      <div v-if="equipmentTree.length === 0" class="p-8 text-center text-slate-400 text-xs">Kayıtlı ekipman bulunamadı.</div>

      <div v-else-if="equipmentTree[selectedRootIndex]" class="space-y-3">
        <div class="p-3 rounded-lg bg-slate-900 text-white flex flex-wrap justify-between items-center gap-2">
          <div>
            <span class="text-[10px] uppercase bg-emerald-500 text-slate-950 font-extrabold px-1.5 py-0.5 rounded mr-2">TESİS</span>
            <span class="font-bold text-sm">{{ equipmentTree[selectedRootIndex].name }}</span>
            <span class="text-xs text-slate-400 ml-2">(ID: #{{ equipmentTree[selectedRootIndex].id }})</span>
          </div>
          <div class="text-xs font-mono">
            Roll-up Maliyet: <strong class="text-emerald-400">{{ formatTL(equipmentTree[selectedRootIndex].rollup_cost_kurus) }}</strong> |
            Roll-up Duruş: <strong class="text-amber-400">{{ equipmentTree[selectedRootIndex].rollup_downtime_hours }} saat</strong>
          </div>
        </div>

        <div v-for="hat in equipmentTree[selectedRootIndex].children" :key="hat.id" class="border border-slate-200 rounded-lg p-3 bg-slate-50">
          <div class="flex flex-wrap justify-between items-center gap-2 mb-2">
            <div>
              <span class="text-[10px] uppercase bg-blue-600 text-white font-bold px-1.5 py-0.5 rounded mr-1.5">HAT</span>
              <span class="font-bold text-xs text-slate-800">{{ hat.code }} — {{ hat.name }}</span>
              <span class="text-[11px] text-slate-500 ml-1">(ID: #{{ hat.id }})</span>
            </div>
            <div class="text-[11px] font-mono text-slate-700">
              Alt Ağaç Toplamı: <strong>{{ formatTL(hat.rollup_cost_kurus) }}</strong> ({{ hat.rollup_downtime_hours }} s duruş)
            </div>
          </div>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-2 pl-3 border-l-2 border-blue-300">
            <div v-for="mak in hat.children.slice(0, 4)" :key="mak.id" class="bg-white p-2 rounded border border-slate-200 text-xs">
              <div class="flex justify-between font-semibold">
                <span>⚙️ {{ mak.code }} (ID: #{{ mak.id }})</span>
                <span class="text-emerald-700 font-mono">{{ formatTL(mak.rollup_cost_kurus) }}</span>
              </div>
              <div class="text-[11px] text-slate-500 mt-0.5">
                Duruş: {{ mak.rollup_downtime_hours }} s | Alt Bileşen Sayısı: {{ mak.children?.length || 0 }}
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="bg-white rounded-xl border border-slate-200 shadow-sm p-4 space-y-3 h-fit">
      <h3 class="text-sm font-bold text-slate-800">Ağaçta Taşıma & Döngü/Seviye Koruması (T-2)</h3>
      <p class="text-xs text-slate-500">
        Alt ağaca taşıma <code>409 CYCLE_DETECTED</code>, yanlış seviyeye bağlama <code>422 INVALID_HIERARCHY_LEVEL</code> ile engellenir.
      </p>
      <form @submit.prevent="testMoveEquipment" class="space-y-2.5 text-xs">
        <div>
          <label class="block font-semibold text-slate-600 mb-1">Taşınacak Ekipman ID</label>
          <input v-model.number="moveForm.equipmentId" type="number" required class="w-full px-2.5 py-1.5 border rounded" />
        </div>
        <div>
          <label class="block font-semibold text-slate-600 mb-1">Yeni Ebeveyn (Parent) ID</label>
          <input v-model.number="moveForm.newParentId" type="number" required class="w-full px-2.5 py-1.5 border rounded" />
        </div>
        <div class="flex flex-col gap-2">
          <button type="button" @click="moveForm.equipmentId = 6; moveForm.newParentId = 31; testMoveEquipment()" class="w-full py-2 bg-amber-600 hover:bg-amber-700 text-white font-semibold rounded">
            Döngü Testi (Hat #6 → Altındaki #31)
          </button>
          <button type="button" @click="moveForm.equipmentId = 31; moveForm.newParentId = 2; testMoveEquipment()" class="w-full py-2 bg-indigo-600 hover:bg-indigo-700 text-white font-semibold rounded">
            Seviye Testi (Makine #31 → Tesis #2)
          </button>
          <button type="submit" class="w-full py-2 bg-slate-900 hover:bg-slate-800 text-white font-semibold rounded">
            Seçili Hedefe Taşı
          </button>
        </div>
      </form>
    </div>
  </div>
</template>