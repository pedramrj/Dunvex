// Thin client for the Dunvex admin API.
declare global {
  interface Window {
    __DVX__?: { base: string; version: string };
  }
}

export const BASE = (window.__DVX__?.base ?? "/panel/") + "api/";
export const VERSION = window.__DVX__?.version ?? "dev";

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
  if (res.status === 401 && path !== "login") onUnauthorized();
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
  deviceList?: { ip: string; lastSeen: number }[];
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
  time: number;
  os?: string;
  arch?: string;
  domain?: string;
  realCert?: boolean; // a Let's Encrypt certificate for its domain
  certExpiry?: number;
  certError?: string;
  certBusy?: boolean;
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
  relayMode: "forward" | "reverse";
  relayPort: number; // a reverse relay's tunnel port
  targetName?: string;
  tunnelLinks: number; // this server's links to a reverse relay in front of it
  tunnelError?: string;
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
