import { AlertTriangle, X } from "lucide-react";

interface ErrorBannerProps {
  error: Error | string | null;
  onDismiss?: () => void;
}

export function ErrorBanner({ error, onDismiss }: ErrorBannerProps) {
  if (!error) return null;
  return (
    <div className="error-banner" role="alert">
      <AlertTriangle size={18} />
      <span>{typeof error === "string" ? error : error.message}</span>
      {onDismiss && (
        <button type="button" onClick={onDismiss} aria-label="エラーを閉じる"><X size={16} /></button>
      )}
    </div>
  );
}
