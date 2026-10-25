<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { store, demoAccounts, loginWithCredentials, logoutUser, apiFetch } from './store'
import { navRoutes } from './router'

const router = useRouter()
const route = useRoute()
const mobileMenuOpen = ref(false)
const loginEmail = ref('yonetici@bakimplan.local')
const loginPassword = ref('Password123!')

const visibleRoutes = computed(() => {
  if (!store.user) return []
  return navRoutes.filter(r => r.roles.includes(store.user.role))
})

async function onSwitchRole(email, pass = 'Password123!') {
  try {
    const user = await loginWithCredentials(email, pass)
    const currentAllowed = navRoutes.find(r => r.name === route.name)?.roles || []
    if (!currentAllowed.includes(user.role)) {
      const first = navRoutes.find(r => r.roles.includes(user.role))
      if (first) router.push(first.path)
    } else {
      router.replace({ path: route.path, query: { _t: Date.now() } })
    }
  } catch (_) {}
}

async function testUnauthorizedEndpoint() {
  try {
    await apiFetch('/audit-logs')
    store.successBanner = 'Yönetici olduğunuz için Denetim Kayıtlarına erişim başarılı (200 OK).'
  } catch (_) {}
}

onMounted(async () => {
  if (!store.token) {
    await onSwitchRole('yonetici@bakimplan.local', 'Password123!')
  }
})
</script>

<template>
  <div class="min-h-screen bg-slate-100 text-slate-800 flex flex-col">
    <header class="bg-slate-900 text-white shadow-lg sticky top-0 z-30">
      <div class="max-w-7xl mx-auto px-4 py-3 flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <button @click="mobileMenuOpen = !mobileMenuOpen" class="lg:hidden p-2 rounded bg-slate-800 hover:bg-slate-700">☰</button>
          <div>
            <div class="flex items-center gap-2">
              <span class="font-extrabold text-lg tracking-tight text-emerald-400">BakımPlan</span>
              <span class="text-xs bg-emerald-950 text-emerald-300 px-2 py-0.5 rounded border border-emerald-700 flex items-center gap-1">
                <span class="w-2 h-2 rounded-full bg-emerald-400 animate-pulse"></span> Canlı Sistem
              </span>
            </div>
            <p class="text-xs text-slate-400 hidden sm:block">Ekipman Bakım, Arıza, Stok ve Güvenilirlik Yönetim Sistemi</p>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-1.5">
          <span class="text-xs text-slate-400 mr-1 hidden md:inline">Hızlı Rol Geçişi:</span>
          <button
            v-for="acc in demoAccounts"
            :key="acc.role"
            @click="onSwitchRole(acc.email)"
            :class="[
              'text-xs px-2.5 py-1 rounded font-medium transition border',
              store.user?.role === acc.role ? `${acc.badge} text-white border-white shadow` : 'bg-slate-800 text-slate-300 border-slate-700 hover:bg-slate-700'
            ]"
          >
            {{ acc.label }}
          </button>
          <button v-if="store.user" @click="logoutUser" class="text-xs px-2.5 py-1 rounded bg-red-900/60 hover:bg-red-700 text-red-200 border border-red-800 ml-1">
            Çıkış
          </button>
        </div>
      </div>
    </header>

    <main v-if="!store.user" class="flex-1 flex items-center justify-center p-4">
      <div class="bg-white rounded-xl shadow-lg border border-slate-200 max-w-md w-full p-6">
        <h2 class="text-xl font-bold text-slate-800 mb-1">Sisteme Giriş Yap</h2>
        <p class="text-xs text-slate-500 mb-4">Demo parolası: <code class="bg-slate-100 px-1.5 py-0.5 rounded font-bold">Password123!</code></p>
        <div v-if="store.errorBanner" class="mb-4 p-3 rounded bg-red-50 border border-red-300 text-red-700 text-xs font-medium">{{ store.errorBanner }}</div>
        <form @submit.prevent="onSwitchRole(loginEmail, loginPassword)" class="space-y-3">
          <input v-model="loginEmail" type="email" required placeholder="E-Posta" class="w-full px-3 py-2 border rounded-lg text-sm" />
          <input v-model="loginPassword" type="password" required placeholder="Parola" class="w-full px-3 py-2 border rounded-lg text-sm" />
          <button type="submit" :disabled="store.loading" class="w-full py-2.5 bg-emerald-600 hover:bg-emerald-700 text-white font-semibold rounded-lg text-sm">
            {{ store.loading ? 'Giriş Yapılıyor...' : 'Giriş Yap (JWT)' }}
          </button>
        </form>
      </div>
    </main>

    <div v-else class="flex-1 max-w-7xl w-full mx-auto flex flex-col lg:flex-row gap-4 p-4">
      <aside :class="['lg:w-64 shrink-0', mobileMenuOpen ? 'block' : 'hidden lg:block']">
        <div class="bg-white rounded-xl shadow-sm border border-slate-200 p-4 sticky top-20">
          <div class="mb-4 pb-3 border-b border-slate-100">
            <div class="text-xs text-slate-400">Aktif Oturum</div>
            <div class="font-bold text-sm text-slate-800 truncate">{{ store.user.full_name }}</div>
            <div class="flex items-center justify-between mt-1">
              <span class="text-xs font-mono uppercase px-2 py-0.5 rounded bg-slate-100 text-slate-700 font-bold">Rol: {{ store.user.role }}</span>
              <button @click="testUnauthorizedEndpoint" class="text-[11px] text-indigo-600 hover:underline">403 Yetki Testi</button>
            </div>
          </div>

          <nav class="space-y-1">
            <router-link
              v-for="r in visibleRoutes"
              :key="r.path"
              :to="r.path"
              @click="mobileMenuOpen = false"
              class="w-full text-left px-3 py-2.5 rounded-lg text-xs font-semibold flex items-center justify-between transition"
              :class="route.path === r.path ? 'bg-slate-900 text-white shadow-sm' : 'text-slate-600 hover:bg-slate-100'"
            >
              <span>{{ r.title }}</span>
              <span :class="['text-[10px] px-1.5 py-0.5 rounded font-mono', route.path === r.path ? 'bg-emerald-500 text-slate-950 font-bold' : 'bg-slate-200 text-slate-600']">
                {{ r.code }}
              </span>
            </router-link>
          </nav>
        </div>
      </aside>

      <section class="flex-1 min-w-0 space-y-4">
        <div v-if="store.errorBanner" class="p-3.5 rounded-xl bg-red-50 border border-red-300 text-red-800 text-xs flex items-center justify-between shadow-sm">
          <div><strong class="font-bold">Sunucu Reddi / Hata:</strong> {{ store.errorBanner }}</div>
          <button @click="store.errorBanner = ''" class="text-red-600 font-bold px-2">✕</button>
        </div>
        <div v-if="store.successBanner" class="p-3.5 rounded-xl bg-emerald-50 border border-emerald-300 text-emerald-800 text-xs flex items-center justify-between shadow-sm">
          <div><strong class="font-bold">İşlem Başarılı:</strong> {{ store.successBanner }}</div>
          <button @click="store.successBanner = ''" class="text-emerald-600 font-bold px-2">✕</button>
        </div>
        <div v-if="store.loading" class="p-4 rounded-xl bg-white border border-slate-200 text-slate-500 text-xs flex items-center gap-2 shadow-sm">
          <span class="animate-spin inline-block w-4 h-4 border-2 border-emerald-600 border-t-transparent rounded-full"></span>
          <span>Veriler PostgreSQL 16 sunucusundan yükleniyor...</span>
        </div>

        <router-view :key="route.fullPath" />
      </section>
    </div>
  </div>
</template>