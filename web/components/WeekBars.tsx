import { isoWeekNumber } from "@/lib/stats";

const W = 520;
const H = 80;
const GAP = 3;

function dateDe(ymd: string): string {
  const [y, m, d] = ymd.split("-");
  return `${d}.${m}.${y}`;
}

export function WeekBars({ weeks }: { weeks: { week: string; count: number }[] }) {
  if (weeks.every((w) => w.count === 0)) {
    return <p className="rounded bg-surface p-3 text-[#888]">noch keine Daten</p>;
  }
  const max = Math.max(1, ...weeks.map((w) => w.count));
  const bw = (W - GAP * (weeks.length - 1)) / weeks.length;
  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      width="100%"
      role="img"
      aria-label="Anfragen pro Woche"
      className="block rounded bg-surface p-2"
    >
      {weeks.map((w, i) => {
        const h = (w.count / max) * H;
        return (
          <rect
            key={w.week}
            x={i * (bw + GAP)}
            y={H - h}
            width={bw}
            height={h}
            fill="#ee4d2e"
          >
            <title>{`KW ${isoWeekNumber(w.week)} · ${dateDe(w.week)}: ${w.count} Anfragen`}</title>
          </rect>
        );
      })}
    </svg>
  );
}
