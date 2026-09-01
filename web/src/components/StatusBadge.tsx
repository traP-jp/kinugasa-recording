import { AlertCircle, CheckCircle2, LoaderCircle, Radio, UploadCloud } from "lucide-react";

type Status = "active" | "inactive" | "activating" | "waiting" | "connected" | "error" |
  "recording" | "errored" | "uploading" | "completed";

const labels: Record<Status, string> = {
  active: "Active",
  inactive: "Inactive",
  activating: "Activating",
  waiting: "Waiting",
  connected: "Connected",
  error: "Error",
  recording: "Recording",
  errored: "Errored",
  uploading: "Uploading",
  completed: "Completed",
};

export function StatusBadge({ status }: { status: Status }) {
  const icon = status === "connected" || status === "completed" || status === "active"
    ? <CheckCircle2 size={13} />
    : status === "recording" ? <Radio size={13} />
      : status === "uploading" ? <UploadCloud size={13} />
        : status === "activating" || status === "waiting" ? <LoaderCircle className="spin" size={13} />
          : <AlertCircle size={13} />;
  return <span className={`status-badge status-${status}`}>{icon}{labels[status]}</span>;
}
