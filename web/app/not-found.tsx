import Link from "next/link";

export default function NotFound() {
  return (
    <section className="py-16 text-center">
      <h1 className="mb-2 text-2xl font-bold">Nicht gefunden</h1>
      <p className="mb-4 text-[#888]">Diese Seite oder dieser Song existiert nicht.</p>
      <Link href="/" className="text-accent hover:underline">
        Zurück zur Startseite
      </Link>
    </section>
  );
}
