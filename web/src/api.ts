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
  tg_chat_id?: number // linked Telegram chat/user id (0 = not linked)
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

// ---- audit log ----

// UserEvent is one audit-log row: what happened to a user, who did it, when.
// `details` is a free-form object whose keys depend on the action (see the Go
// model.Event* constants); the journal UI renders the keys it knows about.
export interface UserEvent {
  id: number
  user_id: number
  user_name: string
  action: string
  actor_kind: 'admin' | 'apikey' | 'telegram' | 'user' | 'system'
  actor_name: string
  details: Record<string, unknown> | null
  created_at: number
}

// EventPage is one page of the trail. `next_before` is the cursor to pass as
// `before` for the next (older) page; 0 means there is nothing older.
export interface EventPage {
  events: UserEvent[]
  next_before: number
}

// EventFilter narrows the global journal. Omitted fields mean "no filter".
export interface EventFilter {
  action?: string
  actor?: string
  user_id?: number
  before?: number
  limit?: number
}

function eventQuery(f: EventFilter): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(f)) {
    if (v !== undefined && v !== '' && v !== 0) q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}

export const getUserEvents = (id: number, before = 0) =>
  api<EventPage>(`api/users/${id}/events${eventQuery({ before })}`)

export const listEvents = (f: EventFilter = {}) =>
  api<EventPage>(`api/events${eventQuery(f)}`)

export const getEventCatalog = () =>
  api<{ key: string; label: string }[]>('api/events/catalog')

export interface ConnInfo {
  key: string
  name: string // default protocol label (input placeholder)
  display_name: string // custom node name ("" = use default)
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
  names: Record<string, string>
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

// Bounces the Xray process. Drops every live VPN connection — confirm first.
export const restartXray = () =>
  api<XrayStatus>('api/xray/restart', { method: 'POST' })

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

// Per-node connections: a node's own transport/protocols/REALITY. Same shape as the
// master's, so the same editor drives both.
export const getNodeConnections = (id: number) =>
  api<ConnectionsStatus>(`api/nodes/${id}/connections`)
export const applyNodeConnections = (id: number, u: ConnectionsUpdate) =>
  api<ConnectionsStatus>(`api/nodes/${id}/connections`, {
    method: 'POST',
    body: JSON.stringify(u),
  })
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

// Role is the panel's permission ladder: an operator can do everything the panel
// exposes for end users, an admin everything but the roster, the owner everything.
export type Role = 'owner' | 'admin' | 'operator'

export const ROLE_LABELS: Record<Role, string> = {
  owner: 'Владелец',
  admin: 'Администратор',
  operator: 'Оператор',
}

export const ROLE_HINTS: Record<Role, string> = {
  owner: 'Полный доступ, включая управление администраторами',
  admin: 'Всё, кроме управления администраторами',
  operator: 'Пользователи, статистика и журнал — без настроек, бэкапов и API',
}

export interface Me {
  username: string
  role: Role
  setup_done: boolean
  timezone: string
  version: string
  must_change_password?: boolean
  billing_enabled?: boolean
}

export const getMe = () => api<Me>('api/me')

export interface Admin {
  id: number
  username: string
  role: Role
  must_change_password: boolean
  created_at: number
  last_login_at: number
}

export interface AdminList {
  admins: Admin[]
  me: number // which row is you — you can't delete or demote yourself
}

export const listAdmins = () => api<AdminList>('api/admins')

export const createAdmin = (
  username: string,
  password: string,
  role: Role,
  currentPassword: string,
) =>
  api<Admin>('api/admins', {
    method: 'POST',
    body: JSON.stringify({
      username,
      password,
      role,
      current_password: currentPassword,
    }),
  })

export const setAdminRole = (id: number, role: Role, currentPassword: string) =>
  api<{ ok: boolean }>(`api/admins/${id}/role`, {
    method: 'POST',
    body: JSON.stringify({ role, current_password: currentPassword }),
  })

export const resetAdminPassword = (
  id: number,
  password: string,
  currentPassword: string,
) =>
  api<{ ok: boolean }>(`api/admins/${id}/password`, {
    method: 'POST',
    body: JSON.stringify({ password, current_password: currentPassword }),
  })

// The owner's password rides in a header: a DELETE body is the kind of thing
// proxies and clients feel free to drop.
export const deleteAdmin = (id: number, currentPassword: string) =>
  api<{ ok: boolean }>(`api/admins/${id}`, {
    method: 'DELETE',
    headers: { 'X-Current-Password': currentPassword },
  })

// The admin trail: what was done to the panel itself (the roster, the settings, TLS,
// backups, sign-ins) and by whom, from where. Owner-only.
export interface AdminAudit {
  id: number
  action: string
  target: string
  actor_kind: string
  actor_name: string
  ip: string
  details?: Record<string, unknown> | null
  created_at: number
}

export interface AdminAuditPage {
  events: AdminAudit[]
  next_before: number // 0 = no older rows
}

// The journal filters by category ("Настройки", "Администраторы", …) rather than by
// each of the two dozen actions: the actions stay precise on the rows, the filter
// stays short.
export const listAdminAudit = (params: {
  category?: string
  action?: string
  actor?: string
  before?: number
  limit?: number
}) => {
  const q = new URLSearchParams()
  if (params.category) q.set('category', params.category)
  if (params.action) q.set('action', params.action)
  if (params.actor) q.set('actor', params.actor)
  if (params.before) q.set('before', String(params.before))
  if (params.limit) q.set('limit', String(params.limit))
  const qs = q.toString()
  return api<AdminAuditPage>(`api/admin-audit${qs ? `?${qs}` : ''}`)
}

export interface AdminAuditCatalog {
  categories: { key: string; label: string }[]
  actions: { key: string; label: string; category: string }[]
}

export const getAdminAuditCatalog = () =>
  api<AdminAuditCatalog>('api/admin-audit/catalog')

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

// ANNOUNCE_MAX is the announcement length clients actually render (Happ cuts at
// 200); the server rejects anything longer, so the form counts down to the same
// number instead of letting the operator write a message that arrives truncated.
export const ANNOUNCE_MAX = 200

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
  sub_announce: string
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
  local_backup_cron: string
  local_backup_keep: number
  user_autodelete_days: number
}

