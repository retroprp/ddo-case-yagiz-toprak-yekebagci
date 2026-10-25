<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import {
  store,
  demoAccounts,
  roleBadge,
  roleLabel,
  loginWithCredentials,
  logoutUser,
  verifySession,
  apiFetch
} from './store'
import { navRoutes, firstRouteFor } from './router'

const router = useRouter()
const route = useRoute()

const mobileMenuOpen = ref(false)
const loginEmail = ref('')
const loginPassword = ref('')
const loginError = ref('')
const rbacTestRunning = ref(false)
const rbacTest = ref(null)

const visibleRoutes = computed(() => {
  if (!store.user) return []
  return navRoutes.filter((r) => r.roles.includes(store.user.role))
})

const roleText = computed(() => roleLabel(store.user?.role))
const badgeClass = computed(() => roleBadge[store.user?.role] || 'bg-slate-600')

function routeAllows(user, routeName) {
  if (!user) return false
  const allowed = navRoutes.find((r) => r.name === routeName)?.roles || []
  return allowed.includes(user.role)
}

async function onLogin() {
  loginError.value = ''
  rbacTest.value = null
  try {
    const user = await loginWithCredentials(loginEmail.value.trim(), loginPassword.value)
    const first = firstRouteFor(user)
    if (routeAllows(user, route.name)) {
      await router.replace({ path: route.path, query: { _t: Date.now() } })
    } else if (first) {
      await router.replace(first.path)
    }
  } catch (err) {
    loginError.value = err.message
  }
}

async function onLogout() {
  logoutUser()
  rbacTest.value = null
  loginError.value = ''
  loginPassword.value = ''
  mobileMenuOpen.value = false
  await router.replace({ name: 'login' })
}

// Demo hesap: yalnızca formu doldurur, giriş yapmaz.
// (Rol değiştirmek için çıkış yapıp o hesapla giriş yapılır.)
function fillDemoAccount(acc) {
  loginEmail.value = acc.email
  loginPassword.value = 'Password123!'
  loginError.value = ''
}

// RBAC testi: /audit-logs yalnızca yöneticiye açıktır.
// 403 burada BEKLENEN bir sonuçtur; bu yüzden hata banner'ı değil,
// nötr bir bilgi kutusu gösterilir (silent: true).
async function testRbac() {
  if (!store.user || rbacTestRunning.value) return
  rbacTestRunning.value = true
  rbacTest.value = null
  try {
    await apiFetch('/audit-logs', { silent: true })
    rbacTest.value = {
      kind: 'ok',
      title: '200 OK',
      text: `"${roleText.value}" rolü /api/v1/audit-logs ucuna yetkili; sunucu kayıtları döndürdü.`
    }
  } catch (err) {
    if (err.status === 403) {
      rbacTest.value = {
        kind: 'expected',
        title: '403 Forbidden (beklenen)',
        text: `"${roleText.value}" rolü /api/v1/audit-logs ucuna yetkili değil. Sunucu isteği reddetti — RBAC çalışıyor.`
      }
    } else {
      rbacTest.value = {
        kind: 'unexpected',
        title: `Beklenmeyen yanıt (${err.status ?? '—'})`,
        text: err.message
      }
    }
  } finally {
    rbacTestRunning.value = false
  }
}

onMounted(async () => {
  // localStorage'da token varsa geçerliliğini doğrula (süresi dolmuş olabilir).
  const valid = await verifySession()
  if (!valid) {
    if (route.name !== 'login') await router.replace({ name: 'login' })
    return
  }
  const first = firstRouteFor(store.user)
  if (first && !routeAllows(store.user, route.name)) {
    await router.replace(first.path)
  }
})
</script>

