import Link from "next/link";
import { getCurrentUser } from "@/lib/session";
import { LoginLink } from "./LoginLink";

export async function UserMenu() {
  const user = await getCurrentUser();
  if (!user) return <LoginLink />;
  const canModerate = !user.banned && (user.role === "moderator" || user.role === "admin");
  return (
    <div className="flex items-center gap-4">
      <span className="text-[#aaa]">{user.name}</span>
      {canModerate && (
        <Link href="/dashboard" className="hover:text-accent">
          Dashboard
        </Link>
      )}
      <form method="post" action="/api/auth/logout">
        <button type="submit" className="cursor-pointer hover:text-accent">
          Abmelden
        </button>
      </form>
    </div>
  );
}
