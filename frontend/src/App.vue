<script setup>
import { ref, onMounted } from 'vue'

const health = ref({ status: 'yükleniyor...' })

onMounted(async () => {
  try {
    const res = await fetch('http://localhost:8080/health')
    health.value = await res.json()
  } catch (e) {
    health.value = { status: 'hata', error: e.message }
  }
})
</script>

<template>
  <div class="min-h-screen flex items-center justify-center p-6">
    <div class="bg-white rounded-xl shadow-md p-8 max-w-md w-full border border-slate-200">
      <h1 class="text-2xl font-bold text-slate-800 mb-2">BakımPlan CMMS</h1>
      <p class="text-sm text-slate-500 mb-4">Vue 3 + Go + PostgreSQL 16 Altyapısı</p>
      <div class="bg-slate-100 p-4 rounded-lg text-sm font-mono">
        API Durumu: <span class="font-bold text-emerald-600">{{ health.status }}</span>
      </div>
    </div>
  </div>
</template>