import type { RISTStatistics } from "../../api/types";

export function packetLossRate(statistics: RISTStatistics): number | null {
  const total = statistics.outputPackets + statistics.lostPackets;
  return total === 0 ? null : statistics.lostPackets / total;
}

export function formatPacketLossRate(rate: number | null): string {
  if (rate === null) return "計測不能";
  return new Intl.NumberFormat("ja-JP", {
    style: "percent",
    minimumFractionDigits: rate > 0 && rate < 0.001 ? 3 : 0,
    maximumFractionDigits: 3,
  }).format(rate);
}
