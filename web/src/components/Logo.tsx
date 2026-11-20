// The owner-approved Dunvex logo (assets/branding/dunvex-logo.png, uniform
// 512px copy in public/brand). Parts are shown through CSS viewports over the
// same square image: no crop, redraw or recolour. In the light theme the
// metal is darkened with a CSS filter so it stays readable.
export const LOGO_SRC = "brand/dunvex-logo-512.png";

export const logoParts = {
  emblem: { ratio: 1.454, size: "132.7% auto", pos: "56.63% 34.44%" },
  wordmark: { ratio: 8.482, size: "104.85% auto", pos: "53.45% 80.32%" },
  credit: { ratio: 15.81, size: "136.75% auto", pos: "51.34% 86.96%" },
  full: { ratio: 1, size: "100% auto", pos: "50% 50%" },
} as const;

export function Logo({ part = "emblem", height, width, className, tone = true, src = LOGO_SRC }: { part?: keyof typeof logoParts; height?: number; width?: number; className?: string; tone?: boolean; src?: string }) {
  const p = logoParts[part];
  const style: React.CSSProperties = {
    aspectRatio: String(p.ratio),
    backgroundImage: `url(${src})`,
    backgroundSize: p.size,
    backgroundPosition: p.pos,
    backgroundRepeat: "no-repeat",
  };
  if (height) style.height = height;
  if (width) style.width = width;
  return <div role="img" aria-label="Dunvex" className={[tone && part !== "emblem" ? "logo-tone" : "", className].filter(Boolean).join(" ")} style={style} />;
}
