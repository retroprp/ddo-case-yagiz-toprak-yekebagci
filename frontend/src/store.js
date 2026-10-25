import { reactive } from 'vue'

const API_BASE = import.meta.env.VITE_API_URL || 'http://localhost:8080/api/v1'

export const demoAccounts = [
  { role: 'yonetici', label: 'Yönetici (Tam Yetki)', email: 'yonetici@bakimplan.local', badge: 'bg-purple-600' },
  { role: 'planlama', label: 'Bakım Planlama', email: 'planlama@bakimplan.local', badge: 'bg-blue-600' },
  { role: 'teknisyen', label: 'Teknisyen', email: 'teknisyen@bakimplan.local', badge: 'bg-emerald-600' },
  { role: 'uretim', label: 'Üretim Sorumlusu', email: 'uretim@bakimplan.local', badge: 'bg-amber-600' },
  { role: 'depo', label: 'Depo Sorumlusu', email: 'depo@bakimplan.local', badge: 'bg-rose-600' }
]

export const store = reactive({
  token: localStorage.getItem('cmms_token') || '',
  user: JSON.parse(localStorage.getItem('cmms_user') || 'null'),
  loading: false,
  errorBanner: '',
  successBanner: ''
})

export function clearBanners() {
  store.errorBanner = ''
  store.successBanner = ''
}

export async function apiFetch(path, options = {}) {
  clearBanners()
  store.loading = true
  try {
    const headers = {
      'Content-Type': 'application/json',
      ...(options.headers || {})
    }
    if (store.token) {
      headers['Authorization'] = `Bearer ${store.token}`
    }
    if (options.method === 'POST' || options.method === 'PATCH') {
      headers['Idempotency-Key'] = `ui-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
    }

    const res = await fetch(`${API_BASE}${path}`, { ...options, headers })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) {
      const msg = data?.error?.message || `HTTP ${res.status} Hatası`
      const code = data?.error?.code || 'ERR'
      throw new Error(`[${code}] ${msg}`)
    }
    return data
  } catch (err) {
    store.errorBanner = err.message
    throw err
  } finally {
    store.loading = false
  }
}

export async function loginWithCredentials(email, password = 'Password123!') {
  const data = await apiFetch('/auth/login', {
    method: 'POST',
    body: JSON.stringify({ email, password })
  })
  store.token = data.access_token
  store.user = data.user
  localStorage.setItem('cmms_token', data.access_token)
  localStorage.setItem('cmms_user', JSON.stringify(data.user))
  store.successBanner = `${data.user.full_name} (${data.user.role}) olarak giriş yapıldı.`
  return data.user
}

export function logoutUser() {
  store.token = ''
  store.user = null
  localStorage.removeItem('cmms_token')
  localStorage.removeItem('cmms_user')
}

export function formatTL(kurus) {
  return ((Number(kurus) || 0) / 100).toLocaleString('tr-TR', { minimumFractionDigits: 2, maximumFractionDigits: 2 }) + ' ₺'
}

// Aktif kullanıcının verilen rollerden birine sahip olup olmadığını döner.
// Buton/aksiyon görünürlüğü için kullanılır; asıl yetki kontrolü her zaman backend'dedir.
export function hasRole(...roles) {
  if (!store.user) return false
  return roles.includes(store.user.role)
}

// /equipments/tree yanıtı iç içe bir ağaçtır; seçim listeleri için düzleştirilir.
export function flattenEquipments(nodes, depth = 0, acc = []) {
  for (const node of nodes || []) {
    acc.push({ ...node, depth })
    flattenEquipments(node.children, depth + 1, acc)
  }
  return acc
}

export function formatDate(value) {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString('tr-TR', { dateStyle: 'short', timeStyle: 'short' })
}

export function formatNumber(value, digits = 2) {
  return (Number(value) || 0).toLocaleString('tr-TR', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits
  })
}