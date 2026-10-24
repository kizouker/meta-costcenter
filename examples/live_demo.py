#!/usr/bin/env python3
"""Write synthetic usage every two seconds. No model calls, credentials, or charges."""
import json
import sys
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path

root=Path(sys.argv[1] if len(sys.argv)>1 else '/tmp/muse-cost-demo')
path=root/'demo-session/session.jsonl'
path.parent.mkdir(parents=True,exist_ok=True)
with path.open('a') as f:
    f.write(json.dumps({'payload_type':'runtime.session.metadata','payload':{'record':{
        'workspace_root':'/demo/sample-project','model_id':'muse-spark-1.3'}}})+'\n')
print(f'Synthetic logs: {root}\nIn another terminal run:\n'
      f'./muse-cost show --source {root} --db {root}/demo.sqlite3 --interval 1',flush=True)
try:
    while True:
        stamp=datetime.now(timezone.utc)
        record={'id':str(uuid.uuid4()),'stream':{'id':'demo-session'},
                'recorded_at':stamp.isoformat(),'payload':{'event':{
                'kind':'model_completed','model':'muse-spark-1.3',
                'usage':{'input_tokens':1200,'output_tokens':80,'cached_tokens':900}}}}
        with path.open('a') as f:f.write(json.dumps(record)+'\n')
        print(f'{stamp.isoformat()} +1,200 input / +80 output',flush=True)
        time.sleep(2)
except KeyboardInterrupt:
    pass
