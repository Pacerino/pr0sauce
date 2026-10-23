import Link from "next/link";
import { relativeTimeDe } from "@/lib/format";
import type { Song } from "@/lib/types";
import { Cover } from "./Cover";

export function SongRow({ song, now }: { song: Song; now: Date }) {
  return (
    <Link
      href={`/${song.itemId}`}
      className="flex items-center gap-2.5 rounded bg-[#1f2124] p-[7px] hover:bg-surface"
    >
      <Cover song={song} size={34} />
      <div className="min-w-0 flex-1">
        <div className="truncate">{song.title ?? "Nicht erkannt"}</div>
        <div className="truncate text-xs text-[#888]">{song.artist ?? ""}</div>
      </div>
      <div className="flex-none text-right text-[11px] text-[#888]">
        <div>#{song.itemId}</div>
        <div>{relativeTimeDe(song.recognizedAt, now)}</div>
      </div>
    </Link>
  );
}
