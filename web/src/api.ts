// API client. All paths are RELATIVE so they resolve against <base href="/<secret>/">
// injected by the Go server — the SPA never needs to know its own secret path.

export interface User {
  id: number
  name: string
  uuid: string
  status: string // active | disabled | expired | limited
  enabled: boolean
  data_limit: number
  expire_at: number
  used_up: number
  used_down: number
  created_at: string
  reset_period: string
  last_seen: number
  device_limit: number
  active_devices: number
  plan_id: number
  plan_name?: string
  telegram_linked?: boolean
  telegram_link?: string
  telegram_deep_link?: string
  system_email: string // Xray client id "u<id>" (logs/stats)
  sub_url: string
  vless: string
  trojan: string
  hysteria2: string
  reality: string
}

export interface DailyPoint {
  day: string
  up: number
  down: number
}

export interface UserTotal {
  user_id: number
  name: string
  up: number
  down: number
}

export interface Connection {
  ip: string
  last_seen: number
  count: number
}

export const getUserConnections = (id: number) =>
  api<Connection[]>(`api/users/${id}/connections`)

export interface ConnInfo {
  key: string
  name: string
  transport: string
  security: string
  port: string
  note: string
  enabled: boolean
  fingerprint: string // uTLS fingerprint; "" for Hysteria2 (no uTLS)
}

export interface ConnectionsStatus {
  host: string
  sni: string
  ws_path: string
  protocols: ConnInfo[]
  hysteria_port: number
  hop_start: number
  hop_end: number
  hop_interval: string
  reality_port: number
  reality_dest: string
  reality_public_key: string
  reality_short_id: string
  reality_service_name: string
  reality_anti_replay: boolean
  tls_fragment: boolean
  tls_min13: boolean
  block_quic: boolean
}

// ConnectionsUpdate is the whole connection surface, applied in one request.
export interface ConnectionsUpdate {
  protocols: Record<string, boolean>
  fingerprints: Record<string, string>
  ws_path: string
  hysteria_port: number
  hop_start: number
  hop_end: number
  hop_interval: string
  reality_port: number
  reality_dest: string
  reality_anti_replay: boolean
  regen_reality_keys: boolean
  tls_fragment: boolean
  tls_min13: boolean
  block_quic: boolean
}

export const applyConnections = (u: ConnectionsUpdate) =>
  api<ConnectionsStatus>('api/connections', {
    method: 'POST',
    body: JSON.stringify(u),
  })

export const FINGERPRINTS = [
  'firefox',
  'chrome',
  'safari',
  'edge',
  'ios',
  'android',
  'random',
  'randomized',
]

export interface SystemStatus {
  cpu_percent: number
  mem_used: number
  mem_total: number
  swap_used: number
  swap_total: number
  disk_used: number
  disk_total: number
  host_uptime: number
  net_up: number
  net_down: number
  xray_running: boolean
  xray_uptime: number
  xray_version: string
  goroutines: number
  cpu_cores: number
  proc_mem: number
  vpn_up: number
  vpn_down: number
  total_up: number
  total_down: number
  users: number
  enabled_users: number
  traffic_today: number
  cert_days_left: number
}

export const getXrayConfig = (): Promise<string> => apiText('api/xray/config')

export interface XrayStatus {
  running: boolean
  started_at: number // unix; advances on every Xray (re)start
}

export const getXrayStatus = () => api<XrayStatus>('api/xray/status')

export interface BackupManifest {
  domain: string
  secret_path: string
  user_count: number
  created_at: string
}

export const getBackupInfo = () => api<BackupManifest>('api/backup/info')

// BackupInspection is the validated preview of an uploaded backup: its manifest
// plus a check that the embedded database is a real, non-empty panel DB.
export interface BackupInspection {
  manifest: BackupManifest
  valid: boolean
  db_users: number
  db_admins: number
  issue: string // human-readable problem when !valid
}

const backupForm = (file: File) => {
  const fd = new FormData()
  fd.append('backup', file)
  return fd
}

