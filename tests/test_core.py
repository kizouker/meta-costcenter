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

if __name__=='__main__':unittest.main()
