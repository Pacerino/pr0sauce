import type { Metadata } from "next";
import { notFound } from "next/navigation";
import Link from "next/link";
import { cache } from "react";
import { Cover } from "@/components/Cover";
import { Suggestions } from "@/components/Suggestions";
import { api } from "@/lib/api";
import { canModerate, parseRole } from "@/lib/dashboard";
import { getCurrentUser } from "@/lib/session";
import { formatDateDe, providerLabel } from "@/lib/format";
import { parseItemId } from "@/lib/ids";
import { buttonClass, streamingButtons } from "@/lib/links";

export const dynamic = "force-dynamic";

const getSong = cache((id: number) => api.song(id));

type Props = { params: Promise<{ id: string }> };

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const id = parseItemId((await params).id);
  if (id === null) notFound();
  const song = await getSong(id);
  if (!song) notFound();
  if (!song.found) return { title: { absolute: `Nicht erkannt #${song.itemId} | pr0sauce` } };
  return { title: { absolute: `${song.title ?? "Unbekannt"} – ${song.artist ?? "Unbekannt"} | pr0sauce` } };
}

export default async function SongPage({ params }: Props) {
  const id = parseItemId((await params).id);
  if (id === null) notFound();
  const song = await getSong(id);
  if (!song) notFound();
  const user = await getCurrentUser();
  const isMod = user !== null && !user.banned && canModerate(parseRole(user.role));

  return (
    <article>
      <div className="flex flex-wrap gap-3.5">
        <Cover song={song} size={110} alt={song.found ? `Cover: ${song.title ?? ""}` : ""} />
        <div className="min-w-0 flex-1 basis-60">
          {song.found ? (
            <>
              <h1 className="text-lg font-semibold break-words">{song.title}</h1>
              <div className="break-words text-[#aaa]">
                {song.artist}
                {song.album ? ` · ${song.album}` : ""}
              </div>
            </>
          ) : (
            <>
              <h1 className="text-lg font-semibold break-words">Nicht erkannt</h1>
              <div className="break-words text-[#aaa]">Der Bot konnte hier keinen Song finden.</div>
            </>
          )}
          <div className="my-2 flex flex-wrap gap-1.5">
            {song.found && !song.corrected && (
              <span className="rounded-full bg-surface px-2 py-0.5 text-[11px] text-[#aaa]">
                erkannt via {providerLabel(song.provider)}
              </span>
            )}
            {song.corrected && (
              <span className="rounded-full bg-surface px-2 py-0.5 text-[11px] text-[#aaa]">
                {song.corrected === "community" ? "korrigiert durch die Community" : "korrigiert durch Moderation"}
              </span>
            )}
            <span className="rounded-full bg-surface px-2 py-0.5 text-[11px] text-[#aaa]">
              {formatDateDe(song.recognizedAt)}
            </span>
          </div>
          <div className="flex flex-wrap gap-2">
            {streamingButtons(song).map((b) => (
              <a
                key={b.kind}
                href={b.href}
                target="_blank"
                rel="noopener noreferrer"
                className={`rounded px-2.5 py-1.5 text-xs ${buttonClass[b.kind]}`}
              >
                {b.label}
              </a>
            ))}
          </div>
        </div>
      </div>

      <div className="mt-4 flex flex-col gap-1.5">
        <a
          href={`https://pr0gramm.com/new/${song.itemId}`}
          target="_blank"
          rel="noopener noreferrer"
          className="rounded bg-[#1f2124] p-2.5 hover:bg-surface"
        >
          <div>Original-Post auf pr0gramm</div>
          <div className="text-xs text-[#888]">
            pr0gramm.com/new/{song.itemId}
            {song.comment && ` · Bot-Kommentar +${song.comment.up} / −${song.comment.down}`}
          </div>
        </a>
        <Suggestions itemId={song.itemId} found={song.found} />
        {isMod && (
          <Link href={`/dashboard/songs/${song.itemId}`} className="text-xs text-accent hover:underline">
            Verlauf (Moderation)
          </Link>
        )}
      </div>
    </article>
  );
}
