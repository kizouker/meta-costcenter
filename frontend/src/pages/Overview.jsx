import { Link } from 'react-router';
import { ChartPanel, Metric, TotalsMetrics } from '../components.jsx';
import { compact, cost, dateTime, duration, number } from '../format.js';

export default function Overview({ data }) {
  const t = data.totals;
  return <>
    <TotalsMetrics totals={t} extra={<Metric label="Active sessions" icon="◷" id="session-count" value={number(data.session_count)} descriptionId="month-label" description={`View all sessions · MTD ${compact(data.month.input + data.month.output)} tokens`} accent to="/sessions" />} />
    <section className="chart-layout"><ChartPanel timeline={data.timeline} />
      <article className="panel cost-panel"><div className="panel-heading"><h2>Cost context</h2></div>
        <div className="cost-item"><span>Estimated pay as you go</span><strong id="payg">{cost(t, 'payg_usd')}</strong></div>
        <div className="cost-item"><span>Subscription API equivalent</span><strong id="subscription">{cost(t, 'subscription_equivalent_usd')}</strong></div>
        <div className="cost-item"><span>Unknown-mode API equivalent</span><strong id="unknown-mode">{cost(t, 'unknown_mode_equivalent_usd')}</strong></div>
        <p className="cost-footnote">Subscription values are comparisons, not charges. Non-token charges and missing rates remain unknown.{data.monthly_warning_usd && ` Local monthly warning: USD ${data.monthly_warning_usd}; not a spend cap.`}</p>
      </article></section>
    <section className="top-layout">
      <article className="panel" id="top-sessions"><div className="panel-heading"><div><h2>Top sessions</h2><p>Highest API-equivalent cost, subagents included</p></div><Link className="panel-link" to="/sessions">All sessions →</Link></div>
        <ul className="rank-list" id="top-session-rows">{!data.top_sessions.length && <li className="rank-empty">No sessions in this period.</li>}
          {data.top_sessions.map(session => <li key={session.id}><Link to={'/sessions/' + encodeURIComponent(session.id)} title={session.id}><strong>{session.project}</strong>
            <span>{dateTime(session.started)} · {duration(session.started, session.last_activity)} · {compact(session.input + session.output)} tokens{session.subagents ? ` · ${session.subagents} subagent${session.subagents === 1 ? '' : 's'}` : ''}</span></Link><span className="rank-cost">{cost(session)}</span></li>)}
        </ul></article>
      <article className="panel" id="top-projects"><div className="panel-heading"><div><h2>Top projects</h2><p>Highest API-equivalent cost</p></div><Link className="panel-link" to="/projects">All projects →</Link></div>
        <ul className="rank-list">{!data.top_projects.length && <li className="rank-empty">No projects in this period.</li>}
          {data.top_projects.map(project => <li key={project.label}><Link to={'/projects/' + encodeURIComponent(project.label)}><strong>{project.label}</strong>
            <span>{number(project.sessions)} session{project.sessions === 1 ? '' : 's'} · {compact(project.input + project.output)} tokens</span></Link><span className="rank-cost">{cost(project)}</span></li>)}
        </ul></article>
    </section>
  </>;
}
