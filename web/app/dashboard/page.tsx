import type { Metadata } from "next";
import Link from "next/link";
import { Flash } from "@/components/Flash";
import { changedFields } from "@/lib/dashboard";
import { modGet, parseOffset, type QueueItem, type QueuePage, type Vote } from "@/lib/dashboard-data";
import { relativeTimeDe, safeHttpUrl } from "@/lib/format";
import { approveAction, rejectAction } from "./actions";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Moderation", robots: { index: false } };

type Props = { searchParams: Promise<{ [k: string]: string | string[] | undefined }> };

const input = "rounded bg-[#15171a] px-2 py-1 text-sm outline-none focus:ring-1 focus:ring-accent";
const hl = "rounded bg-[#4a3a12] px-1 text-yellow-200";

function Field({ label, cur, sug, changed }: { label: string; cur: string | null; sug: string | null; changed: boolean }) {
  return (
    <tr className="align-top">
      <th scope="row" className="pr-3 text-left font-normal text-[#888]">
        {label}
      </th>
      <td className="pr-4 break-words text-[#aaa]">{cur || "–"}</td>
      <td className="break-words">{changed ? <span className={hl}>{sug || "–"}</span> : sug || "–"}</td>
    </tr>
  );
}

function Votes({ votes }: { votes: Vote[] | null }) {
  return (
    <details className="mt-2 text-xs">
      <summary className="cursor-pointer text-[#aaa]">Wer hat abgestimmt?</summary>
      {votes === null ? (
        <p className="pt-1 text-[#888]">Stimmen konnten nicht geladen werden.</p>
      ) : votes.length === 0 ? (
        <p className="pt-1 text-[#888]">Noch keine Stimmen.</p>
      ) : (
        <ul className="pt-1">
          {votes.map((v) => (
            <li key={v.userId}>
              {v.value > 0 ? "+1" : v.value < 0 ? "−1" : "0"} {v.name}
              {v.banned ? " (gesperrt, zählt nicht)" : ""}
            </li>
          ))}
        </ul>
      )}
    </details>
  );
}

function Card({ s, votes, offset, now }: { s: QueueItem; votes: Vote[] | null; offset: number; now: Date }) {
  const ch = changedFields(s.current, s);
  const proof = safeHttpUrl(s.proofUrl);
  return (
    <li className="rounded bg-[#1f2124] p-3">
      <div className="mb-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
        <span className="font-semibold">#{s.itemId}</span>
        <a href={`https://pr0gramm.com/new/${s.itemId}`} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">
          pr0gramm
        </a>
        <Link href={`/${s.itemId}`} className="text-accent hover:underline">
          Song-Seite
        </Link>
        <Link href={`/dashboard/songs/${s.itemId}`} className="text-accent hover:underline">
          Verlauf
        </Link>
        <span className="text-xs text-[#888]">
          von {s.author} · {relativeTimeDe(s.createdAt, now)}
        </span>
      </div>
      <table className="mb-2 text-sm">
        <thead>
          <tr className="text-xs text-[#888]">
            <th />
            <th scope="col" className="pr-4 text-left font-normal">
              Aktuell
            </th>
            <th scope="col" className="text-left font-normal">
              Vorschlag
            </th>
          </tr>
        </thead>
        <tbody>
          <Field label="Titel" cur={s.current.title} sug={s.title} changed={ch.title} />
          <Field label="Interpret" cur={s.current.artist} sug={s.artist} changed={ch.artist} />
          <Field label="Album" cur={s.current.album} sug={s.album} changed={ch.album} />
        </tbody>
      </table>
      <div className="text-xs text-[#aaa]">
        +{s.up} / −{s.down} (netto {s.net})
        {proof && (
          <>
            {" · "}
            <a href={proof} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">
              Beleg
            </a>
          </>
        )}
      </div>
      <Votes votes={votes} />
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <form action={approveAction}>
          <input type="hidden" name="suggestionId" value={s.id} />
          <input type="hidden" name="offset" value={offset} />
          <button type="submit" className="cursor-pointer rounded bg-accent px-3 py-1.5 text-xs font-medium text-white">
            Übernehmen
          </button>
        </form>
        <form action={rejectAction} className="flex flex-wrap items-center gap-2">
          <input type="hidden" name="suggestionId" value={s.id} />
          <input type="hidden" name="offset" value={offset} />
          <input name="reason" maxLength={300} placeholder="Grund (optional)" aria-label="Grund (optional)" className={input} />
          <button type="submit" className="cursor-pointer rounded bg-surface px-3 py-1.5 text-xs font-medium">
            Ablehnen
          </button>
        </form>
      </div>
    </li>
  );
}

export default async function QueuePageView({ searchParams }: Props) {
  const sp = await searchParams;
  const offset = parseOffset(sp.offset);
  const page = await modGet<QueuePage>(`/api/internal/mod/queue?offset=${offset}`);
  const now = new Date();

  const items = page && Array.isArray(page.items) ? page.items : null;
  const votes = items
    ? await Promise.all(items.map((s) => modGet<Vote[]>(`/api/internal/suggestions/${s.id}/votes`)))
    : [];

  return (
    <section>
      <h1 className="mb-4 text-xl font-bold">Warteschlange</h1>
      <Flash e={sp.e} ok={sp.ok} />
      {items === null ? (
        <p className="text-[#888]">Die Warteschlange ist gerade nicht verfügbar.</p>
      ) : items.length === 0 ? (
        <p className="text-[#888]">Keine offenen Vorschläge.</p>
      ) : (
        <ul className="flex flex-col gap-3">
          {items.map((s, i) => (
            <Card key={s.id} s={s} votes={Array.isArray(votes[i]) ? votes[i] : null} offset={offset} now={now} />
          ))}
        </ul>
      )}
      {page && typeof page.nextOffset === "number" && (
        <p className="mt-4">
          <Link href={`/dashboard?offset=${page.nextOffset}`} className="text-accent hover:underline">
            Weiter
          </Link>
        </p>
      )}
    </section>
  );
}
