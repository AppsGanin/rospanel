// API client. All paths are RELATIVE so they resolve against <base href="/<secret>/">
// injected by the Go server — the SPA never needs to know its own secret path.
import i18n from './i18n'

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
  speed_limit: number // kbit/s, 0 = unlimited
  // The measure the panel holds against the user for blocklist traffic, and
  // when it lifts. Absent when there is none.
  abuse_action?: 'throttle' | 'disable'
  abuse_until?: number
  plan_id: number
  plan_name?: string
  telegram_linked?: boolean
  telegram_link?: string
  telegram_deep_link?: string
  tg_chat_id?: number // linked Telegram chat/user id (0 = not linked)
  system_email: string // Xray client id "u<id>" (logs/stats)
  sub_url: string
  vless: string
  hysteria2: string
  reality: string
  // Every lane this user has on the master, built-in and custom, in client order.
  // The three fields above are the built-in lanes kept as their own keys; a custom
  // inbound can only appear here.
  links: { name: string; url: string }[]
  // Groups the user belongs to (empty ⇒ access to everything).
  groups: GroupRef[]
  // The operator's own annotations: a free-text note (panel-only) and a normalised
  // tag list (lower-cased, sorted) for filtering.
  note: string
  tags: string[]
}

export interface GroupRef {
  id: number
  name: string
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

// Device is one client install bound to the account by the id it sends in the
// x-hwid subscription header. Distinct from Connection above: that one is an IP the
// tunnel was used from, this one is an app that fetched the subscription.
export interface Device {
  hwid: string
  os: string
  os_version: string
  model: string
  app: string
  ip: string
  first_seen: number
  last_seen: number
}

export interface DeviceList {
  devices: Device[]
  limit: number // 0 = unlimited
  enabled: boolean // device binding switched on panel-wide
}

export const getUserDevices = (id: number) => api<DeviceList>(`api/users/${id}/devices`)

// Releases one device slot, or every one of them with all=true.
export const unbindUserDevice = (id: number, body: { hwid?: string; all?: boolean }) =>
  api<{ removed: number }>(`api/users/${id}/devices/unbind`, {
    method: 'POST',
    body: JSON.stringify(body),
  })


// AbuseMatch is one destination that hit a blocklist. These are PERSISTED (a short
// window) and are the sensitive part of this feature: they name what an account
// reached, not merely that it connected. category is one of
// custom/malware/badip/piracy/gambling.
export interface AbuseMatch {
  user_id: number
  user_name?: string
  node_id: number
  // The destination that matched — an IP address. Named `domain` because that is the
  // column it has been stored in since before matching was narrowed to addresses;
  // rows written back then may still hold a hostname.
  domain: string
  category: string
  day: string
  count: number
  last_seen: number
}

// A function rather than a table: a table would capture whichever language was
// active when this module was first imported and never update.
export function abuseCategoryLabel(category: string): string {
  switch (category) {
    case 'custom':
      return i18n.t('abuseCat.custom')
    case 'malware':
      return i18n.t('abuseCat.malware')
    case 'badip':
      return i18n.t('abuseCat.badip')
    case 'piracy':
      return i18n.t('abuseCat.piracy')
    case 'gambling':
      return i18n.t('abuseCat.gambling')
    default:
      return category
  }
}

export const getRecentAbuse = (limit = 50) =>
  api<AbuseMatch[]>(`api/stats/abuse?limit=${limit}`)

export const getUserAbuse = (id: number, limit = 20) =>
  api<AbuseMatch[]>(`api/users/${id}/abuse?limit=${limit}`)

// AbuseFeedStatus is one category's live state: whether it's on, how many entries
// are loaded in the matcher, and (for downloaded feeds) the cached copy's size/age.
export interface AbuseFeedStatus {
  category: string
  title_key: string
  enabled: boolean
  present: boolean
  entries: number
  size?: number
  updated?: number
}

// AbuseMeasures is the ladder of automatic responses (model.AbuseMeasures): a
// per-rung matches/day threshold, 0 = that rung is off.
export interface AbuseMeasures {
  warn_min: number
  throttle_min: number
  throttle_kbps: number
  disable_min: number
  hours: number
}

export const EMPTY_ABUSE_MEASURES: AbuseMeasures = {
  warn_min: 0,
  throttle_min: 0,
  throttle_kbps: 1024,
  disable_min: 0,
  hours: 24,
}

export interface AbuseSettingsInfo {
  enabled: boolean
  categories: Record<string, boolean>
  custom: string
  alert_min: number
  measures: AbuseMeasures
  status: AbuseFeedStatus[]
}

export const getAbuseSettings = () =>
  api<AbuseSettingsInfo>('api/settings/abuse')

export const saveAbuseSettings = (cfg: {
  enabled: boolean
  categories: Record<string, boolean>
  custom: string
  alert_min: number
  measures: AbuseMeasures
}) =>
  api<{ ok: boolean }>('api/settings/abuse', {
    method: 'POST',
    body: JSON.stringify(cfg),
  })

export const refreshAbuseFeeds = () =>
  api<{ ok: boolean }>('api/settings/abuse/refresh', { method: 'POST' })

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

// One page of the journal, sent explicitly rather than left to the API default (50,
// core.EventPageLimit) so the panel's lists all open at the same depth. Here the
// number is what actually travels per click — the button below the list fetches the
// next page by cursor — unlike the client-side lists, which get everything at once.
export const EVENT_PAGE = 20

export const getUserEvents = (id: number, before = 0) =>
  api<EventPage>(`api/users/${id}/events${eventQuery({ before, limit: EVENT_PAGE })}`)

export const listEvents = (f: EventFilter = {}) =>
  api<EventPage>(`api/events${eventQuery({ limit: EVENT_PAGE, ...f })}`)

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
  protocols: ConnInfo[]
  hysteria_port: number
  hop_start: number
  hop_end: number
  hop_interval: string
  reality_port: number
  reality_dest: string
  reality_public_key: string
  reality_short_id: string
  reality_path: string // secret XHTTP request path of the REALITY lane
  reality_anti_replay: boolean
  tls_fragment: boolean
  tls_min13: boolean
  block_quic: boolean
  // AmneziaWG (the tunnel's port, the server's public key and obfuscation
  // parameters, in-tunnel DNS); awg_running/awg_error describe the master's own
  // tunnel and mean nothing for a node.
  awg_port: number
  awg_public_key: string
  awg_params: { jc: number; jmin: number; jmax: number; s1: number; s2: number; h1: number; h2: number; h3: number; h4: number }
  awg_dns: string
  awg_running: boolean
  awg_error?: string
}

