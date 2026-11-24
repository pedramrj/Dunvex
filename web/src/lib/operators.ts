// Everyday names of the networks most users connect from, by AS number.
// Anything else shows the registry's name as it is.
const OPS: Record<number, [string, string]> = {
  44244: ["ایرانسل", "Irancell"],
  197207: ["همراه اول", "MCI"],
  58224: ["مخابرات", "TCI"],
  57218: ["رایتل", "RighTel"],
  31549: ["شاتل", "Shatel"],
  16322: ["پارس‌آنلاین", "ParsOnline"],
  43754: ["آسیاتک", "Asiatech"],
  12880: ["زیرساخت", "ITC"],
  48159: ["زیرساخت", "TIC"],
  50810: ["مبین‌نت", "Mobinnet"],
  49100: ["پیشگامان", "Pishgaman"],
  42337: ["رسپینا", "Respina"],
  25184: ["افرانت", "Afranet"],
  24631: ["فن‌آوا", "Fanava"],
};

/** A network's everyday name, or its registry name shortened. */
export function operatorName(asn: number | undefined, org: string | undefined, lang: "fa" | "en"): string {
  const k = asn ? OPS[asn] : undefined;
  if (k) return lang === "fa" ? k[0] : k[1];
  if (!org) return "";
  return org.replace(/\b(Company|Co\.?|Ltd\.?|LLC|Inc\.?|PJSC|JSC|GmbH|S\.?A\.?|Corporation|Limited)\b/gi, "").replace(/[,\s]+$/, "").replace(/\s{2,}/g, " ").trim();
}
