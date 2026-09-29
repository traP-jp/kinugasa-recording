import { useEffect, useRef, useState } from "react";

export interface ReceivedAudioLevel {
  hasTrack: boolean;
  receiving: boolean;
  level: number;
}

const pollInterval = 125;

interface AudioGraph {
  video: HTMLVideoElement;
  context: AudioContext;
  source: MediaElementAudioSourceNode;
  analyser: AnalyserNode;
  silent: GainNode;
}

function closeGraph(graph: AudioGraph): void {
  graph.source.disconnect();
  graph.analyser.disconnect();
  graph.silent.disconnect();
  void graph.context.close();
}

export function useReceivedAudioLevel(video: HTMLVideoElement | null, hasAudio: boolean): ReceivedAudioLevel {
  const [measurement, setMeasurement] = useState<ReceivedAudioLevel>({ hasTrack: false, receiving: false, level: 0 });
  const graph = useRef<AudioGraph | null>(null);
  const disposeTimer = useRef<number | null>(null);

  useEffect(() => {
    if (disposeTimer.current !== null) window.clearTimeout(disposeTimer.current);
    disposeTimer.current = null;
    if (graph.current && graph.current.video !== video) {
      closeGraph(graph.current);
      graph.current = null;
    }
    return () => {
      const current = graph.current;
      if (!current || current.video !== video) return;
      // StrictMode reuses the same media element after its effect cleanup.
      disposeTimer.current = window.setTimeout(() => {
        if (graph.current === current) {
          closeGraph(current);
          graph.current = null;
        }
        disposeTimer.current = null;
      }, 0);
    };
  }, [video]);

  useEffect(() => {
    if (!video || !hasAudio) {
      setMeasurement({ hasTrack: false, receiving: false, level: 0 });
      return;
    }
    if (!graph.current) {
      const context = new AudioContext();
      const source = context.createMediaElementSource(video);
      const analyser = context.createAnalyser();
      const silent = context.createGain();
      silent.gain.value = 0;
      analyser.fftSize = 256;
      analyser.smoothingTimeConstant = 0.45;
      source.connect(analyser);
      analyser.connect(silent);
      silent.connect(context.destination);
      graph.current = { video, context, source, analyser, silent };
    }
    const { context, analyser } = graph.current;
    const samples = new Float32Array(analyser.fftSize);
    const resume = () => { void context.resume().catch(() => undefined); };
    const sample = () => {
      const receiving = video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA && !video.paused;
      let level = 0;
      if (receiving && context.state === "running") {
        analyser.getFloatTimeDomainData(samples);
        let energy = 0;
        for (const value of samples) energy += value * value;
        level = Math.min(1, Math.sqrt(energy / samples.length));
      }
      setMeasurement({ hasTrack: true, receiving, level });
    };
    const timer = window.setInterval(sample, pollInterval);
    document.addEventListener("pointerdown", resume);
    resume();
    sample();
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("pointerdown", resume);
    };
  }, [video, hasAudio]);

  return measurement;
}
