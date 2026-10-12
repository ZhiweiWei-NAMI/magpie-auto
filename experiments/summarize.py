"""Summarize actual request usage and official results without inventing missing counters."""
import argparse
import csv
import json
from pathlib import Path
from collect_native import PRIVATE,collect,classify_compaction

ROOT=Path(__file__).resolve().parent/'jev-cache-20261012'


def numeric(value):
    return value if type(value) in (int,float) else None


def execution_usage(call):
    usage=call.get('usage')
    if not isinstance(usage,dict):return {k:None for k in ('input','output','cache_hit','cache_miss','cache_write')}
    inp=numeric(usage.get('prompt_tokens'))
    out=numeric(usage.get('completion_tokens'))
    hit=numeric(usage.get('prompt_cache_hit_tokens'))
    if hit is None:
        details=usage.get('prompt_tokens_details')
        if isinstance(details,dict):hit=numeric(details.get('cached_tokens'))
    miss=numeric(usage.get('prompt_cache_miss_tokens'))
    # The bridge's write aliases are compatibility zeros. They do not establish
    # a native DeepSeek cache-write observation; retain them only in raw usage.
    return {'input':inp,'output':out,'cache_hit':hit,'cache_miss':miss,'cache_write':None}


def counter_sum(rows,key):
    observed=[r[key] for r in rows if r[key] is not None]
    return {'observed':sum(observed) if observed or not rows else None,
            'unobserved_requests':len(rows)-len(observed),'complete':len(observed)==len(rows)}


