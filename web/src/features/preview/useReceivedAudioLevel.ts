import type * as Watch from "@moq/watch";
import { useEffect, useState } from "react";

export interface ReceivedAudioLevel {
  hasTrack: boolean;
  receiving: boolean;
  level: number;
}

const pollInterval = 125;
const receivingTimeout = 2_000;

export function useReceivedAudioLevel(player?: Watch.Player): ReceivedAudioLevel {
  const [measurement, setMeasurement] = useState<ReceivedAudioLevel>({
    hasTrack: false,
    receiving: false,
    level: 0,
  });

  useEffect(() => {
    if (!player) {
      setMeasurement({ hasTrack: false, receiving: false, level: 0 });
      return;
    }

    let root: AudioNode | undefined;
    let analyser: AnalyserNode | undefined;
    let samples: Float32Array<ArrayBuffer> | undefined;
    let lastBytesReceived = 0;
    let lastReceivedAt = 0;

    const detachAnalyser = () => {
      if (root && analyser) {
        try {
          root.disconnect(analyser);
        } catch {
          // The decoder may already have disconnected its graph.
        }
      }
      root = undefined;
      analyser = undefined;
      samples = undefined;
    };
    const attachAnalyser = (nextRoot: AudioNode | undefined) => {
      detachAnalyser();
      if (!nextRoot) return;
      root = nextRoot;
      analyser = nextRoot.context.createAnalyser();
      analyser.fftSize = 256;
      analyser.smoothingTimeConstant = 0.45;
      samples = new Float32Array(analyser.fftSize);
      nextRoot.connect(analyser);
    };
    const resumeAudioContext = () => {
      const context = root?.context;
      if (typeof AudioContext !== "undefined" && context instanceof AudioContext && context.state === "suspended") {
        void context.resume().catch(() => undefined);
      }
    };
    const sample = () => {
      const now = performance.now();
      const stats = player.audio.out.stats.peek();
      const bytesReceived = stats?.bytesReceived ?? 0;
      if (bytesReceived > lastBytesReceived) lastReceivedAt = now;
      lastBytesReceived = bytesReceived;

      const hasTrack = player.audio.source.out.track.peek() !== undefined;
      const receiving = hasTrack && lastReceivedAt > 0 && now - lastReceivedAt < receivingTimeout;
      let level = 0;
      if (receiving && analyser && samples && analyser.context.state === "running") {
        analyser.getFloatTimeDomainData(samples);
        let energy = 0;
        for (const value of samples) energy += value * value;
        level = Math.min(1, Math.sqrt(energy / samples.length));
      }
      setMeasurement({ hasTrack, receiving, level });
    };

    attachAnalyser(player.audio.out.root.peek());
    const unsubscribeRoot = player.audio.out.root.subscribe(attachAnalyser);
    const timer = window.setInterval(sample, pollInterval);
    document.addEventListener("pointerdown", resumeAudioContext);
    resumeAudioContext();
    sample();

    return () => {
      unsubscribeRoot();
      window.clearInterval(timer);
      document.removeEventListener("pointerdown", resumeAudioContext);
      detachAnalyser();
    };
  }, [player]);

  return measurement;
}
