import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { canModerate, canSeeAdmin, parseRole } from "@/lib/dashboard";
import { getCurrentUser } from "@/lib/session";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Moderation", robots: { index: false } };

/** Fail closed: logged out, API down, banned or any unknown role all read as 404. */
export default async function DashboardLayout({ children }: { children: React.ReactNode }) {
  const user = await getCurrentUser();
  if (!user || user.banned) notFound();
  const role = parseRole(user.role);
  if (!canModerate(role)) notFound();

  return (
    <div>
      <nav className="mb-5 flex gap-4 border-b border-surface pb-2 text-sm">
        <Link href="/dashboard" className="hover:text-accent">
          Warteschlange
        </Link>
        {canSeeAdmin(role) && (
          <Link href="/dashboard/admin" className="hover:text-accent">
            Admin
          </Link>
        )}
      </nav>
      {children}
    </div>
  );
}
