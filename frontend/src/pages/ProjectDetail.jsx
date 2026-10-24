import { Link, useParams } from 'react-router';
import { useResource } from '../api.js';
import { ChartPanel, Metric, ModelsTable, NotFound, SessionsTable, TotalsMetrics } from '../components.jsx';
import { filterQuery, number, periodOnly, tokens } from '../format.js';
import { NoUsageInPeriod } from './SessionDetail.jsx';

export default function ProjectDetail({ filters, setFilters }) {
  const { name } = useParams();
  const { data, error, status } = useResource(`/api/projects/${encodeURIComponent(name)}?` + filterQuery(periodOnly(filters)));
  if (status === 404) return <NotFound kind="Project" message={`No usage has been recorded for project ${name}.`} />;
  if (!data) return error ? <div className="notice danger" role="alert">{error}</div> : <div className="loading-state" role="status">Loading project…</div>;
  const t = data.totals;
  return <>
    <nav className="breadcrumb" aria-label="Breadcrumb"><Link to="/projects">Projects</Link><span aria-hidden="true">›</span><span aria-current="page">{data.project}</span></nav>
    {!t.events && <NoUsageInPeriod filters={filters} setFilters={setFilters} subject="project" />}
    <TotalsMetrics totals={t} extra={<Metric label="Sessions" icon="◷" id="session-count" accent value={number(data.sessions.length)} description="Subagents included in their parent" />} />
    <ChartPanel timeline={data.timeline} />
    <section className="panel" id="project-sessions"><div className="panel-heading"><div><h2>Sessions</h2><p>Select a session to see what it did</p></div></div>
      <SessionsTable sessions={data.sessions} showProject={false} empty="No sessions in this period." /></section>
    <section className="panel"><div className="panel-heading"><h2>Models</h2></div><ModelsTable models={data.models} total={tokens(t)} /></section>
  </>;
}