export const inspectBackup = (file: File) =>
  apiForm<BackupInspection>('api/backup/inspect', backupForm(file))

export const restoreBackup = (file: File) =>
  apiForm<{ ok?: boolean }>('api/restore', backupForm(file)).then(() => {})

// resetPanel wipes all state and restarts the panel into first-run mode. It
// returns the URL the panel will come back on (auto-detected IP + default path),
// which may differ from the current address (e.g. a custom domain).
export const resetPanel = () =>
  api<{ url: string }>('api/reset', { method: 'POST' })

export const getConnections = () => api<ConnectionsStatus>('api/connections')
export const deleteUser = (id: number) =>
  api<{ ok: boolean }>(`api/users/${id}`, { method: 'DELETE' })
export const resetUserTraffic = (id: number) =>
  api<{ ok: boolean }>(`api/users/${id}/reset`, { method: 'POST' })
export const setUserLimits = (
  id: number,
  data_limit: number,
  expire_at: number,
  device_limit: number,
) =>
  api<{ ok: boolean }>(`api/users/${id}/limits`, {
    method: 'POST',
    body: JSON.stringify({ data_limit, expire_at, device_limit }),
  })
export const setUserEnabled = (id: number, enabled: boolean) =>
  api<{ ok: boolean }>(`api/users/${id}/enabled`, {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  })
export const renameUser = (id: number, name: string) =>
  api<{ ok: boolean }>(`api/users/${id}/name`, {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
export const setResetPeriod = (id: number, period: string) =>
  api<{ ok: boolean }>(`api/users/${id}/reset-period`, {
    method: 'POST',
    body: JSON.stringify({ period }),
  })
export const rotateSubToken = (id: number) =>
  api<User>(`api/users/${id}/rotate-sub`, { method: 'POST' })
export const unlinkUserTelegram = (id: number) =>
  api<{ ok: boolean }>(`api/users/${id}/telegram/unlink`, { method: 'POST' })
export const genUserTelegramLink = (id: number) =>
  api<{ deep_link: string; expires_sec: number }>(
    `api/users/${id}/telegram/link`,
    { method: 'POST' },
  )

export const getStatsSeries = (p: { user_id?: number; from?: string; to?: string }) => {
  const q = new URLSearchParams()
  if (p.user_id) q.set('user_id', String(p.user_id))
  if (p.from) q.set('from', p.from)
  if (p.to) q.set('to', p.to)
  return api<DailyPoint[]>('api/stats/series?' + q.toString())
}
export const getStatsByUser = (from?: string, to?: string) => {
  const q = new URLSearchParams()
  if (from) q.set('from', from)
  if (to) q.set('to', to)
  return api<UserTotal[]>('api/stats/users?' + q.toString())
}
export const resetStats = () => api<{ ok: boolean }>('api/stats/reset', { method: 'POST' })

// onUnauthorized is invoked whenever an API call returns 401 (the session expired
// or was revoked server-side). App registers a handler that drops back to the
// login screen, so an expired session can't leave the user stuck on a dashboard
// where every action fails with an opaque toast.
let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

// CSRF_HEADER is sent on every request. State-changing endpoints require it
// server-side: a cross-origin page can't set a custom header without a CORS
// preflight the panel never grants, so this stops form/img/script-driven CSRF.
const CSRF_HEADER = { 'X-RosPanel-CSRF': '1' }

async function api<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...opts,
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER, ...(opts.headers || {}) },
  })
  if (res.status === 401) onUnauthorized?.()
  const text = await res.text()
  const data = text ? JSON.parse(text) : {}
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`)
  return data as T
}

// apiForm POSTs multipart FormData — the browser sets the multipart Content-Type
// (with boundary), so we must NOT set it ourselves — and returns the parsed JSON.
async function apiForm<T>(path: string, body: FormData): Promise<T> {
  const res = await fetch(path, {
    method: 'POST',
    body,
    credentials: 'same-origin',
    headers: { ...CSRF_HEADER },
  })
  if (res.status === 401) onUnauthorized?.()
  const text = await res.text()
  const data = text ? JSON.parse(text) : {}
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`)
  return data as T
}

