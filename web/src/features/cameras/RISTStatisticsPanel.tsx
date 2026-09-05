import { Activity, Clock3 } from "lucide-react";
import type { RISTStatistics } from "../../api/types";
import { formatDateTime } from "../../lib/format";
import { formatPacketLossRate, packetLossRate } from "./ristStatistics";

interface RISTStatisticsPanelProps {
  statistics: RISTStatistics[];
}

export function RISTStatisticsPanel({ statistics }: RISTStatisticsPanelProps) {
  if (statistics.length === 0) {
    return (
      <div className="rist-statistics rist-statistics-empty">
        <Activity size={14} />
        <span>RIST統計を待っています</span>
      </div>
    );
  }
  return (
    <div className="rist-statistics-list">
      {statistics.map((item) => {
        const rate = packetLossRate(item);
        return (
          <section className={`rist-statistics${item.stale ? " rist-statistics-stale" : ""}`} key={`${item.gatewayInstance}:${item.flowId}`}>
            <header>
              <span>回復後 packet loss</span>
              <strong>{formatPacketLossRate(rate)}</strong>
              {item.stale && <span className="rist-stale-label">stale</span>}
            </header>
            <dl>
              <div><dt>output</dt><dd>{item.outputPackets.toLocaleString()}</dd></div>
              <div><dt>lost</dt><dd>{item.lostPackets.toLocaleString()}</dd></div>
              <div><dt>recovered</dt><dd>{item.recoveredPackets.toLocaleString()}</dd></div>
              <div><dt>discontinuity</dt><dd>{item.discontinuities.toLocaleString()}</dd></div>
            </dl>
            <footer>
              <span>flow {item.flowId}</span>
              <span><Clock3 size={11} /> {formatDateTime(item.intervalEnd)}</span>
            </footer>
          </section>
        );
      })}
    </div>
  );
}