// ConnectionsUpdate is the whole connection surface, applied in one request.
export interface ConnectionsUpdate {
  protocols: Record<string, boolean>
  fingerprints: Record<string, string>
  names: Record<string, string>
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
  awg_port: number
  awg_dns: string
  regen_awg_keys: boolean
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
  online_users: number // carrying traffic right now (fleet-wide, 2-minute window)
  traffic_today: number
  cert_days_left: number
}

export const getXrayConfig = (): Promise<string> => apiText('api/xray/config')

export interface XrayStatus {
  running: boolean
  started_at: number // unix; advances on every Xray (re)start
  // unix; advances on every config apply, INCLUDING the ones that changed nothing
  // and so did not restart. Absent on a panel older than this field.
  applied_at?: number
}

export const getXrayStatus = () => api<XrayStatus>('api/xray/status')

// Bounces the Xray process. Drops every live VPN connection — confirm first.
export const restartXray = () =>
  api<XrayStatus>('api/xray/restart', { method: 'POST' })

// Restarts the panel process itself (the service manager brings it back, Xray with
// it). The response is sent before the process goes down — confirm first.
export const restartPanel = () => api<{ ok: boolean }>('api/panel/restart', { method: 'POST' })

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
  issue: string // dictionary key naming the problem when !valid
}

const backupForm = (file: File, currentPassword?: string) => {
  const fd = new FormData()
  fd.append('backup', file)
  // The restore endpoint re-authenticates (it replaces the admin roster this session
  // is authenticated against); the inspect endpoint reads nothing and does not.
  if (currentPassword !== undefined) fd.append('current_password', currentPassword)
  return fd
}

export const inspectBackup = (file: File) =>
  apiForm<BackupInspection>('api/backup/inspect', backupForm(file))

export const restoreBackup = (file: File, currentPassword: string) =>
  apiForm<{ ok?: boolean }>('api/restore', backupForm(file, currentPassword)).then(() => {})

// resetPanel wipes all state and restarts the panel into first-run mode. It
// returns the URL the panel will come back on (auto-detected IP + default path),
// which may differ from the current address (e.g. a custom domain).
export const resetPanel = (currentPassword: string) =>
  api<{ url: string }>('api/reset', {
    method: 'POST',
    body: JSON.stringify({ current_password: currentPassword }),
  })

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
  // Omitted leaves the speed cap untouched — the server reads a missing field as
  // "no opinion", not as "unlimited".
  speed_limit?: number,
) =>
  api<{ ok: boolean }>(`api/users/${id}/limits`, {
    method: 'POST',
    body: JSON.stringify({ data_limit, expire_at, device_limit, speed_limit }),
  })
export const setUserEnabled = (id: number, enabled: boolean) =>
  api<{ ok: boolean }>(`api/users/${id}/enabled`, {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  })
export const setUserNote = (id: number, note: string) =>
  api<{ ok: boolean }>(`api/users/${id}/note`, {
    method: 'POST',
    body: JSON.stringify({ note }),
  })
export const setUserTags = (id: number, tags: string[]) =>
  api<{ ok: boolean }>(`api/users/${id}/tags`, {
    method: 'POST',
    body: JSON.stringify({ tags }),
  })
// Every tag in use with how many users carry it, most used first.
export interface TagCount {
  tag: string
  count: number
}
export const listUserTags = () => api<TagCount[]>('api/users/tags')
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
// NodeTraffic is one server's share of a period's traffic. node_id 0 is the panel's
// own server; the name is resolved server-side so no node list is needed here.
export type NodeTraffic = {
  node_id: number
  name: string
  up: number
  down: number
}
export const getNodeTraffic = (p: { user_id?: number; from?: string; to?: string }) => {
  const q = new URLSearchParams()
  if (p.user_id) q.set('user_id', String(p.user_id))
  if (p.from) q.set('from', p.from)
  if (p.to) q.set('to', p.to)
  return api<NodeTraffic[]>('api/stats/nodes?' + q.toString())
}
export const getStatsByUser = (from?: string, to?: string) => {
  const q = new URLSearchParams()
  if (from) q.set('from', from)
  if (to) q.set('to', to)
  return api<UserTotal[]>('api/stats/users?' + q.toString())
}
export const resetStats = () => api<{ ok: boolean }>('api/stats/reset', { method: 'POST' })

// Connection geo breakdown: distinct source IPs per country over the connection
// retention window. code is a lowercase 2-letter country code, "" = unknown/private.
export interface CountryStat {
  code: string
  ips: number
  hits: number
}

export const getStatsCountries = () => api<CountryStat[]>('api/stats/countries')

// Connection breakdown by network operator (ASN). asn 0 / org '' = unknown/private.
export interface ASNStat {
  asn: number
  org: string
  ips: number
  hits: number
}

export const getStatsASNs = () => api<ASNStat[]>('api/stats/asns')

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

