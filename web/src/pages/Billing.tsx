import { useCallback, useEffect, useState } from "react";
import { BadgeCheck, CalendarDays, Check, Clock, CreditCard, ExternalLink, Pencil, Plus, Receipt, ShoppingBag, Trash2, Wallet, X } from "lucide-react";
import { api, BASE, money, type BillingSettings, type BillingSummary, type Inbound, type Order, type OrderStatus, type Plan, type WalletTx } from "../lib/api";
import { ago, bytes, cx, date, GB } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import { Badge, Button, Checkbox, ConfirmButton, CopyField, Dialog, Dot, Empty, ErrorState, IconButton, Input, KV, Label, Panel, Segmented, Sheet, Skeleton, StatCard, Tabs, Textarea, Toggle, useToast } from "../components/ui";
import { PageHead, useSession } from "../App";

export const orderColor: Record<OrderStatus, string> = {
  pending: "var(--warn)", review: "var(--color-violet)", paid: "var(--ok)", rejected: "var(--bad)", cancelled: "var(--text3)", expired: "var(--text3)",
};

export function OrderBadge({ status }: { status: OrderStatus }) {
  const { t } = useI18n();
  return <Badge color={orderColor[status]}><Dot color={orderColor[status]} size={6} pulse={status === "review"} />{t(("orderStatus_" + status) as Key)}</Badge>;
}

type Tab = "orders" | "plans" | "payments" | "wallets";

/** Selling subscriptions: orders to approve, plans, how buyers pay, resellers' wallets. */
export function BillingPage() {
  const { t, n } = useI18n();
  const { me } = useSession();
  const owner = me.admin.role === "owner";
  const [sum, setSum] = useState<BillingSummary | null>(null);
  const [err, setErr] = useState("");
  const [tab, setTab] = useState<Tab>("orders");
  const load = useCallback(() => api<BillingSummary>("billing").then(setSum).catch((e) => setErr(e.message)), []);
  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load]);
  if (!sum) return err ? <ErrorState message={err} onRetry={load} /> : <Skeleton className="h-96" />;
  const cur = sum.settings.currency;
  const s = sum.stats;
  return (
    <>
      <PageHead title={t("sales")} sub={sum.settings.shop ? <span className="text-ink3">{t("shopLink")}</span> : t("shopWebHint")}
        action={sum.settings.shop ? <Button icon={<ExternalLink className="size-4" />} onClick={() => window.open(sum.shopUrl, "_blank", "noopener")}>{t("openShop")}</Button> : undefined} />
      {sum.settings.shop && <CopyField value={sum.shopUrl} className="mb-5 max-w-xl" sensitive={false} />}
      <div className="grid gap-[var(--gap)] sm:grid-cols-3 mb-[var(--gap)]">
        <StatCard icon={<ShoppingBag className="size-[18px]" />} label={t("todaySales")} value={<span className="num">{n(s.todayTotal)} <span className="text-[13px] font-normal text-ink3">{cur}</span></span>} sub={n(s.todayCount) + " " + t("orderUnit")} accent="var(--ok)" />
        <StatCard icon={<CalendarDays className="size-[18px]" />} label={t("monthSales")} value={<span className="num">{n(s.monthTotal)} <span className="text-[13px] font-normal text-ink3">{cur}</span></span>} sub={n(s.monthCount) + " " + t("orderUnit")} />
        <StatCard icon={<Clock className="size-[18px]" />} label={t("waitingApproval")} value={<span className="num">{n(s.review)}</span>} accent={s.review ? "var(--color-violet)" : "var(--text3)"} onClick={() => setTab("orders")} />
      </div>
      <Tabs value={tab} onChange={setTab} className="mb-[var(--gap)]" items={[
        { id: "orders", label: t("ordersTab"), count: s.review || undefined },
        { id: "plans", label: t("plansTab") },
        ...(owner ? [{ id: "payments" as Tab, label: t("paymentsTab") }] : []),
        ...(owner && sum.settings.wallet ? [{ id: "wallets" as Tab, label: t("walletsTab") }] : []),
      ]} />
      {tab === "orders" && <Orders currency={cur} onChange={load} />}
      {tab === "plans" && <Plans currency={cur} wallet={sum.settings.wallet} />}
      {tab === "payments" && owner && <Payments initial={sum.settings} onSaved={load} />}
      {tab === "wallets" && owner && <Wallets currency={cur} />}
    </>
  );
}

