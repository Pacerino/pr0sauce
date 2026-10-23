import { redirect } from "next/navigation";
import { Kpis } from "@/components/Kpis";
import { SearchBox } from "@/components/SearchBox";
import { SongRow } from "@/components/SongRow";
import { ApiError, api } from "@/lib/api";
import { parseItemId } from "@/lib/ids";
import type { SongList, Stats } from "@/lib/types";

export const dynamic = "force-dynamic";

type SP = Record<string, string | string[] | undefined>;

function first(v: string | string[] | undefined): string {
  return (Array.isArray(v) ? v[0] : v) ?? "";
}

export default async function Home({ searchParams }: { searchParams: Promise<SP> }) {
  const sp = await searchParams;
  const q = first(sp.q).trim();
  const cursor = first(sp.cursor);

  const id = q ? parseItemId(q) : null;
  if (id !== null) redirect(`/${id}`);

  let list: SongList | null = null;
  let stats: Stats | null = null;
  let invalid = false;
  try {
    [list, stats] = await Promise.all([
      api.songs({ q: q || undefined, cursor: cursor || undefined }),
      // KPIs are only shown on the first page of the unfiltered view.
      !q && !cursor ? api.stats() : Promise.resolve(null),
    ]);
  } catch (e) {
    if (e instanceof ApiError && e.status === 400) invalid = true;
    else throw e;
  }

  const now = new Date();
  const more = new URLSearchParams();
  if (q) more.set("q", q);
  if (list?.nextCursor) more.set("cursor", list.nextCursor);

  return (
    <>
      <section className="pt-6 pb-2 text-center">
        <h1 className="text-2xl font-bold">Welches Lied läuft da?</h1>
        <p className="text-[#888]">Alle Songs, die @Sauce auf pr0gramm erkannt hat</p>
      </section>
      <SearchBox defaultValue={q} />
      {stats && <Kpis stats={stats} />}

      <h2 className="mx-0 my-1.5 text-[11px] tracking-wider text-[#888] uppercase">
        {q ? `Ergebnisse für „${q}“` : "Zuletzt erkannt"}
      </h2>

      {invalid ? (
        <p role="alert" className="rounded bg-surface p-3">
          Ungültige Anfrage
        </p>
      ) : list && list.items.length > 0 ? (
        <>
          <div className="flex flex-col gap-1.5">
            {list.items.map((s) => (
              <SongRow key={s.itemId} song={s} now={now} />
            ))}
          </div>
          {list.nextCursor && (
            <p className="mt-4 text-center">
              <a href={`/?${more.toString()}`} className="rounded bg-surface px-4 py-2 hover:text-accent">
                Mehr laden
              </a>
            </p>
          )}
        </>
      ) : (
        <p className="rounded bg-surface p-3 text-[#888]">Keine Songs gefunden.</p>
      )}
    </>
  );
}
