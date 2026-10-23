import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { Flash } from "@/components/Flash";
import { canSeeAdmin, formatHealth, parseRole } from "@/lib/dashboard";
import { modGet, parseOffset, type AdminUser, type BotStatus, type FailedRow } from "@/lib/dashboard-data";
import { formatDateDe, relativeTimeDe } from "@/lib/format";
import { getCurrentUser } from "@/lib/session";
import { addModeratorAction, banAction, removeModeratorAction, unbanAction } from "../actions";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Admin", robots: { index: false } };

type Props = { searchParams: Promise<{ [k: string]: string | string[] | undefined }> };

const input = "rounded bg-[#15171a] px-2 py-1 text-sm outline-none focus:ring-1 focus:ring-accent";
const btn = "cursor-pointer rounded bg-surface px-3 py-1.5 text-xs font-medium";
const th = "px-2 py-1 text-left text-xs font-normal text-[#888]";
const td = "px-2 py-1 align-top";

function Tile({ label, value, tone }: { label: string; value: string; tone?: "ok" | "bad" }) {
  const color = tone === "ok" ? "text-green-400" : tone === "bad" ? "text-red-400" : "";
  return (
    <div className="rounded bg-[#1f2124] p-3">
      <div className="text-xs text-[#888]">{label}</div>
      <div className={`text-lg font-semibold ${color}`}>{value}</div>
    </div>
  );
}

function sessionTile(health: unknown): { value: string; tone?: "ok" | "bad" } {
  const s = typeof health === "object" && health !== null ? (health as Record<string, unknown>).session : undefined;
  if (s === "ok") return { value: "ok", tone: "ok" };
  if (s === "invalid") return { value: "ungültig", tone: "bad" };
  return { value: "unbekannt" };
}

function Bot({ bot, now }: { bot: BotStatus | null; now: Date }) {
  if (!bot) return <p className="text-[#888]">Bot-Status ist gerade nicht verfügbar.</p>;
  const text = formatHealth(bot.health, now);
  const sess = sessionTile(bot.health);
  const lastPoll =
    typeof bot.health === "object" && bot.health !== null && typeof (bot.health as { last_poll?: unknown }).last_poll === "string"
      ? relativeTimeDe((bot.health as { last_poll: string }).last_poll, now)
      : "";
  const req = bot.requests ?? {};
  const retry = bot.retry;
  return (
    <>
      <p className={`mb-2 text-sm ${text === "Bot läuft" ? "text-green-400" : "text-red-400"}`}>{text}</p>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <Tile label="Session" value={sess.value} tone={sess.tone} />
        <Tile label="Letzter Poll" value={lastPoll || "–"} />
        <Tile label="Offen" value={String(req.pending ?? 0)} />
        <Tile label="Antwortet gerade" value={String(req.replying ?? 0)} />
        <Tile label="Erledigt" value={String(req.done ?? 0)} />
        <Tile label="Fehlgeschlagen" value={String(req.failed ?? 0)} tone={(req.failed ?? 0) > 0 ? "bad" : undefined} />
        <Tile label="Übersprungen" value={String(req.skipped ?? 0)} />
        <Tile label="Mark-read-Rückstand" value={String(bot.markReadBacklog ?? 0)} />
        <Tile label="Retries ausstehend" value={retry ? `${retry.pending} (max. ${retry.maxAttempts})` : "–"} />
        <Tile label="Nächster Retry" value={retry?.nextAttemptAt ? relativeTimeDe(retry.nextAttemptAt, now) : "–"} />
        <Tile label="Letzte Antwort" value={bot.lastDoneAt ? relativeTimeDe(bot.lastDoneAt, now) : "–"} />
      </div>
    </>
  );
}

const trunc = (s: string) => (s.length > 160 ? `${s.slice(0, 160)}…` : s);