def main():
    global ROOT
    parser=argparse.ArgumentParser()
    parser.add_argument('--root',type=Path,default=ROOT)
    parser.add_argument('--trial-dir',type=Path,default=PRIVATE)
    parser.add_argument('--tasks-file',type=Path)
    args=parser.parse_args();ROOT=args.root
    tasks=json.loads((args.tasks_file or ROOT/'tasks.json').read_text())['tasks']
    comparisons=[];requests=[]
    for task in tasks:
        for arm in 'ABC':
            directory=ROOT/task['instance_id']/arm
            result_file=directory/'result.json'
            if not result_file.exists():continue
            result=json.loads(result_file.read_text())
            if 'executionStatus' not in result:continue
            captured=json.loads((directory/'requests.json').read_text()) if (directory/'requests.json').exists() else []
            calls=[call for call in captured if call.get('kind','execution')=='execution']
            jev_calls=[call for call in captured if call.get('kind')=='jev_proxy']
            native=collect(directory,task['instance_id'],arm,args.trial_dir)
            events=json.loads((directory/'decisions-and-children.json').read_text()) if (directory/'decisions-and-children.json').exists() else []
            roots={e['session'] for e in events if e['kind']=='root_start'}
            children={e['id'] for e in events if e['kind']=='child_start'}
            execution=[];jev=[];accounts={}
            for call in calls:
                counters=execution_usage(call);execution.append(counters)
                role='main' if call['session'] in roots else 'child' if call['session'] in children else 'unattributed'
                requests.append({'instance_id':task['instance_id'],'arm':arm,'model':'deepseek-v4.1-flash','role':role,
                    'actual_model':call.get('actualModel'),
                    'session':call['session'],'request_number':call['requestNumber'],'first_request':call['requestNumber']==1,
                    'purpose':classify_compaction(call,native),'http_status':call.get('httpStatus'),**counters,
                    'service_credit':numeric(call['usage'].get('credit')) if isinstance(call.get('usage'),dict) else None,
                    'elapsed_seconds':call.get('elapsedSeconds'),
                    'system_unchanged':call['systemUnchanged'],'tools_unchanged':call['toolsUnchanged'],
                    'append_only_history':call['appendOnlyMessages'],'execution_account':call.get('executionAccount'),
                    'account_changed':call['executionAccount']!=accounts[call['session']] if call.get('executionAccount') is not None and accounts.get(call['session']) is not None else None})
                accounts[call['session']]=call.get('executionAccount')
            # The observer sees every admitted Jev call, including one whose
            # client failed before writing its event. Never count it twice.
            jev_events=[dict(call.get('response',{}),session=call['session'],
                trigger=call['trigger'],httpStatus=call.get('httpStatus'),wallSeconds=call.get('elapsedSeconds')) for call in jev_calls]
            if not jev_calls:jev_events=[event for event in events if event['kind']=='jev']
            jev_ordinals={}
            for event in jev_events:
                value=event.get('usage')
                if value is None and isinstance(event.get('decision'),dict):value=event['decision'].get('usage')
                usage=value if isinstance(value,dict) else {}
                counters={'input':numeric(usage.get('input_tokens')),'output':numeric(usage.get('output_tokens')),
                          'cache_hit':None,'cache_miss':None,'cache_write':None}
                jev.append(counters)
                jev_ordinals[event['session']]=jev_ordinals.get(event['session'],0)+1
                ordinal=jev_ordinals[event['session']]
                requests.append({'instance_id':task['instance_id'],'arm':arm,'model':'jev-1.13-free','role':'jev',
                    'actual_model':event.get('model',event.get('decision',{}).get('model')),
                    'session':event['session'],'request_number':ordinal,'first_request':ordinal==1,'purpose':event['trigger'],
                    'http_status':event.get('httpStatus'),**counters,'service_credit':None,
                    'elapsed_seconds':event.get('wallSeconds',event.get('wallMs')/1000 if event.get('wallMs') is not None else None),
                    'system_unchanged':None,'tools_unchanged':None,'append_only_history':None,'execution_account':None,'account_changed':None})
            official_path=directory/'official-report.json'
            official=json.loads(official_path.read_text()) if official_path.exists() else None
            evaluation_path=directory/'evaluation.json'
            evaluation=json.loads(evaluation_path.read_text()) if evaluation_path.exists() else None
            own=[r for r in requests if r['instance_id']==task['instance_id'] and r['arm']==arm]
            result.update({'deepseekRequests':len(calls),'jevRequests':len(jev),
                'deepseekUsage':{key:counter_sum(execution,key) for key in ('input','output','cache_hit','cache_miss','cache_write')},
                'jevUsage':{key:counter_sum(jev,key) for key in ('input','output','cache_hit','cache_miss','cache_write')},
                'workloadInput':counter_sum(execution+jev,'input'),'workloadOutput':counter_sum(execution+jev,'output'),
                'modelRequestSeconds':sum(c['elapsedSeconds'] for c in calls if 'elapsedSeconds' in c),
                'jevRequestSeconds':sum(e.get('elapsedMs',e.get('decision',{}).get('elapsedMs'))/1000 for e in jev_events if numeric(e.get('elapsedMs',e.get('decision',{}).get('elapsedMs'))) is not None),
                'jevTimeUnobserved':sum(numeric(e.get('elapsedMs',e.get('decision',{}).get('elapsedMs'))) is None for e in jev_events),
                'jevProxySeconds':counter_sum([r for r in own if r['role']=='jev'],'elapsed_seconds'),
                'contextReplacements':sum(e['kind']=='context_replaced' for e in events),
                'contextKeepDecisions':sum(e['kind']=='context_kept' for e in events),
                'officialResolved':evaluation['resolved'] if evaluation is not None else official.get(task['instance_id'],{}).get('resolved') if official is not None else None,
                'officialEvaluation':evaluation,
                'requestBreakdown':{role:{'requests':sum(r['role']==role for r in own),
                    **{key:counter_sum([r for r in own if r['role']==role],key) for key in ('input','output','cache_hit','cache_miss')}} for role in ('main','child','jev','unattributed')},
                'executionPurposes':{purpose:{'requests':sum(r['role']!='jev' and r['purpose']==purpose for r in own),
                    **{key:counter_sum([r for r in own if r['role']!='jev' and r['purpose']==purpose],key) for key in ('input','output','cache_hit','cache_miss')}} for purpose in ('execution','compaction')},
                'decisionAPIAttempts':sum(e['kind']=='jev' for e in events),
                'childStopReasons':[{'id':e['id'],'reason':e['stopReason']} for e in events if e['kind']=='child_end'],
                'nativeCompactions':sum(len([e for e in s['compactions'] if e['type']=='compaction/summary']) for s in native['sessions']) if native is not None else None,
                'serviceCredits':counter_sum([r for r in own if r['role']!='jev'],'service_credit'),
                'priceInformation':'未提供可靠计费依据；不计算美元费用',
                'cacheWriteObservation':'不可观测；原始兼容字段保留，但不把兼容零值当作原生写缓存统计'})
            result['accountChangesObserved']=sum(r['account_changed'] is True for r in own)
            result['childrenCompletedObserved']=sum(e['kind']=='child_end' and e['stopReason']=='completed' for e in events)
            result['childEndEvents']=sum(e['kind']=='child_end' for e in events)
            if sum(e['kind']=='child_collected' for e in events)<result['childEndEvents'] and arm=='A':
                result['collectionObservation']='The old plugin per-item log stopped after the first failed outcome. Promise.all returned every outcome; this does not establish successful child results. Fixed in v0.2.2.'
            comparisons.append(result)
    (ROOT/'per-task.json').write_text(json.dumps(comparisons,ensure_ascii=False,indent=2))
    if requests:
        with (ROOT/'per-request.csv').open('w',newline='') as file:
            writer=csv.DictWriter(file,fieldnames=list(requests[0]),lineterminator='\n');writer.writeheader();writer.writerows(requests)
    summary={}
    for arm in 'ABC':
        group=[r for r in comparisons if r['arm']==arm]
        usage=[r for r in requests if r['arm']==arm]
        summary[arm]={'tasks':len(group),'officialResolved':sum(r['officialResolved'] is True for r in group),
            'ungraded':sum(r['officialResolved'] is None for r in group),
            'solveSecondsObserved':sum(r['solveAndExportSeconds'] for r in group if numeric(r.get('solveAndExportSeconds')) is not None),
            'solveSecondsUnobserved':sum(numeric(r.get('solveAndExportSeconds')) is None for r in group),
            'modelRequests':len(usage),
            'executionStatuses':{status:sum(r['executionStatus']==status for r in group) for status in sorted({r['executionStatus'] for r in group})},
            'childrenStarted':counter_sum([{'children':numeric(r.get('childrenStarted'))} for r in group],'children'),
            'childrenCollected':counter_sum([{'children':numeric(r.get('childrenCollected'))} for r in group],'children'),
            'childrenCompletedObserved':sum(r['childrenCompletedObserved'] for r in group),
            'maxChildConcurrencyObserved':max((r['maxChildConcurrency'] for r in group if numeric(r.get('maxChildConcurrency')) is not None),default=None),
            'childConcurrencyUnobservedTasks':sum(numeric(r.get('maxChildConcurrency')) is None for r in group),
            'deepseek':{key:counter_sum([u for u in usage if u['role']!='jev'],key) for key in ('input','output','cache_hit','cache_miss')},
            'jev':{key:counter_sum([u for u in usage if u['role']=='jev'],key) for key in ('input','output')},
            'workload':{key:counter_sum(usage,key) for key in ('input','output')},
            'contextReplacements':sum(r['contextReplacements'] for r in group),
            'firstAndLaterRequests':{label:{'requests':len(part),**{key:counter_sum(part,key) for key in ('input','output','cache_hit','cache_miss','elapsed_seconds')}}
                for label,part in [('first',[r for r in usage if r['role']!='jev' and r['first_request']]),
                                   ('later',[r for r in usage if r['role']!='jev' and not r['first_request']])]},
            'nonAppendRequests':sum(r['append_only_history'] is False for r in usage if r['role']!='jev'),
            'systemChanges':sum(r['system_unchanged'] is False for r in usage if r['role']!='jev'),
            'toolChanges':sum(r['tools_unchanged'] is False for r in usage if r['role']!='jev'),
            'compactionRequests':sum(r['purpose']=='compaction' for r in usage if r['role']!='jev')}
        summary[arm]['requestSeconds']={role:counter_sum([r for r in usage if r['role']==role],'elapsed_seconds') for role in ('main','child','jev','unattributed')}
        summary[arm]['roles']={role:{'requests':len(part),**{key:counter_sum(part,key) for key in ('input','output','cache_hit','cache_miss')}}
            for role,part in [(role,[r for r in usage if r['role']==role]) for role in ('main','child','jev','unattributed')]}
        summary[arm]['actualModels']={model:sum(r['actual_model']==model for r in usage) for model in sorted({r['actual_model'] for r in usage if r['actual_model'] is not None})}
        summary[arm]['actualModelUnobservedRequests']=sum(r['actual_model'] is None for r in usage)
        summary[arm]['jevDecisionSecondsObserved']=sum(r['jevRequestSeconds'] for r in group)
        summary[arm]['jevDecisionTimeUnobserved']=sum(r['jevTimeUnobserved'] for r in group)
        summary[arm]['accountChangesObserved']=sum(r['account_changed'] is True for r in usage)
        summary[arm]['prefixByPurpose']={purpose:{'requests':len(part),
            'nonAppend':sum(r['append_only_history'] is False for r in part),
            'systemChanges':sum(r['system_unchanged'] is False for r in part),
            'toolChanges':sum(r['tools_unchanged'] is False for r in part)}
            for purpose,part in [(p,[r for r in usage if r['role']!='jev' and r['purpose']==p]) for p in ('execution','compaction')]}
    (ROOT/'summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2))
    matched=[task['instance_id'] for task in tasks if len([r for r in comparisons if r['instance_id']==task['instance_id']])==3 and
             all(r['officialResolved'] is True for r in comparisons if r['instance_id']==task['instance_id'])]
    quality_matched={'selection':'Tasks officially resolved by all three groups; full-group failures remain in summary.json.',
                     'tasks':matched,'groups':{}}
    for arm in 'ABC':
        group=[r for r in comparisons if r['arm']==arm and r['instance_id'] in matched]
        usage=[r for r in requests if r['arm']==arm and r['instance_id'] in matched]
        quality_matched['groups'][arm]={'tasks':len(group),'solveSeconds':sum(r['solveAndExportSeconds'] for r in group),
            'modelRequests':len(usage),'childrenStarted':sum(r['childrenStarted'] for r in group),
            'deepseek':{key:counter_sum([r for r in usage if r['role']!='jev'],key) for key in ('input','output','cache_hit','cache_miss')},
            'jev':{key:counter_sum([r for r in usage if r['role']=='jev'],key) for key in ('input','output')},
            'jevProxySeconds':counter_sum([r for r in usage if r['role']=='jev'],'elapsed_seconds')}
    (ROOT/'quality-matched.json').write_text(json.dumps(quality_matched,ensure_ascii=False,indent=2))
    print(json.dumps(summary,ensure_ascii=False,indent=2))


if __name__=='__main__':main()
