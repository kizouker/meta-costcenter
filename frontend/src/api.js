import { useEffect, useRef, useState } from 'react';

export async function jsonRequest(url, options = {}) {
  const response = await fetch(url, { cache: 'no-store', ...options });
  const body = await response.json();
  if (!response.ok) throw Object.assign(new Error(body.error || 'Could not load usage.'), { status: response.status });
  return body;
}

/** Change local data (alerts, rates); the server requires a JSON body for every write. */
export function send(method, url, body = {}) {
  return jsonRequest(url, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
}

/** Fetch url and poll it every two seconds; data resets when url changes. reload() refetches at once. */
export function useResource(url) {
  const [state, setState] = useState({ data: null, error: '', status: 0, loading: true });
  const [tick, setTick] = useState(0);
  const first = useRef(url);
  useEffect(() => {
    let active = true, timer;
    const controller = new AbortController();
    if (first.current !== url) { first.current = url; setState({ data: null, error: '', status: 0, loading: true }); }
    async function poll() {
      try {
        const data = await jsonRequest(url, { signal: controller.signal });
        if (active) setState({ data, error: '', status: 200, loading: false });
      } catch (error) {
        if (active && error.name !== 'AbortError') setState(previous => ({ ...previous, error: error.message, status: error.status || 0, loading: false }));
      } finally {
        if (active) timer = setTimeout(poll, 2000);
      }
    }
    poll();
    return () => { active = false; controller.abort(); clearTimeout(timer); };
  }, [url, tick]);
  return { ...state, reload: () => setTick(value => value + 1) };
}
