"""Export only native lifecycle, tool counts and compaction accounting evidence."""
import collections
import json
from pathlib import Path
import zstandard

PRIVATE=Path('/mnt/data2/weizhiwei/magpie-auto/bench-runtime/trials-controlled-jev')


def collect(directory,task,arm,private_root=PRIVATE):
    sessions=private_root/task/arm/'sessions'
    if not sessions.exists():return None
    rows=[]
    for path in sessions.rglob('*.jsonl.zstd'):
        with path.open('rb') as source:
            records=[json.loads(line) for line in zstandard.ZstdDecompressor().stream_reader(source).read().splitlines()]
        header=records[0]
        events=records[1:]
        compactions=[];starts={}
        calls={};tools=collections.Counter();collections_seen=[]
        for event in events:
            kind,data=event['type'],event['data']
            if kind=='tool/call':calls[data['callId']]=data;tools[data['name']]+=1
            elif kind=='tool/result':
                message=data['message'];call_id=message['source']['callId'];call=calls.get(call_id)
                if call and call['name'] in ('subagent','subagent_fork','job_output'):
                    collections_seen.append({'callId':call_id,'tool':call['name'],
                        'arguments':call['arguments'],'time':event['time'],
                        'content':message['content']})
            elif kind=='compaction/start':starts[data['compactionId']]=event['time']
            elif kind in ('compaction/summary','compaction/end'):
                metadata={key:value for key,value in data.items() if key not in ('summary','rawOutput')}
                compactions.append({'type':kind,'time':event['time'],'startTime':starts[data['compactionId']],**metadata})
        rows.append({'session':header['id'],'tools':dict(tools),'compactions':compactions,'delegationResults':collections_seen})
    result={'source':'dsh native append-only session journal','sessions':rows}
    (directory/'native-accounting.json').write_text(json.dumps(result,ensure_ascii=False,indent=2))
    return result


def classify_compaction(call,native):
    if native is None:return 'unobserved'
    for session in native['sessions']:
        if session['session']!=call['session']:continue
        for event in session['compactions']:
            # Include failed summary calls whose transaction ended without a
            # summary event. Main execution resumes after this native boundary.
            if event['startTime']/1000<=call['startedUnix']<=event['time']/1000:
                return 'compaction'
    return call.get('purpose','unobserved')