// apiText fetches a plaintext (non-JSON) body.
async function apiText(path: string): Promise<string> {
  const res = await fetch(path, { credentials: 'same-origin' })
  if (res.status === 401) onUnauthorized?.()
  const text = await res.text()
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return text
}

export interface Me {
  username: string
  setup_done: boolean
  timezone: string
  version: string
  must_change_password?: boolean
}

export const getMe = () => api<Me>('api/me')

export interface UpdateInfo {
  current: string
  latest?: string
  available: boolean
  notes?: string
  error?: string
}

export const checkUpdate = () => api<UpdateInfo>('api/update')

export const applyUpdate = () =>
  api<{ ok: boolean; version: string }>('api/update', { method: 'POST' })

export const setupPassword = (password: string) =>
  api<{ ok: boolean }>('api/setup/password', {
    method: 'POST',
    body: JSON.stringify({ password }),
  })
export const setupTimezone = (timezone: string) =>
  api<{ ok: boolean }>('api/setup/timezone', {
    method: 'POST',
    body: JSON.stringify({ timezone }),
  })
export const finishSetup = () =>
  api<{ ok: boolean }>('api/setup/finish', { method: 'POST' })

export const updateCredentials = (
  username: string,
  password: string,
  currentPassword: string,
) =>
  api<{ ok: boolean }>('api/account/credentials', {
    method: 'POST',
    body: JSON.stringify({ username, password, current_password: currentPassword }),
  })

export interface SubSettings {
  sub_path: string
  sub_base64: boolean
  sub_name_in_title: boolean
  sub_title: string
  sub_routing: boolean
  sub_routing_happ: string
  sub_routing_incy: string
  sub_routing_mihomo: string
  sub_update_interval: number
}

export interface SettingsInfo extends SubSettings {
  secret_path: string
  ws_path: string
  decoy_template: string
  decoy_templates: string[]
  xray_dns: string
  warp_enabled: boolean
  warp_registered: boolean
  proxy_mode_enabled: boolean
  proxy_mode_type: string
  proxy_mode_port: number
  proxy_mode_user: string
  proxy_mode_pass: string
}

export interface ProxyModeConfig {
  enabled: boolean
  type: string
  port: number
  user: string
  pass: string
}

export const setProxyMode = (c: ProxyModeConfig) =>
  api<{ ok: boolean }>('api/settings/proxy-mode', {
    method: 'POST',
    body: JSON.stringify(c),
  })


export interface RoutingConfig {
  block_bittorrent: boolean
  block_ads: boolean
  block_ips: string[]
  block_domains: string[]
  warp_domains: string[]
  warp_ips: string[]
  opera_domains: string[]
  opera_ips: string[]
  direct_domains: string[]
  direct_ips: string[]
  routing_order: string[]
  proxy_urls: string[]
  proxy_manual: string[]
  proxy_domains: string[]
  proxy_ips: string[]
  proxy_refresh_minutes: number
}

export interface RoutingInfo {
  config: RoutingConfig
  warp_enabled: boolean
  warp_registered: boolean
  opera_enabled: boolean
  opera_country: string
  opera_running: boolean
  opera_alive: boolean
  proxy_count: number
}

export interface GeoCategories {
  geosite: string[]
  geoip: string[]
}

export const getGeoCategories = () => api<GeoCategories>('api/geo/categories')

export interface GeoFile {
  name: string
  present: boolean
  size: number
  modified_at: number
}

export const getGeoStatus = () => api<GeoFile[]>('api/geo')
export const updateGeo = () => api<GeoFile[]>('api/geo/update', { method: 'POST' })

export const getRouting = () => api<RoutingInfo>('api/routing')
export const saveRouting = (
  config: RoutingConfig,
  warpEnabled: boolean,
  operaEnabled: boolean,
  operaCountry: string,
) =>
  api<{ ok: boolean }>('api/routing', {
    method: 'POST',
    body: JSON.stringify({
      ...config,
      warp_enabled: warpEnabled,
      opera_enabled: operaEnabled,
      opera_country: operaCountry,
    }),
  })

