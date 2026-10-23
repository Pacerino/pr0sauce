import type { Metadata } from "next";
import Link from "next/link";
import "./globals.css";

export const metadata: Metadata = {
  title: { default: "pr0sauce", template: "%s | pr0sauce" },
  description: "Songs, die pr0sauce unter pr0gramm-Videos erkannt hat.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="de">
      <body className="min-h-screen antialiased">
        <header className="border-b border-surface">
          <nav className="mx-auto flex max-w-5xl items-center gap-6 px-4 py-3">
            <Link href="/" className="text-lg font-bold text-accent">
              pr0sauce
            </Link>
            <Link href="/" className="hover:text-accent">
              Neu
            </Link>
            <Link href="/stats" className="hover:text-accent">
              Statistik
            </Link>
            <a
              href="https://github.com/Pacerino/pr0sauce"
              target="_blank"
              rel="noopener noreferrer"
              className="ml-auto hover:text-accent"
            >
              GitHub
            </a>
          </nav>
        </header>
        <main className="mx-auto max-w-5xl px-4 py-6">{children}</main>
      </body>
    </html>
  );
}
