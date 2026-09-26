import { useCallback, useEffect, useRef, useState } from "react";

export interface PollingResource<T> {
  data: T | null;
  error: Error | null;
  loading: boolean;
  reload: () => Promise<void>;
}

export function usePollingResource<T>(loader: () => Promise<T>, interval = 0): PollingResource<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(true);
  const mounted = useRef(true);

  const reload = useCallback(async () => {
    try {
      const next = await loader();
      if (!mounted.current) return;
      setData(next);
      setError(null);
    } catch (caught) {
      if (mounted.current) setError(caught instanceof Error ? caught : new Error(String(caught)));
    } finally {
      if (mounted.current) setLoading(false);
    }
  }, [loader]);

  useEffect(() => {
    mounted.current = true;
    void reload();
    if (interval <= 0) return () => void (mounted.current = false);
    const timer = window.setInterval(() => void reload(), interval);
    return () => {
      mounted.current = false;
      window.clearInterval(timer);
    };
  }, [interval, reload]);

  return { data, error, loading, reload };
}
