import { safeHttpUrl } from "@/lib/format";
import type { Song } from "@/lib/types";

/** Cover image if known, otherwise the accent gradient placeholder. */
export function Cover({ song, size, alt = "", className = "" }: { song: Song; size: number; alt?: string; className?: string }) {
  const style = { width: size, height: size };
  const cover = safeHttpUrl(song.coverUrl);
  if (cover) {
    return (
      // eslint-disable-next-line @next/next/no-img-element -- external hosts, deliberately not next/image
      <img
        src={cover}
        alt={alt}
        width={size}
        height={size}
        loading="lazy"
        referrerPolicy="no-referrer"
        style={style}
        className={`flex-none rounded-[3px] bg-surface object-cover ${className}`}
      />
    );
  }
  return (
    <div
      aria-hidden="true"
      style={{ ...style, background: "linear-gradient(135deg,#ee4d2e,#5b2a86)" }}
      className={`flex-none rounded-[3px] ${className}`}
    />
  );
}
