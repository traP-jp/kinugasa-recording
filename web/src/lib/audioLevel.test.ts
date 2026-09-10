import { describe, expect, it } from "vitest";
import { audioLevelToPercentage, calculateAudioLevel } from "./audioLevel";

describe("calculateAudioLevel", () => {
  it("uses the instantaneous WebRTC audio level when available", () => {
    expect(calculateAudioLevel({ audioLevel: 0.25, bytesReceived: 100 })).toBe(0.25);
  });

  it("derives RMS level from cumulative energy", () => {
    const previous = { bytesReceived: 100, totalAudioEnergy: 2, totalSamplesDuration: 10 };
    const current = { bytesReceived: 200, totalAudioEnergy: 2.01, totalSamplesDuration: 11 };
    expect(calculateAudioLevel(current, previous)).toBeCloseTo(0.1);
  });

  it("returns zero without a measurable interval", () => {
    expect(calculateAudioLevel({ bytesReceived: 100, totalAudioEnergy: 2, totalSamplesDuration: 10 })).toBe(0);
  });
});

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
