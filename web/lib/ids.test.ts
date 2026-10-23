import { describe, expect, it } from "vitest";
import { parseItemId } from "./ids";

describe("parseItemId", () => {
  it("parses canonical ids", () => {
    expect(parseItemId("6796145")).toBe(6796145);
    expect(parseItemId("1")).toBe(1);
  });
  it.each(["0123", "-5", "abc", "favicon.ico", "", "0", "1.5", " 1", "99999999999999999"])(
    "rejects %j",
    (s) => expect(parseItemId(s)).toBeNull(),
  );
  it("rejects 16-digit values beyond the safe integer range", () => {
    expect(parseItemId("9999999999999999")).toBeNull();
  });
});
