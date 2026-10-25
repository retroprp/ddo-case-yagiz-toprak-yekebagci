import { createRouter, createWebHistory } from 'vue-router'
import { store } from '../store'
import ReliabilityView from '../views/ReliabilityView.vue'
import EquipmentTreeView from '../views/EquipmentTreeView.vue'
import OperationsView from '../views/OperationsView.vue'
import InventoryAndAuditView from '../views/InventoryAndAuditView.vue'

export const navRoutes = [
  { path: '/reliability', name: 'reliability', code: 'F-7 / T-4', title: 'Güvenilirlik & MTBF', roles: ['yonetici', 'planlama', 'uretim', 'teknisyen'], component: ReliabilityView },
  { path: '/equipments', name: 'equipments', code: 'F-1 / T-2', title: 'Ekipman Ağacı', roles: ['yonetici', 'planlama', 'uretim', 'teknisyen', 'depo'], component: EquipmentTreeView },
  { path: '/workorders', name: 'workorders', code: 'F-3', title: 'İş Emirleri', roles: ['yonetici', 'planlama', 'teknisyen', 'uretim'], component: OperationsView, props: { section: 'workorders' } },
  { path: '/failures', name: 'failures', code: 'F-2', title: 'Arıza Bildirimi', roles: ['yonetici', 'planlama', 'uretim', 'teknisyen'], component: OperationsView, props: { section: 'failures' } },
  { path: '/pm', name: 'pm', code: 'F-4 / T-1', title: 'Önleyici Bakım & Sayaç', roles: ['yonetici', 'planlama', 'teknisyen'], component: OperationsView, props: { section: 'pm' } },
  { path: '/workload', name: 'workload', code: 'F-6', title: 'Teknisyen İş Yükü', roles: ['yonetici', 'planlama', 'teknisyen'], component: OperationsView, props: { section: 'workload' } },
  { path: '/inventory', name: 'inventory', code: 'F-5 / T-3', title: 'Yedek Parça & Depo', roles: ['yonetici', 'depo', 'planlama', 'teknisyen'], component: InventoryAndAuditView, props: { section: 'inventory' } },
  { path: '/audit', name: 'audit', code: 'AUDIT', title: 'Denetim Kaydı', roles: ['yonetici'], component: InventoryAndAuditView, props: { section: 'audit' } }
]

// Oturum yokken gösterilen boş ekran. Giriş formunu App.vue render eder;
// burada amaç yalnızca "/login" için geçerli bir rota tanımı olması.
const LoginView = { name: 'LoginPlaceholder', setup: () => () => null }

// Verilen rolün görebileceği ilk ekran.
export function firstRouteFor(user) {
  if (!user) return null
  return navRoutes.find((r) => r.roles.includes(user.role)) || null
}

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/reliability' },
    { path: '/login', name: 'login', component: LoginView },
    ...navRoutes
  ]
})

/**
 * Erişim kuralları:
 *  - Oturum yok  → /login (başka bir URL'e doğrudan gelinebilse bile)
 *  - Oturum var  → rolün yetkisi olmayan ekrana girilirse ilk izinli ekrana yönlendir
 *
 * Not: Bu kontrol YALNIZCA kullanıcı deneyimi içindir. Gerçek yetkilendirme
 * backend'de `RequireRoles` middleware'i ile yapılır (403).
 */
router.beforeEach((to, _from, next) => {
  if (!store.user) {
    return to.name === 'login' ? next() : next({ name: 'login' })
  }

  const first = firstRouteFor(store.user)
  if (!first) return next({ name: 'login' })

  if (to.name === 'login') return next(first.path)

  const allowedRoles = to.meta?.roles || navRoutes.find((r) => r.name === to.name)?.roles
  if (allowedRoles && !allowedRoles.includes(store.user.role)) {
    return next(first.path)
  }

  next()
})

export default router
