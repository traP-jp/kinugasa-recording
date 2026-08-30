import { describe, expect, it } from "vitest";
import { formatDuration, shortHash } from "./format";

describe("formatDuration", () => {
  it("formats an elapsed interval", () => {
    expect(formatDuration("2026-08-31T00:00:00Z", "2026-08-31T00:02:05Z")).toBe("2:05");
  });
});

describe("shortHash", () => {
  it("keeps both ends of a long hash", () => {
    expect(shortHash("abcdefghijklmnopqrstuvwxyz0123456789")).toBe("abcdefghijkl…456789");
  });
});
