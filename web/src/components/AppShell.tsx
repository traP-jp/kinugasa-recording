import { Aperture, RadioTower } from "lucide-react";
import type { ReactNode } from "react";
import { Link, NavLink } from "react-router-dom";

interface AppShellProps {
  children: ReactNode;
  sessionName?: string;
}

export function AppShell({ children, sessionName }: AppShellProps) {
  return (
    <div className="app-shell">
      <header className="topbar">
        <Link className="brand" to="/sessions" aria-label="Kinugasa Recording">
          <span className="brand-mark"><Aperture size={21} strokeWidth={2.4} /></span>
          <span>kinugasa</span>
          <span className="brand-subtitle">recording</span>
        </Link>
        <div className="system-state" title="Console server connected">
          <span className="pulse-dot" />
          <RadioTower size={15} />
          <span>console online</span>
        </div>
      </header>
      {sessionName && (
        <nav className="session-nav" aria-label="Session navigation">
          <div className="session-nav-name">{sessionName}</div>
          <NavLink end to={`/sessions/${encodeURIComponent(sessionName)}`}>Console</NavLink>
          <NavLink to={`/sessions/${encodeURIComponent(sessionName)}/takes`}>Takes</NavLink>
        </nav>
      )}
      <main className="page-frame">{children}</main>
    </div>
  );
}
