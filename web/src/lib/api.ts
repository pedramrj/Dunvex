// Thin client for the Dunvex admin API.
declare global {
  interface Window {
    __DVX__?: { base: string; version: string };
  }
}

export const BASE = (window.__DVX__?.base ?? "/panel/") + "api/";
export const VERSION = window.__DVX__?.version ?? "dev";
/** The version as shown: releases already carry their "v" ("v1.19.0"); a development build is "dev". */
export const VERSION_LABEL = /^v|^dev$/.test(VERSION) ? VERSION : "v" + VERSION;

export class ApiError extends Error {
  status: number;
  data: any;
  constructor(status: number, msg: string, data?: any) {
    super(msg);
    this.status = status;
    this.data = data;
  }
}

let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

export async function api<T = unknown>(path: string, opts: { method?: string; body?: unknown; form?: FormData } = {}): Promise<T> {
  const method = opts.method ?? (opts.body !== undefined || opts.form ? "POST" : "GET");
  const headers: Record<string, string> = { "X-Dunvex": "1" };
  let body: BodyInit | undefined;
  if (opts.form) body = opts.form;
  else if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.body);
  }
  const res = await fetch(BASE + path, { method, headers, body, credentials: "same-origin" });
  if (res.status === 401 && !path.startsWith("login")) onUnauthorized(); // a refused sign-in is no lost session
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || res.statusText, data);
  return data as T;
}

// ---------------------------------------------------------------- types

export type Status = "active" | "pending" | "limited" | "expired" | "disabled" | "paused";

export interface User {
  id: number;
  name: string;
  uuid: string;
  password: string;
  subToken: string;
  enabled: boolean;
  dataLimit: number;
  usedUp: number;
  usedDown: number;
  lifetimeUsed: number;
  expireAt: number;
  durationDays: number;
  firstUseAt: number;
  resetPeriod: "none" | "daily" | "weekly" | "monthly";
  inboundIds: number[];
  note: string;
  createdAt: number;
  lastOnlineAt: number;
  status: Status;
  online: boolean;
  used: number;
  subUrl: string;
  speedUp: number;
  speedDown: number;
  daysLeft: number | null;
  ipLimit: number;
  pausedUntil: number;
  ownerId: number;
  devices: number;
  owner?: string;
  deviceList?: DeviceInfo[];
  loc?: { city?: string; cc?: string; places: number; org?: string }; // where the user is now (the list)
  tags: string[]; // the owner's labels
}

/** A tag and how many users carry it (GET /users/tags). */
export interface TagCount {
  tag: string;
  count: number;
}

export type Role = "owner" | "admin" | "reseller";

/** A signed-in browser of an admin. */
export interface AdminSession {
  id: string;
  adminId: number;
  admin: string;
  ip: string;
  agent: string;
  device: string;
  createdAt: number;
  lastSeen: number;
  expiresAt: number;
  current: boolean;
}

/** An API key; the key itself is shown only when it is made. */
export interface ApiKey {
  id: number;
  adminId: number;
  admin: string;
  name: string;
  prefix: string;
  readOnly: boolean;
  createdAt: number;
  lastUsed: number;
  expiresAt: number;
}

export interface Admin {
  id: number;
  username: string;
  role: Role;
  totp: boolean;
  maxUsers: number;
  maxData: number;
  enabled: boolean;
  createdAt: number;
  userCount?: number;
  allocated?: number;
  balance?: number;
}

export interface Quota {
  users: number;
  maxUsers: number;
  allocated: number;
  maxData: number;
}

export interface Me {
  admin: Admin;
  version: string;
  quota?: Quota;
}

export interface StatusInfo {
  online: number;
  devices: number;
  users: number;
  cores: { name: string; installed: boolean; running: boolean; version: string; idle?: boolean }[];
  problems: number;
  version: string;
}

export interface Geo {
  ip: string;
  country: string;
  countryCode: string;
  city: string;
  lat: number;
  lon: number;
  isp: string;
  /** placed by the offline country data: no city */
  offline?: boolean;
}

