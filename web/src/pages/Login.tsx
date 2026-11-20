import { useEffect, useState } from "react";
import { ArrowLeft, ArrowRight, Eye, EyeOff, KeyRound, Languages, Lock, ShieldCheck, Sparkles, Terminal, User } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { Button, Input, Label } from "../components/ui";
import { Logo } from "../components/Logo";

/** The one-time token from a first-run link: #/setup/<token>. */
const setupToken = () => /^#\/setup\/([A-Za-z0-9]+)/.exec(location.hash)?.[1] ?? "";

export function Login({ onDone }: { onDone: () => void }) {
  const { t, lang, setLang } = useI18n();
  const [pending, setPending] = useState<boolean | null>(null);
  useEffect(() => {
    api<{ pending: boolean }>("setup-state").then((r) => setPending(r.pending)).catch(() => setPending(false));
  }, []);
  const [username, setUser] = useState("");
  const [password, setPass] = useState("");
  const [code, setCode] = useState("");
  const [need2fa, setNeed2fa] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const Arrow = lang === "fa" ? ArrowLeft : ArrowRight;

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    fetchLogin({ username, password, code: need2fa ? code : "" })
      .then(onDone)
      .catch((e: ApiError & { need2fa?: boolean }) => {
        if (e.need2fa) {
          if (need2fa) setErr(t("enterCode"));
          setNeed2fa(true);
          setCode("");
          return;
        }
        setErr(e.status === 401 ? t("wrongLogin") : e.message);
      })
      .finally(() => setBusy(false));
  };

  return (
    <div className="relative min-h-dvh overflow-hidden">
      <div className="aurora" aria-hidden />
      <MetroArt />
      <div className="relative z-[1] min-h-dvh grid lg:grid-cols-[1.15fr_1fr]">
        {/* the logo stage */}
        <div className="flex flex-col items-center justify-center px-8 pt-14 pb-6 lg:py-12">
          <div className="relative">
            <div className="absolute inset-0 -z-10 blur-3xl rounded-full bg-primary/25 scale-75" aria-hidden />
            <Logo part="full" className="w-[230px] sm:w-[300px] lg:w-[420px] drop-shadow-[0_24px_60px_color-mix(in_oklab,var(--primary)_30%,transparent)]" />
          </div>
        </div>

        {/* the form */}
        <div className="flex flex-col justify-center px-5 pb-12 lg:py-12">
          {pending === null ? <div className="h-[420px]" aria-busy="true" /> : pending ? <SetupCard onDone={onDone} /> : (
          <form onSubmit={submit} className="rise glass-strong edge w-full max-w-[400px] mx-auto rounded-[26px] p-7 sm:p-8 shadow-[0_40px_100px_-40px_rgba(0,0,0,.9)]">
            <div className="flex items-center gap-3 mb-7">
              <span className="grid place-items-center size-11 rounded-2xl brand-gradient text-white shadow-[0_10px_30px_-10px_color-mix(in_oklab,var(--primary)_80%,transparent)]">
                {need2fa ? <ShieldCheck className="size-5" /> : <Lock className="size-5" />}
              </span>
              <div>
                <h1 className="text-[20px] font-bold leading-tight">{need2fa ? t("twoFactor") : t("signIn")}</h1>
                <p className="text-ink3 text-[12.5px]">{need2fa ? t("enterCode") : "Dunvex · Powered by Pedram"}</p>
              </div>
            </div>

            {!need2fa ? (
              <>
                <label className="block mb-4">
                  <Label>{t("username")}</Label>
                  <div className="relative">
                    <User className="size-4 text-ink3 absolute top-3.5 [inset-inline-start:14px] pointer-events-none" />
                    <Input value={username} onChange={(e) => setUser(e.target.value)} autoComplete="username" dir="ltr" autoFocus required className="ps-10" />
                  </div>
                </label>
                <label className="block mb-6">
                  <Label>{t("password")}</Label>
                  <div className="relative">
                    <KeyRound className="size-4 text-ink3 absolute top-3.5 [inset-inline-start:14px] pointer-events-none" />
                    <Input type="password" value={password} onChange={(e) => setPass(e.target.value)} autoComplete="current-password" dir="ltr" required className="ps-10" />
                  </div>
                </label>
              </>
            ) : (
              <label className="block mb-6">
                <Label>{t("code")}</Label>
                <Input value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))} inputMode="numeric" autoComplete="one-time-code" dir="ltr" autoFocus required
                  className="mono text-center text-[24px] tracking-[.5em] h-14" placeholder="••••••" />
              </label>
            )}

            {err && <div role="alert" className="mb-4 rounded-xl border border-bad/40 bg-bad/[.08] px-3 py-2.5 text-sm text-bad">{err}</div>}
            <Button variant="primary" size="lg" className="w-full" loading={busy} type="submit" icon={<Arrow className="size-4" />}>
              {t("login")}
            </Button>
            {need2fa && (
              <button type="button" onClick={() => { setNeed2fa(false); setErr(""); }} className="mt-4 text-ink3 hover:text-ink text-[13px] w-full">{t("back")}</button>
            )}
          </form>
          )}
          <button type="button" onClick={() => setLang(lang === "fa" ? "en" : "fa")} className="mt-6 mx-auto inline-flex items-center gap-2 text-ink3 hover:text-ink text-[13px] h-10 px-3 rounded-xl">
            <Languages className="size-4" />
            {lang === "fa" ? "English" : "فارسی"}
          </button>
        </div>
      </div>
    </div>
  );
}