// ---------------------------------------------------------------- orders

function Orders({ currency, onChange }: { currency: string; onChange: () => void }) {
  const { t, lang, n } = useI18n();
  const [filter, setFilter] = useState<"review" | "open" | "paid" | "">("review");
  const [list, setList] = useState<Order[] | null>(null);
  const [open, setOpen] = useState<Order | null>(null);
  const load = useCallback(() => api<Order[]>("billing/orders?limit=200" + (filter ? "&status=" + filter : "")).then(setList).catch(() => setList([])), [filter]);
  useEffect(() => {
    setList(null);
    load();
    const id = setInterval(load, 10000);
    return () => clearInterval(id);
  }, [load]);
  const done = () => { load(); onChange(); };
  return (
    <Panel pad={false}>
      <div className="flex flex-wrap items-center gap-3 p-[var(--pad)] pb-3">
        <Segmented value={filter} onChange={setFilter} options={[
          { value: "review", label: t("filterWaiting") }, { value: "open", label: t("filterOpen") },
          { value: "paid", label: t("filterPaid") }, { value: "", label: t("all") },
        ]} />
      </div>
      {!list ? <div className="p-[var(--pad)]"><Skeleton className="h-40" /></div> : list.length === 0 ? (
        <Empty icon={<Receipt className="size-6" />} title={t("noOrders")} />
      ) : (
        <ul className="divide-y divide-hair">
          {list.map((o) => (
            <li key={o.id}>
              <button onClick={() => setOpen(o)} className="w-full text-start flex flex-wrap items-center gap-x-4 gap-y-1 px-[var(--pad)] py-3 hover:bg-fg/[.03] transition-colors">
                <span className="mono text-[13px] font-semibold w-24" dir="ltr">{o.code}</span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13.5px] font-medium">{t(("orderKind_" + o.kind) as Key)}{o.planName && " · " + o.planName}</span>
                  <span className="block truncate text-[12px] text-ink3" dir="auto">{o.userName || o.adminName || o.contact || "—"}</span>
                </span>
                <span className="num text-[13.5px] font-semibold">{n(o.amount)} <span className="text-ink3 font-normal text-[12px]">{currency}</span></span>
                <span className="text-[12px] text-ink3 w-24">{t(("method_" + o.method) as Key)}</span>
                <OrderBadge status={o.status} />
                <span className="text-[12px] text-ink3 w-20 text-end">{ago(o.createdAt, lang)}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {list && list.length >= 200 && <div className="p-3 text-center text-[12px] text-ink3">{n(200)}+</div>}
      {open && <OrderSheet order={open} currency={currency} onClose={() => setOpen(null)} onDone={() => { setOpen(null); done(); }} />}
    </Panel>
  );
}

function OrderSheet({ order: o, currency, onClose, onDone }: { order: Order; currency: string; onClose: () => void; onDone: () => void }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const [busy, setBusy] = useState("");
  const [reason, setReason] = useState("");
  const [rejecting, setRejecting] = useState(false);
  const act = (what: "approve" | "reject" | "cancel") => {
    setBusy(what);
    api<Order>(`billing/orders/${o.id}/${what}`, { method: "POST", body: what === "reject" ? { reason } : {} })
      .then(() => { toast(what === "approve" ? t("approved") : t("rejected")); onDone(); })
      .catch((e) => toast(e.message, "error"))
      .finally(() => setBusy(""));
  };
  const openStatus = o.status === "pending" || o.status === "review" || o.status === "rejected" || o.status === "expired";
  const canApprove = o.method !== "wallet" && openStatus;
  return (
    <Sheet open onClose={onClose} title={<span dir="ltr" className="mono">{o.code}</span>} sub={t(("orderKind_" + o.kind) as Key) + (o.planName ? " · " + o.planName : "")}
      footer={canApprove ? (
        <div className="flex flex-wrap gap-2 w-full">
          <Button variant="primary" loading={busy === "approve"} icon={<Check className="size-4" />} onClick={() => act("approve")}>{t("approve")}</Button>
          {(o.status === "pending" || o.status === "review") && <Button loading={busy === "reject"} icon={<X className="size-4" />} onClick={() => setRejecting(true)}>{t("reject")}</Button>}
          {o.status === "pending" && <ConfirmButton onConfirm={() => act("cancel")}>{t("cancelOrder")}</ConfirmButton>}
        </div>
      ) : undefined}>
      <div className="flex flex-col gap-5">
        <div className="flex flex-wrap items-center gap-2"><OrderBadge status={o.status} />{o.status === "paid" && !o.doneAt && <Badge color="var(--bad)">{t("notApplied")}</Badge>}</div>
        <div className="grid grid-cols-2 gap-4">
          <KV label={t("amountField")}><span className="num">{n(o.amount)}</span> {currency}</KV>
          <KV label={t("payMethod")}>{t(("method_" + o.method) as Key)}{o.cryptoAmount && <span className="num text-ink3" dir="ltr"> · {o.cryptoAmount} USDT</span>}</KV>
          <KV label={t("buyer")}><span dir="auto">{o.userName || o.adminName || o.contact || "—"}</span></KV>
          <KV label={o.paidAt ? t("paidAt") : t("created")}>{date(o.paidAt || o.createdAt, lang)}</KV>
          {o.contact && o.userName && <KV label={t("note")}><span dir="auto">{o.contact}</span></KV>}
          {o.payRef && <KV label={t("payRef")}><span className="mono text-[12px] break-all" dir="ltr">{o.payRef}</span></KV>}
        </div>
        {o.receiptNote && <KV label={t("trackingNote")}><span className="mono" dir="auto">{o.receiptNote}</span></KV>}
        {o.receipt && (
          <div>
            <Label>{t("receipt")}</Label>
            <a href={`${BASE}billing/orders/${o.id}/receipt`} target="_blank" rel="noopener" className="block mt-1.5">
              <img src={`${BASE}billing/orders/${o.id}/receipt`} alt={t("receipt")} className="max-h-[420px] w-auto rounded-2xl border border-hair bg-fg/[.03]" />
            </a>
          </div>
        )}
        {o.note && <KV label={t("note")}><span dir="auto">{o.note}</span></KV>}
        {o.kind !== "topup" && o.method !== "wallet" && <div><Label>{t("orderPage")}</Label><CopyField value={o.page} sensitive={false} className="mt-1.5" /></div>}
        {o.userId > 0 && o.status === "paid" && <a className="text-primary2 text-[13px] hover:underline" href={`#/users/${o.userId}`}>{o.userName}</a>}
      </div>
      <Dialog open={rejecting} onClose={() => setRejecting(false)} title={t("reject")} danger
        footer={<><Button onClick={() => setRejecting(false)}>{t("cancel")}</Button><Button variant="danger" loading={busy === "reject"} onClick={() => { setRejecting(false); act("reject"); }}>{t("reject")}</Button></>}>
        <Label>{t("rejectReason")}</Label>
        <Input value={reason} onChange={(e) => setReason(e.target.value)} maxLength={200} className="mt-1.5" autoFocus />
      </Dialog>
    </Sheet>
  );
}

// ---------------------------------------------------------------- plans

const emptyPlan: Plan = { id: 0, name: "", note: "", price: 0, dataLimit: 0, days: 30, ipLimit: 0, inboundIds: [], startOnFirstUse: true, enabled: true, sort: 0, createdAt: 0 };

function Plans({ currency, wallet }: { currency: string; wallet: boolean }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const [list, setList] = useState<Plan[] | null>(null);
  const [edit, setEdit] = useState<Plan | null>(null);
  const load = useCallback(() => api<Plan[]>("billing/plans").then(setList).catch(() => setList([])), []);
  useEffect(() => { load(); }, [load]);
  const del = (p: Plan) => api(`billing/plans/${p.id}`, { method: "DELETE" }).then(() => { toast(t("deleted")); load(); }).catch((e) => toast(e.message, "error"));
  if (!list) return <Skeleton className="h-40" />;
  return (
    <>
      <div className="flex justify-end mb-3"><Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ ...emptyPlan })}>{t("newPlan")}</Button></div>
      {list.length === 0 ? (
        <Panel><Empty icon={<ShoppingBag className="size-6" />} title={t("noPlans")} hint={t("noPlansHint")}
          action={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ ...emptyPlan })}>{t("newPlan")}</Button>} /></Panel>
      ) : (
        <div className="grid gap-[var(--gap)] sm:grid-cols-2 xl:grid-cols-3">
          {list.map((p) => (
            <Panel key={p.id} className={cx(!p.enabled && "opacity-70")}>
              <div className="flex items-start gap-3">
                <div className="min-w-0 flex-1">
                  <div className="text-[16px] font-bold truncate">{p.name}</div>
                  <div className="text-[22px] font-bold mt-1 num">{n(p.price)} <span className="text-[13px] font-normal text-ink3">{currency}</span></div>
                </div>
                <Badge color={p.enabled ? "var(--ok)" : "var(--text3)"}>{p.enabled ? t("forSale") : t("hidden")}</Badge>
              </div>
              <div className="flex flex-wrap gap-x-4 gap-y-1 mt-3 text-[13px] text-ink2">
                <span>{p.dataLimit ? bytes(p.dataLimit, lang) : t("unlimited")}</span>
                <span>{p.days ? n(p.days) + " " + t("days") : t("unlimited")}</span>
                {p.ipLimit > 0 && <span>{n(p.ipLimit)} {t("devices")}</span>}
                {p.startOnFirstUse && p.days > 0 && <span className="text-ink3">{t("startOnFirst")}</span>}
              </div>
              {wallet && <div className="text-[12.5px] text-ink3 mt-2">{t("resellerPrice")}: <span className="num">{n(p.resellerPrice ?? p.price)}</span></div>}
              {p.note && <div className="text-[12.5px] text-ink3 mt-2 line-clamp-2">{p.note}</div>}
              <div className="flex gap-1 mt-3 -mb-1">
                <IconButton label={t("edit")} onClick={() => setEdit({ ...p })}><Pencil className="size-4" /></IconButton>
                <ConfirmButton icon={<Trash2 className="size-4" />} onConfirm={() => del(p)}>{t("delete")}</ConfirmButton>
              </div>
            </Panel>
          ))}
        </div>
      )}
      {edit && <PlanSheet plan={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); load(); }} />}
    </>
  );
}

