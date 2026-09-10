import type { TrackReference } from "@livekit/components-react";
import { useEffect, useState } from "react";
import { calculateAudioLevel, type AudioEnergySample } from "../../lib/audioLevel";

interface ReceivedAudioLevel {
  hasTrack: boolean;
  receiving: boolean;
  level: number;
}

const pollInterval = 125;
const receivingTimeout = 2_000;

export function useReceivedAudioLevel(trackReference?: TrackReference): ReceivedAudioLevel {
  const track = trackReference?.publication.track;
  const [measurement, setMeasurement] = useState<ReceivedAudioLevel>({
    hasTrack: track !== undefined,
    receiving: false,
    level: 0,
  });

  useEffect(() => {
    let cancelled = false;
    let timer = 0;
    let previous: AudioEnergySample | undefined;
    let lastReceivedAt = 0;

    if (!track) {
      setMeasurement({ hasTrack: false, receiving: false, level: 0 });
      return;
    }
    const audioTrack = track;

    async function update() {
      const now = performance.now();
      try {
        const report = await audioTrack.getRTCStatsReport();
        const current = report ? findInboundAudioSample(report) : undefined;
        if (current) {
          if (current.bytesReceived > (previous?.bytesReceived ?? 0)) lastReceivedAt = now;
          const receiving = lastReceivedAt > 0 && now - lastReceivedAt < receivingTimeout;
          const level = receiving ? calculateAudioLevel(current, previous) : 0;
          if (!cancelled) setMeasurement({ hasTrack: true, receiving, level });
          previous = current;
        } else if (!cancelled) {
          setMeasurement({ hasTrack: true, receiving: false, level: 0 });
        }
      } catch {
        if (!cancelled) setMeasurement({ hasTrack: true, receiving: false, level: 0 });
      }
      if (!cancelled) timer = window.setTimeout(() => void update(), pollInterval);
    }

    void update();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [track]);

  return measurement;
}

function findInboundAudioSample(report: RTCStatsReport): AudioEnergySample | undefined {
  let sample: AudioEnergySample | undefined;
  report.forEach((stats) => {
    if (sample || stats.type !== "inbound-rtp") return;
    const inbound = stats as RTCInboundRtpStreamStats;
    const mediaType = inbound.kind ?? (inbound as RTCInboundRtpStreamStats & { mediaType?: string }).mediaType;
    if (mediaType !== undefined && mediaType !== "audio") return;
    sample = {
      audioLevel: inbound.audioLevel,
      bytesReceived: inbound.bytesReceived ?? 0,
      totalAudioEnergy: inbound.totalAudioEnergy,
      totalSamplesDuration: inbound.totalSamplesDuration,
    };
  });
  return sample;
}
