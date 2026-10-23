export function SearchBox({ defaultValue }: { defaultValue?: string }) {
  return (
    <form action="/" method="get" role="search" className="my-4">
      <input
        type="search"
        name="q"
        defaultValue={defaultValue}
        maxLength={200}
        placeholder="Titel, Artist oder Post-ID suchen …"
        aria-label="Suche"
        className="w-full rounded bg-surface px-3 py-2.5 text-text placeholder:text-[#888] outline-none focus:ring-2 focus:ring-accent"
      />
    </form>
  );
}