// ApiError carries the server's dictionary code alongside its message. errMessage
// (notify.ts) translates the code and falls back to the message, so every existing
// `notifyError(errMessage(e))` call site gets a localised toast without changing.
export class ApiError extends Error {
  constructor(
    message: string,
    readonly code?: string,
    readonly args?: Record<string, unknown>,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

// apiError builds the thrown value from a parsed error body.
function apiError(data: Record<string, unknown>, status: number): ApiError {
  const msg = typeof data.error === 'string' ? data.error : `HTTP ${status}`
  const code = typeof data.code === 'string' ? data.code : undefined
  const args =
    data.args && typeof data.args === 'object'
      ? (data.args as Record<string, unknown>)
      : undefined
  return new ApiError(msg, code, args)
}

// parseBody turns a response body into the object the callers expect, and refuses to
// let a non-JSON one reach them as a parser error.
//
// A body that is not JSON means the request was answered by something other than the
// API — a proxy's page, or the panel's own SPA shell when the endpoint no longer
// exists. `JSON.parse` then throws "Unexpected token '<', \"<!doctype\"... is not
// valid JSON", which names neither the request nor the cause; the one operator who hit
// it concluded the fault was theirs. The status is consulted first, so a plain HTTP
// error still reports as that error rather than as a parse failure.
function parseBody(text: string, status: number, ok: boolean): Record<string, unknown> {
  if (!text) return {}
  try {
    return JSON.parse(text) as Record<string, unknown>
  } catch {
    if (!ok) throw new ApiError(`HTTP ${status}`, undefined, undefined)
    throw new ApiError('', 'err.staleTab', undefined)
  }
}

async function api<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...opts,
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER, ...(opts.headers || {}) },
  })
  if (res.status === 401) onUnauthorized?.()
  const data = parseBody(await res.text(), res.status, res.ok)
  if (!res.ok) throw apiError(data, res.status)
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
  const data = parseBody(await res.text(), res.status, res.ok)
  if (!res.ok) throw apiError(data, res.status)
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

export const roleLabel = (r: Role): string => i18n.t(`roles.${r}`)

export const roleHint = (r: Role): string =>
  i18n.t(`roles.${r}Desc` as 'roles.ownerDesc')

