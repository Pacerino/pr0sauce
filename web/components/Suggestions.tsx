import { safeHttpUrl } from "@/lib/format";
import { internalFetch } from "@/lib/internal";
import { getCurrentUser, getSessionToken } from "@/lib/session";
import { remainingToApprove, suggestTriggerLabel } from "@/lib/suggestions";
import type { Suggestion } from "@/lib/types";
import { SuggestForm } from "./SuggestForm";
import { VoteButtons } from "./VoteButtons";

async function loadSuggestions(itemId: number): Promise<Suggestion[] | null> {
  try {
    const token = await getSessionToken();
    const res = await internalFetch(`/api/internal/songs/${itemId}/suggestions`, { sessionToken: token ?? undefined });
    if (!res.ok) return null;
    const data = (await res.json()) as unknown;
    return Array.isArray(data) ? (data as Suggestion[]) : null;
  } catch {
    return null;
  }
}

function Trigger({ children }: { children: React.ReactNode }) {
  return <div className="rounded bg-[#1f2124] p-2.5">{children}</div>;
}

/** Correction trigger (login link or form) plus the list of open suggestions. */
export async function Suggestions({ itemId, found }: { itemId: number; found: boolean }) {
  const [user, list] = await Promise.all([getCurrentUser(), loadSuggestions(itemId)]);

  return (
    <>
      {!user ? (
        <a href={`/api/auth/login?next=/${itemId}`} className="rounded bg-[#1f2124] p-2.5 hover:bg-surface">
          ⚑ {suggestTriggerLabel(found)}
        </a>
      ) : user.banned ? (
        <Trigger>
          <span className="text-[#aaa]">Gesperrt</span>
        </Trigger>
      ) : (
        <details className="rounded bg-[#1f2124] p-2.5">
          <summary className="cursor-pointer">⚑ {suggestTriggerLabel(found)}</summary>
          <SuggestForm itemId={itemId} />
        </details>
      )}

      {list === null ? (
        <div className="px-1 text-xs text-[#888]">Vorschläge sind gerade nicht verfügbar.</div>
      ) : (
        list.length > 0 && (
          <ul className="flex flex-col gap-1.5">
            {list.map((s) => {
              const proof = safeHttpUrl(s.proofUrl);
              const left = remainingToApprove(s.up, s.net);
              return (
                <li key={s.id} className="flex items-center gap-2.5 rounded bg-[#1f2124] p-2.5">
                  <div className="min-w-0 flex-1">
                    <div className="break-words">
                      {s.title} – {s.artist}
                      {s.album ? ` · ${s.album}` : ""}
                    </div>
                    <div className="text-xs text-[#888]">
                      von {s.author}
                      {s.mine && " · dein Vorschlag"}
                      {proof && (
                        <>
                          {" · "}
                          <a href={proof} rel="nofollow noopener noreferrer" target="_blank" className="underline hover:text-accent">
                            Beleg
                          </a>
                        </>
                      )}
                      {left > 0 && ` · noch ${left} bis zur Übernahme`}
                    </div>
                  </div>
                  <span className="flex-none tabular-nums text-[#aaa]" title="Netto-Stimmen">
                    {s.net}
                  </span>
                  {user && !user.banned && !s.mine && <VoteButtons itemId={itemId} suggestionId={s.id} myVote={s.myVote} />}
                </li>
              );
            })}
          </ul>
        )
      )}
    </>
  );
}