function PlanSheet({ plan, onClose, onSaved }: { plan: Plan; onClose: () => void; onSaved: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [p, setP] = useState(plan);
  const [gb, setGb] = useState(plan.dataLimit ? String(Math.round((plan.dataLimit / GB) * 100) / 100) : "0");
  const [inbounds, setInbounds] = useState<Inbound[]>([]);
  const [busy, setBusy] = useState(false);
  useEffect(() => { api<Inbound[]>("inbounds").then(setInbounds).catch(() => {}); }, []);
  const set = <K extends keyof Plan>(k: K, v: Plan[K]) => setP((x) => ({ ...x, [k]: v }));
  const save = () => {
    setBusy(true);
    const body = { ...p, dataLimit: Math.round((parseFloat(gb) || 0) * GB) };
    api<Plan>(p.id ? `billing/plans/${p.id}` : "billing/plans", { method: p.id ? "PUT" : "POST", body })
      .then(() => { toast(t("saved")); onSaved(); })
      .catch((e) => toast(e.message, "error"))
      .finally(() => setBusy(false));
  };
  const num = (v: string) => Math.max(0, parseInt(v.replace(/[^\d]/g, ""), 10) || 0);
  return (
    <Sheet open onClose={onClose} title={p.id ? t("editPlan") : t("newPlan")}
      footer={<div className="flex gap-2"><Button variant="primary" loading={busy} onClick={save}>{t("save")}</Button><Button onClick={onClose}>{t("cancel")}</Button></div>}>
      <div className="flex flex-col gap-4">
        <label><Label>{t("planName")}</Label><Input value={p.name} maxLength={60} onChange={(e) => set("name", e.target.value)} autoFocus /></label>
        <label><Label>{t("price")}</Label><Input inputMode="numeric" className="num" dir="ltr" value={p.price ? money(p.price) : ""} onChange={(e) => set("price", num(e.target.value))} /></label>
        <div className="grid grid-cols-2 gap-3">
          <label><Label>{t("dataGB")}</Label><Input inputMode="decimal" className="num" dir="ltr" value={gb} onChange={(e) => setGb(e.target.value.replace(/[^\d.]/g, ""))} /></label>
          <label><Label>{t("daysField")}</Label><Input inputMode="numeric" className="num" dir="ltr" value={String(p.days)} onChange={(e) => set("days", num(e.target.value))} /></label>
        </div>
        <label><Label>{t("devicesField")}</Label><Input inputMode="numeric" className="num" dir="ltr" value={String(p.ipLimit)} onChange={(e) => set("ipLimit", Math.min(100, num(e.target.value)))} /></label>
        <Toggle checked={p.startOnFirstUse} onChange={(v) => set("startOnFirstUse", v)} label={t("startOnFirst")} />
        <Toggle checked={p.enabled} onChange={(v) => set("enabled", v)} label={t("forSale")} />
        {inbounds.length > 0 && (
          <div>
            <Label>{t("planProtocols")}</Label>
            <div className="flex flex-col gap-1.5 mt-1.5">
              {inbounds.map((ib) => (
                <Checkbox key={ib.id} label={ib.remark || ib.name || ib.preset} checked={p.inboundIds.includes(ib.id)}
                  onChange={(v) => set("inboundIds", v ? [...p.inboundIds, ib.id] : p.inboundIds.filter((x) => x !== ib.id))} />
              ))}
            </div>
          </div>
        )}
        <label><Label>{t("note")}</Label><Textarea value={p.note} maxLength={200} rows={2} onChange={(e) => set("note", e.target.value)} /></label>
      </div>
    </Sheet>
  );
}

// ---------------------------------------------------------------- payments

function Payments({ initial, onSaved }: { initial: BillingSettings; onSaved: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [s, setS] = useState<BillingSettings>({ ...initial, cards: initial.cards?.length ? initial.cards : [] });
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof BillingSettings>(k: K, v: BillingSettings[K]) => setS((x) => ({ ...x, [k]: v }));
  const save = () => {
    setBusy(true);
    api("billing/settings", { method: "PUT", body: s }).then(() => { toast(t("saved")); onSaved(); }).catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
  };
  const card = (i: number, k: "number" | "holder" | "bank", v: string) => set("cards", s.cards.map((c, j) => (j === i ? { ...c, [k]: v } : c)));
  return (
    <div className="grid gap-[var(--gap)] xl:grid-cols-2 items-start">
      <Panel title={t("sales")} icon={<ShoppingBag className="size-4" />}>
        <div className="flex flex-col gap-4">
          <Toggle checked={s.shop} onChange={(v) => set("shop", v)} label={t("shopWeb")} hint={t("shopWebHint")} />
          <Toggle checked={s.bot} onChange={(v) => set("bot", v)} label={t("shopBot")} hint={t("shopBotHint")} />
          <div className="grid grid-cols-2 gap-3">
            <label><Label>{t("shopTitle")}</Label><Input value={s.title} maxLength={60} onChange={(e) => set("title", e.target.value)} /></label>
            <label><Label>{t("currency")}</Label><Input value={s.currency} maxLength={20} onChange={(e) => set("currency", e.target.value)} /></label>
          </div>
          <label><Label>{t("supportLink")}</Label><Input dir="ltr" value={s.support} onChange={(e) => set("support", e.target.value)} /></label>
          <label><Label>{t("orderHours")}</Label><Input inputMode="numeric" dir="ltr" className="num w-32" value={String(s.orderHours)} onChange={(e) => set("orderHours", parseInt(e.target.value, 10) || 0)} /></label>
        </div>
      </Panel>
      <Panel title={t("cardToCard")} icon={<CreditCard className="size-4" />}>
        <Toggle checked={s.card} onChange={(v) => set("card", v)} label={t("cardToCard")} hint={t("cardToCardHint")} />
        <div className="flex flex-col gap-3 mt-4">
          {s.cards.map((c, i) => (
            <div key={i} className="grid grid-cols-[1fr_auto] gap-2 rounded-2xl border border-hair p-3">
              <Input dir="ltr" className="num" placeholder={t("cardNumber")} value={c.number} onChange={(e) => card(i, "number", e.target.value)} />
              <IconButton label={t("delete")} tone="danger" onClick={() => set("cards", s.cards.filter((_, j) => j !== i))}><Trash2 className="size-4" /></IconButton>
              <div className="grid grid-cols-2 gap-2 col-span-2">
                <Input placeholder={t("cardHolder")} value={c.holder} onChange={(e) => card(i, "holder", e.target.value)} />
                <Input placeholder={t("bank")} value={c.bank} onChange={(e) => card(i, "bank", e.target.value)} />
              </div>
            </div>
          ))}
          <Button icon={<Plus className="size-4" />} onClick={() => set("cards", [...s.cards, { number: "", holder: "", bank: "" }])}>{t("addCard")}</Button>
        </div>
      </Panel>
      <Panel title={t("zarinpal")} icon={<BadgeCheck className="size-4" />}>
        <div className="flex flex-col gap-4">
          <Toggle checked={s.zarinpal} onChange={(v) => set("zarinpal", v)} label={t("zarinpal")} hint={t("zarinpalHint")} />
          <label><Label>{t("merchantId")}</Label><Input dir="ltr" className="mono" value={s.zarinpalMerchant} maxLength={36} onChange={(e) => set("zarinpalMerchant", e.target.value.trim())} /></label>
          <Toggle checked={s.zarinpalSandbox} onChange={(v) => set("zarinpalSandbox", v)} label={t("sandbox")} hint={t("sandboxHint")} />
        </div>
      </Panel>
      <Panel title={t("usdtPay")} icon={<Wallet className="size-4" />}>
        <div className="flex flex-col gap-4">
          <Toggle checked={s.usdt} onChange={(v) => set("usdt", v)} label={t("usdtPay")} hint={t("usdtHint")} />
          <label><Label>{t("usdtAddress")}</Label><Input dir="ltr" className="mono" value={s.usdtAddress} maxLength={34} onChange={(e) => set("usdtAddress", e.target.value.trim())} /></label>
          <div className="grid grid-cols-2 gap-3">
            <label><Label>{t("usdtRate")}</Label><Input inputMode="numeric" dir="ltr" className="num" value={s.usdtRate ? money(s.usdtRate) : ""} onChange={(e) => set("usdtRate", parseInt(e.target.value.replace(/[^\d]/g, ""), 10) || 0)} /></label>
            <label><Label>{t("tronKey")}</Label><Input dir="ltr" className="mono" value={s.tronApiKey} onChange={(e) => set("tronApiKey", e.target.value.trim())} /></label>
          </div>
        </div>
      </Panel>
      <Panel title={t("resellerWallet")} icon={<Wallet className="size-4" />}>
        <div className="flex flex-col gap-4">
          <Toggle checked={s.wallet} onChange={(v) => set("wallet", v)} label={t("resellerWallet")} hint={t("resellerWalletHint")} />
          <label><Label>{t("discount")}</Label><Input inputMode="numeric" dir="ltr" className="num w-32" value={String(s.discount)} onChange={(e) => set("discount", Math.min(90, parseInt(e.target.value, 10) || 0))} /></label>
        </div>
      </Panel>
      <div className="xl:col-span-2 flex justify-end"><Button variant="primary" loading={busy} onClick={save}>{t("save")}</Button></div>
    </div>
  );
}

// ---------------------------------------------------------------- reseller wallets (owner)

function Wallets({ currency }: { currency: string }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const [list, setList] = useState<{ id: number; username: string; balance: number }[] | null>(null);
  const [adjust, setAdjust] = useState<{ id: number; username: string } | null>(null);
  const [history, setHistory] = useState<{ username: string; txs: WalletTx[] } | null>(null);
  const [amount, setAmount] = useState("");
  const [note, setNote] = useState("");
  const load = useCallback(() => api<{ id: number; username: string; balance: number }[]>("billing/wallets").then(setList).catch(() => setList([])), []);
  useEffect(() => { load(); }, [load]);
  const save = () => {
    if (!adjust) return;
    const v = parseInt(amount.replace(/[^\d-]/g, ""), 10) || 0;
    api(`billing/wallets/${adjust.id}`, { method: "POST", body: { amount: v, note } })
      .then(() => { toast(t("saved")); setAdjust(null); setAmount(""); setNote(""); load(); })
      .catch((e) => toast(e.message, "error"));
  };
  if (!list) return <Skeleton className="h-40" />;
  return (
    <Panel pad={false}>
      {list.length === 0 ? <Empty icon={<Wallet className="size-6" />} title={t("noTransactions")} /> : (
        <ul className="divide-y divide-hair">
          {list.map((w) => (
            <li key={w.id} className="flex items-center gap-3 px-[var(--pad)] py-3">
              <span className="flex-1 font-medium" dir="ltr">{w.username}</span>
              <span className="num font-semibold">{n(w.balance)} <span className="text-ink3 font-normal text-[12px]">{currency}</span></span>
              <Button size="sm" onClick={() => api<WalletTx[]>(`billing/wallets/${w.id}`).then((txs) => setHistory({ username: w.username, txs }))}>{t("walletHistory")}</Button>
              <Button size="sm" onClick={() => setAdjust(w)}>{t("adjustBalance")}</Button>
            </li>
          ))}
        </ul>
      )}
      <Dialog open={!!adjust} onClose={() => setAdjust(null)} title={t("adjustBalance") + (adjust ? " · " + adjust.username : "")}
        footer={<><Button onClick={() => setAdjust(null)}>{t("cancel")}</Button><Button variant="primary" onClick={save}>{t("save")}</Button></>}>
        <Label hint={t("adjustHint")}>{t("amountField")}</Label>
        <Input dir="ltr" className="num mt-1.5" value={amount} onChange={(e) => setAmount(e.target.value)} autoFocus />
        <Label>{t("note")}</Label>
        <Input className="mt-1.5" value={note} maxLength={120} onChange={(e) => setNote(e.target.value)} />
      </Dialog>
      <Sheet open={!!history} onClose={() => setHistory(null)} title={t("walletHistory")} sub={history?.username}>
        <TxList txs={history?.txs ?? []} currency={currency} lang={lang} />
      </Sheet>
    </Panel>
  );
}

export function TxList({ txs, currency, lang }: { txs: WalletTx[]; currency: string; lang: "fa" | "en" }) {
  const { t, n } = useI18n();
  if (txs.length === 0) return <Empty icon={<Wallet className="size-6" />} title={t("noTransactions")} />;
  return (
    <ul className="divide-y divide-hair">
      {txs.map((x) => (
        <li key={x.id} className="flex items-center gap-3 py-2.5">
          <span className={cx("num font-semibold w-32", x.amount >= 0 ? "text-online" : "text-bad")} dir="ltr">{x.amount >= 0 ? "+" : ""}{n(x.amount)}</span>
          <span className="flex-1 min-w-0 truncate text-[13px]" dir="auto">{x.note}</span>
          <span className="num text-[12px] text-ink3">{n(x.balance)} {currency}</span>
          <span className="text-[12px] text-ink3 w-20 text-end">{ago(x.at, lang)}</span>
        </li>
      ))}
    </ul>
  );
}