export const setXrayDNS = (dns: string) =>
  api<{ ok: boolean }>('api/settings/dns', { method: 'POST', body: JSON.stringify({ dns }) })

export const saveSubSettings = (s: SubSettings) =>
  api<{ ok: boolean }>('api/settings/subscription', {
    method: 'POST',
    body: JSON.stringify(s),
  })

export interface ThemeColors {
  accent: string // primary colour #rrggbb (drives the whole brand ramp)
  text: string // main text
  muted: string // secondary/muted text
  bg: string // page background
  surface: string // cards / inputs / panels
}

export interface BrandingInfo {
  panel_name: string
  theme: ThemeColors
  has_custom_logo: boolean
  default_name: string
  default_theme: ThemeColors
}

export const getBranding = () => api<BrandingInfo>('api/branding')
export const saveBranding = (panelName: string, theme: ThemeColors) =>
  api<BrandingInfo>('api/settings/branding', {
    method: 'POST',
    body: JSON.stringify({ panel_name: panelName, theme }),
  })
export const uploadBrandingLogo = (file: File) => {
  const fd = new FormData()
  fd.append('logo', file)
  return apiForm<BrandingInfo>('api/settings/branding/logo', fd)
}
export const deleteBrandingLogo = () =>
  api<BrandingInfo>('api/settings/branding/logo', { method: 'DELETE' })

export const getSettings = () => api<SettingsInfo>('api/settings')
export const regenSecret = () =>
  api<{ secret_path: string }>('api/settings/secret', { method: 'POST' })
export const setDecoy = (template: string) =>
  api<{ ok: boolean }>('api/settings/decoy', {
    method: 'POST',
    body: JSON.stringify({ template }),
  })

export interface TelegramInfo {
  enabled: boolean
  token: string
  backup_cron: string // 5-field cron in the operator timezone; "" = off
  chat_ids: number[] // linked (authorized) chat IDs
  link_code: string // pending one-time linking code (if any)
  bot_username: string // admin bot @username (empty if token unset/invalid)
  user_enabled: boolean
  user_token: string
  user_reg_enabled: boolean
  user_bot_username: string // user bot @username
  admin_events: Record<string, boolean> // admin notification categories (key→on)
}

export const getTelegram = () => api<TelegramInfo>('api/telegram')

export const saveTelegram = (
  enabled: boolean,
  token: string,
  backup_cron: string,
  user_enabled: boolean,
  user_token: string,
  user_reg_enabled: boolean,
  admin_events: Record<string, boolean>,
) =>
  api<{ ok: boolean }>('api/telegram', {
    method: 'POST',
    body: JSON.stringify({
      enabled,
      token,
      backup_cron,
      user_enabled,
      user_token,
      user_reg_enabled,
      admin_events,
    }),
  })

export const genTelegramLink = () =>
  api<{ code: string; bot_username: string }>('api/telegram/link', {
    method: 'POST',
  })

// getTelegramLinkStatus is a cheap poll used while a link code is pending, so the
// page reflects a just-linked chat without a reload. pending=false ⇒ linked.
export const getTelegramLinkStatus = () =>
  api<{ chat_ids: number[]; pending: boolean }>('api/telegram/link/status')

// cancelTelegramLink drops the pending one-time link code.
export const cancelTelegramLink = () =>
  api<{ ok: boolean }>('api/telegram/link/cancel', { method: 'POST' })

export const unlinkTelegram = (chat_id: number) =>
  api<{ ok: boolean }>('api/telegram/unlink', {
    method: 'POST',
    body: JSON.stringify({ chat_id }),
  })

export const testTelegramBackup = () =>
  api<{ ok: boolean }>('api/telegram/test-backup', { method: 'POST' })

