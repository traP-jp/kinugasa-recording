import { Archive } from "lucide-react";
import { useCallback, useState } from "react";
import { useParams } from "react-router-dom";
import { api } from "../api/client";
import { AppShell } from "../components/AppShell";
import { EmptyState } from "../components/EmptyState";
import { ErrorBanner } from "../components/ErrorBanner";
import { PageLoading } from "../components/PageLoading";
import { Pagination } from "../components/Pagination";
import { TakeListItem } from "../features/takes/TakeListItem";
import { usePollingResource } from "../hooks/usePollingResource";

export function TakesPage() {
  const { sessionName = "" } = useParams();
  const [page, setPage] = useState(1);
  const load = useCallback(() => api.listTakes(sessionName, page, 20), [page, sessionName]);
  const takes = usePollingResource(load, 3000);
  return (
    <AppShell sessionName={sessionName}>
      <header className="page-hero compact-hero"><div><span className="eyebrow">Archive</span><h1>Finished Takes</h1><p>{sessionName} の録画・アップロード履歴</p></div><div className="hero-metric"><span>{takes.data?.pagination.total ?? "—"}</span><small>finished takes</small></div></header>
      <ErrorBanner error={takes.error} />
      {takes.loading && !takes.data ? <PageLoading /> : takes.data?.items.length ? (
        <section className="panel take-list-panel">
          {takes.data.items.map((take) => <TakeListItem key={take.id} sessionName={sessionName} take={take} />)}
          <Pagination value={takes.data.pagination} onChange={setPage} />
        </section>
      ) : <EmptyState icon={<Archive size={28} />} title="Finished Takeはありません" description="録画を停止すると、ここでアップロード状況を確認できます。" />}
    </AppShell>
  );
}
