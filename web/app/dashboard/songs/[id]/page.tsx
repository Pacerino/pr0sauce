import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { Flash } from "@/components/Flash";
import { modGet, sourceLabel, snapshotProviderLabel, type HistoryEntry } from "@/lib/dashboard-data";
import { relativeTimeDe } from "@/lib/format";
import { parseItemId } from "@/lib/ids";
import { revertAction } from "../../actions";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Verlauf", robots: { index: false } };

type Props = {
  params: Promise<{ id: string }>;
  searchParams: Promise<{ [k: string]: string | string[] | undefined }>;
};

const str = (o: unknown, k: string): string | null => {
  if (typeof o !== "object" || o === null) return null;
  const v = (o as Record<string, unknown>)[k];
  return typeof v === "string" ? v : null;
};

export default async function HistoryPage({ params, searchParams }: Props) {
  const id = parseItemId((await params).id);
  if (id === null) notFound();
  const sp = await searchParams;
  const list = await modGet<HistoryEntry[]>(`/api/internal/songs/${id}/history`);
  const now = new Date();

  return (
    <section>
      <h1 className="mb-1 text-xl font-bold">Verlauf #{id}</h1>
      <p className="mb-4 text-sm">
        <Link href={`/${id}`} className="text-accent hover:underline">
          Song-Seite
        </Link>
        {" · "}
        <a href={`https://pr0gramm.com/new/${id}`} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">
          pr0gramm
        </a>
      </p>
      <Flash e={sp.e} ok={sp.ok} />
      {list === null ? (
        <p className="text-[#888]">Der Verlauf ist gerade nicht verfügbar.</p>
      ) : list.length === 0 ? (
        <p className="text-[#888]">Keine Änderungen bisher.</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {list.map((h, i) => (
            <li key={h.id} className="rounded bg-[#1f2124] p-3">
              <div className="mb-1 text-xs text-[#888]">
                {sourceLabel(h.source)} · {h.actor ?? "System"} · {relativeTimeDe(h.createdAt, now)}
                {i === list.length - 1 ? " · Original des Bots" : ""}
              </div>
              <div className="break-words">
                {str(h.snapshot, "title") ?? "–"} – {str(h.snapshot, "artist") ?? "–"}
              </div>
              <div className="text-xs text-[#aaa]">Quelle: {snapshotProviderLabel(str(h.snapshot, "provider") ?? "")}</div>
              <form action={revertAction} className="mt-2 flex flex-wrap items-center gap-3">
                <input type="hidden" name="itemId" value={id} />
                <input type="hidden" name="historyId" value={h.id} />
                <label className="flex items-center gap-1.5 text-xs">
                  <input type="checkbox" name="confirm" value="yes" required />
                  Ja, wirklich zurücksetzen
                </label>
                <button type="submit" className="cursor-pointer rounded bg-surface px-3 py-1.5 text-xs font-medium">
                  Zurücksetzen
                </button>
              </form>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
