import { Link, useNavigate } from 'react-router';
import { useResource } from '../api.js';
import { EmptyRow, Share, useSort } from '../components.jsx';
import { cost, dateTime, equivalent, filterQuery, number, percent, tokens } from '../format.js';

const SORT = { label: p => p.label.toLowerCase(), sessions: p => p.sessions, last_activity: p => p.last_activity, tokens, cost: equivalent };
export default function Projects({ filters }) {
  const navigate = useNavigate();
  const { data, error } = useResource('/api/projects?' + filterQuery(filters));
  const { header, apply } = useSort({ key: 'cost', desc: true });
  if (!data) return error ? <div className="notice danger" role="alert">{error}</div> : <div className="loading-state" role="status">Loading projects…</div>;
  const total = data.reduce((sum, p) => sum + tokens(p), 0);
  return <section className="panel" id="projects"><div className="panel-heading"><div><h2>Projects</h2><p>Select a project to see its sessions and models.</p></div></div>
    <div className="table-wrap"><table><thead><tr>{header('label', 'Project')}{header('sessions', 'Sessions')}{header('last_activity', 'Last activity')}{header('tokens', 'Input / output')}{header('cost', 'API-equivalent cost')}<th scope="col">Token share</th></tr></thead>
      <tbody id="project-rows">{!data.length && <EmptyRow columns={6}>No projects match the current filters.</EmptyRow>}
        {apply(data, (row, key) => SORT[key](row)).map(row => <tr key={row.label} className="clickable" onClick={event => { if (!event.target.closest('a')) navigate('/projects/' + encodeURIComponent(row.label)); }}>
          <td><Link className="row-link" to={'/projects/' + encodeURIComponent(row.label)}>{row.label}</Link></td><td>{number(row.sessions)}</td><td>{dateTime(row.last_activity)}</td>
          <td>{number(row.input)} / {number(row.output)}</td><td>{cost(row)}</td><td><Share value={percent(tokens(row), total)} /></td></tr>)}
      </tbody></table></div></section>;
}