export const setUserAutoDelete = (days: number) =>
  api<{ ok: boolean }>('api/settings/autodelete', {
    method: 'POST',
    body: JSON.stringify({ user_autodelete_days: days }),
  })

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

export interface LocalBackupConfig {
  cron: string
  keep: number
}

export const setLocalBackup = (c: LocalBackupConfig) =>
  api<{ ok: boolean }>('api/settings/local-backup', {
    method: 'POST',
    body: JSON.stringify(c),
  })


// EgressLane is one named proxy egress: its own upstream proxies + its own match
// rules, so e.g. ".ru" and ".com" can leave through different proxies. `id` is a
// stable slug (lowercase alphanumerics, no dashes — see model.ValidLaneID) that
// routing_order references.
export interface EgressLane {
  id: string
  name: string
  enabled: boolean
  urls: string[]
  manual: string[]
  domains: string[]
  ips: string[]
}

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
  lanes: EgressLane[]
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
  proxy_count: number // total live proxies across every lane
  proxy_counts: Record<string, number> // live proxies per lane id
}

export interface GeoCategories {
  geosite: string[]
  geoip: string[]
  // iplist group names ("russia/vk", "global/youtube"), referenced in routing
  // rules as "iplist:<name>". Empty when the iplist databases aren't downloaded.
  iplist: string[]
}

export const getGeoCategories = () => api<GeoCategories>('api/geo/categories')

export interface GeoFile {
  name: string
  present: boolean
  size: number
  modified_at: number
}

// GeoInfo is the databases' status plus each set's own auto-refresh cadence
// (hours; 0 = off). The iplist fields are panel-only — a node reports just the geo
// .dat files it actually reads, so they are absent there.
export interface GeoInfo {
  files: GeoFile[]
  iplist_files?: GeoFile[]
  refresh_hours: number
  iplist_refresh_hours?: number
}

