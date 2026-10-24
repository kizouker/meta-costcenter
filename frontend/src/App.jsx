import { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Navigate, NavLink, Route, Routes, matchPath, useLocation, useNavigate } from 'react-router';
import { jsonRequest, useResource } from './api.js';
import { FilterChips, Filters, HeadingHelp } from './components.jsx';
import { EMPTY, filterQuery, number, periodOnly, validateFilters } from './format.js';
import Breakdown, { GROUPS } from './pages/Breakdown.jsx';
import Overview from './pages/Overview.jsx';
import ProjectDetail from './pages/ProjectDetail.jsx';
import Projects from './pages/Projects.jsx';
import SessionDetail from './pages/SessionDetail.jsx';
import Sessions from './pages/Sessions.jsx';

const STORAGE = 'muse-cost.react.v1';
const NAV = [['/', '◫', 'Overview'], ['/sessions', '↳', 'Sessions'], ['/projects', '▤', 'Projects'], ['/breakdown', '▦', 'Breakdown']];
const TITLES = { '/': 'Usage overview', '/sessions': 'Sessions', '/projects': 'Projects', '/breakdown': 'Usage breakdown' };

/** The current page: its title, whether it is a URL-scoped detail page, and what Export downloads. */
function pageFor(pathname, filters) {
  const session = matchPath('/sessions/:id', pathname), project = matchPath('/projects/:name', pathname);
  if (session) return { title: 'Session', scoped: true, exportQuery: { ...periodOnly(filters), session: session.params.id }, exportHelp: 'Export this session and its subagents as a JSON file.' };
  if (project) return { title: project.params.name, scoped: true, exportQuery: { ...periodOnly(filters), project: project.params.name }, exportHelp: 'Export this project’s usage as a JSON file.' };
  return { title: TITLES[pathname] || 'Page not found', scoped: false, exportQuery: filters, exportHelp: 'Export usage matching the current filters as a JSON file.' };
}