/** One country users connect from, for the map's arcs. */
export interface ClientCountry {
  cc: string;
  lat: number;
  lon: number;
  devices: number;
  users: number;
}

/** Connected clients by country (counts only; addresses never leave the server). */
export interface ClientMap {
  countries: ClientCountry[];
  unplaced: number;
  devices: number;
  users: number;
  window: number;
  source: string;
  built: number;
  missing?: string;
}

export interface Event {
  id: number;
  at: number;
  kind: string;
  message: string;
}

export interface Field {
  key: string;
  label: string;
  labelFa: string;
  type: "text" | "number" | "select" | "bool" | "secret";
  options?: string[];
  /** names of the options in the panel (same order) */
  optionLabels?: string[];
  optionLabelsFa?: string[];
  advanced?: boolean;
}

export type Group = "reality" | "cdn" | "udp" | "classic";

export interface Preset {
  id: string;
  name: string;
  engine: "xray" | "singbox";
  group: Group;
  desc: string;
  descFa: string;
  network: "tcp" | "udp";
  defaultPort: number;
  security: string;
  needsDomain: boolean;
  status: "ready" | "beta";
  recommended: boolean;
  fields: Field[];
}

/** The free trial the Telegram bot gives (Settings › Telegram). */
export interface TrialSettings {
  on: boolean;
  dataMB: number; // 0 = no limit
  hours: number;
  devices: number; // 0 = no limit
  inbounds: number[]; // empty = every protocol
  daily: number; // 0 = no cap
  channel: string; // "@name" to join first, "" = none
  cleanupDays: number; // 0 = keep ended trials
}

export interface TrialInfo {
  settings: TrialSettings;
  stats: { total: number; day: number; bought: number; active: number };
  recent: { chat: number; userId: number; name: string; at: number; ends: number; bought: boolean; status: string }[];
  bot?: { configured: boolean; username: string };
}

/** A backup kept on the server (Settings › Backup). */
export interface BackupFile {
  name: string;
  size: number;
  at: number;
  encrypted?: boolean;
  safety?: boolean; // the copy taken just before a restore
}

/** One backup and where its copies went ("ok", the error, or absent when not due). */
export interface BackupRun {
  at: number;
  reason: "schedule" | "manual" | "update";
  file?: string;
  size?: number;
  encrypted?: boolean;
  telegram?: string;
  s3?: string;
  error?: string;
}

export interface BackupInfo {
  settings: {
    hours: number;
    keep: number;
    telegram: boolean;
    encrypted: boolean;
    s3: { endpoint: string; region: string; bucket: string; prefix: string; access: string; secretSet: boolean };
  };
  files: BackupFile[];
  runs: BackupRun[];
  next: number;
  telegram: { configured: boolean; admins: number };
}

export type BroadcastAudience = "all" | "active" | "ending" | "ended" | "selected" | "tag";

/** One message sent (or sending) to many users through the Telegram bot. */
export interface BroadcastRun {
  id: number;
  at: number;
  done: number; // 0 while sending
  by: string;
  audience: BroadcastAudience;
  tag?: string; // for the "tag" audience
  text: string;
  html: boolean;
  total: number;
  sent: number;
  failed: number;
  blocked: number; // blocked the bot or left Telegram (unlinked)
  canceled?: boolean;
  interrupted?: boolean;
  errors?: string[];
}

export interface BroadcastInfo {
  counts: Record<"all" | "active" | "ending" | "ended", { users: number; linked: number }>;
  tags?: { tag: string; users: number; linked: number }[]; // the audience of every tag
  history: BroadcastRun[];
  userBot: boolean;
  bot?: { configured: boolean; running: boolean; username: string; admins: number };
}

/** One person's conversation with support through the Telegram bot. */
export interface SupportThread {
  id: number;
  chat: number; // the Telegram chat
  userId: number;
  name: string; // Telegram first name
  username: string; // Telegram @username, without the @
  open: boolean;
  unread: number; // the person's messages nobody opened yet
  lastAt: number;
  lastText: string;
  lastStaff: boolean; // the newest message is an answer
  createdAt: number;
  closedAt: number;
  closedBy: string;
  messages: number;
  waiting: boolean; // open, the person wrote last
  user: SupportUser | null; // null: no subscription, or it was deleted
}

