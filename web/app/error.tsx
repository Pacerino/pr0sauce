"use client";

import Link from "next/link";

export default function Error({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <section role="alert" className="py-16 text-center">
      <h1 className="mb-2 text-2xl font-bold">Etwas ist schiefgelaufen</h1>
      <p className="mb-4 text-[#888]">Bitte versuche es gleich noch einmal.</p>
      <div className="flex items-center justify-center gap-4">
        <button
          type="button"
          onClick={() => reset()}
          className="rounded bg-accent px-4 py-2 font-semibold text-white hover:opacity-90"
        >
          Erneut versuchen
        </button>
        <Link href="/" className="text-accent hover:underline">
          Zur Startseite
        </Link>
      </div>
    </section>
  );
}