export const getGeoStatus = () => api<GeoInfo>('api/geo')
export const updateGeo = () => api<GeoInfo>('api/geo/update', { method: 'POST' })
export const updateIPLists = () => api<GeoInfo>('api/geo/lists/update', { method: 'POST' })

// setIPListCadence sets how often the iplist lists auto-refresh (hours; 0 = never).
export const setIPListCadence = (refresh_hours: number) =>
  api<{ ok: boolean }>('api/geo/lists/cadence', {
    method: 'POST',
    body: JSON.stringify({ refresh_hours }),
  })

// setGeoCadence sets how often the geo databases auto-refresh (hours; 0 = never).
export const setGeoCadence = (refresh_hours: number) =>
  api<{ ok: boolean }>('api/geo/cadence', {
    method: 'POST',
    body: JSON.stringify({ refresh_hours }),
  })

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
  user_reg_mode: RegMode // off | open | moderation | invite
  user_reg_code: string // invite code (mode === 'invite')
  user_bot_username: string // user bot @username
  admin_events: Record<string, boolean> // admin notification categories (key→on)
}

// Self-registration modes for the public user bot.
export type RegMode = 'off' | 'open' | 'moderation' | 'invite'

export const getTelegram = () => api<TelegramInfo>('api/telegram')

export const saveTelegram = (
  enabled: boolean,
  token: string,
  backup_cron: string,
  user_enabled: boolean,
  user_token: string,
  user_reg_mode: RegMode,
  user_reg_code: string,
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
      user_reg_mode,
      user_reg_code,
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

// Moderated self-registration queue: signups awaiting an admin decision. No user
// exists until a request is approved.
export interface RegistrationRequest {
  id: number
  chat_id: number
  name: string
  created_at: number
}

export const getRegistrations = () =>
  api<{ moderation: boolean; requests: RegistrationRequest[] }>(
    'api/registrations',
  )

export const approveRegistration = (id: number) =>
  api<{ ok: boolean }>(`api/registrations/${id}/approve`, { method: 'POST' })

export const rejectRegistration = (id: number) =>
  api<{ ok: boolean }>(`api/registrations/${id}/reject`, { method: 'POST' })

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

export type HealthStatus = 'ok' | 'warn' | 'error' | 'info'

export interface HealthCheck {
  key: string
  label: string
  status: HealthStatus
  detail: string
  hint?: string
}

export interface HealthReport {
  status: 'ok' | 'warn' | 'error'
  checks: HealthCheck[]
}

export const getHealth = () => api<HealthReport>('api/health')

export interface SelfTestResult {
  proto: string
  label: string
  ok: boolean
  detail: string
  exit_ip?: string
}

// runSelfTest connects to each enabled protocol as a real client and reports
// whether traffic flows end-to-end. Slow (spawns a client per protocol), so the
// caller shows a spinner; the request itself is bounded server-side.
export const runSelfTest = () =>
  api<{ results: SelfTestResult[] }>('api/health/selftest', { method: 'POST' })

export type BulkAction = 'enable' | 'disable' | 'reset' | 'extend' | 'delete'

// bulkUsers applies one action to many users in a single server pass (one Xray
// sync). `days` is only used by the "extend" action. Returns how many were changed.
export const bulkUsers = (ids: number[], action: BulkAction, days = 0) =>
  api<{ affected: number }>('api/users/bulk', {
    method: 'POST',
    body: JSON.stringify({ ids, action, days }),
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
  plan_users?: Record<string, number> // plan id → number of users on it
}

export const getBilling = () => api<BillingInfo>('api/billing')

// A payment provider's settings form is described by the server (internal/payments
// registry), so adding a provider needs no frontend change — the form renders from
// these fields. Secret fields never carry their value: only `is_set` says whether
// one is stored; sending an empty secret keeps the current one.
export type PaymentFieldKind = 'text' | 'secret' | 'bool' | 'select'

export interface PaymentField {
  key: string
  label: string
  kind: PaymentFieldKind
  placeholder?: string
  help?: string
  optional?: boolean
  value?: string | boolean // text/select → string, bool → boolean; absent for secrets
  is_set?: boolean // secrets only: whether a value is stored
  options?: { value: string; label: string }[] // select only
}

export interface PaymentProvider {
  key: string
  label: string
  note: string
  enabled: boolean
  fields: PaymentField[]
  webhook_url: string
}

export const getPayments = () =>
  api<{ providers: PaymentProvider[] }>('api/payments')

export const savePaymentProvider = (p: {
  key: string
  enabled: boolean
  config: Record<string, string> // secrets: empty = keep current; bools: '1' | ''
}) =>
  api<{ providers: PaymentProvider[] }>('api/payments', {
    method: 'POST',
    body: JSON.stringify(p),
  })

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

export const migratePlanUsers = (id: number, toPlanId: number) =>
  api<{ migrated: number }>(`api/billing/plans/${id}/migrate`, {
    method: 'POST',
    body: JSON.stringify({ to_plan_id: toPlanId }),
  })

export const listPaymentOrders = (status?: string) =>
  api<PaymentOrder[]>(`api/billing/orders${status ? `?status=${status}` : ''}`)

export interface ProviderStat {
  provider: string
  count: number
  sum: number
}

export interface PaymentStats {
  total_paid: number
  paid_count: number
  earned_today: number
  earned_month: number
  pending_count: number
  pending_sum: number
  by_provider: ProviderStat[]
}

export const getPaymentStats = () => api<PaymentStats>('api/payments/stats')

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

// ---- External REST API (keys + surface) ----

export interface ApiKey {
  id: number
  name: string
  prefix: string
  created_at: number
  last_used_at: number
  revoked_at: number
  raw_key?: string // only present in the create response
}

export interface ApiKeysInfo {
  enabled: boolean
  api_path: string
  base_url: string
  keys: ApiKey[]
}

export const getApiKeys = () => api<ApiKeysInfo>('api/apikeys')

export const createApiKey = (name: string) =>
  api<{ key: ApiKey; base_url: string }>('api/apikeys', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })

export const revokeApiKey = (id: number) =>
  api<{ ok: boolean }>(`api/apikeys/${id}`, { method: 'DELETE' })

export const setApiPath = (enabled: boolean, rotate = false) =>
  api<{ enabled: boolean; api_path: string; base_url: string }>(
    'api/settings/api-path',
    { method: 'POST', body: JSON.stringify({ enabled, rotate }) },
  )

// ---- Webhooks ----

export interface Webhook {
  id: number
  url: string
  secret: string
  events: string[]
  enabled: boolean
  created_at: number
  last_status: number
  last_attempt_at: number
  last_error: string
}

export interface WebhookEventDef {
  key: string
  label: string
}

export interface WebhooksInfo {
  webhooks: Webhook[]
  events: WebhookEventDef[]
}

export const getWebhooks = () => api<WebhooksInfo>('api/webhooks')

export const createWebhook = (url: string, events: string[]) =>
  api<Webhook>('api/webhooks', {
    method: 'POST',
    body: JSON.stringify({ url, events }),
  })

export const updateWebhook = (
  id: number,
  url: string,
  events: string[],
  enabled: boolean,
) =>
  api<{ ok: boolean }>(`api/webhooks/${id}`, {
    method: 'POST',
    body: JSON.stringify({ url, events, enabled }),
  })

export const deleteWebhook = (id: number) =>
  api<{ ok: boolean }>(`api/webhooks/${id}`, { method: 'DELETE' })

export const testWebhook = (id: number) =>
  api<{ status: number; ok: boolean; error?: string }>(
    `api/webhooks/${id}/test`,
    { method: 'POST' },
  )

// --- Nodes (multi-node) -----------------------------------------------------

// NodeView is one row of the Nodes page. The local server is node 0 (is_local).
export interface NodeView {
  id: number
  name: string
  host: string
  enabled: boolean
  is_local: boolean
  online: boolean
  joined: boolean
  last_seen: number
  node_version: string
  xray_version: string
  xray_running: boolean
  version_skew: boolean
  vless_enabled: boolean
  trojan_enabled: boolean
  hysteria_enabled: boolean
  reality_enabled: boolean
  decoy_template: string
  cert_self_signed: boolean // true = still on the self-signed fallback (no CA cert yet)
  cert_issuer: string // ≈ ACME provider that signed the cert (empty for the master)
  cert_expires_at: number // unix; 0 = unknown
  geo_refresh_hours: number // this server's own geo auto-refresh cadence (0 = never)
  traffic_up: number
  traffic_down: number
  routing: RoutingConfig | null // node's own routing, null = not configured (direct)
  xray_dns: string | null // node's own DNS, null = not configured (default resolver)
  // Per-node egress (independent of the master; all off by default). For the local
  // node (master) these carry the panel's own settings.
  warp_enabled: boolean
  warp_registered: boolean
  opera_enabled: boolean
  opera_country: string
  // REALITY identity (per-server). reality_dest "" on a node = inherits the master's
  // donor. The public key / short id / gRPC service are shown; private key is hidden.
  reality_dest: string
  reality_public_key: string
  reality_short_id: string
  reality_service_name: string
  master_label?: string // config-label name of the master (local node only)
}

export const listNodes = () => api<{ nodes: NodeView[] }>('api/nodes')

// setMasterName sets the panel server's display name shown in config labels.
export const setMasterName = (name: string) =>
  api<{ ok: boolean }>('api/nodes/master-name', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })

