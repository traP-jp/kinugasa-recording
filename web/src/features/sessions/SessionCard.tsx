import { ArrowUpRight, CalendarDays } from "lucide-react";
import { Link } from "react-router-dom";
import type { Session } from "../../api/types";
import { StatusBadge } from "../../components/StatusBadge";
import { formatDateTime } from "../../lib/format";

export function SessionCard({ session }: { session: Session }) {
  return (
    <Link className="session-card" to={`/sessions/${encodeURIComponent(session.name)}`}>
      <div className="session-card-head">
        <StatusBadge status={session.state} />
        <ArrowUpRight size={18} />
      </div>
      <h2>{session.name}</h2>
      <div className="session-card-meta"><CalendarDays size={14} />{formatDateTime(session.createdAt)}</div>
      <div className="session-id">{session.id}</div>
    </Link>
  );
}
