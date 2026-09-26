import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { PageLoading } from "./components/PageLoading";

const SessionsPage = lazy(() => import("./pages/SessionsPage").then((module) => ({ default: module.SessionsPage })));
const SessionConsolePage = lazy(() => import("./pages/SessionConsolePage").then((module) => ({ default: module.SessionConsolePage })));
const TakesPage = lazy(() => import("./pages/TakesPage").then((module) => ({ default: module.TakesPage })));
const TakeDetailPage = lazy(() => import("./pages/TakeDetailPage").then((module) => ({ default: module.TakeDetailPage })));
const NotFoundPage = lazy(() => import("./pages/NotFoundPage").then((module) => ({ default: module.NotFoundPage })));

export function App() {
  return (
    <Suspense fallback={<PageLoading />}>
      <Routes>
        <Route path="/" element={<Navigate replace to="/sessions" />} />
        <Route path="/sessions" element={<SessionsPage />} />
        <Route path="/sessions/:sessionName" element={<SessionConsolePage />} />
        <Route path="/sessions/:sessionName/takes" element={<TakesPage />} />
        <Route path="/sessions/:sessionName/takes/:takeName" element={<TakeDetailPage />} />
        <Route path="*" element={<NotFoundPage />} />
      </Routes>
    </Suspense>
  );
}
