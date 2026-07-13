import { useEffect, useRef, useState } from "react";
import {
  RemoteTrack,
  RemoteTrackPublication,
  Room,
  RoomEvent,
  Track,
} from "livekit-client";

import { CameraStatus, getPreviewToken } from "./api";

function VideoTrack({ track }: { track: RemoteTrack }) {
  const element = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    const video = element.current;
    if (video !== null) track.attach(video);
    return () => {
      if (video !== null) track.detach(video);
    };
  }, [track]);
  return <video ref={element} autoPlay playsInline muted />;
}

export function Preview({ cameras }: { cameras: CameraStatus[] }) {
  const [tracks, setTracks] = useState<Record<string, RemoteTrack>>({});
  const [connectionError, setConnectionError] = useState(false);

  useEffect(() => {
    if (cameras.length === 0) return;
    const room = new Room({ adaptiveStream: true });
    const subscribed = (
      track: RemoteTrack,
      publication: RemoteTrackPublication,
    ) => {
      if (track.kind === Track.Kind.Video)
        setTracks((current) => ({
          ...current,
          [publication.trackName]: track,
        }));
    };
    const unsubscribed = (
      _track: RemoteTrack,
      publication: RemoteTrackPublication,
    ) => {
      setTracks((current) => {
        const next = { ...current };
        delete next[publication.trackName];
        return next;
      });
    };
    room.on(RoomEvent.TrackSubscribed, subscribed);
    room.on(RoomEvent.TrackUnsubscribed, unsubscribed);
    room.on(RoomEvent.Disconnected, () => setConnectionError(true));
    let disposed = false;
    void getPreviewToken()
      .then((token) => room.connect(token.serverUrl, token.participantToken))
      .then(() => {
        if (!disposed) setConnectionError(false);
      })
      .catch(() => {
        if (!disposed) setConnectionError(true);
      });
    return () => {
      disposed = true;
      void room.disconnect();
    };
  }, [cameras.length]);

  return (
    <section aria-labelledby="preview-heading">
      <h2 id="preview-heading">Preview</h2>
      {connectionError && <p role="alert">LiveKit previewへ接続できません。</p>}
      <div className="preview-grid">
        {cameras.map((camera) => (
          <article className="preview-card" key={camera.name}>
            <h3>{camera.name}</h3>
            {tracks[camera.name] ? (
              <VideoTrack track={tracks[camera.name]} />
            ) : (
              <div className="video-placeholder">映像を待っています</div>
            )}
            {camera.phase !== "Connected" && (
              <p className="warning" role="status">
                Camera未接続: {camera.phase}
              </p>
            )}
          </article>
        ))}
      </div>
    </section>
  );
}
