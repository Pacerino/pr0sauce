import type { Metadata } from "next";
import { WeekBars } from "@/components/WeekBars";
import { api } from "@/lib/api";
import { formatDateDe, formatNumberDe, formatPercentDe, providerLabel } from "@/lib/format";
import { fillWeeks, providerShares } from "@/lib/stats";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: { absolute: "Statistik | pr0sauce" } };

const h2 = "mx-0 mt-5 mb-1.5 text-[11px] tracking-wider text-[#888] uppercase";

export default async function Statistik() {
  const stats = await api.stats();
  const shares = providerShares(stats.providers);
  const top = shares[0];
  const weeks = fillWeeks(stats.perWeek, new Date());
  const since = stats.requestersSince ? formatDateDe(stats.requestersSince) : "";

  const kpis = [
    { value: formatNumberDe(stats.totalLookups), label: "gesamt" },
    { value: formatPercentDe(stats.hitRate), label: "Trefferquote" },
    { value: top ? `${providerLabel(top.provider)} ${top.percent} %` : "–", label: "häufigster Anbieter" },
  ];

  return (
    <>
      <h1 className="pt-6 pb-3 text-2xl font-bold">Statistik</h1>
      <div className="flex gap-2">
        {kpis.map((k) => (
          <div key={k.label} className="flex-1 rounded bg-surface p-2 text-center">
            <b className="block text-base text-accent">{k.value}</b>
            <small className="text-[#888]">{k.label}</small>
          </div>
        ))}
      </div>

      {shares.length > 0 && (
        <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-sm text-[#aaa]">
          {shares.map((s) => (
            <li key={s.provider}>
              {providerLabel(s.provider)} {s.percent} %
            </li>
          ))}
        </ul>
      )}

      <h2 className={h2}>Anfragen pro Woche</h2>
      <WeekBars weeks={weeks} />

      <div className="grid gap-6 sm:grid-cols-2">
        <section>
          <h2 className={h2}>Top Songs</h2>
          {stats.topSongs.length > 0 ? (
            <ol className="flex flex-col gap-1 text-sm">
              {stats.topSongs.map((s, i) => (
                <li key={`${s.artist}-${s.title}-${i}`} className="flex justify-between gap-3">
                  <span>
                    {i + 1}. {s.artist} – {s.title}
                  </span>
                  <span className="text-[#888]">×{formatNumberDe(s.count)}</span>
                </li>
              ))}
            </ol>
          ) : (
            <p className="text-[#888]">noch keine Daten</p>
          )}
        </section>
        <section>
          <h2 className={h2}>Top Fragesteller</h2>
          {stats.topRequesters.length > 0 ? (
            <>
              {since && <p className="mb-1 text-xs text-[#888]">seit {since}</p>}
              <ol className="flex flex-col gap-1 text-sm">
                {stats.topRequesters.map((r, i) => (
                  <li key={`${r.name}-${i}`} className="flex justify-between gap-3">
                    <span>
                      {i + 1}. {r.name}
                    </span>
                    <span className="text-[#888]">×{formatNumberDe(r.count)}</span>
                  </li>
                ))}
              </ol>
            </>
          ) : (
            <p className="text-[#888]">noch keine Daten</p>
          )}
        </section>
      </div>
    </>
  );
}
