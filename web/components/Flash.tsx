/** Result banner for dashboard redirects (?e= error, ?ok= success). Rendered as plain text. */
export function Flash({ e, ok }: { e?: string | string[]; ok?: string | string[] }) {
  const err = typeof e === "string" ? e.slice(0, 200) : "";
  const done = typeof ok === "string" ? ok.slice(0, 100) : "";
  if (err) {
    return (
      <p role="alert" className="mb-4 rounded bg-[#3a1d1d] p-2.5 text-sm text-red-300">
        {err}
      </p>
    );
  }
  if (done) {
    return (
      <p role="status" className="mb-4 rounded bg-[#1d3a25] p-2.5 text-sm text-green-300">
        {done}
      </p>
    );
  }
  return null;
}
