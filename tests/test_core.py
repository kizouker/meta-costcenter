import json
import sys
import tempfile
import unittest
from datetime import datetime, timezone
from decimal import Decimal
from pathlib import Path

sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'src'))
from meta_cost_center.collector import scan
from meta_cost_center.reports import (NotFound, query, totals, reconciliation, lines, sessions,
                                      session_detail, project_detail, projects)
from meta_cost_center.store import connect


def event(rid='event-1', session='parent', inp=100, cached=20, model='test-model'):
    return {'id':rid,'stream':{'id':session},'sequence':1,
            'recorded_at':int(datetime.now(timezone.utc).timestamp()*1_000_000),
            'payload':{'event':{'kind':'model_completed','model':model,
                        'usage':{'input_tokens':inp,'output_tokens':10,'cached_tokens':cached},
                        'response':'SECRET_RESPONSE'},'prompt':'SECRET_PROMPT'}}



def raw_alert(db, **fields):
    """Insert an alert without validation, for limits smaller than the fixture's sub-cent spend."""
    fields = {'warn_levels': '[80]', 'enabled': 1, **fields}
    with db:
        return db.execute(f'INSERT INTO alerts ({", ".join(fields)}) VALUES ({", ".join("?" * len(fields))})',
                          list(fields.values())).lastrowid

class CollectorTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory()
        self.root=Path(self.tmp.name)
        self.logs=self.root/'logs'
        self.path=self.logs/'parent/session.jsonl'
        self.path.parent.mkdir(parents=True)
        self.db=connect(self.root/'db.sqlite3')
        self.rates=[{'model':'test-model','effective_from':'2020-01-01','snapshot_version':'fixture',
                     'input_per_million':'1','cached_input_per_million':'0.1','output_per_million':'2'}]
    def tearDown(self):
        self.db.close()
        self.tmp.cleanup()
    def write(self,record,path=None,mode='a'):
        path=path or self.path
        path.parent.mkdir(parents=True,exist_ok=True)
        with path.open(mode) as f:
            f.write(json.dumps(record)+'\n')
    def scan(self):
        return scan(self.db,[self.logs])
    def rows(self):
        return query(self.db,{'period':'all'})
    def test_rescan_partial_and_privacy(self):
        self.write(event())
        self.assertEqual(self.scan()['added'],1)
        self.assertEqual(self.scan()['added'],0)
        record=json.dumps(event('event-2'))
        with self.path.open('a') as f:f.write(record[:50])
        self.assertEqual(self.scan()['added'],0)
        self.assertEqual(self.db.execute('SELECT pending FROM sources').fetchone()[0],1)
        with self.path.open('a') as f:f.write(record[50:]+'\n')
        self.assertEqual(self.scan()['added'],1)
        dump='\n'.join(self.db.iterdump())
        self.assertNotIn('SECRET_',dump)
        self.assertEqual(len(self.rows()),2)
    def test_subagent_and_summary_not_added(self):
        self.write(event())
        self.write(event('child-event','child',50), self.logs/'parent/subagent/child/session.jsonl')
        self.write({'id':'summary','type':'run.usage','session_id':'parent',
                    'timestamp':datetime.now(timezone.utc).isoformat(),
                    'usage':{'models':[{'model':'test-model','input_tokens':150,'output_tokens':20,'cached_tokens':40}]}})
        self.scan()
        rows=query(self.db,{'period':'all','session':'parent'})
        self.assertEqual(sum(r['input'] for r in rows),150)
        self.assertEqual(len(rows),2)
        self.assertEqual(reconciliation(self.db)[0]['status'],'match')
    def test_replacement_and_copy_dedup(self):
        self.write(event())
        self.scan()
        self.write(event(),self.logs/'copied/session.jsonl')
        self.write(event('event-2'),mode='w')
        self.scan()
        self.assertEqual(len(self.rows()),2)
        self.assertTrue(any('retained' in r[0] for r in self.db.execute('SELECT status FROM sources')))
    def test_invalid_and_unknown(self):
        self.write(event(inp=-2))
        self.write(event('unknown',cached=None,model='other'))
        self.scan()
        self.assertEqual(len(self.rows()),1)
        t=totals(self.rows(),self.rates)
        self.assertEqual(t['cost_unknown_events'],1)
        self.assertEqual(self.db.execute('SELECT errors FROM sources').fetchone()[0],1)
        self.scan()
        self.assertEqual(self.db.execute('SELECT errors FROM sources').fetchone()[0],1)
    def test_estimate_and_filters(self):
        self.write(event())
        config={'sessions':{'parent':{'project':'demo','billing_mode':'subscription'}}}
        scan(self.db,[self.logs],config)
        rows=query(self.db,{'period':'all','project':'demo','billing_mode':'subscription'})
        t=totals(rows,self.rates)
        self.assertEqual(t['subscription_equivalent_usd'],Decimal('0.000102'))
        self.assertEqual(t['payg_usd'],0)
        self.assertEqual(query(self.db,{'period':'all','model':'missing'}),[])
        self.assertIn('Not entered','\n'.join(lines(self.db,{'period':'all'},self.rates)))
        empty='\n'.join(lines(self.db,{'period':'all','from':'2000-01-01','to':'2000-01-02'},self.rates))
        self.assertIn('Estimated PAYG: USD 0.000000',empty)  # no usage costs nothing; it is not unknown
    def test_native_metadata_survives_incremental_scan(self):
        self.write({'payload_type':'runtime.session.metadata','payload':{'record':{
            'workspace_root':'/private/work/demo','model_id':'test-model'}}})
        record=event()
        del record['payload']['event']['model']
        self.write(record)
        self.scan()
        self.assertEqual(self.rows()[0]['project'],'demo')
        self.assertEqual(self.rows()[0]['model'],'test-model')
        record['id']='next'
        self.write(record)
        scan(self.db,[self.logs],{'sessions':{'parent':{'billing_mode':'payg'}}})
        self.assertTrue(all(r['project']=='demo' and r['billing_mode']=='payg' for r in self.rows()))
        self.assertNotIn('/private/work','\n'.join(self.db.iterdump()))

    def test_unavailable_is_not_zero(self):
        r=scan(self.db,[self.root/'missing'])
        self.assertTrue(r['warnings'])
    def test_unsupported_summary_is_diagnosed(self):
        self.write({'type':'run.usage','timestamp':datetime.now(timezone.utc).isoformat(),'usage':{'new_shape':True}})
        self.scan()
        self.assertEqual(self.db.execute('SELECT errors FROM sources').fetchone()[0],1)