function Dashboard({ config }) {
  const navigate = useNavigate(), location = useLocation();
  const [saved] = useState(() => {
    try { const value = JSON.parse(sessionStorage.getItem(STORAGE)); return value?.instance === config.instance_id ? value : null; } catch { return null; }
  });
  const [filters, setFilters] = useState(() => {
    const legacy = Object.fromEntries([...new URLSearchParams(location.search)].filter(([key, value]) => key in EMPTY && value));
    return validateFilters({ ...config.filters, ...saved?.filters, ...legacy });
  });
  const [grouping, setGrouping] = useState(() => GROUPS[saved?.grouping] ? saved.grouping : 'project');
  const { data, loading, error } = useResource('/api/dashboard?' + filterQuery(filters));
  const [options, setOptions] = useState({});
  const [exportError, setExportError] = useState('');
  const [exporting, setExporting] = useState(false);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const activeFilters = Object.keys(EMPTY).filter(key => filters[key] !== EMPTY[key]).length;
  const page = pageFor(location.pathname, filters);
  useEffect(() => { if (data) setOptions(data.options); }, [data]);
  useEffect(() => {
    try { sessionStorage.setItem(STORAGE, JSON.stringify({ instance: config.instance_id, filters, grouping })); } catch { /* Storage may be disabled. */ }
  }, [config.instance_id, filters, grouping]);
  useEffect(() => { document.title = 'Muse Cost · ' + page.title; }, [page.title]);
  useEffect(() => { window.scrollTo(0, 0); }, [location.pathname]);
  useEffect(() => { if (location.search || location.hash) navigate(location.pathname, { replace: true }); }, [location, navigate]);
  function selectFilter(name, value) {
    setFilters(previous => ({ ...previous, [name]: value }));
  }
  async function exportUsage() {
    setExporting(true); setExportError('');
    try {
      const response = await fetch('/api/export?' + filterQuery(page.exportQuery));
      if (!response.ok) throw new Error('Could not export usage. Check the current filters.');
      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement('a'); link.href = url; link.download = 'muse-usage.json'; link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (error) { setExportError(error.message); }
    finally { setExporting(false); }
  }
  const status = data?.status;
  const pageProps = { filters, setFilters };
  return <>
    <aside className="sidebar"><NavLink className="brand" to="/" aria-label="Muse Cost home"><img className="brand-logo" src="/logo.png" alt="" width="512" height="512" /></NavLink>
      {NAV.map(([path, icon, label]) => <NavLink key={path} end={path === '/'} className={({ isActive }) => 'nav' + (isActive ? ' active' : '')} to={path}><span aria-hidden="true">{icon}</span>{label}</NavLink>)}
    </aside>
    <main data-page={location.pathname === '/' ? 'overview' : location.pathname.split('/')[1]}>
      <header className="page-header">
        <div className="header-row">
          <div className="header-heading"><h1>{page.title}</h1>
            <span className="live-indicator" id="live-status" role="status" aria-label={error ? 'Disconnected' : loading ? 'Loading usage' : status?.error ? 'Collection error' : status?.scanning ? 'Scanning logs' : status?.warnings.length ? 'Coverage warning' : 'Live updates'}
              title={`${error ? 'Disconnected' : status?.scanning ? 'Scanning logs' : 'Live updates'}${status?.last_scan ? ` · Last scan ${new Date(status.last_scan).toLocaleString()} · ${number(data.source_count)} sources` : ''}`}><span className="dot" /></span>
          </div>
          <div className="actions"><button id="toggle-filters" type="button" aria-label="Filters" aria-expanded={filtersOpen} aria-controls="filter-sidebar" onClick={() => setFiltersOpen(true)}>Filters{activeFilters > 0 && <span className="filter-count" aria-hidden="true">{activeFilters}</span>}</button><HeadingHelp buttonId="export" icon label="Export usage" text={page.exportHelp} onClick={exportUsage} disabled={exporting}>
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5" /></svg>
          </HeadingHelp></div>
        </div>
        <FilterChips filters={filters} setFilters={setFilters} openFilters={() => setFiltersOpen(true)} scoped={page.scoped} />
      </header>
      {(error || exportError || status?.error) && <div id="error" className="notice danger" role="alert">{error || exportError || status.error}{data && ' Showing the last imported usage; it may be stale.'}</div>}
      {Boolean(status?.warnings.length) && <div id="warnings" className="notice">{status.warnings.map(warning => <p key={warning}>{warning}</p>)}</div>}
      <Routes>
        <Route path="/" element={data ? <Overview data={data} /> : <div className="loading-state" role="status">Loading usage…</div>} />
        <Route path="/breakdown" element={data ? <Breakdown data={data} grouping={grouping} setGrouping={setGrouping} selectFilter={selectFilter} /> : <div className="loading-state" role="status">Loading usage…</div>} />
        <Route path="/sessions" element={<Sessions {...pageProps} />} />
        <Route path="/sessions/:id" element={<SessionDetail {...pageProps} />} />
        <Route path="/projects" element={<Projects {...pageProps} />} />
        <Route path="/projects/:name" element={<ProjectDetail {...pageProps} />} />
        <Route path="/activity" element={<Navigate to="/sessions" replace />} />
        <Route path="*" element={<p>Choose a page from the sidebar.</p>} />
      </Routes>
      <footer><span>Local coverage only. Token estimates exclude unobserved charges.</span></footer>
    </main>
    {filtersOpen && <Filters filters={filters} setFilters={setFilters} options={options} onClose={() => setFiltersOpen(false)} />}
  </>;
}
function App() {
  const [config, setConfig] = useState(null), [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    jsonRequest('/api/config', { signal: controller.signal }).then(setConfig).catch(error => { if (error.name !== 'AbortError') setError(error.message); });
    return () => controller.abort();
  }, [retry]);
  if (error) return <div className="startup" role="alert"><h1>Could not connect to Muse Cost</h1><p>{error}</p><button onClick={() => { setError(''); setRetry(value => value + 1); }}>Retry</button></div>;
  if (!config) return <div className="startup" role="status">Loading Muse Cost…</div>;
  return <BrowserRouter><Dashboard config={config} /></BrowserRouter>;
}
createRoot(document.getElementById('root')).render(<App />);
