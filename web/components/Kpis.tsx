import { formatNumberDe, formatPercentDe } from "@/lib/format";
import type { Stats } from "@/lib/types";

export function Kpis({ stats }: { stats: Stats }) {
  const items = [
    { value: formatNumberDe(stats.totalLookups), label: "Anfragen" },
    { value: formatPercentDe(stats.hitRate), label: "erkannt" },
    { value: formatNumberDe(stats.today), label: "heute" },
  ];
  return (
    <div className="mb-3 flex gap-2">
      {items.map((k) => (
        <div key={k.label} className="flex-1 rounded bg-surface p-2 text-center">
          <b className="block text-base text-accent">{k.value}</b>
          <small className="text-[#888]">{k.label}</small>
        </div>
      ))}
    </div>
  );
}
