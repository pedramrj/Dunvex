import { useCallback, useEffect, useState } from "react";
import { Percent, Plus, Receipt, Wallet as WalletIcon } from "lucide-react";
import { api, money, type Order, type PayMethod, type WalletInfo } from "../lib/api";
import { ago } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import { Button, Empty, ErrorState, Input, Label, Panel, Segmented, Skeleton, StatCard, useToast } from "../components/ui";
import { PageHead } from "../App";
import { OrderBadge, TxList } from "./Billing";

/** A reseller's wallet: balance, top-ups and what it paid for. */
export function WalletPage() {
  const { t, lang, pc, n } = useI18n();
  const toast = useToast();
  const [w, setW] = useState<WalletInfo | null>(null);
  const [orders, setOrders] = useState<Order[]>([]);
  const [err, setErr] = useState("");
  const [amount, setAmount] = useState("");
  const [method, setMethod] = useState<PayMethod | "">("");
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => {
    api<WalletInfo>("wallet").then((x) => { setW(x); setMethod((m) => m || (x.methods?.[0] ?? "")); }).catch((e) => setErr(e.message));
    api<Order[]>("billing/orders?limit=50").then(setOrders).catch(() => {});
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load]);
  if (!w) return err ? <ErrorState message={err} onRetry={load} /> : <Skeleton className="h-96" />;
  const methods = w.methods ?? [];
  const topUp = () => {
    const v = parseInt(amount.replace(/[^\d]/g, ""), 10) || 0;
    // the payment page opens in a new tab before the answer comes back, so
    // browsers do not block it as a pop-up
    const tab = window.open("", "_blank");
    setBusy(true);
    api<Order>("wallet/topup", { method: "POST", body: { amount: v, method } })
      .then((o) => {
        if (tab) tab.location.href = o.page;
        else window.location.href = o.page;
        setAmount("");
        load();
      })
      .catch((e) => { tab?.close(); toast(e.message, "error"); })
      .finally(() => setBusy(false));
  };
  return (
    <>
      <PageHead title={t("wallet")} sub={w.enabled ? t("walletPageHint") : t("walletOff")} />
      <div className="grid gap-[var(--gap)] grid-cols-2 mb-[var(--gap)]">
        <StatCard dense icon={<WalletIcon className="size-[18px]" />} label={t("balance")} value={<span className="num">{n(w.balance)} <span className="text-[13px] font-normal text-ink3">{w.currency}</span></span>} accent="var(--ok)" />
        <StatCard dense icon={<Percent className="size-[18px]" />} label={t("yourDiscount")} value={<span className="num">{pc(w.discount, 0)}</span>} />
      </div>
      {w.enabled && (
        <Panel title={t("topUp")} sub={t("topUpHint")} icon={<Plus className="size-4" />} className="mb-[var(--gap)]">
          {methods.length === 0 ? <div className="text-ink3 text-[13px]">{t("noMethods")}</div> : (
            <div className="flex flex-wrap items-end gap-3">
              <label className="min-w-0"><Label>{t("amountField")} ({w.currency})</Label>
                <Input inputMode="numeric" dir="ltr" className="num w-48" value={amount} onChange={(e) => { const v = parseInt(e.target.value.replace(/[^\d]/g, ""), 10); setAmount(v ? money(v) : ""); }} /></label>
              <div><Label>{t("payMethod")}</Label><Segmented value={method} onChange={setMethod} options={methods.map((m) => ({ value: m, label: t(("method_" + m) as Key) }))} /></div>
              <Button variant="primary" loading={busy} disabled={!amount || !method} onClick={topUp}>{t("topUp")}</Button>
            </div>
          )}
        </Panel>
      )}
      <div className="grid gap-[var(--gap)] xl:grid-cols-2 items-start">
        <Panel title={t("ordersTab")} icon={<Receipt className="size-4" />}>
          {orders.length === 0 ? <Empty icon={<Receipt className="size-6" />} title={t("noOrders")} /> : (
            <ul className="divide-y divide-hair">
              {orders.map((o) => (
                <li key={o.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2.5">
                  <span className="mono text-[12.5px] w-24" dir="ltr">{o.code}</span>
                  <span className="flex-1 min-w-0 truncate text-[13px]">{t(("orderKind_" + o.kind) as Key)}{o.planName && " · " + o.planName}{o.userName && " · " + o.userName}</span>
                  <span className="num text-[13px] font-semibold">{n(o.amount)}</span>
                  <OrderBadge status={o.status} />
                  {(o.status === "pending" || o.status === "rejected") && o.kind === "topup" && <a className="text-primary2 text-[12.5px] hover:underline" href={o.page} target="_blank" rel="noopener">{t("open")}</a>}
                  <span className="text-[12px] text-ink3 w-20 text-end">{ago(o.createdAt, lang)}</span>
                </li>
              ))}
            </ul>
          )}
        </Panel>
        <Panel title={t("walletHistory")} icon={<WalletIcon className="size-4" />}>
          <TxList txs={w.transactions} currency={w.currency} lang={lang} />
        </Panel>
      </div>
    </>
  );
}