/** The person's subscription beside the thread. */
export interface SupportUser {
  id: number;
  name: string;
  status: Status;
  online: boolean;
  used: number;
  dataLimit: number;
  expireAt: number;
  daysLeft: number | null;
  devices: number;
  ipLimit: number;
  note: string;
  owner?: string; // the reseller's name (owners and admins only)
}

export interface SupportMessage {
  id: number;
  threadId: number;
  at: number;
  staff: boolean; // an answer
  by?: string; // the admin who answered
  text: string;
  photo?: string;
  seen: boolean;
  answered: boolean;
}

export interface SupportCounts { open: number; waiting: number; unread: number; closed: number }

export interface SupportInfo {
  threads: SupportThread[];
  counts: SupportCounts;
  bot: { configured: boolean; running?: boolean; username?: string };
  on: boolean;
  maxAnswer: number;
}

/** Settings › Telegram › Support. */
export interface SupportSettings {
  on: boolean;
  guests: boolean; // people without a linked subscription may write too
  alert: boolean; // each message goes to the bot's admins
  rate: number; // messages an hour per person (0 = no limit)
  maxLen: number; // characters in one message
  keep: number; // messages kept per conversation (0 = all)
  days: number; // removed after this many days (0 = never)
  note: string; // a line on the person's support screen
}

/** One config's result in the config health check (GET /health). */
export interface HealthResult {
  inboundId: number;
  preset: string;
  name: string; // as in the users' apps
  server?: string; // node name; empty = this server
  via?: "cdn" | "relay" | "domain";
  status: "ok" | "fail" | "skip";
  ms?: number;
  error?: string; // why it failed, or why it was not tested
}

export interface HealthReport {
  at: number; // when the last check finished (0 = never)
  took: number;
  results: HealthResult[];
  running: boolean;
  started?: number;
  hours: number; // the schedule (0 = only when asked)
  next?: number;
  summary: { total: number; ok: number; fail: number; skip: number };
}

export interface Inbound {
  id: number;
  preset: string;
  remark: string;
  port: number;
  enabled: boolean;
  params: Record<string, any>;
  up: number;
  down: number;
  createdAt: number;
  name: string;
  engine: string;
  error: string;
  /** 0 = this server, otherwise the node it runs on */
  nodeId: number;
}

/** Health a node reports. */
export interface NodeStatus {
  version: string;
  hostname: string;
  publicIp: string;
  certSha256: string;
  cores: { name: string; installed: boolean; running: boolean; version: string; uptime: number; restarts: number; error: string; idle?: boolean }[];
  coreErrors: Record<string, string>;
  inboundErrors: Record<string, string>;
  cpu: number;
  memUsed: number;
  memTotal: number;
  diskUsed: number;
  diskTotal: number;
  uptime: number;
  netUp: number;
  netDown: number;
  users: number;
  inbounds: number;
  forwards?: RelayStat[];
  tunnel?: { port: number; listening: boolean; error?: string; links: number; peer?: string; since?: number } | null;
  tunnelLinks?: { name: string; addr: string; links: number; error?: string; since?: number; streams: number }[];
  bridge?: BridgeStat | null; // a bridge relay's carriers
  bridgeLinks?: BridgeClientStat[]; // this server's Bridge sessions to relays
  time: number;
  os?: string;
  arch?: string;
  domain?: string;
  realCert?: boolean; // a Let's Encrypt certificate for its domain
  certExpiry?: number;
  certError?: string;
  certBusy?: boolean;
}

/** One Bridge carrier's live state. */
export interface CarrierStat {
  name: string; // udp, icmp, faketcp, tls-cdn, dns
  up: boolean;
  rttMs: number; // -1 no answer, 0 not measured
  lossPct: number;
  sentB: number;
  recvB: number;
  rateUp: number;
  rateDown: number;
  lastErr?: string;
  lastFail?: number;
  capMBps?: number;
}

