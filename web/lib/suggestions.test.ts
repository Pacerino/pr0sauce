import { describe, expect, it } from "vitest";
import { mapApiError, remainingToApprove, suggestTriggerLabel, validateSuggestion } from "./suggestions";

describe("remainingToApprove", () => {
  it("is 0 once up and net both reach 5", () => {
    expect(remainingToApprove(5, 5)).toBe(0);
    expect(remainingToApprove(9, 7)).toBe(0);
  });
  it("is the larger of the two gaps", () => {
    expect(remainingToApprove(3, 1)).toBe(4);
    expect(remainingToApprove(0, 0)).toBe(5);
    expect(remainingToApprove(6, 2)).toBe(3);
  });
  it("never goes negative", () => {
    expect(remainingToApprove(50, 50)).toBe(0);
  });
});

describe("validateSuggestion", () => {
  const ok = { title: "Song", artist: "Band" };
  it("accepts minimal input", () => {
    expect(validateSuggestion(ok)).toEqual({ ok: true, errors: {} });
  });
  it("trims before measuring", () => {
    expect(validateSuggestion({ title: "   ", artist: "Band" }).errors.title).toBeTruthy();
    expect(validateSuggestion({ title: " a ", artist: " b " }).ok).toBe(true);
  });
  it("limits title, artist and album to 200", () => {
    const long = "x".repeat(201);
    expect(validateSuggestion({ ...ok, title: "x".repeat(200) }).ok).toBe(true);
    expect(validateSuggestion({ ...ok, title: long }).errors.title).toBeTruthy();
    expect(validateSuggestion({ ...ok, artist: long }).errors.artist).toBeTruthy();
    expect(validateSuggestion({ ...ok, album: long }).errors.album).toBeTruthy();
    expect(validateSuggestion({ ...ok, album: "x".repeat(200) }).ok).toBe(true);
  });
  it("requires an http(s) proof URL of at most 500 chars", () => {
    expect(validateSuggestion({ ...ok, proofUrl: "https://example.com/a" }).ok).toBe(true);
    expect(validateSuggestion({ ...ok, proofUrl: "" }).ok).toBe(true);
    expect(validateSuggestion({ ...ok, proofUrl: "javascript:alert(1)" }).errors.proofUrl).toBeTruthy();
    expect(validateSuggestion({ ...ok, proofUrl: "not a url" }).errors.proofUrl).toBeTruthy();
    const longUrl = "https://example.com/" + "a".repeat(481);
    expect(validateSuggestion({ ...ok, proofUrl: longUrl }).errors.proofUrl).toBeTruthy();
  });
});

describe("mapApiError", () => {
  it("maps suggest conflicts and rate limits", () => {
    expect(mapApiError(409, "you already have an open suggestion", "suggest")).toBe("Du hast hier schon einen offenen Vorschlag");
    expect(mapApiError(429, "too many", "suggest")).toBe("Zu viele Vorschläge heute");
  });
  it("maps 409 on vote to a closed notice", () => {
    expect(mapApiError(409, "suggestion is not open", "vote")).toBe("Vorschlag ist nicht mehr offen");
  });
  it("maps 403", () => {
    expect(mapApiError(403, "Gesperrt", "vote")).toBe("Gesperrt");
    expect(mapApiError(403, "cannot vote on your own suggestion", "vote")).toBe("Nicht erlaubt");
  });
  it("passes German 400 text through, hides English", () => {
    expect(mapApiError(400, "Titel zu lang", "suggest")).toBe("Titel zu lang");
    expect(mapApiError(400, "title must be 1-200 characters", "suggest")).toBe("Ungültige Eingabe");
    expect(mapApiError(400, undefined, "suggest")).toBe("Ungültige Eingabe");
  });
  it("has a generic fallback", () => {
    expect(mapApiError(500, "boom", "vote")).toBe("Das hat leider nicht geklappt");
    expect(mapApiError(401, "x", "vote")).toBe("Bitte anmelden");
  });
});

describe("suggestTriggerLabel", () => {
  it("asks for a correction on hits", () => {
    expect(suggestTriggerLabel(true)).toBe("Falsch erkannt? Korrektur vorschlagen");
  });
  it("asks for the song on misses", () => {
    expect(suggestTriggerLabel(false)).toBe("Kennst du den Song? Jetzt vorschlagen");
  });
});
