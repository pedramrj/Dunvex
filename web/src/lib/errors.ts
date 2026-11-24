// Raw errors from the server (Go, the cores, the network) as a sentence an
// owner can act on, in Persian or English. The raw text is kept for the
// details view; anything not recognised is shown as it is.

export interface Readable {
  text: string; // the sentence
  fix?: string; // what to do, when there is a clear step
  raw: string; // the original, for the details view
  known: boolean;
}

type Rule = [RegExp, (m: RegExpExecArray) => { fa: string; en: string; faFix?: string; enFix?: string }];

const iso = (s: string) => "⁨" + s + "⁩";

// "Put "https://1.2.3.4:62051/state": dial tcp …" → the host the request went to
function target(raw: string): string {
  const m = /"https?:\/\/([^/":]+)(?::\d+)?[^"]*"/.exec(raw) ?? /dial (?:tcp|udp) ([^\s:]+):\d+/.exec(raw);
  return m ? m[1] : "";
}

const rules: Rule[] = [
  [/([\w.-]+?)(?:-go)? is not installed/i, (m) => ({
    fa: `هستهٔ ${iso(m[1])} روی این سرور نصب نیست`, en: `The ${m[1]} core is not installed on this server`,
    faFix: "از تنظیمات › هسته‌ها نصبش کنید", enFix: "Install it in Settings › Cores",
  })],
  [/telegram is not reachable/i, () => ({
    fa: "تلگرام از این سرور در دسترس نیست", en: "Telegram cannot be reached from this server",
    faFix: "یک پروکسی برای API تلگرام در تنظیمات ربات بگذارید", enFix: "Set a proxy for the Telegram API in the bot settings",
  })],
  [/actively refused|connection refused|ECONNREFUSED/i, (m) => {
    const h = target(m.input);
    return {
      fa: h ? `${iso(h)} اتصال را رد کرد؛ برنامه روی آن پورت در حال اجرا نیست` : "اتصال رد شد؛ برنامه روی آن پورت در حال اجرا نیست",
      en: h ? `${h} refused the connection: nothing is listening on that port` : "The connection was refused: nothing is listening on that port",
      faFix: "روشن بودن سرور و سرویس Dunvex روی آن را بررسی کنید", enFix: "Check the server is up and its Dunvex service is running",
    };
  }],
  [/i\/o timeout|deadline exceeded|Client\.Timeout|timed out/i, (m) => {
    const h = target(m.input);
    return {
      fa: h ? `${iso(h)} در زمان مقرر جواب نداد` : "جوابی در زمان مقرر نیامد",
      en: h ? `${h} did not answer in time` : "No answer came in time",
      faFix: "ممکن است فایروال یا فیلترینگ مسیر را بسته باشد", enFix: "A firewall or filtering may be blocking the way",
    };
  }],
  [/no such host|server misbehaving|NXDOMAIN/i, () => ({ fa: "این نام در DNS پیدا نشد", en: "The name could not be found in DNS", faFix: "رکورد DNS دامنه را بررسی کنید", enFix: "Check the domain's DNS record" })],
  [/network is unreachable|no route to host/i, () => ({ fa: "از این سرور مسیری به مقصد نیست", en: "There is no route from this server to the destination" })],
  [/x509|certificate|tls: /i, () => ({ fa: "گواهی TLS پذیرفته نشد", en: "The TLS certificate was not accepted", faFix: "گواهی دامنه را در صفحهٔ دامنه‌ها بررسی کنید", enFix: "Check the domain's certificate on the Domains page" })],
  [/address already in use|bind: /i, () => ({ fa: "پورت را برنامهٔ دیگری گرفته است", en: "Another program holds the port", faFix: "پورت دیگری انتخاب کنید یا آن برنامه را ببندید", enFix: "Pick another port or stop that program" })],
  [/permission denied|operation not permitted/i, () => ({ fa: "اجازهٔ این کار روی سرور نیست", en: "The server does not allow this operation" })],
  [/connection reset|broken pipe|unexpected EOF|\bEOF\b/i, () => ({ fa: "اتصال وسط کار قطع شد", en: "The connection dropped midway" })],
  [/\b401\b|unauthori[sz]ed|forbidden|\b403\b/i, () => ({ fa: "درخواست رد شد: کلید یا توکن نادرست است", en: "Rejected: the key or token is wrong" })],
];

/** A raw error as a readable sentence for lang. */
export function readable(raw: string, lang: "fa" | "en"): Readable {
  const s = (raw ?? "").trim();
  for (const [re, f] of rules) {
    const m = re.exec(s);
    if (m) {
      const r = f(m);
      return { text: lang === "fa" ? r.fa : r.en, fix: lang === "fa" ? r.faFix : r.enFix, raw: s, known: true };
    }
  }
  return { text: s, raw: s, known: false };
}

/** Replaces a raw error after a colon in an event line ("server X is not reachable: …"). */
export function readableTail(message: string, lang: "fa" | "en"): string {
  const i = message.indexOf(": ");
  if (i < 0) return message;
  const r = readable(message.slice(i + 2), lang);
  return r.known ? message.slice(0, i + 2) + r.text : message;
}
