import { lazy, Suspense } from "react";
import { useI18n } from "../lib/i18n";
import { Skeleton } from "../components/ui";
import { PageHead } from "../App";

// the map and its world data load only when this page opens
const EarthView = lazy(() => import("../components/Earth").then((m) => ({ default: m.EarthView })));

/** Where every user connects from and which server they reach, live. */
export function MapPage() {
  const { t } = useI18n();
  return (
    <div>
      <PageHead title={t("liveMap")} sub={t("liveMapSub")} />
      <Suspense fallback={<Skeleton className="h-[560px]" />}>
        <EarthView />
      </Suspense>
    </div>
  );
}