class DrillDownTests(unittest.TestCase):
    write=CollectorTests.write
    scan=CollectorTests.scan
    rows=CollectorTests.rows
    tearDown=CollectorTests.tearDown
    def setUp(self):
        CollectorTests.setUp(self)
        self.write(event())
        self.write(event('child-event','child',50), self.logs/'parent/subagent/child/session.jsonl')
        self.write(event('grand-event','grand',30), self.logs/'parent/subagent/child/subagent/grand/session.jsonl')
        self.write(event('orphan-event','orphan',7,cached=None), self.logs/'gone/subagent/orphan/session.jsonl')
        self.write(event('other-event','other',5,cached=0), self.logs/'other/session.jsonl')
        self.scan()
    def test_sessions_roll_up_into_roots(self):
        rows={s['id']:s for s in sessions(self.db,{'period':'all'},self.rates)}
        # The orphan's parent has no usage, so it is listed as its own root.
        self.assertEqual(set(rows),{'parent','orphan','other'})
        self.assertEqual(rows['parent']['input'],180)
        self.assertEqual(rows['parent']['subagents'],2)
        self.assertEqual(rows['parent']['models'],['test-model'])
        self.assertEqual(rows['orphan']['cost_unknown_events'],1)
        self.assertEqual(sessions(self.db,{'period':'all','from':'2000-01-01','to':'2000-01-02'},self.rates),[])
        self.assertEqual(sum(s['input'] for s in rows.values()),totals(self.rows(),self.rates)['input'])
    def test_session_detail_tree(self):
        detail=session_detail(self.db,'parent',{'period':'all','project':'ignored'},self.rates)
        self.assertEqual(detail['totals']['input'],180)
        self.assertEqual(detail['own']['input'],100)
        tree=detail['tree']
        self.assertEqual([tree['id'],tree['children'][0]['id'],tree['children'][0]['children'][0]['id']],
                         ['parent','child','grand'])
        def node_sum(n):return n['input']+sum(node_sum(c) for c in n['children'])
        self.assertEqual(node_sum(tree),detail['totals']['input'])
        self.assertEqual(len(detail['events']),3)
        self.assertNotIn('source',detail['events'][0])
        self.assertEqual(detail['timeline_unit'],'hour')
        child=session_detail(self.db,'child',{'period':'all'},self.rates)
        self.assertEqual((child['parent'],child['root'],child['totals']['input']),('parent','parent',80))
        with self.assertRaises(NotFound):session_detail(self.db,'missing',{'period':'all'},self.rates)
    def test_subagents_inherit_root_project(self):
        self.db.execute("UPDATE events SET project='site' WHERE session='parent'")
        rows=query(self.db,{'period':'all','project':'site'})
        self.assertEqual({r['session'] for r in rows},{'parent','child','grand'})
        self.assertEqual(project_detail(self.db,'site',{'period':'all'},self.rates)['totals']['input'],180)
        self.assertEqual(query(self.db,{'period':'all','session':'orphan'})[0]['project'],'unassigned')
    def test_project_detail_matches_breakdown(self):
        listed={p['label']:p for p in projects(self.db,{'period':'all'},self.rates)}
        self.assertEqual(listed['unassigned']['sessions'],3)
        detail=project_detail(self.db,'unassigned',{'period':'all','model':'ignored'},self.rates)
        self.assertEqual(detail['totals']['input'],listed['unassigned']['input'])
        self.assertEqual(len(detail['sessions']),3)
        with self.assertRaises(NotFound):project_detail(self.db,'missing',{'period':'all'},self.rates)

