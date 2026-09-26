import { describe, expect, it } from "vitest";
import { audioLevelToPercentage } from "./audioLevel";

describe("audioLevelToPercentage", () => {
  it("maps -60 dBFS to zero and -30 dBFS to half scale", () => {
    expect(audioLevelToPercentage(0.001)).toBe(0);
    expect(audioLevelToPercentage(10 ** (-30 / 20))).toBe(50);
  });

  it("clamps the output range", () => {
    expect(audioLevelToPercentage(0)).toBe(0);
    expect(audioLevelToPercentage(2)).toBe(100);
  });
});
