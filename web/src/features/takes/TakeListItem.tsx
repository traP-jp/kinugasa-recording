import { ArrowRight, Clock3 } from "lucide-react";
import { Link } from "react-router-dom";
import type { FinishedTake } from "../../api/types";
import { StatusBadge } from "../../components/StatusBadge";
import { formatDateTime, formatDuration } from "../../lib/format";

export function TakeListItem({ sessionName, take }: { sessionName: string; take: FinishedTake }) {
  return (
    <Link className="take-list-item" to={`/sessions/${encodeURIComponent(sessionName)}/takes/${encodeURIComponent(take.name)}`}>
      <div><StatusBadge status={take.state} /><h3>{take.name}</h3></div>
      <div className="take-time"><span>{formatDateTime(take.finishedAt)}</span><span><Clock3 size={14} />{formatDuration(take.startedAt, take.finishedAt)}</span></div>
      {take.error && <span className="inline-error">{take.error}</span>}
      <ArrowRight size={18} />
    </Link>
  );
}