// setMasterProtocols toggles the panel's own protocols on/off (the master card).
// Connection details stay in the global Подключения settings.
export const setMasterProtocols = (p: {
  vless_enabled: boolean
  trojan_enabled: boolean
  hysteria_enabled: boolean
  reality_enabled: boolean
}) =>
  api<{ ok: boolean }>('api/nodes/master-protocols', {
    method: 'POST',
    body: JSON.stringify(p),
  })

// setMasterReality / setNodeReality set a server's REALITY donor (masquerade SNI) and,
// when regen is true, regenerate its REALITY keys (invalidates that server's links).
export const setMasterReality = (dest: string, regen: boolean) =>
  api<{ ok: boolean }>('api/nodes/master-reality', {
    method: 'POST',
    body: JSON.stringify({ dest, regen }),
  })

export const setNodeReality = (id: number, dest: string, regen: boolean) =>
  api<{ ok: boolean }>(`api/nodes/${id}/reality`, {
    method: 'POST',
    body: JSON.stringify({ dest, regen }),
  })

// refreshNodeGeo asks a node to re-download its geo databases now.
export const refreshNodeGeo = (id: number) =>
  api<{ ok: boolean }>(`api/nodes/${id}/geo-refresh`, { method: 'POST' })

// setNodeGeoCadence sets a node's own geo auto-refresh cadence (hours; 0 = never).
export const setNodeGeoCadence = (id: number, refresh_hours: number) =>
  api<{ ok: boolean }>(`api/nodes/${id}/geo-cadence`, {
    method: 'POST',
    body: JSON.stringify({ refresh_hours }),
  })

