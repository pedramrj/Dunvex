// Panel events are stored in English by the server. This turns the known
// messages into Persian for the Persian interface; anything unknown is shown
// as it is, left to right.

const iso = (s: string) => "⁨" + s + "⁩"; // keep names, IPs and versions in their own direction
const fd = (s: string) => s.replace(/\d/g, (d) => "۰۱۲۳۴۵۶۷۸۹"[+d]);

const roles: Record<string, string> = { owner: "مالک", admin: "مدیر", reseller: "نماینده فروش" };
const states: Record<string, string> = { active: "فعال", disabled: "غیرفعال", expired: "منقضی", limited: "حجم‌تمام", paused: "متوقف" };
const reasons: Record<string, string> = { "wrong password": "رمز اشتباه", "wrong two-step code": "کد دومرحله‌ای اشتباه" };
const bulk: Record<string, string> = { enable: "فعال‌سازی", disable: "غیرفعال‌سازی", delete: "حذف", reset: "صفر کردن مصرف", extend: "تمدید", "add-data": "افزودن حجم" };

type Rule = [RegExp, (m: string[]) => string];

const rules: Rule[] = [
  [/^admin (.+) signed in from (.+)$/, (m) => `${iso(m[1])} از ${iso(m[2])} وارد شد`],
  [/^owner account (.+) created from (.+)$/, (m) => `حساب مالک ${iso(m[1])} از ${iso(m[2])} ساخته شد`],
  [/^(.+) signed out a session of (.+) \((.+), (.+)\)$/, (m) => `${iso(m[1])} نشست ${iso(m[2])} (${iso(m[3])}، ${iso(m[4])}) را بست`],
  [/^(.+) signed out a session \((.+), (.+)\)$/, (m) => `${iso(m[1])} یک نشست (${iso(m[2])}، ${iso(m[3])}) را بست`],
  [/^(.+) signed out (\d+) other session\(s\)$/, (m) => `${iso(m[1])} از ${fd(m[2])} نشست دیگر خارج شد`],
  [/^(.+) created the API key "(.*)"$/, (m) => `${iso(m[1])} کلید API ${iso(m[2])} را ساخت`],
  [/^(.+) deleted the API key "(.*)"$/, (m) => `${iso(m[1])} کلید API ${iso(m[2])} را حذف کرد`],
  [/^(.+) added the server (.+) \((.+)\)$/, (m) => `${iso(m[1])} سرور ${iso(m[2])} (${iso(m[3])}) را افزود`],
  [/^server (.+) was added and paired$/, (m) => `سرور ${iso(m[1])} اضافه و جفت شد`],
  [/^server (.+) is not reachable: (.+)$/, (m) => `سرور ${iso(m[1])} در دسترس نیست: ${iso(m[2])}`],
  [/^server (.+) is reachable again$/, (m) => `سرور ${iso(m[1])} دوباره در دسترس است`],
  [/^(.+) removed the server (.+) and its protocols$/, (m) => `${iso(m[1])} سرور ${iso(m[2])} و پروتکل‌هایش را حذف کرد`],
  [/^failed sign-in for "(.*)" from (.+) \((.+)\)$/, (m) => `ورود ناموفق ${iso(m[1])} از ${iso(m[2])} (${reasons[m[3]] ?? iso(m[3])})`],
  [/^(.+) added admin (.+) \((.+)\)$/, (m) => `${iso(m[1])} مدیر ${iso(m[2])} را با نقش ${roles[m[3]] ?? iso(m[3])} افزود`],
  [/^(.+) removed admin (.+)$/, (m) => `${iso(m[1])} مدیر ${iso(m[2])} را حذف کرد`],
  [/^(.+) turned on two-step login$/, (m) => `${iso(m[1])} ورود دومرحله‌ای را روشن کرد`],
  [/^(.+) turned off two-step login$/, (m) => `${iso(m[1])} ورود دومرحله‌ای را خاموش کرد`],
  [/^(.+) was still connected after losing access; Xray restarted to disconnect it$/, (m) => `${iso(m[1])} پس از قطع دسترسی هنوز وصل بود؛ Xray برای قطع آن دوباره راه افتاد`],
  [/^(.+): port (\d+) is busy, that protocol was paused$/, (m) => `${iso(m[1])}: پورت ${iso(m[2])} اشغال است و این پروتکل متوقف شد`],
  [/^(.+) updated to (.+)$/, (m) => `${iso(m[1])} به نسخهٔ ${iso(m[2])} به‌روز شد`],
  [/^(.+) added on port (\d+)$/, (m) => `${iso(m[1])} روی پورت ${iso(m[2])} اضافه شد`],
  [/^inbound on port (\d+) removed$/, (m) => `ورودی پورت ${iso(m[1])} حذف شد`],
  [/^(.+) used (\d+) devices at once \(limit (\d+)\) and is paused for (\d+) minutes$/, (m) =>
    `${iso(m[1])} هم‌زمان با ${fd(m[2])} دستگاه وصل شد (سقف ${fd(m[3])}) و ${fd(m[4])} دقیقه متوقف شد`],
  [/^settings updated$/, () => "تنظیمات به‌روز شد"],
  [/^certificate issued for (.+)$/, (m) => `گواهی برای ${iso(m[1])} صادر شد`],
  [/^(.+) changed from Telegram \((.+)\)$/, (m) => `${iso(m[1])} از تلگرام تغییر کرد (${iso(m[2])})`],
  [/^(.+) connected for the first time; plan of (\d+) days started$/, (m) => `${iso(m[1])} برای اولین بار وصل شد و طرح ${fd(m[2])} روزه شروع شد`],
  [/^(.+) created (\d+) user\(s\) \((.+)\)$/, (m) => `${iso(m[1])} ${fd(m[2])} کاربر ساخت (${iso(m[3])})`],
  [/^(\d+) user\(s\) created \((.+)\)$/, (m) => `${fd(m[1])} کاربر ساخته شد (${iso(m[2])})`],
  [/^(.+) is now (\w+) and was disconnected$/, (m) => `${iso(m[1])} ${states[m[2]] ?? iso(m[2])} شد و اتصالش قطع شد`],
  [/^(.+) is now (\w+)$/, (m) => `${iso(m[1])} ${states[m[2]] ?? iso(m[2])} شد`],
  [/^(.+) linked Telegram$/, (m) => `${iso(m[1])} تلگرام را وصل کرد`],
  [/^(.+) usage reset \((.+)\)$/, (m) => `مصرف ${iso(m[1])} صفر شد (${iso(m[2])})`],
  [/^(.+): bulk (\S+) on (\d+) user\(s\)$/, (m) => `${iso(m[1])}: ${bulk[m[2]] ?? iso(m[2])} گروهی روی ${fd(m[3])} کاربر`],
  [/^credentials of (.+) regenerated$/, (m) => `اعتبار اتصال ${iso(m[1])} از نو ساخته شد`],
  [/^user (.+) created from Telegram$/, (m) => `کاربر ${iso(m[1])} از تلگرام ساخته شد`],
  [/^user (.+) deleted from Telegram$/, (m) => `کاربر ${iso(m[1])} از تلگرام حذف شد`],
  [/^user (.+) deleted$/, (m) => `کاربر ${iso(m[1])} حذف شد`],
];

/** The event text for the current language, and the direction to show it in. */
export function eventText(message: string, lang: string): { text: string; dir: "rtl" | "ltr" } {
  if (lang === "fa") {
    for (const [re, fa] of rules) {
      const m = re.exec(message);
      if (m) return { text: fa(m), dir: "rtl" };
    }
  }
  return { text: message, dir: "ltr" };
}
