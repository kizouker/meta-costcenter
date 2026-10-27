import { useState } from 'react';
import { send, useResource } from '../api.js';
import { EmptyRow } from '../components.jsx';
import { COST_NOTE, cost, number, tokens } from '../format.js';

const FIELDS = [['input_per_million', 'Input'], ['cached_input_per_million', 'Cached input'], ['output_per_million', 'Output']];
const SOURCE = { missing: 'No rate', local: 'Local rate', public: 'Public rate' };
const money = value => '$' + Number(value).toLocaleString(undefined, { maximumFractionDigits: 4 });
// Image models are priced per image; their token rates are zero.
const rateText = rate => !rate ? '—' : Number(rate.per_image) > 0 && FIELDS.every(([key]) => !Number(rate[key])) ? `${money(rate.per_image)} per image`
  : FIELDS.map(([key]) => money(rate[key])).join(' / ') + (Number(rate.per_image) > 0 ? ` + ${money(rate.per_image)} per image` : '');

function RateForm({ model, initial, onDone }) {
  const [values, setValues] = useState(() => ({ ...Object.fromEntries(FIELDS.map(([key]) => [key, initial?.[key] ?? ''])), per_image: initial?.per_image ?? '0' }));
  const [error, setError] = useState('');
  async function save(event) {
    event.preventDefault();
    try { await send('PUT', '/api/pricing/' + encodeURIComponent(model), values); onDone(); }
    catch (failure) { setError(failure.message); }
  }
  return <form className="rate-form" onSubmit={save} aria-label={`Rate for ${model}`}>
    {FIELDS.map(([key, label]) => <label key={key}>{label} <span className="unit">USD / 1M tokens</span>
      <input name={key} type="number" min="0" step="any" required value={values[key]} onChange={event => setValues(previous => ({ ...previous, [key]: event.target.value }))} /></label>)}
    <label>Per image <span className="unit">USD, for image models</span>
      <input name="per_image" type="number" min="0" step="any" value={values.per_image} onChange={event => setValues(previous => ({ ...previous, per_image: event.target.value }))} /></label>
    <div className="form-actions"><button type="submit" className="primary">Save local rate</button><button type="button" onClick={onDone}>Cancel</button></div>
    {error && <p className="form-error" role="alert">{error}</p>}
  </form>;
}

export default function Pricing() {
  const { data, error, reload } = useResource('/api/pricing');
  const [editing, setEditing] = useState(null);
  const [actionError, setActionError] = useState('');
  if (!data) return error ? <div className="notice danger" role="alert">{error}</div> : <div className="loading-state" role="status">Loading pricing…</div>;
  const total = data.reduce((sum, m) => sum + tokens(m), 0), unpriced = data.filter(m => m.source === 'missing');
  const unpricedShare = total ? unpriced.reduce((sum, m) => sum + tokens(m), 0) / total * 100 : 0;
  async function remove(model) {
    setActionError('');
    try { await send('DELETE', '/api/pricing/' + encodeURIComponent(model)); reload(); }
    catch (failure) { setActionError(failure.message); }
  }
  function done() { setEditing(null); reload(); }
  return <>
    <section className="panel intro-panel"><h2>{unpriced.length ? `${unpriced.length} model${unpriced.length === 1 ? '' : 's'} without a rate` : 'Every model has a rate'}</h2>
      <p>{unpriced.length ? `${unpricedShare.toFixed(1)}% of all recorded tokens can’t be priced yet. ` : ''}Public rates ship with Muse Cost and apply to usage before their verified date as an assumption, marked “≈”.
        Rates you enter here are <strong>stored only in your local usage database</strong>, never in the project files, so models without public pricing stay private. A local rate overrides the public rate for its model.</p></section>
    {actionError && <div className="notice danger" role="alert">{actionError}</div>}
    <section className="panel" id="pricing"><div className="panel-heading"><div><h2>Models</h2><p>Every model seen in your usage, all time. Rates are USD per million tokens: input / cached input / output.</p></div></div>
      <div className="table-wrap"><table><thead><tr><th scope="col">Model</th><th scope="col">Rate</th><th scope="col">Input / cached / output</th><th scope="col">Completions</th><th scope="col">Token share</th><th scope="col">Est. cost</th><th scope="col"><span className="visually-hidden">Actions</span></th></tr></thead>
        <tbody id="pricing-rows">{!data.length && <EmptyRow columns={7}>No usage recorded yet.</EmptyRow>}
          {data.map(m => <tr key={m.model} className={m.source === 'missing' ? 'needs-rate' : ''}>
            <td>{m.model}</td>
            <td><span className={'badge badge-' + m.source}>{SOURCE[m.source]}</span>
              {m.source === 'public' && m.cost_assumed_events > 0 && <span className="secondary-text nowrap" title={`Verified from ${m.verified_from}; earlier usage assumes this rate`}>≈ before {new Date(m.verified_from + 'T00:00').toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</span>}
              {m.source === 'local' && m.public_rate && <span className="secondary-text">overrides public {rateText(m.public_rate)}</span>}</td>
            <td>{rateText(m.rate)}</td><td>{number(m.events)}</td><td>{m.token_share.toFixed(1)}%</td><td>{cost(m)}</td>
            <td><span className="row-actions">{editing === m.model ? null : <>
              <button type="button" className="small-button" onClick={() => setEditing(m.model)}>{m.source === 'local' ? 'Edit' : m.source === 'missing' ? 'Set rate' : 'Override'}</button>
              {m.source === 'local' && <button type="button" className="small-button" onClick={() => remove(m.model)}>Remove</button>}</>}</span></td>
          </tr>).flatMap(row => {
            const model = row.key, m = data.find(item => item.model === model);
            return editing === model ? [row, <tr key={model + '-edit'} className="edit-row"><td colSpan={7}><RateForm model={model} initial={m.source === 'local' ? m.rate : m.public_rate} onDone={done} /></td></tr>] : [row];
          })}
        </tbody></table></div>
      <div className="table-footer">{COST_NOTE}</div></section>
  </>;
}
