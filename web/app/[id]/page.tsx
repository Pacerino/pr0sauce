import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { cache } from "react";
import { Cover } from "@/components/Cover";
import { api } from "@/lib/api";
import { formatDateDe, providerLabel } from "@/lib/format";
import { parseItemId } from "@/lib/ids";

export const dynamic = "force-dynamic";

const getSong = cache((id: number) => api.song(id));

type Props = { params: Promise<{ id: string }> };

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const id = parseItemId((await params).id);
  if (id === null) notFound();
  const song = await getSong(id);
  if (!song) notFound();
  return { title: { absolute: `${song.title} – ${song.artist} | pr0sauce` } };
}

export default async function SongPage({ params }: Props) {
  const id = parseItemId((await params).id);
  if (id === null) notFound();
  const song = await getSong(id);
  if (!song) notFound();

  return (
    <article>
      <div className="flex flex-wrap gap-3.5">
        <Cover song={song} size={110} />
        <div className="min-w-0 flex-1 basis-60">
          <h1 className="text-lg font-semibold break-words">{song.title}</h1>
          <div className="break-words text-[#aaa]">
            {song.artist}
            {song.album ? ` · ${song.album}` : ""}
          </div>
          <div className="my-2 flex flex-wrap gap-1.5">
            <span className="rounded-full bg-surface px-2 py-0.5 text-[11px] text-[#aaa]">
              erkannt via {providerLabel(song.provider)}
            </span>
            <span className="rounded-full bg-surface px-2 py-0.5 text-[11px] text-[#aaa]">
              {formatDateDe(song.recognizedAt)}
            </span>
          </div>
          <div className="flex gap-1.5">
            {song.spotifyUrl && (
              <a
                href={song.spotifyUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="rounded bg-[#1db954] px-2.5 py-1.5 text-xs text-white"
              >
                Spotify
              </a>
            )}
            {song.youtubeUrl && (
              <a
                href={song.youtubeUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="rounded bg-[#e62117] px-2.5 py-1.5 text-xs text-white"
              >
                YouTube
              </a>
            )}
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
        <div aria-disabled="true" className="rounded bg-[#1f2124] p-2.5 opacity-45 select-none">
          <div>⚑ Falsch erkannt? Korrektur vorschlagen</div>
          <div className="text-xs text-[#888]">bald</div>
        </div>
      </div>
    </article>
  );
}
