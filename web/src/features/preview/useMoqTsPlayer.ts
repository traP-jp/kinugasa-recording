import type * as Moq from "@moq/net";
import mpegts from "mpegts.js";
import { useEffect, useState } from "react";
import { createMoqTsLoader } from "../../lib/moqTs";

type Status = "offline" | "loading" | "live" | "unsupported" | "error";

interface PlayerState {
  status: Status;
  hasVideo: boolean;
  hasAudio: boolean;
}

const idle: PlayerState = { status: "offline", hasVideo: false, hasAudio: false };

export function useMoqTsPlayer(video: HTMLVideoElement | null, connection: Moq.Connection | undefined, cameraName: string): PlayerState {
  const [state, setState] = useState<PlayerState>(idle);

  useEffect(() => {
    setState(idle);
    if (!video || !connection) return;
    if (!mpegts.isSupported()) {
      setState({ ...idle, status: "unsupported" });
      return;
    }

    const player = mpegts.createPlayer(
      { type: "mpegts", isLive: true, url: `moq://${cameraName}` },
      {
        customLoader: createMoqTsLoader(connection, cameraName),
        enableStashBuffer: false,
        lazyLoad: false,
        liveBufferLatencyChasing: true,
        autoCleanupSourceBuffer: true,
      },
    );
    const onPlaying = () => setState((current) => ({ ...current, status: "live", hasVideo: true }));
    const onInfo = (info: { hasAudio?: boolean }) => {
      setState((current) => ({ ...current, hasAudio: info.hasAudio === true }));
    };
    const onError = () => setState((current) => ({ ...current, status: "error" }));
    video.addEventListener("playing", onPlaying);
    player.on(mpegts.Events.MEDIA_INFO, onInfo);
    player.on(mpegts.Events.ERROR, onError);
    player.attachMediaElement(video);
    player.load();
    setState({ ...idle, status: "loading" });
    void Promise.resolve(player.play()).catch(onError);

    return () => {
      video.removeEventListener("playing", onPlaying);
      player.off(mpegts.Events.MEDIA_INFO, onInfo);
      player.off(mpegts.Events.ERROR, onError);
      player.destroy();
    };
  }, [video, connection, cameraName]);

  return state;
}
