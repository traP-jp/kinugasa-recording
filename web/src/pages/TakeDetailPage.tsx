import { Check, Clipboard, FileVideo2, HardDrive, Hash } from "lucide-react";
import { useCallback, useState } from "react";
import { useParams } from "react-router-dom";
import { api } from "../api/client";
import type { VideoFile } from "../api/types";
import { AppShell } from "../components/AppShell";
import { ErrorBanner } from "../components/ErrorBanner";
import { PageLoading } from "../components/PageLoading";
import { StatusBadge } from "../components/StatusBadge";
import { usePollingResource } from "../hooks/usePollingResource";
import { formatDateTime, formatDuration, shortHash } from "../lib/format";

export function TakeDetailPage() {
  const { sessionName = "", takeName = "" } = useParams();
  const load = useCallback(() => api.getTake(sessionName, takeName), [sessionName, takeName]);
  const take = usePollingResource(load, 3000);
  if (take.loading && !take.data) return <AppShell sessionName={sessionName}><PageLoading /></AppShell>;
  return (
    <AppShell sessionName={sessionName}>
      <ErrorBanner error={take.error} />
      {take.data && (
        <>
          <header className="take-detail-hero">
            <div><span className="eyebrow">Take detail</span><div className="title-with-status"><h1>{take.data.name}</h1><StatusBadge status={take.data.state} /></div><p>{take.data.id}</p></div>
            <dl><div><dt>Started</dt><dd>{formatDateTime(take.data.startedAt)}</dd></div><div><dt>Finished</dt><dd>{formatDateTime(take.data.finishedAt)}</dd></div><div><dt>Duration</dt><dd>{formatDuration(take.data.startedAt, take.data.finishedAt)}</dd></div></dl>
          </header>
          {take.data.error && <ErrorBanner error={take.data.error} />}
          <section className="file-grid">
            {take.data.videoFiles.map((file) => <VideoFileCard key={file.cameraName} file={file} />)}
          </section>
        </>
      )}
    </AppShell>
  );
}

function VideoFileCard({ file }: { file: VideoFile }) {
  const [copied, setCopied] = useState<"hash" | "key" | null>(null);
  async function copy(kind: "hash" | "key", value: string | null) {
    if (!value) return;
    await navigator.clipboard.writeText(value);
    setCopied(kind);
    window.setTimeout(() => setCopied(null), 1500);
  }
  return (
    <article className="file-card">
      <header><div className="file-icon"><FileVideo2 size={20} /></div><div><h2>{file.cameraName}</h2><span>video.mp4</span></div><StatusBadge status={file.state} /></header>
      {file.error && <p className="inline-error">{file.error}</p>}
      <dl>
        <div><dt><HardDrive size={14} />Object key</dt><dd title={file.objectKey ?? undefined}>{file.objectKey ?? "未確定"}<button disabled={!file.objectKey} onClick={() => void copy("key", file.objectKey)} aria-label="Object keyをコピー">{copied === "key" ? <Check size={14} /> : <Clipboard size={14} />}</button></dd></div>
        <div><dt><Hash size={14} />SHA-256 (Base64)</dt><dd title={file.hash ?? undefined}><code>{shortHash(file.hash)}</code><button disabled={!file.hash} onClick={() => void copy("hash", file.hash)} aria-label="Hashをコピー">{copied === "hash" ? <Check size={14} /> : <Clipboard size={14} />}</button></dd></div>
      </dl>
      <footer><span>{formatDateTime(file.startedAt)}</span><span>{formatDuration(file.startedAt, file.finishedAt)}</span></footer>
    </article>
  );
}
