import { useEffect, useState } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { useModalDialog } from '../components.jsx';
import { send, useResource } from '../api.js';
import { number, usd } from '../format.js';

const PERIODS = { day: 'Day', week: 'Week (from Monday)', month: 'Month' };
const BLANK = { period: 'day', project: '', billing_mode: '', limit_usd: '', warn_levels: [80] };
const MAX_WARNINGS = 4;
// Suggested values for the optional warning levels when they are first switched on.
const SUGGESTED = [80, 50, 90, 95];

/** Four warning slots: the first is always on; saved levels fill slots in ascending order. */
function warningSlots(levels) {
  const unused = SUGGESTED.filter(value => !levels.includes(value));
  return Array.from({ length: MAX_WARNINGS }, (_, i) => i < levels.length ? { on: true, value: levels[i] } : { on: false, value: unused.shift() ?? '' });
}

export function windowText(alert) {
  if (alert.period === 'day') return 'today';
  const start = new Date(alert.window_start + 'T00:00').toLocaleDateString(undefined, { weekday: alert.period === 'week' ? 'short' : undefined, month: 'short', day: 'numeric' });
  return `since ${start}`;
}

export const alertText = alert => `${alert.label}: ${usd(alert.spent_usd)} of ${usd(alert.limit_usd)} ${windowText(alert)} (${alert.percent}%)`
  + (alert.level === 'warning' && alert.warning_at ? ` · passed your ${alert.warning_at}% warning` : '');

/** Sample alerts for previewing the alert bar; nothing is saved. */
export const SAMPLE_ALERTS = {
  exceeded: { id: 'preview', preview: true, level: 'exceeded', period: 'day', label: 'Daily limit for all projects', spent_usd: 6.1, limit_usd: 5, percent: 122 },
  warning: { id: 'preview', preview: true, level: 'warning', period: 'day', label: 'Daily limit for all projects', spent_usd: 4.2, limit_usd: 5, percent: 84, warning_at: 80 },
};