<template>
  <div class="min-h-screen bg-slate-100 text-slate-800 flex flex-col">
    <header class="bg-slate-900 text-white shadow-lg sticky top-0 z-30">
      <div class="max-w-7xl mx-auto px-4 py-3 flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <button
            v-if="store.user"
            @click="mobileMenuOpen = !mobileMenuOpen"
            class="lg:hidden p-2 rounded bg-slate-800 hover:bg-slate-700"
            aria-label="Menü"
          >☰</button>
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

        <div v-if="store.user" class="flex items-center gap-2">
          <div class="text-right hidden sm:block">
            <div class="text-xs font-semibold text-slate-200 leading-tight">{{ store.user.full_name }}</div>
            <div class="text-[10px] text-slate-400 leading-tight">{{ store.user.email }}</div>
          </div>
          <span :class="['text-[10px] px-2 py-1 rounded font-bold uppercase tracking-wide', badgeClass]">
            {{ roleText }}
          </span>
          <button
            @click="onLogout"
            class="text-xs px-2.5 py-1 rounded bg-red-900/60 hover:bg-red-700 text-red-200 border border-red-800"
          >
            Çıkış
          </button>
        </div>
      </div>
    </header>

    <!-- GİRİŞ EKRANI ------------------------------------------------------- -->
    <main v-if="!store.user" class="flex-1 flex items-start sm:items-center justify-center p-4">
      <div class="w-full max-w-lg space-y-3">
        <div class="bg-white rounded-xl shadow-lg border border-slate-200 p-6">
          <h1 class="text-xl font-bold text-slate-800">Sisteme Giriş</h1>
          <p class="text-xs text-slate-500 mt-1 mb-4">
            Rolünüze ait ekranlar, giriş yaptığınız hesapla belirlenir.
          </p>

          <div v-if="loginError" class="mb-4 p-3 rounded-lg bg-red-50 border border-red-300 text-red-700 text-xs">
            {{ loginError }}
          </div>

          <form @submit.prevent="onLogin" class="space-y-3">
            <label class="block">
              <span class="text-[11px] font-semibold text-slate-500 uppercase tracking-wide">E-Posta</span>
              <input
                v-model="loginEmail"
                type="email"
                required
                autocomplete="username"
                placeholder="ornek@bakimplan.local"
                class="mt-1 w-full px-3 py-2 border border-slate-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-emerald-500"
              />
            </label>
            <label class="block">
              <span class="text-[11px] font-semibold text-slate-500 uppercase tracking-wide">Parola</span>
              <input
                v-model="loginPassword"
                type="password"
                required
                autocomplete="current-password"
                placeholder="••••••••"
                class="mt-1 w-full px-3 py-2 border border-slate-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-emerald-500"
              />
            </label>
            <button
              type="submit"
              :disabled="store.loading"
              class="w-full py-2.5 bg-emerald-600 hover:bg-emerald-700 disabled:opacity-50 text-white font-semibold rounded-lg text-sm"
            >
              {{ store.loading ? 'Giriş yapılıyor...' : 'Giriş Yap' }}
            </button>
          </form>
        </div>

        <div class="bg-white rounded-xl border border-slate-200 p-4">
          <div class="flex items-center justify-between mb-2">
            <h2 class="text-xs font-bold text-slate-700">Demo Hesaplar</h2>
            <span class="text-[10px] text-slate-400">
              parola: <code class="bg-slate-100 px-1 py-0.5 rounded font-bold">Password123!</code>
            </span>
          </div>
          <p class="text-[10px] text-slate-400 mb-2">
            Bir hesaba tıklamak yalnızca formu doldurur; girişi siz başlatırsınız.
          </p>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-1.5">
            <button
              v-for="acc in demoAccounts"
              :key="acc.role"
              type="button"
              @click="fillDemoAccount(acc)"
              class="flex items-center gap-2 text-left text-xs px-2.5 py-2 rounded-lg border border-slate-200 hover:border-slate-300 hover:bg-slate-50 transition"
            >
              <span :class="['w-2 h-2 rounded-full shrink-0', acc.badge]"></span>
              <span class="min-w-0">
                <span class="block font-semibold text-slate-700 truncate">{{ acc.label }}</span>
                <span class="block text-[10px] text-slate-400 truncate">{{ acc.email }}</span>
              </span>
            </button>
          </div>
        </div>
      </div>
    </main>

    <!-- OTURUM AÇIK -------------------------------------------------------- -->
    <div v-else class="flex-1 max-w-7xl w-full mx-auto flex flex-col lg:flex-row gap-4 p-4">
      <aside :class="['lg:w-64 shrink-0', mobileMenuOpen ? 'block' : 'hidden lg:block']">
        <div class="bg-white rounded-xl shadow-sm border border-slate-200 p-4 sticky top-20">
          <div class="mb-4 pb-3 border-b border-slate-100">
            <div class="text-xs text-slate-400">Aktif Oturum</div>
            <div class="font-bold text-sm text-slate-800 truncate">{{ store.user.full_name }}</div>
            <div class="flex items-center justify-between mt-1 gap-2">
              <span :class="['text-[10px] font-mono uppercase px-2 py-0.5 rounded text-white font-bold', badgeClass]">
                {{ store.user.role }}
              </span>
              <button
                @click="testRbac"
                :disabled="rbacTestRunning"
                class="text-[11px] text-indigo-600 hover:underline disabled:opacity-50"
              >
                {{ rbacTestRunning ? 'Test ediliyor...' : '403 Yetki Testi' }}
              </button>
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

          <div class="mt-4 pt-3 border-t border-slate-100 text-[10px] text-slate-400 leading-relaxed">
            Bu rolle erişilebilen ekran sayısı: <strong class="text-slate-600">{{ visibleRoutes.length }}</strong>
          </div>
        </div>
      </aside>

      <section class="flex-1 min-w-0 space-y-4">
        <!-- RBAC test sonucu: hata değil, bilgi -->
        <div
          v-if="rbacTest"
          :class="[
            'p-3.5 rounded-xl border text-xs flex items-start justify-between gap-3 shadow-sm',
            rbacTest.kind === 'ok'
              ? 'bg-emerald-50 border-emerald-300 text-emerald-800'
              : rbacTest.kind === 'expected'
                ? 'bg-indigo-50 border-indigo-300 text-indigo-800'
                : 'bg-amber-50 border-amber-300 text-amber-900'
          ]"
        >
          <div>
            <div class="font-bold mb-0.5">RBAC testi — {{ rbacTest.title }}</div>
            <div>{{ rbacTest.text }}</div>
          </div>
          <button @click="rbacTest = null" class="font-bold px-1 opacity-60 hover:opacity-100">✕</button>
        </div>

        <div v-if="store.errorBanner" class="p-3.5 rounded-xl bg-red-50 border border-red-300 text-red-800 text-xs flex items-center justify-between shadow-sm">
          <div><strong class="font-bold">Sunucu Reddi:</strong> {{ store.errorBanner }}</div>
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
