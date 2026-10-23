"use client";

import { usePathname } from "next/navigation";

/** Login link that brings the user back to the page they started on. */
export function LoginLink() {
  const pathname = usePathname() || "/";
  const href = pathname === "/" ? "/api/auth/login" : `/api/auth/login?next=${encodeURIComponent(pathname)}`;
  return (
    <a href={href} className="hover:text-accent">
      Mit pr0gramm anmelden
    </a>
  );
}