class PricingAndAlertTests(unittest.TestCase):
    write=CollectorTests.write
    scan=CollectorTests.scan
    tearDown=CollectorTests.tearDown
    def setUp(self):
        CollectorTests.setUp(self)
        self.write(event())                                    # test-model: priced
        self.write(event('private-event','private',100,model='secret-model'),self.logs/'private/session.jsonl')
        self.scan()
    def rates_from(self,day):
        return [dict(self.rates[0],effective_from=day)]
    def test_back_pricing_is_assumed(self):
        from meta_cost_center.reports import estimate
        row=query(self.db,{'period':'all','model':'test-model'})[0]
        value,snapshot,assumed=estimate(row,self.rates_from('2999-01-01'))
        self.assertEqual((value,assumed),(Decimal('0.000102'),True))
        self.assertFalse(estimate(row,self.rates)[2])
        t=totals(query(self.db,{'period':'all'}),self.rates_from('2999-01-01'))
        self.assertEqual((t['cost_assumed_events'],t['cost_unknown_events']),(1,1))
        self.assertEqual(t['priced_tokens'],110)
    def test_local_rate_overrides_and_prices_unknown_models(self):
        from meta_cost_center import pricing
        from meta_cost_center.reports import with_local_rates
        pricing.set_local(self.db,'secret-model',{'input_per_million':'2','cached_input_per_million':'1','output_per_million':'4'})
        pricing.set_local(self.db,'test-model',{'input_per_million':'0','cached_input_per_million':'0','output_per_million':'0'})
        rates=with_local_rates(self.db,self.rates)
        t=totals(query(self.db,{'period':'all'}),rates)
        self.assertEqual(t['cost_unknown_events'],0)
        self.assertEqual(t['unknown_mode_equivalent_usd'],Decimal('0.00022'))
        rows={m['model']:m for m in pricing.table(self.db,rates)}
        self.assertEqual((rows['secret-model']['source'],rows['test-model']['source']),('local','local'))
        self.assertEqual(with_local_rates(self.db,rates).count(rates[-1]),1)  # merging twice does not duplicate
        pricing.delete_local(self.db,'secret-model')
        self.assertEqual(pricing.table(self.db,with_local_rates(self.db,self.rates))[0]['source'],'missing')
        with self.assertRaises(ValueError):pricing.set_local(self.db,'x',{'input_per_million':'-1'})
        with self.assertRaises(NotFound):pricing.delete_local(self.db,'secret-model')
    def test_alert_windows_scopes_and_levels(self):
        from datetime import date
        from meta_cost_center import alerts
        self.assertEqual(alerts.window('week',date(2026,10,24)),(date(2026,10,19),date(2026,10,24)))
        self.assertEqual(alerts.window('month',date(2026,10,24))[0],date(2026,10,1))
        self.db.execute("UPDATE events SET project='site'")
        self.db.commit()
        # The fixture spends far less than a cent, so these limits are stored directly, past validation.
        raw_alert(self.db,period='day',limit_usd='0.0001')                                   # spent 0.000102: exceeded
        raw_alert(self.db,period='week',project='site',limit_usd='0.00012',warn_levels='[50, 80, 90]')  # 85%: warning at 80
        alerts.create(self.db,{'period':'month','project':'other','limit_usd':'1'})         # nothing spent
        alerts.create(self.db,{'period':'month','billing_mode':'payg','limit_usd':'1','enabled':False})
        levels=[a['level'] for a in alerts.status(self.db,self.rates)]
        self.assertEqual(levels,['exceeded','warning','ok','paused'])
        statuses=alerts.status(self.db,self.rates)
        self.assertEqual((statuses[1]['warn_levels'],statuses[1]['warning_at']),([50,80,90],80))
        first=statuses[0]
        self.assertEqual((first['spent_usd'],first['unpriced_events']),(Decimal('0.000102'),1))
        for bad in ({'period':'year','limit_usd':'1'},{'period':'day','limit_usd':'0'},{'period':'day','limit_usd':'x'},
                    {'period':'day','limit_usd':'1','warn_levels':[100]},{'period':'day','limit_usd':'1','warn_levels':[]},
                    {'period':'day','limit_usd':'1','warn_levels':[10,20,30,40,50]},{'period':'day','limit_usd':'1','warn_levels':[50,50]},
                    {'period':'day','limit_usd':'1','billing_mode':'subscription'},
                    {'period':'day','limit_usd':'0.009'},{'period':'day','limit_usd':'0.015'}):
            with self.assertRaises(ValueError):alerts.validate(bad)
        alerts.delete(self.db,1)
        with self.assertRaises(NotFound):alerts.update(self.db,1,{'period':'day','limit_usd':'1'})
    def test_alert_key_revocation_settings(self):
        from meta_cost_center import alerts
        valid=alerts.validate({'period':'day','limit_usd':'1','revoke_keys':True,'key_match':' bootcamp-* , key1,key1 '})
        self.assertEqual((valid['revoke_keys'],valid['key_match']),(1,'bootcamp-*,key1'))
        # The pattern is dropped when revocation is off, so a stale pattern never revokes anything.
        self.assertIsNone(alerts.validate({'period':'day','limit_usd':'1','key_match':'*'})['key_match'])
        for bad in ('', ' , ', 'has space', 'semi;colon', ','.join(['k']*21)):
            with self.assertRaises(ValueError):alerts.validate({'period':'day','limit_usd':'1','revoke_keys':True,'key_match':bad})
        self.assertEqual(alerts.validate({'period':'day','limit_usd':'0.01'})['limit_usd'],'0.01')
        alert_id=raw_alert(self.db,period='day',limit_usd='0.0001',revoke_keys=1,key_match='*')
        status=next(a for a in alerts.status(self.db,self.rates) if a['id']==alert_id)
        self.assertEqual((status['revoke_keys'],status['key_match'],status['level']),(True,'*','exceeded'))
        alerts.update(self.db,alert_id,{'period':'day','limit_usd':'1'})
        status=next(a for a in alerts.status(self.db,self.rates) if a['id']==alert_id)
        self.assertEqual((status['revoke_keys'],status['key_match']),(False,None))
    def test_key_revocation_columns_migrate(self):
        self.db.executescript('''DROP TABLE alerts;
            CREATE TABLE alerts (id INTEGER PRIMARY KEY, period TEXT, project TEXT, billing_mode TEXT,
              limit_usd TEXT, warn_levels TEXT DEFAULT '[80]', enabled INTEGER DEFAULT 1, created_at TEXT);
            INSERT INTO alerts (period, limit_usd) VALUES ('day', '5');''')
        self.db.close()
        self.db=connect(self.root/'db.sqlite3')
        from meta_cost_center import alerts
        self.assertEqual(alerts.status(self.db,self.rates)[0]['revoke_keys'],False)
    def test_single_warning_column_migrates_to_levels(self):
        self.db.executescript('''DROP TABLE alerts;
            CREATE TABLE alerts (id INTEGER PRIMARY KEY, period TEXT, project TEXT, billing_mode TEXT,
              limit_usd TEXT, warn_percent INTEGER DEFAULT 80, enabled INTEGER DEFAULT 1, created_at TEXT);
            INSERT INTO alerts (period, limit_usd, warn_percent) VALUES ('day', '5', 70);''')
        self.db.close()
        self.db=connect(self.root/'db.sqlite3')
        from meta_cost_center import alerts
        self.assertEqual(alerts.status(self.db,self.rates)[0]['warn_levels'],[70])
        self.assertNotIn('warn_percent',{r[1] for r in self.db.execute('PRAGMA table_info(alerts)')})
    def test_monthly_warning_migrates_to_alert(self):
        self.db.execute("INSERT INTO settings VALUES ('monthly_usd','30')")
        self.db.execute("INSERT INTO settings VALUES ('alert_levels','[50, 80, 100]')")
        self.db.commit()
        self.db.close()
        self.db=connect(self.root/'db.sqlite3')
        alert=dict(self.db.execute('SELECT * FROM alerts').fetchone())
        self.assertEqual((alert['period'],alert['project'],alert['limit_usd'],alert['warn_levels']),('month',None,'30','[50, 80]'))
        self.assertIsNone(self.db.execute("SELECT 1 FROM settings WHERE key='monthly_usd'").fetchone())

if __name__=='__main__':unittest.main()
