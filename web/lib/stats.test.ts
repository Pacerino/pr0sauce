import { describe, expect, it } from "vitest";
import { fillWeeks, isoWeekNumber, providerShares } from "./stats";

describe("fillWeeks", () => {
  const now = new Date("2026-10-23T13:15:00Z"); // Friday, week of Mon 2026-10-19

  it("returns exactly n consecutive Monday weeks ending with the current one", () => {
    const w = fillWeeks([], now);
    expect(w).toHaveLength(26);
    expect(w[25]).toEqual({ week: "2026-10-19", count: 0 });
    expect(w[0].week).toBe("2026-04-27");
    for (let i = 1; i < w.length; i++) {
      const d = Date.parse(w[i].week) - Date.parse(w[i - 1].week);
      expect(d).toBe(7 * 86_400_000);
    }
  });

  it("keeps counts for present weeks and zero-fills the rest", () => {
    const w = fillWeeks([{ week: "2026-10-19", count: 7 }, { week: "2026-10-05", count: 3 }], now, 4);
    expect(w).toEqual([
      { week: "2026-09-28", count: 0 },
      { week: "2026-10-05", count: 3 },
      { week: "2026-10-12", count: 0 },
      { week: "2026-10-19", count: 7 },
    ]);
  });

  it("drops input weeks outside the window", () => {
    const w = fillWeeks([{ week: "2025-01-06", count: 99 }, { week: "2026-11-02", count: 5 }], now, 4);
    expect(w.map((x) => x.count)).toEqual([0, 0, 0, 0]);
  });

  it("uses the Berlin week: Sunday 23:30 UTC is already Monday in Berlin", () => {
    const sunLate = new Date("2026-10-25T23:30:00Z"); // Mon 2026-10-26 00:30 CET
    expect(fillWeeks([], sunLate, 1)[0].week).toBe("2026-10-26");
    const sunEarly = new Date("2026-10-25T20:00:00Z"); // Sun 21:00 CET
    expect(fillWeeks([], sunEarly, 1)[0].week).toBe("2026-10-19");
  });

  it("handles the DST change week (2026-10-25) without drifting", () => {
    const w = fillWeeks([{ week: "2026-10-19", count: 4 }], new Date("2026-11-03T10:00:00Z"), 3);
    expect(w).toEqual([
      { week: "2026-10-19", count: 4 },
      { week: "2026-10-26", count: 0 },
      { week: "2026-11-02", count: 0 },
    ]);
    const spring = fillWeeks([], new Date("2026-03-30T10:00:00Z"), 3); // DST starts 2026-03-29
    expect(spring.map((x) => x.week)).toEqual(["2026-03-16", "2026-03-23", "2026-03-30"]);
  });
});

describe("isoWeekNumber", () => {
  it("computes ISO weeks", () => {
    expect(isoWeekNumber("2026-10-19")).toBe(43);
    expect(isoWeekNumber("2026-01-01")).toBe(1);
    expect(isoWeekNumber("2024-12-30")).toBe(1);
    expect(isoWeekNumber("2027-01-03")).toBe(53);
  });
});

describe("providerShares", () => {
  it("rounds count/sum to whole percent, sorted desc, empty provider kept", () => {
    const s = providerShares([
      { provider: "acrcloud", count: 7 },
      { provider: "shazam", count: 93 },
      { provider: "", count: 100 },
    ]);
    expect(s.map((x) => [x.provider, x.percent])).toEqual([
      ["", 50],
      ["shazam", 47],
      ["acrcloud", 4],
    ]);
  });
  it("empty input", () => expect(providerShares([])).toEqual([]));
});
