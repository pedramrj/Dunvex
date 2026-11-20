import { useEffect, useRef, useState } from "react";
import gsap from "gsap";
import { useGSAP } from "@gsap/react";
import { Particles } from "@/components/ui/particles";
import { Logo, LOGO_SRC, logoParts } from "./Logo";
import { usePrefs } from "../lib/prefs";
import { useI18n } from "../lib/i18n";

gsap.registerPlugin(useGSAP);

/** Should the intro play now? Once per browser session by default. */
export function introDue(mode: "always" | "session" | "off"): boolean {
  if (mode === "off") return false;
  if (mode === "always") return true;
  try {
    return sessionStorage.getItem("dvx-intro") !== "1";
  } catch {
    return true;
  }
}

/**
 * The opening sequence: the emblem rises out of the dark, a light sweeps
 * across the metal, the wordmark opens from its centre, an ice line draws and
 * the credit settles. Click, tap or any key skips. Reduced motion shows a
 * still frame for a moment.
 */
export function Intro({ onDone }: { onDone: () => void }) {
  const root = useRef<HTMLDivElement>(null);
  const { prefs } = usePrefs();
  const { lang } = useI18n();
  const [gone, setGone] = useState(false);
  const reduced = prefs.motion === "reduced" || (typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches);
  const tl = useRef<gsap.core.Timeline | null>(null);

  const finish = () => {
    if (gone) return;
    setGone(true);
    try {
      sessionStorage.setItem("dvx-intro", "1");
    } catch {
      /* storage blocked */
    }
    onDone();
  };

  useGSAP(
    () => {
      if (reduced) {
        const id = setTimeout(finish, 700);
        return () => clearTimeout(id);
      }
      const t = gsap.timeline({ defaults: { ease: "expo.out" }, onComplete: finish });
      tl.current = t;
      t.set(".i-stage", { opacity: 1 })
        .fromTo(".i-particles", { opacity: 0 }, { opacity: 1, duration: 1.2, ease: "power2.out" }, 0)
        .fromTo(".i-halo", { opacity: 0, scale: 0.55 }, { opacity: 1, scale: 1, duration: 1.6, ease: "power3.out" }, 0.1)
        .fromTo(".i-floor", { opacity: 0, scaleX: 0.4 }, { opacity: 1, scaleX: 1, duration: 1.4 }, 0.25)
        .fromTo(".i-emblem", { opacity: 0, y: 34, scale: 0.9, filter: "blur(18px) brightness(.4)" }, { opacity: 1, y: 0, scale: 1, filter: "blur(0px) brightness(1)", duration: 1.35 }, 0.25)
        .fromTo(".i-sheen", { xPercent: -140 }, { xPercent: 160, duration: 1.05, ease: "power2.inOut" }, 1.05)
        .fromTo(".i-word", { clipPath: "inset(0 50% 0 50%)", opacity: 0 }, { clipPath: "inset(0 0% 0 0%)", opacity: 1, duration: 0.95, ease: "power3.out" }, 1.15)
        .fromTo(".i-line", { scaleX: 0, opacity: 0 }, { scaleX: 1, opacity: 1, duration: 0.8, ease: "power2.out" }, 1.55)
        .fromTo(".i-credit", { opacity: 0, letterSpacing: "0.9em", y: 6 }, { opacity: 1, letterSpacing: "0.42em", y: 0, duration: 0.9, ease: "power3.out" }, 1.75)
        .to({}, { duration: 0.55 })
        .to(".i-lockup", { scale: 0.94, opacity: 0, filter: "blur(10px)", duration: 0.55, ease: "power2.in" })
        .to(".i-stage", { opacity: 0, duration: 0.45, ease: "power1.out" }, "<0.15");
    },
    { scope: root },
  );

  useEffect(() => {
    const skip = () => {
      const t = tl.current;
      if (t && t.progress() < 0.86) t.progress(0.86);
      else finish();
    };
    window.addEventListener("keydown", skip);
    return () => window.removeEventListener("keydown", skip);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const em = logoParts.emblem;
  return (
    <div ref={root} className="fixed inset-0 z-[90]" onClick={() => (tl.current && tl.current.progress() < 0.86 ? tl.current.progress(0.86) : finish())}>
      <div className="i-stage absolute inset-0 opacity-0 bg-[#03050a] overflow-hidden">
        <div className="i-particles absolute inset-0">
          <Particles className="absolute inset-0" quantity={110} ease={70} size={0.5} color="#7fb6ff" staticity={40} />
        </div>
        <div className="i-halo absolute left-1/2 top-[40%] -translate-x-1/2 -translate-y-1/2 w-[70vmin] h-[70vmin] rounded-full"
          style={{ background: "radial-gradient(circle, rgba(57,123,255,.32) 0%, rgba(36,207,210,.12) 35%, transparent 68%)" }} />
        <div className="i-floor absolute left-1/2 bottom-0 -translate-x-1/2 w-[90vw] h-[38vh]"
          style={{ background: "radial-gradient(ellipse at 50% 100%, rgba(79,140,255,.18), transparent 70%)" }} />

        <div className="i-lockup absolute inset-0 flex flex-col items-center justify-center gap-[2.2vmin]">
          <div className="relative">
            <Logo part="emblem" className="i-emblem w-[min(46vmin,420px)] drop-shadow-[0_30px_80px_rgba(57,123,255,.35)]" />
            {/* a light sweeping across the metal, masked to the emblem's own shape */}
            <div className="absolute inset-0 overflow-hidden pointer-events-none"
              style={{
                WebkitMaskImage: `url(${LOGO_SRC})`, maskImage: `url(${LOGO_SRC})`,
                WebkitMaskSize: em.size, maskSize: em.size, WebkitMaskPosition: em.pos, maskPosition: em.pos,
                WebkitMaskRepeat: "no-repeat", maskRepeat: "no-repeat",
              }}>
              <div className="i-sheen absolute inset-y-0 w-1/2" style={{ background: "linear-gradient(100deg, transparent, rgba(255,255,255,.85), rgba(160,215,255,.6), transparent)" }} />
            </div>
          </div>
          <Logo part="wordmark" tone={false} className="i-word w-[min(58vmin,520px)]" />
          <div className="i-line h-px w-[min(26vmin,240px)] origin-center" style={{ background: "linear-gradient(90deg, transparent, #7fd8ff, transparent)", boxShadow: "0 0 18px rgba(127,216,255,.8)" }} />
          <div className="i-credit text-[min(1.9vmin,14px)] font-medium text-[#aab8cc] uppercase" dir="ltr" style={{ letterSpacing: "0.42em", fontFamily: "DVX Geist, sans-serif" }}>
            Powered by Pedram
          </div>
        </div>

        <button onClick={(e) => { e.stopPropagation(); finish(); }}
          className="absolute bottom-6 [inset-inline-end:24px] h-10 px-4 rounded-xl text-[13px] font-medium text-[#cfd9e8] bg-white/[.06] border border-white/10 hover:bg-white/[.1] transition-colors">
          {lang === "fa" ? "رد شدن" : "Skip"}
        </button>
      </div>
    </div>
  );
}