// getNodeGeo returns a node's geo file status + its cadence, for the node Geo tab.
export const getNodeGeo = (id: number) => api<GeoInfo>(`api/nodes/${id}/geo`)

// Node TLS/ACME — same shape/UI as the master's domain page.
export const getNodeTLS = (id: number) => api<TLSStatus>(`api/nodes/${id}/tls`)
export const setNodeACME = (
  id: number,
  target: string,
  email: string,
  provider: string,
) =>
  api<TLSStatus>(`api/nodes/${id}/tls`, {
    method: 'POST',
    body: JSON.stringify({ target, email, provider }),
  })

export const createNode = (name: string, host: string) =>
  api<{ id: number; install_command: string }>('api/nodes', {
    method: 'POST',
    body: JSON.stringify({ name, host }),
  })

// NodePatch carries a node edit (name/host/decoy). Protocols are edited on the
// Подключения tab and are OPTIONAL here: omitting them tells the panel to preserve the
// node's current values, so a name/decoy save can't revert a just-made protocol change.
export interface NodePatch {
  name: string
  host: string
  decoy_template: string
  vless_enabled?: boolean
  trojan_enabled?: boolean
  hysteria_enabled?: boolean
  reality_enabled?: boolean
}

export const updateNode = (id: number, patch: NodePatch) =>
  api<{ ok: boolean }>(`api/nodes/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  })

export const setNodeEnabled = (id: number, enabled: boolean) =>
  api<{ ok: boolean }>(`api/nodes/${id}/enabled`, {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  })

export const deleteNode = (id: number) =>
  api<{ ok: boolean }>(`api/nodes/${id}`, { method: 'DELETE' })

export const regenNodeJoin = (id: number) =>
  api<{ install_command: string }>(`api/nodes/${id}/regen-join`, {
    method: 'POST',
  })

export const updateNodeVersion = (id: number) =>
  api<{ ok: boolean }>(`api/nodes/${id}/update`, { method: 'POST' })

export const updateAllNodes = () =>
  api<{ nodes: number }>('api/nodes/update-all', { method: 'POST' })

// getNodeLogs returns a node's recent log tail; polling it also asks the node to
// send fresh logs on its next sync.
export const getNodeLogs = (id: number) =>
  api<{ lines: string[]; at: number }>(`api/nodes/${id}/logs`)

// setNodeRouting saves a node's routing + egress override. A null routing means
// "inherit the panel's"; egress (WARP/Opera) is the node's own. DNS has its own
// endpoint (setNodeDNS). Mirrors the master's saveRouting shape.
export const setNodeRouting = (
  id: number,
  routing: RoutingConfig | null,
  warpEnabled: boolean,
  operaEnabled: boolean,
  operaCountry: string,
) =>
  api<{ ok: boolean }>(`api/nodes/${id}/routing`, {
    method: 'POST',
    body: JSON.stringify({
      routing,
      warp_enabled: warpEnabled,
      opera_enabled: operaEnabled,
      opera_country: operaCountry,
    }),
  })

// setNodeDNS saves a node's own DNS override (null ⇒ inherit the panel's), independent
// of routing.
export const setNodeDNS = (id: number, xray_dns: string | null) =>
  api<{ ok: boolean }>(`api/nodes/${id}/dns`, {
    method: 'POST',
    body: JSON.stringify({ xray_dns }),
  })

// ProvisionCreds are the throwaway SSH credentials used to install a node over SSH.
export interface ProvisionCreds {
  ssh_host: string
  ssh_port: number
  ssh_user: string
  ssh_password?: string
  ssh_key?: string
  ssh_key_passphrase?: string
}

// provisionNode installs a created node onto a remote server over SSH, streaming
// the install log line-by-line to onLine. Resolves "done" or "error" when the
// stream ends. The response is an SSE stream over a POST (so credentials go in the
// body, not the URL), read here with a stream reader rather than EventSource.
export async function provisionNode(
  id: number,
  creds: ProvisionCreds,
  onLine: (line: string) => void,
): Promise<'done' | 'error'> {
  const res = await fetch(`api/nodes/${id}/provision`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify(creds),
  })
  if (res.status === 401) onUnauthorized?.()
  if (!res.ok || !res.body) {
    const text = await res.text().catch(() => '')
    throw new Error(text || `HTTP ${res.status}`)
  }
  const reader = res.body.getReader()
  const dec = new TextDecoder()
  let buf = ''
  let outcome: 'done' | 'error' = 'error'
  const handle = (frame: string) => {
    const line = frame.replace(/^data: ?/, '').trim()
    if (line === 'event:done') outcome = 'done'
    else if (line === 'event:error') outcome = 'error'
    else if (line) onLine(line)
  }
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += dec.decode(value, { stream: true })
    // SSE frames are separated by a blank line; each carries a "data: <text>" line.
    const frames = buf.split('\n\n')
    buf = frames.pop() ?? ''
    for (const f of frames) handle(f)
  }
  // Flush a trailing frame if the stream ended without a final blank line, so a
  // terminal "event:done"/"event:error" isn't dropped (false 'error' on success).
  if (buf.trim()) handle(buf)
  return outcome
}
