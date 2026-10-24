import { useEffect, useState } from 'react';

export async function jsonRequest(url, options = {}) {
  const response = await fetch(url, { cache: 'no-store', ...options });
  const body = await response.json();
  if (!response.ok) throw Object.assign(new Error(body.error || 'Could not load usage.'), { status: response.status });
  return body;
}

/** Fetch url and poll it every two seconds; data resets when url changes. */
export function useResource(url) {
  const [state, setState] = useState({ data: null, error: '', status: 0, loading: true });
  useEffect(() => {
    let active = true, timer;
    const controller = new AbortController();
    setState({ data: null, error: '', status: 0, loading: true });
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
  }, [url]);
  return state;
}