export interface Me {
  username: string
  role: Role
  setup_done: boolean
  timezone: string
  version: string
  must_change_password?: boolean
  billing_enabled?: boolean
  user_bot_enabled?: boolean
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

// The journal filters by category ("Settings", "Administrators", …) rather than by
// each of the two dozen actions: the actions stay precise on the rows, the filter
// stays short.
export interface AdminAuditFilter {
  category?: string
  action?: string
  actor?: string
  search?: string // free-text over action/target/actor/ip/details
  from?: number // created_at >= this (unix seconds)
  to?: number // created_at <= this (unix seconds)
}

// adminAuditQuery renders the shared filter into query params so the paged list and
// the CSV export can never diverge on what "the current filter" means.
function adminAuditQuery(f: AdminAuditFilter): URLSearchParams {
  const q = new URLSearchParams()
  if (f.category) q.set('category', f.category)
  if (f.action) q.set('action', f.action)
  if (f.actor) q.set('actor', f.actor)
  if (f.search) q.set('search', f.search)
  if (f.from) q.set('from', String(f.from))
  if (f.to) q.set('to', String(f.to))
  return q
}

export const listAdminAudit = (
  params: AdminAuditFilter & { before?: number; limit?: number },
) => {
  const q = adminAuditQuery(params)
  if (params.before) q.set('before', String(params.before))
  if (params.limit) q.set('limit', String(params.limit))
  const qs = q.toString()
  return api<AdminAuditPage>(`api/admin-audit${qs ? `?${qs}` : ''}`)
}

// adminAuditExportURL is the relative href for the CSV download (owner cookie auth,
// so a plain <a download> works). Carries the same filter as the on-screen list.
export const adminAuditExportURL = (f: AdminAuditFilter): string => {
  const qs = adminAuditQuery(f).toString()
  return `api/admin-audit/export${qs ? `?${qs}` : ''}`
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
  // Render the "individual configs" card on the subscription page (the raw share
  // link of every lane). On by default; off leaves the page offering the
  // subscription link and the client buttons only.
  sub_show_configs: boolean
  // How servers are ordered in a subscription: manual | nearest | load | nearest_load.
  sub_order_mode: string
  // Drop a node from subscriptions while it is offline (off by default).
  sub_hide_offline: boolean
}

// HWIDSettings gates device binding: which installs may fetch the subscription and
// how long a silent one keeps its slot.
export interface HWIDSettings {
  enabled: boolean
  require: boolean // refuse clients that send no x-hwid at all
  fallback_limit: number // cap for users whose own device limit is 0
  ttl_days: number // forget a device after N days of silence (0 = never)
  // Which counter enforces the device limit: "auto" (addresses only while HWID is not
  // authoritative), "hwid" or "both".
  count_mode: string
}

export const saveHWIDSettings = (s: HWIDSettings) =>
  api<{ ok: boolean }>('api/settings/hwid', { method: 'POST', body: JSON.stringify(s) })

export interface SettingsInfo extends SubSettings {
  sub_dpi?: SubDPI
  secret_path: string
  decoy_template: string
  decoy_templates: string[]
  xray_dns: string
  warp_enabled: boolean
  warp_registered: boolean
  local_backup_cron: string
  local_backup_keep: number
  user_autodelete_days: number
  maintenance_mode: boolean
  probe_detect: boolean
  probe_block: boolean
  watchdog: WatchdogInfo
  hwid: HWIDSettings
}

// WatchdogInfo is the wedged-Xray auto-recovery state (master).
export interface WatchdogInfo {
  enabled: boolean
  restarts: number
  last_at: number // unix seconds, 0 = never fired
}

// StatusPageSettings controls the public status page: the one surface that answers
// to a caller holding no token, so it is off until an operator turns it on.
export interface StatusPageSettings {
  enabled: boolean
  path: string
}

export const getStatusPage = () => api<StatusPageSettings>('api/settings/status-page')

export const saveStatusPage = (s: StatusPageSettings) =>
  api<{ ok: boolean }>('api/settings/status-page', {
    method: 'POST',
    body: JSON.stringify(s),
  })

export const setUserAutoDelete = (days: number) =>
  api<{ ok: boolean }>('api/settings/autodelete', {
    method: 'POST',
    body: JSON.stringify({ user_autodelete_days: days }),
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
  // Loopback address anything on the box can dial to leave through that lane, "" when
  // the lane is off. Paste-able into the Telegram proxy field (or anywhere else).
  warp_proxy_url?: string
  opera_proxy_url?: string
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

// ---- Client-side DPI evasion handed out by the subscription ------------------
export interface SubDPI {
  json_clients: boolean
  fragment: boolean
  fragment_packets: string // tlshello | 1-1 | 1-3
  fragment_length: string // "100-200"
  fragment_interval: string // "10-20"
  noise: boolean
  noise_type: string // rand | str | base64
  noise_packet: string
  noise_delay: string
  record_fragment: boolean
}
export const DEFAULT_SUB_DPI: SubDPI = {
  json_clients: false,
  fragment: false,
  fragment_packets: 'tlshello',
  fragment_length: '100-200',
  fragment_interval: '10-20',
  noise: false,
  noise_type: 'rand',
  noise_packet: '10-20',
  noise_delay: '10-16',
  record_fragment: false,
}
export const saveSubDPI = (d: SubDPI) =>
  api<{ ok: boolean }>('api/settings/sub-dpi', { method: 'POST', body: JSON.stringify(d) })

export const saveSubSettings = (s: SubSettings) =>
  api<{ ok: boolean }>('api/settings/subscription', {
    method: 'POST',
    body: JSON.stringify(s),
  })

// Subscription response rules: evaluated in order before format auto-detection.
export interface SubRule {
  field: 'user_agent' | 'device_os' | 'ver_os' | 'device_model'
  op: 'contains' | 'equals' | 'prefix' | 'regex' | 'not_contains'
  value: string
  action: 'v2ray' | 'clash' | 'singbox' | 'xray-json' | 'block'
  enabled: boolean
}

export const getSubRules = () =>
  api<{ rules: SubRule[] }>('api/settings/sub-rules').then((r) => r.rules)

export const saveSubRules = (rules: SubRule[]) =>
  api<{ ok: boolean }>('api/settings/sub-rules', {
    method: 'POST',
    body: JSON.stringify({ rules }),
  })

export const saveMaintenance = (enabled: boolean) =>
  api<{ ok: boolean }>('api/settings/maintenance', {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  })

// Secret-path probe detection: record IPs that scan for the hidden panel.
export const saveProbeDetect = (enabled: boolean) =>
  api<{ ok: boolean }>('api/settings/probe-detect', {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  })

// Drop flagged scanner IPs at the firewall (nftables).
export const saveProbeBlock = (enabled: boolean) =>
  api<{ ok: boolean }>('api/settings/probe-block', {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  })

// Wedged-process auto-recovery toggle.
export const saveWatchdog = (enabled: boolean) =>
  api<{ ok: boolean }>('api/settings/watchdog', {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  })

export interface ProbeHit {
  ip: string
  first_seen: number
  last_seen: number
  hits: number
  paths: number
  // Derived from the geo tables on read, so all three are absent when those tables
  // are missing or cover no range for the address.
  country?: string
  asn?: number
  org?: string
}

export const getProbes = () =>
  api<{ probes: ProbeHit[]; retention_days: number }>('api/security/probes')

// Routing/egress config snapshots (undo a change that broke the tunnels).
export interface ConfigSnapshot {
  id: number
  created_at: number
  label: string
  auto: boolean
}

export const getConfigSnapshots = () =>
  api<{ snapshots: ConfigSnapshot[] }>('api/config/snapshots').then((r) => r.snapshots)

export const createConfigSnapshot = (label: string) =>
  api<{ ok: boolean }>('api/config/snapshots', { method: 'POST', body: JSON.stringify({ label }) })

export const rollbackConfigSnapshot = (id: number) =>
  api<{ ok: boolean }>(`api/config/snapshots/${id}/rollback`, { method: 'POST' })

export const deleteConfigSnapshot = (id: number) =>
  api<{ ok: boolean }>(`api/config/snapshots/${id}`, { method: 'DELETE' })

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
  // Language the ADMIN bot writes in. Panel-wide, unlike the client and support
  // bots, which follow each person's own Telegram language: the admin bot also
  // pushes unprompted alerts, which carry no update to read a language from.
  lang: string
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
  // What the USER bot tells the person themselves, and how many days ahead the
  // expiry warning goes out.
  user_events: Record<string, boolean>
  user_expiring_days: number

  // Support relay: a third bot that carries messages between a user's chat and a
  // per-user topic in support_group_id (a forum supergroup the admins answer in).
  support_enabled: boolean
  support_token: string
  support_group_id: number // 0 = not configured
  support_greeting: string // shown on /start; empty = built-in text
  support_bot_username: string // resolved on save; empty ⇒ entry point stays hidden

  // How everything Telegram-bound is routed — all three bots and the server-side
  // fetch of the Mini App SDK. proxy is the URL the 'custom' mode uses; it survives a
  // switch to 'direct' so a typed address is not lost. To send Telegram through WARP
  // or Opera, paste the address the Routing page publishes for that lane.
  proxy_mode: 'direct' | 'custom'
  proxy: string
}

// Self-registration modes for the public user bot.
export type RegMode = 'off' | 'open' | 'moderation' | 'invite'

export const getTelegram = () => api<TelegramInfo>('api/telegram')

// Takes an object rather than a positional list: the three bots contribute a dozen
// fields, half of them same-typed, and a swapped token argument would fail silently.
export const saveTelegram = (t: {
  enabled: boolean
  token: string
  backup_cron: string
  lang: string
  user_enabled: boolean
  user_token: string
  user_reg_mode: RegMode
  user_reg_code: string
  admin_events: Record<string, boolean>
  user_events: Record<string, boolean>
  user_expiring_days: number
  support_enabled: boolean
  support_token: string
  support_group_id: number
  support_greeting: string
  proxy_mode: string
  proxy: string
}) =>
  api<{ ok: boolean }>('api/telegram', {
    method: 'POST',
    body: JSON.stringify(t),
  })

// SupportGroup is a group the support bot has been added to — an option for the
// picker, never applied on its own (anyone can add a public bot to a group).
export interface SupportGroup {
  chat_id: number
  title: string
  is_forum: boolean
  is_admin: boolean
}

// listSupportGroups powers the group picker, so nobody has to dig a chat id out of
// a Telegram Web URL and remember to prefix it with -100.
export const listSupportGroups = () =>
  api<SupportGroup[]>('api/telegram/support/groups')

// checkTelegramSupport validates the support group end to end (reachable, a forum,
// bot is an admin able to manage topics) — the privacy-mode failure it catches is
// otherwise invisible: the bot receives users' messages but never the admins' replies.
export const checkTelegramSupport = () =>
  api<{ ok: boolean; bot_username: string; group_title: string }>(
    'api/telegram/support/check',
    { method: 'POST' },
  )

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

// Mass broadcasts through the user bot. The audience is snapshotted when a broadcast
// starts, so total never moves once it is running.
export type BroadcastStatus = 'running' | 'paused' | 'done' | 'cancelled'
// A plain key ("all", "expired") or a parameterised one carrying its horizon
// ("seen:7"). The string is what gets stored and displayed, so the parameter travels
// with it rather than in a second field that would have to be kept in sync.
export type BroadcastAudience = string

export interface BroadcastButton {
  text: string
  url: string
}

export interface Broadcast {
  id: number
  created_by: string
  text: string
  media_kind: '' | 'photo' | 'document'
  media_name: string
  buttons: BroadcastButton[] | null
  audience: BroadcastAudience
  status: BroadcastStatus
  created_at: number
  started_at: number
  finished_at: number
  total: number
  sent: number
  failed: number
  blocked: number
  // Unsubscribed after the audience was frozen and skipped over on purpose. Counts
  // toward total, so leaving it out of the progress arithmetic strands the bar
  // short of 100% on any run where somebody opted out mid-flight.
  skipped: number
}

export const listBroadcasts = () => api<Broadcast[]>('api/broadcasts')

// broadcastAudience previews how many recipients a filter resolves to right now.
export const broadcastAudience = (audience: BroadcastAudience) =>
  api<{ count: number }>(
    `api/broadcasts/audience?audience=${encodeURIComponent(audience)}`,
  )

// broadcastForm packs the composed message the way both create and test expect: a
// JSON "payload" part plus an optional file, since an attachment can't ride in JSON.
const broadcastForm = (
  b: { text: string; audience: BroadcastAudience; buttons: BroadcastButton[] },
  media: File | null,
) => {
  const fd = new FormData()
  fd.append('payload', JSON.stringify(b))
  if (media) fd.append('media', media)
  return fd
}

export const createBroadcast = (
  b: { text: string; audience: BroadcastAudience; buttons: BroadcastButton[] },
  media: File | null,
) => apiForm<Broadcast>('api/broadcasts', broadcastForm(b, media))

// testBroadcast delivers the composed message to the linked admin chats first.
// Broken markup seen by the whole audience can only be fixed by another broadcast.
export const testBroadcast = (
  b: { text: string; audience: BroadcastAudience; buttons: BroadcastButton[] },
  media: File | null,
) => apiForm<{ ok: boolean }>('api/broadcasts/test', broadcastForm(b, media))

export const pauseBroadcast = (id: number) =>
  api<Broadcast>(`api/broadcasts/${id}/pause`, { method: 'POST' })

export const resumeBroadcast = (id: number) =>
  api<Broadcast>(`api/broadcasts/${id}/resume`, { method: 'POST' })

export const cancelBroadcast = (id: number) =>
  api<Broadcast>(`api/broadcasts/${id}/cancel`, { method: 'POST' })

export const retryBroadcast = (id: number) =>
  api<Broadcast>(`api/broadcasts/${id}/retry`, { method: 'POST' })

// messageUser sends one message to one user's Telegram chat — the same thing a
// broadcast does, for an audience of one, but answered synchronously so the operator
// learns immediately whether it arrived.
export const messageUser = (id: number, text: string, media: File | null) => {
  // Same multipart shape as a broadcast — one parser on the server decides photo vs
  // document, so a file behaves identically wherever it was attached.
  const fd = new FormData()
  fd.append('payload', JSON.stringify({ text }))
  if (media) fd.append('media', media)
  return apiForm<{ ok: boolean }>(`api/users/${id}/telegram/message`, fd)
}

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

// login sends the second factor when the panel has asked for one. The code is
// optional because the first attempt deliberately does not carry it: the panel only
// admits that an account has 2FA once the password is already right.
export const login = (username: string, password: string, code?: string) =>
  api<{ ok: boolean }>('api/login', {
    method: 'POST',
    body: JSON.stringify({ username, password, code }),
  })

// ---- Where clients may connect from (the source policy) ---------------------
export interface ConnPolicy {
  mode: 'off' | 'allow' | 'block'
  countries: string[]
  asns: number[]
  enforce: boolean
  block_hours: number
}
export interface BlockedIP {
  ip: string
  reason: string // country | asn
  country: string
  asn: number
  org: string
  user_id: number
  at: number
  until: number
}
export interface ConnPolicyInfo {
  policy: ConnPolicy
  blocked: BlockedIP[]
  // false when this machine has no nftables: refusals are recorded, not enforced.
  can_enforce: boolean
}
export const getConnPolicy = () => api<ConnPolicyInfo>('api/security/conn-policy')
export const saveConnPolicy = (p: ConnPolicy) =>
  api<{ ok: boolean }>('api/security/conn-policy', { method: 'POST', body: JSON.stringify(p) })
export const unblockIP = (ip: string) =>
  api<{ ok: boolean }>('api/security/unblock', { method: 'POST', body: JSON.stringify({ ip }) })

// ---- The admin's own open sessions -----------------------------------------
export interface AdminSession {
  id: number
  ip: string // last address it was used from
  user_agent: string
  created_at: number
  last_seen_at: number
  expires_at: number
  current: boolean // the session making this request
}
export const listSessions = () => api<AdminSession[]>('api/account/sessions')
export const revokeSession = (id: number) =>
  api<{ ok: boolean }>(`api/account/sessions/${id}`, { method: 'DELETE' })
export const revokeOtherSessions = () =>
  api<{ revoked: number }>('api/account/sessions/revoke-others', { method: 'POST' })

// ---- The admin's own second factor (TOTP) ----------------------------------
//
// Every call acts on the CALLER; there is no id anywhere, so nobody manages anybody
// else's 2FA through the panel. A lost phone is cleared on the server with
// `rospanel totp reset <login>`.

export interface TOTPStatus {
  enabled: boolean
}

export const getTOTP = () => api<TOTPStatus>('api/account/totp')

// startTOTP returns the secret ONCE, with the otpauth URI to render as a QR.
export const startTOTP = (current_password: string) =>
  api<{ secret: string; uri: string }>('api/account/totp/start', {
    method: 'POST',
    body: JSON.stringify({ current_password }),
  })

export const enableTOTP = (code: string) =>
  api<{ ok: boolean }>('api/account/totp/enable', {
    method: 'POST',
    body: JSON.stringify({ code }),
  })

export const disableTOTP = (current_password: string) =>
  api<{ ok: boolean }>('api/account/totp/disable', {
    method: 'POST',
    body: JSON.stringify({ current_password }),
  })

export const logout = () => api<{ ok: boolean }>('api/logout', { method: 'POST' })

export const listUsers = () => api<User[]>('api/users')

export const createUser = (name: string, data_limit = 0, expire_at = 0) =>
  api<User>('api/users', {
    method: 'POST',
    body: JSON.stringify({ name, data_limit, expire_at }),
  })

export type HealthStatus = 'ok' | 'warn' | 'error' | 'info'

// The server sends dictionary KEYS plus the values to interpolate, not sentences:
// the panel's language is a per-browser choice the server cannot see, so anything
// it worded itself would be stuck in one language on a bilingual page.
//
// `detail` is the exception and carries verbatim text — some details are not ours
// to word (Xray's own config error, an ACME failure). Exactly one of detail_key /
// detail is set.
export interface HealthCheck {
  key: string
  label_key: string
  status: HealthStatus
  detail_key?: string
  detail?: string
  args?: Record<string, unknown>
  hint_key?: string
}

export interface HealthReport {
  status: 'ok' | 'warn' | 'error'
  checks: HealthCheck[]
}

// Per-server diagnostics for the Nodes page. Node 0 is the panel's own server (the
// full local report); a node's report is built from what it last told the panel.
// (`api/health` still serves the panel-wide report for /v1 clients; the SPA reads
// it per server.)
export const getNodeHealth = (id: number) => api<HealthReport>(`api/nodes/${id}/health`)

export type BulkAction = 'enable' | 'disable' | 'reset' | 'extend' | 'delete'

// bulkUsers applies one action to many users in a single server pass (one Xray
// sync). `days` is only used by the "extend" action. Returns how many were changed.
export const bulkUsers = (ids: number[], action: BulkAction, days = 0) =>
  api<{ affected: number }>('api/users/bulk', {
    method: 'POST',
    body: JSON.stringify({ ids, action, days }),
  })

// ---- Import from another panel (Marzban, 3x-ui) ----------------------------
// One user as read from the other panel's file, in this panel's terms. `issues`
// are dictionary keys under importUsers.issue.*; `exists` means the UUID is
// already here (the import skips it), `name_taken` is informational.
export interface ImportCandidate {
  name: string
  uuid: string
  password: string
  data_limit: number
  expire_at: number
  used_up: number
  used_down: number
  device_limit: number
  enabled: boolean
  note: string
  issues: string[]
  exists: boolean
  existing_id?: number
  name_taken: boolean
}
export interface ImportPreview {
  source: 'marzban' | '3x-ui'
  users: ImportCandidate[]
}
export interface ImportResult {
  created: number
  skipped: number
  failed: { name: string; code: string }[]
}
// exportUsers downloads this panel's own export file — the one inspectImport
// reads back. Served as an attachment, so the browser saves it rather than the
// SPA holding every credential in memory.
export const exportUsersURL = () => 'api/users/export'

export const inspectImport = (file: File) => {
  const fd = new FormData()
  fd.append('file', file)
  return apiForm<ImportPreview>('api/users/import/inspect', fd)
}
export const importUsers = (source: string, users: ImportCandidate[], tags: string[]) =>
  api<ImportResult>('api/users/import', {
    method: 'POST',
    body: JSON.stringify({ source, users, tags }),
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


// --- Billing / tariffs (Settings → Plans) ---

export interface TariffPlan {
  id: number
  slug: string
  name: string
  price_rub: number
  period_days: number
  data_limit: number
  device_limit: number
  speed_limit: number // kbit/s, 0 = unlimited
  sort_order: number
  enabled: boolean
  // Access groups the plan grants: whoever is put on the plan joins these groups and
  // leaves them when they move off it. Null/empty = the plan says nothing about access.
  group_ids: number[] | null
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

// Key only: the panel labels each event from its own dictionaries, so the picker
// reads in the admin's language rather than the server's.
export interface WebhookEventDef {
  key: string
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
  // Node's last-reported count of sync failures in the past hour. Nonzero ⇒ its
  // long-poll is limping (transport degraded) though it still looks online. 0 = local.
  sync_fails: number
  // State of an operator-requested Xray bounce: 'pending' until the node proves it
  // happened, then 'done' (or 'timeout' if we gave up) for a few seconds, then gone.
  // Absent for the master, whose restart is synchronous.
  xray_restart?: 'pending' | 'done' | 'timeout'
  vless_enabled: boolean
  hysteria_enabled: boolean
  reality_enabled: boolean
  decoy_template: string
  cert_self_signed: boolean // true = still on the self-signed fallback (no CA cert yet)
  cert_issuer: string // ≈ ACME provider that signed the cert (empty for the master)
  cert_expires_at: number // unix; 0 = unknown
  geo_refresh_hours: number // this server's own geo auto-refresh cadence (0 = never)
  traffic_up: number
  traffic_down: number
  // The machine this server runs on, as it last reported it. has_host_stats is false
  // for a node that never checked in (or an agent older than these fields) — read the
  // rest as unknown then, not as an idle machine.
  has_host_stats: boolean
  cpu_percent: number
  mem_used: number
  mem_total: number
  disk_used: number
  disk_total: number
  host_uptime: number
  net_up: number
  net_down: number
  routing: RoutingConfig | null // node's own routing, null = not configured (direct)
  xray_dns: string | null // node's own DNS, null = not configured (default resolver)
  // Per-node egress (independent of the master; all off by default). For the local
  // node (master) these carry the panel's own settings.
  warp_enabled: boolean
  warp_registered: boolean
  opera_enabled: boolean
  opera_country: string
  traffic_coefficient: number
  // Placement in subscriptions (see PlacementFields) and the live online count.
  country: string
  sort_weight: number
  capacity: number
  hide_when_full: boolean
  online_users: number
  // REALITY identity (per-server). reality_dest "" on a node = inherits the master's
  // donor. The public key / short id / XHTTP path are shown; private key is hidden.
  reality_dest: string
  reality_public_key: string
  reality_short_id: string
  reality_path: string
  master_label?: string // config-label name of the master (local node only)
  proxy: SystemProxy // this server's system proxy (SOCKS/HTTP forward listeners)
}

// SystemProxy is one server's forward proxy: SOCKS5 and/or HTTP listeners for
// traffic that is NOT a VPN client (a scraper, a bot, another panel chaining its
// egress here). No user credential opens them and no access group gates them — they
// carry the server's own account, and their traffic follows that server's routing.
export interface SystemProxyAccount {
  user: string
  pass: string
}

export interface SystemProxy {
  socks_enabled: boolean
  socks_port: number
  http_enabled: boolean
  http_port: number
  // Several logins, so each consumer can be given its own and revoked on its own.
  accounts: SystemProxyAccount[] | null
}

// setServerProxy configures one server's system proxy (id 0 = the master).
export const setServerProxy = (id: number, proxy: SystemProxy) =>
  api<{ ok: boolean }>(`api/nodes/${id}/proxy`, {
    method: 'POST',
    body: JSON.stringify(proxy),
  })

export const listNodes = () => api<{ nodes: NodeView[] }>('api/nodes')

// setMasterName sets the panel server's display name shown in config labels.
export const setMasterName = (name: string) =>
  api<{ ok: boolean }>('api/nodes/master-name', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })

// setMasterProtocols toggles the panel's own protocols on/off (the master card).
// Connection details stay in the global Connections settings.
export const setMasterProtocols = (p: {
  vless_enabled: boolean
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
// Connections tab and are OPTIONAL here: omitting them tells the panel to preserve the
// node's current values, so a name/decoy save can't revert a just-made protocol change.
// Placement is where a server sits in subscriptions: country (ISO-2, blank =
// detect from the address on save), a manual weight, capacity in users and
// whether a full server drops out until it has room again.
export interface Placement {
  country: string
  sort_weight: number
  capacity: number
  hide_when_full: boolean
}

export interface NodePatch {
  name: string
  host: string
  decoy_template: string
  vless_enabled?: boolean
  hysteria_enabled?: boolean
  reality_enabled?: boolean
  traffic_coefficient?: number
  placement?: Placement
}

export const setMasterPlacement = (p: Placement) =>
  api<{ ok: boolean }>('api/nodes/master-placement', { method: 'POST', body: JSON.stringify(p) })

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

// getNodeXrayConfig returns a server's Xray config as text: the master's live file
// for node 0, and for a node the config the panel generates and pushes to it.
export const getNodeXrayConfig = (id: number): Promise<string> =>
  apiText(`api/nodes/${id}/xray-config`)

// restartNodeXray asks a node to bounce its Xray. The panel can only flag the wish;
// the node acts on it at its next sync (which the flag wakes immediately).
export const restartNodeXray = (id: number) =>
  api<{ ok: boolean }>(`api/nodes/${id}/xray-restart`, { method: 'POST' })

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

// ---- Custom inbounds -------------------------------------------------------
//
// Operator-defined listeners that sit beside the three built-in lanes. Each
// belongs to exactly one server (0 = the master, a node id otherwise), because
// its port, REALITY identity and hop range are all facts about that machine.

export interface InboundOpts {
  transport: string
  security: string
  sni?: string
  fp?: string
  path?: string
  host?: string
  mode?: string
  service_name?: string
  reality_dest?: string
  reality_public_key?: string
  reality_short_id?: string
  reality_max_time_diff?: number
  hop_start?: number
  hop_end?: number
  hop_interval?: string
  // Shadowsocks-2022: the AEAD method. The server key is generated and never sent to
  // the client, so there is no field for it here.
  method?: string
  // Advanced. header_* / authority / multi_mode are simple mirrored-into-links knobs.
  // The three JSON sections travel as typed forms, not on opts — see the *_form fields
  // on Inbound below (the server nils the raw blobs out of opts).
  header_type?: string
  header_hosts?: string[]
  header_paths?: string[]
  authority?: string
  multi_mode?: boolean
}

// The advanced JSON sections, presented as typed fields. A field left empty is
// omitted; `raw` is a JSON-object string of any key the panel doesn't surface (the
// escape hatch), preserved verbatim across a round trip.
export interface XmuxForm {
  maxConcurrency?: string
  maxConnections?: string
  cMaxReuseTimes?: string
  hMaxRequestTimes?: string
  hMaxReusableSecs?: string
  hKeepAlivePeriod?: number
}
export interface XHTTPExtraForm {
  headers?: Record<string, string>
  xPaddingBytes?: string
  xPaddingObfsMode?: boolean
  xPaddingKey?: string
  xPaddingHeader?: string
  xPaddingPlacement?: string
  xPaddingMethod?: string
  uplinkHTTPMethod?: string
  sessionIDPlacement?: string
  sessionIDKey?: string
  sessionIDTable?: string
  sessionIDLength?: string
  seqPlacement?: string
  seqKey?: string
  uplinkDataPlacement?: string
  uplinkDataKey?: string
  uplinkChunkSize?: string
  noGRPCHeader?: boolean
  noSSEHeader?: boolean
  scMaxEachPostBytes?: string
  scMinPostsIntervalMs?: string
  scMaxBufferedPosts?: number
  scStreamUpServerSecs?: string
  serverMaxHeaderBytes?: number
  xmux?: XmuxForm
  raw?: string
}
export interface SockoptForm {
  mark?: number
  tcpFastOpen?: boolean
  tproxy?: string
  domainStrategy?: string
  dialerProxy?: string
  tcpKeepAliveInterval?: number
  tcpKeepAliveIdle?: number
  tcpCongestion?: string
  tcpWindowClamp?: number
  tcpMaxSeg?: number
  penetrate?: boolean
  tcpUserTimeout?: number
  v6only?: boolean
  interface?: string
  tcpMptcp?: boolean
  addressPortStrategy?: string
  raw?: string
}
export interface TLSExtraForm {
  minVersion?: string
  maxVersion?: string
  cipherSuites?: string
  rejectUnknownSni?: boolean
  curvePreferences?: string[]
  enableSessionResumption?: boolean
  disableSystemRoot?: boolean
  verifyPeerCertByName?: string[]
  raw?: string
}

export interface Inbound {
  id: number
  server_id: number
  enabled: boolean
  sort: number
  name: string
  protocol: string
  port: number
  opts: InboundOpts
  created_at: number
  // Subscription formats that cannot carry this combination and will skip it.
  // A warning, not an error — those clients just won't see this lane.
  unsupported: string[] | null
  reality_public_key?: string
  reality_short_id?: string
  // The advanced sections, disassembled into forms the editor binds directly.
  xhttp_extra_form: XHTTPExtraForm
  sockopt_form: SockoptForm
  tls_extra_form: TLSExtraForm
}

// InboundInput is the editable shape sent on create/update. The server owns the
// id, the server_id and the REALITY private key, so none of them appear here.
export interface InboundInput {
  enabled: boolean
  name: string
  protocol: string
  port: number
  transport: string
  security: string
  sni: string
  fp: string
  path: string
  host: string
  mode: string
  service_name: string
  reality_dest: string
  reality_anti_replay: boolean
  hop_start: number
  hop_end: number
  hop_interval: string
  header_type: string
  header_hosts: string[]
  header_paths: string[]
  authority: string
  multi_mode: boolean
  // Shadowsocks-2022 method; ignored by the server for the other protocols.
  method: string
  // The three advanced sections as typed forms; the server assembles them into the
  // JSON blob Xray reads and validates that.
  xhttp_extra: XHTTPExtraForm
  sockopt: SockoptForm
  tls_extra: TLSExtraForm
}

// InboundCombo is one valid protocol × transport pair with the security layers it
// accepts. The editor drives its dropdowns from this so the UI and the server-side
// validator can never disagree about which combinations exist.
export interface InboundCombo {
  protocol: string
  transport: string
  securities: string[]
  unsupported: string[] | null
}

// InboundEnums are the advanced-field dropdown options, straight from Xray's parser.
export interface InboundEnums {
  placements: string[]
  uplink_methods: string[]
  tproxy: string[]
  domain_strategy: string[]
  address_port_strategy: string[]
  tls_versions: string[]
  ss_methods: string[]
}

export interface InboundCatalog {
  protocols: string[]
  combos: InboundCombo[]
  fingerprints: string[]
  xhttp_modes: string[]
  max: number
  enums: InboundEnums
}

export const getInboundCatalog = () => api<InboundCatalog>('api/inbounds/catalog')

export const listInbounds = (serverId: number) =>
  api<Inbound[]>(`api/servers/${serverId}/inbounds`)

export const createInbound = (serverId: number, v: InboundInput) =>
  api<Inbound>(`api/servers/${serverId}/inbounds`, {
    method: 'POST',
    body: JSON.stringify(v),
  })

export const updateInbound = (id: number, v: InboundInput) =>
  api<Inbound>(`api/inbounds/${id}`, { method: 'POST', body: JSON.stringify(v) })

export const deleteInbound = (id: number) =>
  api<{ ok: boolean }>(`api/inbounds/${id}`, { method: 'DELETE' })

export const regenInboundReality = (id: number) =>
  api<Inbound>(`api/inbounds/${id}/regen-reality`, { method: 'POST' })

// ---- User groups -----------------------------------------------------------
//
// A group gates which connections its members may use. A user in no group reaches
// everything; a user in groups reaches the union of their grants. Grants are opaque
// tokens (a built-in lane on a server, or a custom inbound) that GroupTargets resolves
// to names.

export interface Group {
  id: number
  name: string
  created_at: number
  grants: string[] | null
  members: number
  member_ids: number[] | null
}

export interface GroupLaneOpt {
  lane: string // vless | reality | hysteria2
  label: string
  token: string
  enabled: boolean
}
export interface GroupInboundOpt {
  id: number
  name: string
  token: string
  enabled: boolean
}
// GroupTarget is one server's grantable connections, for the group editor.
export interface GroupTarget {
  server_id: number
  server_name: string
  lanes: GroupLaneOpt[]
  inbounds: GroupInboundOpt[]
}

export const listGroups = () => api<Group[]>('api/groups')
export const getGroupTargets = () => api<GroupTarget[]>('api/groups/targets')
export const createGroup = (name: string, grants: string[]) =>
  api<Group>('api/groups', { method: 'POST', body: JSON.stringify({ name, grants }) })
export const updateGroup = (id: number, name: string, grants: string[]) =>
  api<{ ok: boolean }>(`api/groups/${id}`, { method: 'POST', body: JSON.stringify({ name, grants }) })
export const deleteGroup = (id: number) =>
  api<{ ok: boolean }>(`api/groups/${id}`, { method: 'DELETE' })
export const setGroupMembers = (groupId: number, userIds: number[]) =>
  api<{ ok: boolean }>(`api/groups/${groupId}/members`, {
    method: 'POST',
    body: JSON.stringify({ user_ids: userIds }),
  })
export const setUserGroups = (userId: number, groupIds: number[]) =>
  api<{ ok: boolean }>(`api/users/${userId}/groups`, {
    method: 'POST',
    body: JSON.stringify({ group_ids: groupIds }),
  })
