import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';
import { useResource } from '../api.js';
import { ChartPanel, EmptyRow, Metric, ModelsTable, NotFound, Share, TotalsMetrics } from '../components.jsx';
import { BILLING_MODES, BILLING_SOURCES, cost, dateTime, duration, filterQuery, number, percent, periodLabel, periodOnly, shortId, tokens } from '../format.js';

const PAGE = 100;
const sessionLink = id => '/sessions/' + encodeURIComponent(id);

/** How the billing mode was found: the Muse hook's detections, or a hint when there are none. */
function BillingSource({ detections }) {
  if (!detections?.length) return <span className="billing-source" id="billing-source">Not detected. Sessions record their billing mode once the Muse hook is installed with meta-cost hook install.</span>;
  return <span className="billing-source" id="billing-source">{detections.map(d => `${BILLING_MODES[d.billing_mode] || d.billing_mode}: ${BILLING_SOURCES[d.source] || d.source}`
    + (d.key_label ? `, Keyguard key ${d.key_label}` : '') + (d.hook_source === 'resume' ? ' (resumed)' : '') + ` at ${dateTime(d.detected_at)}`).join('; ')}</span>;
}

function TreeNode({ node, total, current }) {
  return <li>
    <div className="tree-row">
      {node.id === current ? <strong title={node.id}>{shortId(node.id)} <span className="tree-tag">this session</span></strong> : <Link className="row-link" to={sessionLink(node.id)} title={node.id}>{shortId(node.id)}</Link>}
      <span className="tree-stats">{number(tokens(node))} tokens · {cost(node)}</span>
      <span className="tree-share"><Share value={percent(tokens(node), total)} /></span>
    </div>
    {node.children.length > 0 && <ul>{node.children.map(child => <TreeNode key={child.id} node={child} total={total} current={current} />)}</ul>}
  </li>;
}

export function NoUsageInPeriod({ filters, setFilters, subject }) {
  return <div className="notice" role="status">No usage for this {subject} in {periodLabel(filters).toLowerCase()}.
    {filters.period !== 'all' && <> <button type="button" className="inline-button" onClick={() => setFilters(previous => ({ ...previous, period: 'all', from: '', to: '' }))}>Show all time</button></>}</div>;
}

export default function SessionDetail({ filters, setFilters }) {
  const { id } = useParams();
  const { data, error, status } = useResource(`/api/sessions/${encodeURIComponent(id)}?` + filterQuery(periodOnly(filters)));
  const [page, setPage] = useState(0);
  const [copied, setCopied] = useState(false);
  useEffect(() => setPage(0), [id]);
  if (status === 404) return <NotFound kind="Session" message={`No usage has been recorded for session ${id}.`} />;
  if (!data) return error ? <div className="notice danger" role="alert">{error}</div> : <div className="loading-state" role="status">Loading session…</div>;
  const t = data.totals, family = tokens(t);
  const pages = Math.max(1, Math.ceil(data.events.length / PAGE)), current = Math.min(page, pages - 1);
  const events = data.events.slice(current * PAGE, (current + 1) * PAGE);
  function copy() { navigator.clipboard?.writeText(data.id).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); }); }
  return <>
    <nav className="breadcrumb" aria-label="Breadcrumb"><Link to="/sessions">Sessions</Link>
      {data.root !== data.id && <><span aria-hidden="true">›</span><Link to={sessionLink(data.root)} title={data.root}>{shortId(data.root)}</Link></>}
      <span aria-hidden="true">›</span><span aria-current="page">{shortId(data.id)}</span></nav>
    <section className="panel detail-header" id="session-header">
      <div className="detail-title"><h2>{data.project ? <Link to={'/projects/' + encodeURIComponent(data.project)}>{data.project}</Link> : 'Session'}</h2>
        <code title="Session ID">{data.id}</code><button type="button" className="small-button" onClick={copy}>{copied ? 'Copied' : 'Copy ID'}</button></div>
      <dl className="facts">
        {data.parent && <div><dt>Subagent of</dt><dd><Link className="row-link" id="parent-link" to={sessionLink(data.parent)} title={data.parent}>{shortId(data.parent)}</Link></dd></div>}
        <div><dt>Started</dt><dd>{dateTime(data.started)}</dd></div>
        <div><dt>Last activity</dt><dd>{dateTime(data.last_activity)}</dd></div>
        <div><dt>Duration</dt><dd>{duration(data.started, data.last_activity)}</dd></div>
        <div><dt>Billing</dt><dd>{data.billing_modes.map(mode => BILLING_MODES[mode] || mode).join(', ') || '—'}<BillingSource detections={data.billing_detections} /></dd></div>
      </dl>
    </section>
    {!t.events && <NoUsageInPeriod filters={filters} setFilters={setFilters} subject="session" />}
    <TotalsMetrics totals={t} extra={<Metric label="This session alone" icon="◷" id="own-share" accent value={percent(tokens(data.own), family).toFixed(1) + '%'}
      description={`${number(tokens(data.own))} tokens · ${cost(data.own)} · subagents make up the rest`} />} />
    <section className="chart-layout"><ChartPanel timeline={data.timeline} unit={data.timeline_unit} />
      <article className="panel" id="subagents"><div className="panel-heading"><div><h2>Subagents</h2><p>Each session’s own usage and share of the total</p></div></div>
        {data.tree && data.tree.children.length ? <ul className="tree" id="subagent-tree"><TreeNode node={data.tree} total={family} current={data.id} /></ul>
          : <p className="panel-empty">This session started no subagents{t.events ? '' : ' in this period'}.</p>}
      </article></section>
    <section className="panel"><div className="panel-heading"><h2>Models</h2></div><ModelsTable models={data.models} total={family} /></section>
    <section className="panel" id="events"><div className="panel-heading"><div><h2>Model completions</h2><p>Every recorded completion for this session and its subagents, oldest first</p></div>
      {pages > 1 && <div className="pager"><button type="button" disabled={current === 0} onClick={() => setPage(current - 1)}>Previous</button><span>Page {current + 1} of {pages}</span><button type="button" disabled={current === pages - 1} onClick={() => setPage(current + 1)}>Next</button></div>}</div>
      <div className="table-wrap"><table><thead><tr><th>Observed</th><th>Session</th><th>Model</th><th>Input / output</th><th>Cached</th><th>API-equivalent cost</th><th>Provenance</th></tr></thead><tbody id="event-rows">
        {!events.length && <EmptyRow columns={7}>No completions in this period.</EmptyRow>}
        {events.map(row => <tr key={row.id}><td>{new Date(row.recorded_at).toLocaleString()}</td>
          <td>{row.session === data.id ? <span title={row.session}>{shortId(row.session)}</span> : <Link className="row-link nowrap" to={sessionLink(row.session)} title={row.session}>{shortId(row.session)}</Link>}</td>
          <td>{row.model}</td><td>{number(row.input)} / {number(row.output)}</td><td>{row.cached == null ? 'Unknown' : number(row.cached)}</td>
          <td>{row.api_equivalent_usd == null ? 'Unknown' : '$' + Number(row.api_equivalent_usd).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 6 })}</td>
          <td className="record" title={`Record ${row.id}; sequence ${row.sequence}; usage ${row.usage_status}; pricing ${row.pricing_snapshot || 'unknown'}`}>#{row.sequence} · {row.id}</td></tr>)}
      </tbody></table></div></section>
  </>;
}
