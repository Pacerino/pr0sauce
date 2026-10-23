"use client";

import { useActionState } from "react";
import { voteAction, type ActionResult } from "@/app/[id]/actions";

/** ▲/▼ vote buttons; clicking the active vote again retracts it. */
export function VoteButtons({ itemId, suggestionId, myVote }: { itemId: number; suggestionId: number; myVote: number | null }) {
  const [state, action, pending] = useActionState<ActionResult | null, FormData>(voteAction, null);
  const btn = (value: 1 | -1, label: string, glyph: string) => {
    const active = myVote === value;
    return (
      <button
        type="submit"
        name="value"
        value={active ? 0 : value}
        disabled={pending}
        aria-pressed={active}
        aria-label={label}
        className={`cursor-pointer rounded px-1.5 py-0.5 hover:bg-surface disabled:opacity-50 ${active ? "text-accent" : "text-[#aaa]"}`}
      >
        {glyph}
      </button>
    );
  };
  return (
    <form action={action} className="flex flex-none items-center">
      <input type="hidden" name="itemId" value={itemId} />
      <input type="hidden" name="suggestionId" value={suggestionId} />
      {btn(1, "Dafür", "▲")}
      {btn(-1, "Dagegen", "▼")}
      {state && !state.ok && state.error && (
        <span role="alert" className="ml-2 text-xs text-red-400">
          {state.error}
        </span>
      )}
    </form>
  );
}
