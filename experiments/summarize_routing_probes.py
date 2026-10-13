"""Account every observed development probe; keep failures separate from benchmark results."""
import csv
import json
import statistics
from collections import Counter
from pathlib import Path

from summarize import execution_usage, counter_sum, numeric

ROOT=Path(__file__).resolve().parent/'jev-routing-revision'


def main():
    cases=[];requests=[]
    for directory in sorted(ROOT.glob('native-continuation*')):
        result=json.loads((directory/'result.json').read_text())
        request_file=directory/'requests.json'
        event_file=directory/'events.json'
        calls=json.loads(request_file.read_text()) if request_file.exists() else None
        events=json.loads(event_file.read_text()) if event_file.exists() else None
        children={e['id'] for e in events if e['kind']=='child_start'} if events is not None else None
        roots={e['session'] for e in events if e['kind']=='root_start'} if events is not None else None
        own=[]
        for call in calls or []:
            is_jev=call['kind']=='jev_proxy'
            if is_jev:
                response=call.get('response',{});decision=response.get('decision',response)
                usage=decision.get('usage')
                counters={k:None for k in ('input','output','cache_hit','cache_miss','cache_write')}
                if isinstance(usage,dict):counters.update(input=numeric(usage.get('input_tokens')),output=numeric(usage.get('output_tokens')))
                role='jev';model=decision.get('model')
            else:
                counters=execution_usage(call);model=call.get('actualModel')
                role='child' if children is not None and call['session'] in children else 'main' if roots is not None and call['session'] in roots else 'unattributed'
            row={'case':directory.name,'arm':result.get('arm','A'),'session':call['session'],'role':role,
                'actual_model':model,'http_status':call.get('httpStatus'),'request_number':call.get('requestNumber'),
                'first_execution_request':call.get('requestNumber')==1 if not is_jev else None,
                **counters,'service_credit':numeric(call['usage'].get('credit')) if not is_jev and isinstance(call.get('usage'),dict) else None,
                'elapsed_seconds':call.get('elapsedSeconds'),'system_unchanged':call.get('systemUnchanged'),
                'tools_unchanged':call.get('toolsUnchanged'),'append_only_history':call.get('appendOnlyMessages')}
            own.append(row);requests.append(row)
        cases.append({**result,'case':directory.name,'requestObservationAvailable':calls is not None,
            'roles':{role:{'requests':sum(r['role']==role for r in own),
                **{key:counter_sum([r for r in own if r['role']==role],key) for key in ('input','output','cache_hit','cache_miss','elapsed_seconds')}} for role in ('main','child','jev','unattributed')} if calls is not None else None,
            'distinctChildren':len(children) if children is not None else None,
            'eventCounts':dict(Counter(e['kind'] for e in events)) if events is not None else None})
    config=json.loads((ROOT/'configuration-probes.json').read_text())
    config_calls=[{'input':x['strategy']['decision'].get('usage',{}).get('input_tokens'),
        'output':x['strategy']['decision'].get('usage',{}).get('output_tokens'),'elapsed_seconds':x['wallSeconds']} for x in config]
    repeat_decisions=json.loads((ROOT/'decision-repeats.json').read_text())
    direct_calls=[{'input':x['response'].get('usage',{}).get('input_tokens'),
        'output':x['response'].get('usage',{}).get('output_tokens'),'elapsed_seconds':x['wallSeconds']} for x in repeat_decisions]
    all_observed=[{'input':r['input'],'output':r['output'],'elapsed_seconds':r['elapsed_seconds']} for r in requests]+config_calls+direct_calls
    summary={'scope':'Development / short probes, fixed decision repeats and configuration only; SWE-bench diagnostic is accounted separately. Failed probes included.',
        'cases':cases,'configuration':{'requests':len(config_calls),**{k:counter_sum(config_calls,k) for k in ('input','output','elapsed_seconds')}},
        'fixedDecisionRepeats':{'requests':len(direct_calls),**{k:counter_sum(direct_calls,k) for k in ('input','output','elapsed_seconds')}},
        'observedAllModelsIncludingConfiguration':{'requests':len(all_observed),**{k:counter_sum(all_observed,k) for k in ('input','output','elapsed_seconds')}},
        'unobservedCases':[c['case'] for c in cases if not c['requestObservationAvailable']],
        'executionServiceCredit':counter_sum([r for r in requests if r['role']!='jev'],'service_credit'),
        'pricing':'No reliable price/billing basis; Jev cache and cache writes are unobservable.'}
    repeats=[c for c in cases if c['case'].startswith('native-continuation-short-')]
    summary['M6_shortRepeats']={}
    for arm in 'ABC':
        group=[c for c in repeats if c.get('arm')==arm]
        if not group:continue
        durations=[c['solveSeconds'] for c in group]
        summary['M6_shortRepeats'][arm]={'runs':len(group),'testsPassed':sum(c.get('testExitCode')==0 for c in group),
            'medianSeconds':statistics.median(durations),'minSeconds':min(durations),'maxSeconds':max(durations),
            'requests':sum(c['roles'][role]['requests'] for c in group for role in ('main','child','jev','unattributed')),
            'jevRequests':sum(c['roles']['jev']['requests'] for c in group),
            'usage':{key:counter_sum([r for r in requests if r['case'] in {c['case'] for c in group}],key) for key in ('input','output','cache_hit','cache_miss')},
            'perRun':[{'case':c['case'],'seconds':c['solveSeconds'],
                **{key:counter_sum([r for r in requests if r['case']==c['case']],key) for key in ('input','output','cache_hit','cache_miss')}} for c in group],
            'interpretation':'Three repetitions do not establish a reliable P95 or statistical superiority.'}
    (ROOT/'probe-summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
    if requests:
        with (ROOT/'probe-requests.csv').open('w',newline='') as file:
            writer=csv.DictWriter(file,fieldnames=list(requests[0]),lineterminator='\n');writer.writeheader();writer.writerows(requests)
    print(json.dumps({k:v for k,v in summary.items() if k!='cases'},ensure_ascii=False,indent=2))


if __name__=='__main__':main()
