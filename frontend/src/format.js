export const EMPTY = { period: 'today', from: '', to: '', project: '', model: '', session: '', billing_mode: '' };
export const PERIODS = { today: 'Today', week: 'Last 7 days', month: 'This month', all: 'All time' };
export const BILLING_MODES = { payg: 'Pay as you go', subscription: 'Subscription', unknown: 'Unknown' };

export const number = value => Number(value || 0).toLocaleString();
export const compact = value => Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(Number(value || 0));
export const equivalent = t => Number(t.payg_usd) + Number(t.subscription_equivalent_usd) + Number(t.unknown_mode_equivalent_usd);
export const tokens = t => t.input + t.output;
export function cost(t, key) {
  if (!t.events || t.cost_unknown_events === t.events) return 'Unknown';
  const value = key ? Number(t[key]) : equivalent(t);
  return '$' + value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 4 }) + (t.cost_unknown_events ? ' +' : '');
}
// UUIDv7 session IDs share a time-based prefix, so keep the random tail too.
export const shortId = id => id && id.length > 14 ? id.slice(0, 8) + '…' + id.slice(-4) : id;
export function dateTime(value) {
  if (!value) return '—';
  const date = new Date(value);
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return date.toLocaleString(undefined, { month: 'short', day: 'numeric', ...(sameYear ? {} : { year: 'numeric' }), hour: 'numeric', minute: '2-digit' });
}
export function duration(start, end) {
  if (!start || !end) return '—';
  const minutes = Math.round((new Date(end) - new Date(start)) / 60000);
  if (minutes < 1) return 'under 1 min';
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}
export const percent = (part, whole) => whole ? part / whole * 100 : 0;

export function filterQuery(filters) {
  // Explicit period=all for a custom range; omit empty fields from API requests.
  return new URLSearchParams(Object.entries(filters).filter(([, value]) => value !== '' && value != null)).toString();
}
/** Detail pages are scoped by their URL; only the date range carries over. */
export const periodOnly = ({ period, from, to }) => ({ period, from, to });
export function periodLabel(filters) {
  if (filters.from || filters.to) return `${filters.from || 'Start'} – ${filters.to || 'today'}`;
  return PERIODS[filters.period];
}
export function validateFilters(value) {
  const result = { ...EMPTY };
  for (const key of Object.keys(EMPTY)) if (typeof value?.[key] === 'string') result[key] = value[key];
  if (!PERIODS[result.period]) result.period = 'today';
  if (!['', ...Object.keys(BILLING_MODES)].includes(result.billing_mode)) result.billing_mode = '';
  return result;
}
