import { Link, useParams } from 'react-router';
import { useResource } from '../api.js';
import { ChartPanel, Metric, ModelsTable, NotFound, SessionsTable, TotalsMetrics } from '../components.jsx';
import { filterQuery, number, periodOnly, tokens, usd } from '../format.js';
import { windowText } from './Alerts.jsx';
import { NoUsageInPeriod } from './SessionDetail.jsx';

export default function ProjectDetail({ filters, setFilters, alerts = [], resolution, setResolution }) {
  const { name } = useParams();
  const { data, error, status } = useResource(`/api/projects/${encodeURIComponent(name)}?` + filterQuery({ ...periodOnly(filters), resolution }));
  if (status === 404) return <NotFound kind="Project" message={`No usage has been recorded for project ${name}.`} />;
  if (!data) return error ? <div className="notice danger" role="alert">{error}</div> : <div className="loading-state" role="status">Loading project…</div>;
  const t = data.totals;
  return <>
    <nav className="breadcrumb" aria-label="Breadcrumb"><Link to="/projects">Projects</Link><span aria-hidden="true">›</span><span aria-current="page">{data.project}</span></nav>
    {!t.events && <NoUsageInPeriod filters={filters} setFilters={setFilters} subject="project" />}
    <TotalsMetrics totals={t} extra={<Metric label="Sessions" icon="◷" id="session-count" accent value={number(data.sessions.length)} description="Subagents included in their parent" />} />
    <ChartPanel chart={data.chart} setResolution={setResolution} />
    <section className="panel" id="project-alerts"><div className="panel-heading"><div><h2>Alerts</h2><p>Cost limits that cover this project</p></div>
      <Link className="panel-link" to="/alerts" state={{ project: data.project }}>Add alert for this project →</Link></div>
      {(() => { const covering = alerts.filter(alert => !alert.project || alert.project === data.project);
        return covering.length ? <ul className="rank-list">{covering.map(alert => <li key={alert.id} className={'level-' + alert.level}><div className="rank-main"><strong>{alert.label}</strong>
          <span>{alert.level === 'paused' ? 'Paused' : `${usd(alert.spent_usd)} of ${usd(alert.limit_usd)} ${windowText(alert)} · ${alert.percent}%`}</span></div></li>)}</ul>
          : <p className="panel-empty">No alerts cover this project yet.</p>; })()}
    </section>
    <section className="panel" id="project-sessions"><div className="panel-heading"><div><h2>Sessions</h2><p>Select a session to see what it did</p></div></div>
      <SessionsTable sessions={data.sessions} showProject={false} empty="No sessions in this period." /></section>
    <section className="panel"><div className="panel-heading"><h2>Models</h2></div><ModelsTable models={data.models} total={tokens(t)} /></section>
  </>;
}