/** A bridge relay's side of its Bridge. */
export interface BridgeStat {
  links: number;
  since?: number;
  carriers: CarrierStat[];
}

/** A target's Bridge session to one relay. */
export interface BridgeClientStat {
  name: string;
  relayHost: string;
  links: number;
  since?: number;
  error?: string;
  streams: number;
  carriers?: CarrierStat[];
}

/** One carrier's panel-side setting on a bridge relay. */
export interface BridgeCarrierPref {
  kind: string; // udp, icmp, faketcp, tls-cdn, dns
  on: boolean;
  priority?: number;
  capMBps?: number;
  port?: number;
  serverRequests?: boolean; // icmp
}

/** A bridge relay's carriers and options. */
export interface BridgeSettings {
  carriers: BridgeCarrierPref[];
  tlsDomain?: string;
  dnsZone?: string;
}

/** One port a relay forwards to its target server. */
export interface RelayStat {
  name: string;
  listen: number;
  network: "tcp" | "udp" | "tcp+udp";
  target: string;
  listening: boolean;
  error?: string;
  conns: number;
  up: number;
  down: number;
  rttMs: number; // -1 unreachable, 0 not checked
}

/** Another server this panel runs protocols on. */
export interface NodeView {
  id: number;
  name: string;
  address: string;
  agentPort: number;
  fingerprint: string;
  enabled: boolean;
  lastSeen: number;
  createdAt: number;
  paired: boolean;
  online: boolean;
  error: string;
  lastTry: number;
  status: NodeStatus | null;
  inbounds: number;
  rateUp: number;
  rateDown: number;
  role: "server" | "relay";
  relayTarget: number; // 0 = this panel's server
  relayOnly: boolean;
  relayShift: number;
  relayMode: "forward" | "reverse" | "bridge";
  relayPort: number; // a reverse relay's tunnel port
  targetName?: string;
  tunnelLinks: number; // this server's links to a reverse relay in front of it
  tunnelError?: string;
  bridge?: BridgeSettings; // a bridge relay's carriers (for the edit dialog)
  bridgeClient?: BridgeClientStat; // this server's own Bridge session to the relay (target == this server)
  domain: string; // the server's own domain ("" = none)
  realCert: boolean;
  // the server runs another version than the panel: via "panel" (its binary)
  // or "github"; "" with a reason (old: no remote updates yet; dev: a dev panel)
  update?: { panel: string; via: "panel" | "github" | ""; newer: boolean; reason?: "old" | "dev" } | null;
}

export interface Line {
  id: number;
  preset: string;
  name: string;
  group: Group;
  remark: string;
  port: number;
  enabled: boolean;
  error: string;
  up: number;
  down: number;
  rateUp: number;
  rateDown: number;
}

export interface CoreStatus {
  name: string;
  installed: boolean;
  running: boolean;
  version: string;
  uptime: number;
  restarts: number;
  error: string;
  /** nothing to serve, so stopped on purpose */
  idle?: boolean;
  /** part of Dunvex itself: nothing to install or update */
  builtIn?: boolean;
}

export interface SystemInfo {
  hostname: string;
  os: string;
  arch: string;
  cores: number;
  cpu: number;
  load: number[] | null;
  memTotal: number;
  memUsed: number;
  swapTotal: number;
  swapUsed: number;
  diskTotal: number;
  diskUsed: number;
  uptime: number;
  netUp: number;
  netDown: number;
  netSent: number;
  netRecv: number;
  tcp: number;
  udp: number;
  publicIp: string;
}

export interface Overview {
  role?: Role;
  quota?: Quota;
  devices: number;
  lines: Line[];
  users: number;
  online: number;
  statuses: Partial<Record<Status, number>>;
  today: { up: number; down: number };
  month: { up: number; down: number };
  lifetime: number;
  series: { t: number; up: number; down: number }[];
  top: { id: number; name: string; bytes: number }[];
  live: { t: number; up: number; down: number }[];
  system: SystemInfo;
  history: { t: number; cpu: number; mem: number; up: number; down: number }[];
  cores: CoreStatus[];
  coreErrors: Record<string, string>;
  inboundErrs: number;
  inbounds: number;
  inboundsOn: number;
  version: string;
}

