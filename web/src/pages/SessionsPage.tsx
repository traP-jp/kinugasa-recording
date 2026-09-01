import { Layers3, Plus } from "lucide-react";
import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import { AppShell } from "../components/AppShell";
import { EmptyState } from "../components/EmptyState";
import { ErrorBanner } from "../components/ErrorBanner";
import { PageLoading } from "../components/PageLoading";
import { Pagination } from "../components/Pagination";
import { ResourceNameForm } from "../components/ResourceNameForm";
import { SessionCard } from "../features/sessions/SessionCard";
import { usePollingResource } from "../hooks/usePollingResource";

export function SessionsPage() {
  const [page, setPage] = useState(1);
  const [actionError, setActionError] = useState<Error | null>(null);
  const navigate = useNavigate();
  const load = useCallback(() => api.listSessions(page), [page]);
  const sessions = usePollingResource(load);

  async function create(name: string) {
    setActionError(null);
    try {
      const session = await api.createSession(name);
      navigate(`/sessions/${encodeURIComponent(session.name)}`);
    } catch (error) {
      setActionError(error instanceof Error ? error : new Error(String(error)));
      throw error;
    }
  }

  return (
    <AppShell>
      <header className="page-hero">
        <div><span className="eyebrow">Recording workspace</span><h1>Sessions</h1><p>収録単位を作成し、multi-camera consoleを開きます。</p></div>
        <div className="hero-metric"><span>{sessions.data?.pagination.total ?? "—"}</span><small>total sessions</small></div>
      </header>
      <ErrorBanner error={actionError ?? sessions.error} onDismiss={() => setActionError(null)} />
      <section className="panel create-panel">
        <div className="panel-heading"><div className="section-icon"><Plus size={19} /></div><div><h2>Sessionを作成</h2><p>一意の名前を指定してください。</p></div></div>
        <ResourceNameForm label="Session name" placeholder="studio-a" actionLabel="作成" onSubmit={create} />
      </section>
      {sessions.loading ? <PageLoading /> : sessions.data && sessions.data.items.length > 0 ? (
        <>
          <section className="session-grid" aria-label="Sessions">
            {sessions.data.items.map((session) => <SessionCard key={session.id} session={session} />)}
          </section>
          <Pagination value={sessions.data.pagination} onChange={setPage} />
        </>
      ) : <EmptyState icon={<Layers3 size={28} />} title="Sessionはまだありません" description="上のフォームから最初のSessionを作成してください。" />}
    </AppShell>
  );
}
