"use client";

import { useActionState } from "react";
import { suggestAction, type ActionResult } from "@/app/[id]/actions";

const input = "w-full rounded bg-[#15171a] px-2 py-1.5 text-sm outline-none focus:ring-1 focus:ring-accent";

export function SuggestForm({ itemId }: { itemId: number }) {
  const [state, action, pending] = useActionState<ActionResult | null, FormData>(suggestAction, null);
  return (
    <form action={action} className="flex flex-col gap-2 pt-2">
      <input type="hidden" name="itemId" value={itemId} />
      <label className="text-xs text-[#aaa]">
        Titel
        <input name="title" required maxLength={200} className={input} />
      </label>
      <label className="text-xs text-[#aaa]">
        Interpret
        <input name="artist" required maxLength={200} className={input} />
      </label>
      <label className="text-xs text-[#aaa]">
        Album (optional)
        <input name="album" maxLength={200} className={input} />
      </label>
      <label className="text-xs text-[#aaa]">
        Beleg-Link (optional)
        <input name="proofUrl" type="url" maxLength={500} className={input} />
      </label>
      <div className="flex items-center gap-3">
        <button
          type="submit"
          disabled={pending}
          className="cursor-pointer rounded bg-accent px-3 py-1.5 text-xs font-medium text-white disabled:opacity-50"
        >
          Vorschlag senden
        </button>
        {state?.ok && <span role="status" className="text-xs text-green-400">Danke, dein Vorschlag ist eingegangen.</span>}
        {state && !state.ok && state.error && (
          <span role="alert" className="text-xs text-red-400">
            {state.error}
          </span>
        )}
      </div>
    </form>
  );
}
