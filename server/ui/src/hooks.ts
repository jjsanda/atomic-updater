import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError } from './api';

export interface PollState<T> {
  data: T | undefined;
  error: string | null;
  loading: boolean; // true only until the first response arrives
  refreshing: boolean;
  refresh: () => void;
}

// Polls fn every intervalMs. Previous data is held during refetches so the
// UI never flashes a skeleton; out-of-order responses are discarded.
export function usePoll<T>(
  fn: () => Promise<T>,
  intervalMs: number,
  deps: readonly unknown[] = [],
): PollState<T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [tick, setTick] = useState(0);
  const generation = useRef(0);
  const fnRef = useRef(fn);
  fnRef.current = fn;

  const refresh = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    let cancelled = false;
    const gen = ++generation.current;

    const run = async () => {
      setRefreshing(true);
      try {
        const result = await fnRef.current();
        if (!cancelled && gen === generation.current) {
          setData(result);
          setError(null);
        }
      } catch (err) {
        if (!cancelled && gen === generation.current) {
          setError(err instanceof ApiError ? err.message : String(err));
        }
      } finally {
        if (!cancelled && gen === generation.current) {
          setLoading(false);
          setRefreshing(false);
        }
      }
    };

    void run();
    const timer = intervalMs > 0 ? window.setInterval(run, intervalMs) : 0;
    return () => {
      cancelled = true;
      if (timer) window.clearInterval(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs, tick, ...deps]);

  return { data, error, loading, refreshing, refresh };
}

// Debounces a fast-changing value (search inputs).
export function useDebounced<T>(value: T, delayMs = 300): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delayMs);
    return () => window.clearTimeout(timer);
  }, [value, delayMs]);
  return debounced;
}