export interface ConfigEntry {
  inboundId: number;
  preset: string;
  name: string;
  link: string;
  /** set for a config that is a whole file (AmneziaWG .conf) */
  file?: string;
  /** its QR as SVG, sent inline so the private key never goes in a URL */
  qr?: string;
  /** what an app without this link type needs typed in (an open DNS tunnel) */
  note?: string;
}

/** saves text as a file in the browser */
export function saveText(name: string, text: string) {
  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

export const qrURL = (text: string) => BASE + "tools/qr?text=" + encodeURIComponent(text);

// ---------------------------------------------------------------- billing

export interface Plan {
  id: number;
  name: string;
  note: string;
  price: number;
  dataLimit: number;
  days: number;
  ipLimit: number;
  inboundIds: number[];
  startOnFirstUse: boolean;
  enabled: boolean;
  sort: number;
  createdAt: number;
  resellerPrice?: number;
}

export type OrderStatus = "pending" | "review" | "paid" | "rejected" | "cancelled" | "expired";
export type PayMethod = "card" | "zarinpal" | "usdt" | "wallet" | "manual";

export interface Order {
  id: number;
  code: string;
  kind: "new" | "renew" | "topup";
  planId: number;
  planName: string;
  userId: number;
  adminId: number;
  amount: number;
  method: PayMethod;
  status: OrderStatus;
  contact: string;
  tgChat: number;
  receipt: string;
  receiptNote: string;
  payRef: string;
  cryptoAmount: string;
  note: string;
  createdAt: number;
  paidAt: number;
  doneAt: number;
  userName: string;
  adminName: string;
  page: string;
}

export interface BankCard { number: string; holder: string; bank: string }

export interface BillingSettings {
  shop: boolean;
  bot: boolean;
  title: string;
  currency: string;
  support: string;
  card: boolean;
  cards: BankCard[];
  zarinpal: boolean;
  zarinpalMerchant: string;
  zarinpalSandbox: boolean;
  usdt: boolean;
  usdtAddress: string;
  usdtRate: number;
  tronApiKey: string;
  wallet: boolean;
  discount: number;
  orderHours: number;
}

export interface BillingSummary {
  settings: BillingSettings;
  shopUrl: string;
  methods: PayMethod[] | null;
  stats: { todayCount: number; todayTotal: number; monthCount: number; monthTotal: number; review: number };
}

export interface WalletTx { id: number; adminId: number; amount: number; balance: number; orderId: number; note: string; at: number }

export interface WalletInfo {
  enabled: boolean;
  balance: number;
  discount: number;
  methods: PayMethod[] | null;
  currency: string;
  transactions: WalletTx[];
}

/** 150000 → "150,000" (Latin digits, as on the shop) */
export const money = (v: number) => Math.round(v).toLocaleString("en-US");

/** A place from the server's offline data. */
export interface GeoPlace { city?: string; region?: string; cc: string; lat: number; lon: number }
/** One address a user is connected from, with where it is and what it reached (user detail). */
export interface DeviceInfo {
  ip: string;
  lastSeen: number;
  via: number[]; // 0 is this server, others node ids
  place?: GeoPlace;
  net?: { asn: number; org: string };
  precise?: boolean;
  relay?: number;
  servers: string[];
}
/** GET /network/users: servers and the places users connect from, never addresses. */
export interface MapServer { id: number; name: string; role: string; target?: number; online: boolean; place?: GeoPlace; network?: string }
export interface MapDevice { user: string; uid: number; via: number[]; last: number; asn?: number; org?: string }
export interface MapPlace extends GeoPlace { precise: boolean; devices: MapDevice[] }
export interface UserMap { servers: MapServer[]; places: MapPlace[]; devices: number; users: number; relayed: number; unplaced: number; city: boolean; source?: string; window: number }
