import "server-only";

/** A redirect with any number of Set-Cookie lines; never cached. */
export function redirectWithCookies(location: string, status: 302 | 303, setCookies: string[] = []): Response {
  const headers = new Headers({ location, "cache-control": "no-store" });
  for (const c of setCookies) headers.append("set-cookie", c);
  return new Response(null, { status, headers });
}

/** A short German plain-text error page. */
export function textError(status: number, message: string): Response {
  return new Response(message, {
    status,
    headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
  });
}
