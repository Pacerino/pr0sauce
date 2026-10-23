import { describe, expect, it } from "vitest";
import { formatNumberDe, formatPercentDe, providerLabel, relativeTimeDe } from "./format";

const now = new Date("2026-10-23T13:15:00Z");

describe("relativeTimeDe", () => {
  it("minutes", () => expect(relativeTimeDe("2026-10-23T13:12:00Z", now)).toBe("vor 3 Min"));
  it("hours", () => expect(relativeTimeDe("2026-10-23T12:05:00Z", now)).toBe("vor 1 Std"));
  it("just now", () => expect(relativeTimeDe("2026-10-23T13:14:40Z", now)).toBe("gerade eben"));
  it("yesterday evening Berlin", () =>
    expect(relativeTimeDe("2026-10-22T18:00:00Z", now)).toBe("gestern"));
  it("older date", () => expect(relativeTimeDe("2026-05-05T10:00:00Z", now)).toBe("05.05.2026"));

  it("Berlin midnight boundary in summer (UTC+2)", () => {
    // 2026-07-10 00:30 Berlin == 2026-07-09T22:30Z; now 10:00 Berlin same day
    const n = new Date("2026-07-10T08:00:00Z");
    // 2026-07-09 23:30 Berlin (21:30Z): previous Berlin day -> gestern, though < 24h ago
    expect(relativeTimeDe("2026-07-09T21:30:00Z", n)).toBe("gestern");
    // 2026-07-10 00:30 Berlin (22:30Z on the 9th UTC): still "today" in Berlin
    expect(relativeTimeDe("2026-07-09T22:30:00Z", n)).toBe("vor 9 Std");
  });

  it("Berlin midnight boundary in winter (UTC+1)", () => {
    const n = new Date("2026-12-10T12:00:00Z");
    expect(relativeTimeDe("2026-12-09T22:30:00Z", n)).toBe("gestern"); // 23:30 Berlin on the 9th
    expect(relativeTimeDe("2026-12-09T23:30:00Z", n)).toBe("vor 12 Std"); // 00:30 Berlin on the 10th
  });

  it("two Berlin days ago is a date", () =>
    expect(relativeTimeDe("2026-10-21T20:00:00Z", now)).toBe("21.10.2026"));
  it("future / invalid input does not throw", () => {
    expect(relativeTimeDe("2026-10-23T13:16:00Z", now)).toBe("gerade eben");
    expect(relativeTimeDe("garbage", now)).toBe("");
  });
});

describe("providerLabel", () => {
  it.each([
    ["shazam", "Shazam"],
    ["acrcloud", "ACRCloud"],
    ["auddio", "audd.io"],
    ["", "unbekannt"],
    ["foo", "unbekannt"],
  ])("%s -> %s", (p, label) => expect(providerLabel(p)).toBe(label));
});

describe("number formatting", () => {
  it("German thousands separator", () => expect(formatNumberDe(57503)).toBe("57.503"));
  it("whole percent", () => expect(formatPercentDe(0.7149)).toBe("71 %"));
});
