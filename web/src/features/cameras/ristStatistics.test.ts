import { describe, expect, it } from "vitest";
import type { RISTStatistics } from "../../api/types";
import { formatPacketLossRate, packetLossRate } from "./ristStatistics";

function statistics(outputPackets: number, lostPackets: number): RISTStatistics {
  return {
    cameraName: "camera-1",
    gatewayInstance: "pod-uid",
    flowId: 42,
    intervalStart: "2026-09-06T12:00:00Z",
    intervalEnd: "2026-09-06T12:00:05Z",
    outputPackets,
    lostPackets,
    recoveredPackets: 0,
    discontinuities: 0,
    stale: false,
  };
}

describe("packetLossRate", () => {
  it("derives the rate from raw quantities", () => {
    expect(packetLossRate(statistics(98, 2))).toBe(0.02);
  });

  it("does not represent an empty interval as zero loss", () => {
    expect(packetLossRate(statistics(0, 0))).toBeNull();
    expect(formatPacketLossRate(null)).toBe("計測不能");
  });
});
