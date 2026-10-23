import { describe, expect, it } from "vitest";
import { canSeeAdmin, changedFields, formatHealth, parseRole, validatePr0Name, validateReason, mapModError } from "./dashboard";

describe("parseRole", () => {
  it("accepts the three roles", () => {
    expect(parseRole("user")).toBe("user");
    expect(parseRole("moderator")).toBe("moderator");
    expect(parseRole("admin")).toBe("admin");
  });
  it("rejects anything else", () => {
    for (const x of ["Admin", "root", "", " admin", null, undefined, 1, {}, "admin\n"]) expect(parseRole(x)).toBeNull();
  });
});

describe("canSeeAdmin", () => {
  it("only admin", () => {
    expect(canSeeAdmin("admin")).toBe(true);
    expect(canSeeAdmin("moderator")).toBe(false);
    expect(canSeeAdmin("user")).toBe(false);
    expect(canSeeAdmin(null)).toBe(false);
  });
});

describe("formatHealth", () => {
  const now = new Date("2026-10-24T12:00:00Z");
  const ok = { db: "ok", session: "ok", last_poll: "2026-10-24T11:59:30Z" };
  it("ok", () => expect(formatHealth(ok, now)).toBe("Bot läuft"));
  it("session invalid", () =>
    expect(formatHealth({ ...ok, session: "invalid" }, now)).toBe("Session ungültig – bot-login nötig"));
  it("session unknown", () => expect(formatHealth({ ...ok, session: "unknown" }, now)).toBe("Session unbekannt"));
  it("unreachable", () => expect(formatHealth({ error: "unreachable" }, now)).toBe("Bot nicht erreichbar"));
  it("invalid response", () => {
    expect(formatHealth({ error: "invalid response" }, now)).toBe("Ungültige Antwort vom Bot");
    expect(formatHealth(null, now)).toBe("Ungültige Antwort vom Bot");
    expect(formatHealth("x", now)).toBe("Ungültige Antwort vom Bot");
    expect(formatHealth({}, now)).toBe("Ungültige Antwort vom Bot");
  });
  it("db error", () => expect(formatHealth({ ...ok, db: "error" }, now)).toBe("Bot: Datenbankfehler"));
  it("stale poll", () =>
    expect(formatHealth({ ...ok, last_poll: "2026-10-24T11:50:00Z" }, now)).toBe("Bot hängt – letzter Poll zu alt"));
  it("never polled", () => expect(formatHealth({ ...ok, last_poll: null }, now)).toBe("Bot hängt – letzter Poll zu alt"));
});

describe("validatePr0Name", () => {
  it("trims and accepts", () => expect(validatePr0Name("  Pacerino ")).toEqual({ ok: true, name: "Pacerino" }));
  it("accepts - and _", () => expect(validatePr0Name("a_b-c9").ok).toBe(true));
  it("rejects empty, long, odd chars", () => {
    for (const x of ["", "   ", "a".repeat(33), "a b", "ä", "a/b", "a\nb", "../x"]) expect(validatePr0Name(x).ok).toBe(false);
  });
  it("32 is fine", () => expect(validatePr0Name("a".repeat(32)).ok).toBe(true));
});

describe("validateReason", () => {
  it("trims, empty is ok", () => expect(validateReason("  hi ")).toEqual({ ok: true, reason: "hi" }));
  it("rejects over 300", () => expect(validateReason("x".repeat(301)).ok).toBe(false));
  it("300 ok", () => expect(validateReason("x".repeat(300)).ok).toBe(true));
});

describe("changedFields", () => {
  it("flags differing fields", () => {
    expect(changedFields({ title: "A", artist: "B", album: null }, { title: "A2", artist: "B", album: "X" })).toEqual({
      title: true,
      artist: false,
      album: true,
    });
  });
  it("null and empty are equal, trimmed", () => {
    expect(changedFields({ title: " A", artist: null, album: "" }, { title: "A", artist: "", album: null })).toEqual({
      title: false,
      artist: false,
      album: false,
    });
  });
});

describe("mapModError", () => {
  it("passes German API text through", () => {
    expect(mapModError(403, "Eigener Vorschlag")).toBe("Eigener Vorschlag");
    expect(mapModError(409, "Admin kann nicht gesperrt werden")).toBe("Admin kann nicht gesperrt werden");
  });
  it("hides English API text", () => {
    expect(mapModError(400, "invalid offset")).toBe("Ungültige Eingabe");
    expect(mapModError(403, "forbidden")).toBe("Nicht erlaubt");
    expect(mapModError(500, "boom")).toBe("Das hat leider nicht geklappt");
  });
});