/** Add or edit an alert in a modal dialog. */
function AlertDialog({ initial, projects, onSaved, onClose }) {
  const modal = useModalDialog(onClose);
  const [values, setValues] = useState({ ...BLANK, ...initial });
  const [warnings, setWarnings] = useState(() => warningSlots(initial.warn_levels || BLANK.warn_levels));
  const setWarning = (index, change) => setWarnings(previous => previous.map((slot, i) => i === index ? { ...slot, ...change } : slot));
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const change = event => setValues(previous => ({ ...previous, [event.target.name]: event.target.value }));
  async function save(event) {
    event.preventDefault();
    const body = { ...values, warn_levels: warnings.filter(slot => slot.on).map(slot => Number(slot.value)), project: values.project || null, billing_mode: values.billing_mode || null };
    setSaving(true);
    try {
      await (values.id ? send('PUT', '/api/alerts/' + values.id, body) : send('POST', '/api/alerts', body));
      onSaved();
    } catch (failure) { setError(failure.message); setSaving(false); }
  }
  const projectOptions = values.project && !projects.includes(values.project) ? [values.project, ...projects] : projects;
  return <dialog {...modal} id="alert-dialog" className="modal" aria-labelledby="alert-dialog-title">
    <div className="modal-heading"><h2 id="alert-dialog-title">{values.id ? 'Edit alert' : 'Add alert'}</h2><button type="button" aria-label="Close" onClick={onClose}>×</button></div>
    <form className="alert-form" id="alert-form" onSubmit={save}>
      <p className="modal-intro">Get warned when estimated cost passes a limit. Alerts never stop usage.</p>
      <label>Period<select name="period" value={values.period} onChange={change}>{Object.entries(PERIODS).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></label>
      <label>Project<select name="project" value={values.project || ''} onChange={change}><option value="">All projects</option>{projectOptions.map(project => <option key={project} value={project}>{project}</option>)}</select></label>
      <label>Usage<select name="billing_mode" value={values.billing_mode || ''} onChange={change}><option value="">All usage</option><option value="payg">Pay as you go only</option></select></label>
      <fieldset className="warning-levels"><legend>Warn me at</legend>
        <p className="field-help">Percent of the limit. Each level notifies you once per period.</p>
        {warnings.map((slot, i) => <div key={i} className={'warning-slot' + (slot.on ? '' : ' off')}>
          {i === 0 ? <span className="warning-check" aria-hidden="true">✓</span>
            : <input type="checkbox" id={`warning-on-${i}`} checked={slot.on} onChange={event => setWarning(i, { on: event.target.checked })} aria-label={`Use warning ${i + 1}`} />}
          <label htmlFor={`warning-${i}`}>Warning {i + 1}</label>
          <span className="percent-input"><input id={`warning-${i}`} name={`warning-${i}`} type="number" min="1" max="99" step="1" required={slot.on} disabled={!slot.on}
            value={slot.value} onChange={event => setWarning(i, { value: event.target.value })} /><span aria-hidden="true">%</span></span>
        </div>)}
      </fieldset>
      <label>Limit (USD)<input name="limit_usd" type="number" min="0.01" step="any" required value={values.limit_usd} onChange={change} placeholder="5.00" /></label>
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="modal-actions"><button type="button" onClick={onClose}>Cancel</button><button type="submit" className="primary" disabled={saving}>{values.id ? 'Save alert' : 'Add alert'}</button></div>
    </form>
  </dialog>;
}

function NotificationControl() {
  const supported = typeof Notification !== 'undefined';
  const [permission, setPermission] = useState(supported ? Notification.permission : 'unsupported');
  if (permission === 'unsupported') return <p className="muted">This browser does not support desktop notifications.</p>;
  if (permission === 'granted') return <div className="notification-control"><p className="muted" id="notification-state">Desktop notifications are on. They appear while a Muse Cost tab is open.</p>
    <button type="button" id="test-notification" className="small-button" onClick={() => new Notification('Muse Cost: test notification', { body: alertText(SAMPLE_ALERTS.exceeded), icon: '/logo.png' })}>Send test notification</button></div>;
  if (permission === 'denied') return <p className="muted" id="notification-state">Desktop notifications are blocked for this page. Allow them in your browser’s site settings to receive alerts.</p>;
  return <button type="button" id="enable-notifications" onClick={() => Notification.requestPermission().then(setPermission)}>Enable desktop notifications</button>;
}

export default function Alerts({ options, onPreview }) {
  const location = useLocation(), navigate = useNavigate();
  const { data, error, reload } = useResource('/api/alerts');
  // null when closed; otherwise the values the dialog opens with (an id means edit).
  const [dialog, setDialog] = useState(() => location.state?.project ? { project: location.state.project } : null);
  const [actionError, setActionError] = useState('');
  const projects = options.project || [];
  // A project page's "Add alert" link opens the dialog once; clear it so refresh or back doesn't reopen it.
  useEffect(() => { if (location.state?.project) navigate(location.pathname, { replace: true, state: null }); }, [location, navigate]);
  async function act(request) {
    setActionError('');
    try { await request; reload(); } catch (failure) { setActionError(failure.message); }
  }
  const toggle = alert => act(send('PUT', '/api/alerts/' + alert.id, { ...alert, enabled: !alert.enabled }));
  const remove = alert => { if (confirm(`Delete the ${alert.label.toLowerCase()}?`)) act(send('DELETE', '/api/alerts/' + alert.id)); };
  return <>
    {(error || actionError) && <div className="notice danger" role="alert">{error || actionError}</div>}
    <section className="panel" id="alerts"><div className="panel-heading"><div><h2>Your alerts</h2><p>Get warned when estimated cost passes a limit for a day, a week, or a month, for all projects or one. Unpriced usage is not counted, and alerts never stop usage.</p></div>
      <button type="button" id="add-alert" className="primary" onClick={() => setDialog({})}>Add alert</button></div>
      {!data ? <p className="panel-empty">Loading alerts…</p> : !data.length ? <p className="panel-empty">No alerts yet. Add a daily limit to start.</p> :
        <ul className="alert-list" id="alert-rows">{data.map(alert => <li key={alert.id} className={'alert-item level-' + alert.level}>
          <div className="alert-summary"><strong>{alert.label}</strong>
            <span className="alert-status">{alert.level === 'paused' ? 'Paused' : `${usd(alert.spent_usd)} of ${usd(alert.limit_usd)} ${windowText(alert)}`}</span></div>
          <div className="alert-meter" role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.min(alert.percent, 100)} aria-label={`${alert.percent}% of limit`}>
            <span style={{ width: Math.min(alert.percent, 100) + '%' }} />{alert.warn_levels.map(value => <i key={value} style={{ left: value + '%' }} title={`Warns at ${value}%`} />)}</div>
          <div className="alert-details"><span>{alert.percent}% used · warns at {alert.warn_levels.map(value => value + '%').join(', ')}{alert.unpriced_events ? ` · ${number(alert.unpriced_events)} unpriced completions not counted` : ''}</span>
            <span className="row-actions"><button type="button" className="small-button" onClick={() => setDialog({ id: alert.id, period: alert.period, project: alert.project || '', billing_mode: alert.billing_mode || '', limit_usd: alert.limit_usd, warn_levels: alert.warn_levels, enabled: alert.enabled })}>Edit</button>
              <button type="button" className="small-button" onClick={() => toggle(alert)}>{alert.enabled ? 'Pause' : 'Resume'}</button>
              <button type="button" className="small-button" onClick={() => remove(alert)}>Delete</button></span></div>
        </li>)}</ul>}
    </section>
    <section className="panel preview-panel" id="alert-preview"><div><h2>Notifications and preview</h2><p>See how the alert bar looks with sample numbers. Nothing is saved.</p></div>
      <div className="preview-controls"><NotificationControl />
        <div className="form-actions"><button type="button" id="preview-exceeded" onClick={() => onPreview(SAMPLE_ALERTS.exceeded)}>Preview limit exceeded</button>
          <button type="button" id="preview-warning" onClick={() => onPreview(SAMPLE_ALERTS.warning)}>Preview warning</button></div></div></section>
    {dialog && <AlertDialog initial={dialog} projects={projects} onSaved={() => { setDialog(null); reload(); }} onClose={() => setDialog(null)} />}
  </>;
}