/** First visit: the owner picks the username and password, using the one-time link from the install. */
function SetupCard({ onDone }: { onDone: () => void }) {
  const { t, lang } = useI18n();
  const [token, setToken] = useState(setupToken);
  useEffect(() => {
    const on = () => setToken(setupToken());
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  const [username, setUser] = useState("admin");
  const [password, setPass] = useState("");
  const [confirm, setConfirm] = useState("");
  const [show, setShow] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const Arrow = lang === "fa" ? ArrowLeft : ArrowRight;
  const score = Math.min(4, [password.length >= 8, password.length >= 12, /[A-Z]/.test(password) && /[a-z]/.test(password), /\d/.test(password) && /[^A-Za-z0-9]/.test(password)].filter(Boolean).length);
  const meter = ["var(--bad)", "var(--bad)", "var(--warn)", "var(--primary2)", "var(--ok)"][score];
  const card = "rise glass-strong edge w-full max-w-[420px] mx-auto rounded-[26px] p-7 sm:p-8 shadow-[0_40px_100px_-40px_rgba(0,0,0,.9)]";

  if (!token) {
    return (
      <div className={card}>
        <div className="flex items-center gap-3 mb-5">
          <span className="grid place-items-center size-11 rounded-2xl brand-gradient text-white"><Terminal className="size-5" /></span>
          <div>
            <h1 className="text-[20px] font-bold leading-tight">{t("setupNeeded")}</h1>
            <p className="text-ink3 text-[12.5px]">Dunvex · Powered by Pedram</p>
          </div>
        </div>
        <p className="text-ink2 text-[14px] leading-7">{t("setupNoLink")}</p>
        <div className="mt-4 rounded-xl border border-hair bg-fg/[.04] px-3.5 py-3 mono text-[13px]" dir="ltr" translate="no">dunvex info</div>
      </div>
    );
  }

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (password.length < 8) return setErr(t("passwordShort"));
    if (password !== confirm) return setErr(t("passwordsDiffer"));
    setBusy(true);
    setErr("");
    api("setup", { body: { token, username: username.trim(), password } })
      .then(() => { history.replaceState(null, "", location.pathname + "#/overview"); onDone(); })
      .catch((e: ApiError) => setErr(e.message))
      .finally(() => setBusy(false));
  };

  return (
    <form onSubmit={submit} className={card}>
      <div className="flex items-center gap-3 mb-6">
        <span className="grid place-items-center size-11 rounded-2xl brand-gradient text-white"><Sparkles className="size-5" /></span>
        <div>
          <h1 className="text-[20px] font-bold leading-tight">{t("setupTitle")}</h1>
          <p className="text-ink3 text-[12.5px]">{t("setupSub")}</p>
        </div>
      </div>
      <label className="block mb-4">
        <Label>{t("username")}</Label>
        <div className="relative">
          <User className="size-4 text-ink3 absolute top-3.5 [inset-inline-start:14px] pointer-events-none" />
          <Input value={username} onChange={(e) => setUser(e.target.value)} autoComplete="username" dir="ltr" required minLength={3} maxLength={32} pattern="[A-Za-z0-9._\-]{3,32}" className="ps-10" />
        </div>
      </label>
      <label className="block mb-2">
        <Label>{t("password")}</Label>
        <div className="relative">
          <KeyRound className="size-4 text-ink3 absolute top-3.5 [inset-inline-start:14px] pointer-events-none" />
          <Input type={show ? "text" : "password"} value={password} onChange={(e) => setPass(e.target.value)} autoComplete="new-password" dir="ltr" required autoFocus className="ps-10 pe-11" />
          <button type="button" onClick={() => setShow(!show)} aria-label={show ? t("hide") : t("show")} className="absolute top-1.5 [inset-inline-end:6px] grid place-items-center size-8 rounded-lg text-ink3 hover:text-ink">
            {show ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
          </button>
        </div>
      </label>
      <div className="flex gap-1 mb-4" aria-hidden>
        {[0, 1, 2, 3].map((i) => <i key={i} className="h-1 flex-1 rounded-full transition-colors" style={{ background: i < score ? meter : "color-mix(in oklab, var(--fg) 10%, transparent)" }} />)}
      </div>
      <label className="block mb-6">
        <Label>{t("confirmPassword")}</Label>
        <Input type={show ? "text" : "password"} value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" dir="ltr" required />
      </label>
      {err && <div role="alert" className="mb-4 rounded-xl border border-bad/40 bg-bad/[.08] px-3 py-2.5 text-sm text-bad">{err}</div>}
      <Button variant="primary" size="lg" className="w-full" loading={busy} type="submit" icon={<Arrow className="size-4" />}>{t("createAccount")}</Button>
      <p className="mt-4 text-ink3 text-[12px] leading-6">{t("setupOnce")}</p>
    </form>
  );
}

// login needs the extra `need2fa` flag that api() drops
async function fetchLogin(body: { username: string; password: string; code: string }) {
  try {
    return await api("login", { body });
  } catch (e) {
    const err = e as ApiError & { need2fa?: boolean; data?: any };
    if (err.status === 401 && err.data?.need2fa) err.need2fa = true;
    throw err;
  }
}

/** Faint metro lines behind the login: the four protocol families meeting. */
function MetroArt() {
  return (
    <svg viewBox="0 0 1200 800" className="absolute inset-0 w-full h-full opacity-[.16] pointer-events-none" preserveAspectRatio="xMidYMid slice" aria-hidden>
      <path d="M-20 260 H340 L440 360 V820" fill="none" stroke="var(--color-reality)" strokeWidth={10} strokeLinejoin="round" />
      <path d="M1220 180 H760 L660 280 V360" fill="none" stroke="var(--color-cdn)" strokeWidth={10} strokeLinejoin="round" />
      <path d="M-20 560 H300 L400 460" fill="none" stroke="var(--color-udp)" strokeWidth={10} strokeLinejoin="round" />
      <path d="M1220 600 H780 L680 500 V-20" fill="none" stroke="var(--color-classic)" strokeWidth={10} strokeLinejoin="round" />
    </svg>
  );
}
