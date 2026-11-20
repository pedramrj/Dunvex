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
  cores: { name: string; installed: boolean; running: boolean; version: string }[];
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
}

export const qrURL = (text: string) => BASE + "tools/qr?text=" + encodeURIComponent(text);
