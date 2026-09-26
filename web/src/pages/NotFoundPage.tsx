import { Link } from "react-router-dom";
import { AppShell } from "../components/AppShell";

export function NotFoundPage() {
  return <AppShell><div className="not-found"><span>404</span><h1>ページが見つかりません</h1><Link className="button button-primary" to="/sessions">Sessionsへ戻る</Link></div></AppShell>;
}