export const login = (username: string, password: string) =>
  api<{ ok: boolean }>('api/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  })

export const logout = () => api<{ ok: boolean }>('api/logout', { method: 'POST' })

export const listUsers = () => api<User[]>('api/users')

export const createUser = (name: string, data_limit = 0, expire_at = 0) =>
  api<User>('api/users', {
    method: 'POST',
    body: JSON.stringify({ name, data_limit, expire_at }),
  })

export interface CertInfo {
  subject: string
  issuer: string
  not_after: string
  days_left: number
}

export interface TLSStatus {
  mode: string
  domain: string
  sni: string
  acme_email: string
  acme_provider: string
  cert: CertInfo | null
}

export const getTLS = () => api<TLSStatus>('api/tls')

export const setACME = (target: string, email: string, provider: string) =>
  api<TLSStatus>('api/tls', {
    method: 'POST',
    body: JSON.stringify({ target, email, provider }),
  })


// --- Billing / tariffs (Settings → Тарифы) ---

export interface TariffPlan {
  id: number
  slug: string
  name: string
  price_rub: number
  period_days: number
  data_limit: number
  device_limit: number
  is_free: boolean
  payment_url: string
  sort_order: number
  enabled: boolean
}

export interface PaymentOrder {
  id: number
  user_id: number
  user_name?: string
  plan_id: number
  plan_name?: string
  amount_rub: number
  status: string
  provider: string // "" (manual) | yookassa | cryptobot
  provider_id?: string // external payment/invoice id
  pay_url?: string
  created_at: number
  paid_at: number
}

export interface BillingInfo {
  enabled: boolean
  trial_days: number
  free_plan_id: number
  trial_plan_id: number
  payment_note: string
  plans: TariffPlan[]
}

export const getBilling = () => api<BillingInfo>('api/billing')

export interface PaymentSettings {
  yookassa_enabled: boolean
  yookassa_shop_id: string
  yookassa_test: boolean
  yookassa_key_set: boolean
  cryptobot_enabled: boolean
  cryptobot_testnet: boolean
  cryptobot_token_set: boolean
  webhook_yookassa: string
  webhook_cryptobot: string
}

export interface PaymentSettingsInput {
  yookassa_enabled: boolean
  yookassa_shop_id: string
  yookassa_secret_key: string // empty = keep current
  yookassa_test: boolean
  cryptobot_enabled: boolean
  cryptobot_token: string // empty = keep current
  cryptobot_testnet: boolean
}

export const getPayments = () => api<PaymentSettings>('api/payments')
export const savePayments = (s: PaymentSettingsInput) =>
  api<PaymentSettings>('api/payments', { method: 'POST', body: JSON.stringify(s) })

export const saveBilling = (b: {
  enabled: boolean
  trial_days: number
  free_plan_id: number
  trial_plan_id: number
  payment_note: string
}) =>
  api<{ ok: boolean }>('api/billing', {
    method: 'POST',
    body: JSON.stringify(b),
  })

export const saveTariffPlan = (p: TariffPlan) =>
  api<TariffPlan>('api/billing/plans', {
    method: 'POST',
    body: JSON.stringify(p),
  })

export const deleteTariffPlan = (id: number) =>
  api<{ ok: boolean }>(`api/billing/plans/${id}`, { method: 'DELETE' })

export const listPaymentOrders = (status?: string) =>
  api<PaymentOrder[]>(`api/billing/orders${status ? `?status=${status}` : ''}`)

export const confirmPaymentOrder = (id: number, current_password: string) =>
  api<{ ok: boolean }>(`api/billing/orders/${id}/confirm`, {
    method: 'POST',
    body: JSON.stringify({ current_password }),
  })

export const cancelPaymentOrder = (id: number, current_password: string) =>
  api<{ ok: boolean }>(`api/billing/orders/${id}/cancel`, {
    method: 'POST',
    body: JSON.stringify({ current_password }),
  })

export const setUserPlan = (id: number, plan_id: number) =>
  api<{ ok: boolean }>(`api/users/${id}/plan`, {
    method: 'POST',
    body: JSON.stringify({ plan_id }),
  })