export default async function AdminPage({ searchParams }: Props) {
  const user = await getCurrentUser();
  if (!user || user.banned || !canSeeAdmin(parseRole(user.role))) notFound();
  const sp = await searchParams;
  const offset = parseOffset(sp.offset);
  const [mods, bans, bot, failed] = await Promise.all([
    modGet<AdminUser[]>("/api/internal/admin/moderators"),
    modGet<AdminUser[]>("/api/internal/admin/bans"),
    modGet<BotStatus>("/api/internal/admin/bot"),
    modGet<FailedRow[]>(`/api/internal/admin/failed?offset=${offset}`),
  ]);
  const now = new Date();

  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-xl font-bold">Admin</h1>
      <Flash e={sp.e} ok={sp.ok} />

      <section>
        <h2 className="mb-2 text-lg font-semibold">Moderatoren</h2>
        {mods === null ? (
          <p className="text-[#888]">Nicht verfügbar.</p>
        ) : mods.length === 0 ? (
          <p className="text-[#888]">Keine Moderatoren.</p>
        ) : (
          <ul className="mb-3 flex flex-col gap-1">
            {mods.map((m) => (
              <li key={m.id} className="flex items-center gap-3 rounded bg-[#1f2124] px-3 py-1.5">
                <span className="flex-1">{m.name}</span>
                <form action={removeModeratorAction}>
                  <input type="hidden" name="userId" value={m.id} />
                  <button type="submit" className={btn}>
                    Entfernen
                  </button>
                </form>
              </li>
            ))}
          </ul>
        )}
        <form action={addModeratorAction} className="flex flex-wrap items-center gap-2">
          <input name="name" required maxLength={32} placeholder="Hinzufügen (pr0gramm-Name)" aria-label="Hinzufügen (pr0gramm-Name)" className={input} />
          <button type="submit" className={btn}>
            Hinzufügen
          </button>
        </form>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Sperren</h2>
        {bans === null ? (
          <p className="text-[#888]">Nicht verfügbar.</p>
        ) : bans.length === 0 ? (
          <p className="text-[#888]">Keine gesperrten Nutzer.</p>
        ) : (
          <table className="mb-3 w-full text-sm">
            <thead>
              <tr>
                <th className={th}>Name</th>
                <th className={th}>Grund</th>
                <th className={th}>Seit</th>
                <th className={th} />
              </tr>
            </thead>
            <tbody>
              {bans.map((b) => (
                <tr key={b.id} className="border-t border-surface">
                  <td className={td}>{b.name}</td>
                  <td className={`${td} break-words`}>{b.reason ?? "–"}</td>
                  <td className={td}>{b.bannedAt ? formatDateDe(b.bannedAt) : "–"}</td>
                  <td className={td}>
                    <form action={unbanAction}>
                      <input type="hidden" name="userId" value={b.id} />
                      <button type="submit" className={btn}>
                        Entsperren
                      </button>
                    </form>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <form action={banAction} className="flex flex-wrap items-center gap-2">
          <input name="name" required maxLength={32} placeholder="Sperren (Name)" aria-label="Sperren (Name)" className={input} />
          <input name="reason" maxLength={300} placeholder="Grund" aria-label="Grund" className={input} />
          <button type="submit" className={btn}>
            Sperren
          </button>
        </form>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Bot</h2>
        <Bot bot={bot} now={now} />
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Fehlgeschlagen</h2>
        {failed === null ? (
          <p className="text-[#888]">Nicht verfügbar.</p>
        ) : failed.length === 0 ? (
          <p className="text-[#888]">Keine fehlgeschlagenen Anfragen.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr>
                  <th className={th}>ID</th>
                  <th className={th}>Post</th>
                  <th className={th}>Anfragender</th>
                  <th className={th}>Versuche</th>
                  <th className={th}>Letzter Fehler</th>
                  <th className={th}>Zeit</th>
                </tr>
              </thead>
              <tbody>
                {failed.map((f) => (
                  <tr key={f.id} className="border-t border-surface">
                    <td className={td}>{f.id}</td>
                    <td className={td}>
                      <a href={`https://pr0gramm.com/new/${f.itemId}`} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">
                        #{f.itemId}
                      </a>
                    </td>
                    <td className={td}>{f.requester}</td>
                    <td className={td}>{f.attempts}</td>
                    <td className={`${td} break-words`} title={f.lastError ?? undefined}>
                      {f.lastError ? trunc(f.lastError) : "–"}
                    </td>
                    <td className={td}>{relativeTimeDe(f.updatedAt, now)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <p className="mt-3 flex gap-4 text-sm">
          {offset > 0 && (
            <Link href={`/dashboard/admin?offset=${Math.max(0, offset - 50)}`} className="text-accent hover:underline">
              Zurück
            </Link>
          )}
          {failed !== null && failed.length === 50 && (
            <Link href={`/dashboard/admin?offset=${offset + 50}`} className="text-accent hover:underline">
              Weiter
            </Link>
          )}
        </p>
      </section>
    </div>
  );
}
