import { Link } from 'react-router';
import { ChartPanel, CostMetric, Metric } from '../components.jsx';
import { COST_NOTE, compact, cost, coverage, dateTime, duration, equivalent, number, percent, tokens, usd } from '../format.js';

/** Top models by estimated cost, with the tokens no rate covers called out. */
function CostByModel({ data }) {
  const t = data.totals, total = equivalent(t);
  const priced = data.groups.model.filter(m => m.events > m.cost_unknown_events).sort((a, b) => equivalent(b) - equivalent(a));
  const unpricedTokens = tokens(t) - t.priced_tokens;
  return <article className="panel cost-panel" id="cost-by-model"><div className="panel-heading"><div><h2>Cost by model</h2><p>Estimated API-equivalent USD</p></div><Link className="panel-link" to="/pricing">Pricing →</Link></div>
    <ul className="rank-list">
      {!priced.length && <li className="rank-empty">No priced usage in this period.</li>}
      {priced.slice(0, 5).map(model => <li key={model.label}><div className="rank-main"><strong>{model.label}</strong>
        <span className="share"><span className="share-fill" style={{ display: 'block', width: percent(equivalent(model), total) + '%' }} /></span></div><span className="rank-cost">{cost(model)}</span></li>)}
      {unpricedTokens > 0 && <li className="unpriced-row"><div className="rank-main"><strong>Unpriced</strong>
        <span>{compact(unpricedTokens)} tokens ({(100 - coverage(t)).toFixed(1)}%) have no rate · <Link className="inline-link" to="/pricing">set rates</Link></span></div><span className="rank-cost">Unknown</span></li>}
    </ul>
    <p className="cost-footnote">Pay as you go {usd(t.payg_usd)} · subscription equivalent {usd(t.subscription_equivalent_usd)} · unknown billing mode {usd(t.unknown_mode_equivalent_usd)}. Subscription values are comparisons, not charges. {COST_NOTE}</p>
  </article>;
}

export default function Overview({ data, setResolution }) {
  const t = data.totals, activeDays = data.timeline.filter(day => day.events).length;
  return <>
    <section className="metrics" aria-label="Usage totals">
      <CostMetric totals={t} />
      <Metric label="Average per active day" icon="◔" id="daily-cost" value={!activeDays ? usd(0) : t.events > t.cost_unknown_events ? usd(equivalent(t) / activeDays) : 'Unknown'} description={`${number(activeDays)} day${activeDays === 1 ? '' : 's'} with usage`} />
      <Metric label="Input tokens" icon="↗" id="input" value={t.input >= 1e8 ? compact(t.input) : number(t.input)} descriptionId="output-label" description={`${compact(t.output)} output · ${compact(t.cached)} cached`} />
      <Metric label="Active sessions" icon="◷" id="session-count" value={number(data.session_count)} descriptionId="month-label" description={`View all sessions · MTD ${cost(data.month)}`} accent to="/sessions" />
    </section>
    <section className="chart-layout"><ChartPanel chart={data.chart} setResolution={setResolution} /><CostByModel data={data} /></section>
    <section className="top-layout">
      <article className="panel" id="top-sessions"><div className="panel-heading"><div><h2>Top sessions</h2><p>Highest estimated cost, subagents included</p></div><Link className="panel-link" to="/sessions">All sessions →</Link></div>
        <ul className="rank-list" id="top-session-rows">{!data.top_sessions.length && <li className="rank-empty">No sessions in this period.</li>}
          {data.top_sessions.map(session => <li key={session.id}><Link to={'/sessions/' + encodeURIComponent(session.id)} title={session.id}><strong>{session.project}</strong>
            <span>{dateTime(session.started)} · {duration(session.started, session.last_activity)} · {compact(session.input + session.output)} tokens{session.subagents ? ` · ${session.subagents} subagent${session.subagents === 1 ? '' : 's'}` : ''}</span></Link><span className="rank-cost">{cost(session)}</span></li>)}
        </ul></article>
      <article className="panel" id="top-projects"><div className="panel-heading"><div><h2>Top projects</h2><p>Highest estimated cost</p></div><Link className="panel-link" to="/projects">All projects →</Link></div>
        <ul className="rank-list">{!data.top_projects.length && <li className="rank-empty">No projects in this period.</li>}
          {data.top_projects.map(project => <li key={project.label}><Link to={'/projects/' + encodeURIComponent(project.label)}><strong>{project.label}</strong>
            <span>{number(project.sessions)} session{project.sessions === 1 ? '' : 's'} · {compact(project.input + project.output)} tokens</span></Link><span className="rank-cost">{cost(project)}</span></li>)}
        </ul></article>
    </section>
  </>;
}
