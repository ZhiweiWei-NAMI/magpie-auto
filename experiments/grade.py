"""Grade actual exported patches with the official CPU SWE-bench harness."""
import argparse
import concurrent.futures
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

BASE=Path(__file__).resolve().parent/'jev-cache-20261012'
PRIVATE=Path('/mnt/data2/weizhiwei/magpie-auto/bench-runtime/evaluator')


def evaluate(directory,label):
    result=json.loads((directory/'result.json').read_text())
    instance,arm=result['instance_id'],result['arm']
    run_id=f"jev20261012-{label}-{arm}-{instance.rsplit('-',1)[-1]}"
    model=f'magpie-{arm}-deepseek-v4.1-flash'
    work=PRIVATE/'grading'/run_id;work.mkdir(parents=True,exist_ok=True)
    prediction=work/'prediction.jsonl'
    patch=(directory/'patch.diff').read_text() if (directory/'patch.diff').exists() else None
    prediction.write_text(json.dumps({'instance_id':instance,'model_name_or_path':model,'model_patch':patch})+'\n')
    env=os.environ.copy();env.update(CUDA_VISIBLE_DEVICES='',USE_TF='0')
    started=time.perf_counter()
    with (work/'harness.log').open('w') as log:
        process=subprocess.run([sys.executable,'-m','swebench.harness.run_evaluation',
            '--dataset_name',str(PRIVATE/'verified-subset.json'),'--predictions_path',str(prediction),
            '--instance_ids',instance,'--run_id',run_id,'--max_workers','1','--namespace','swebench',
            '--cache_level','instance','--timeout','600'],cwd=work,env=env,stdout=log,stderr=subprocess.STDOUT)
    elapsed=round(time.perf_counter()-started,4)
    report=work/'logs/run_evaluation'/run_id/model/instance/'report.json'
    summary_file=work/f'{model}.{run_id}.json'
    summary=json.loads(summary_file.read_text()) if summary_file.exists() else None
    metadata={'harnessVersion':'v4.1.0','runId':run_id,'exitCode':process.returncode,
              'evaluationSeconds':elapsed,'resolved':None,'status':'harness_error'}
    if report.exists():
        shutil.copyfile(report,directory/'official-report.json')
        metadata.update(resolved=json.loads(report.read_text())[instance]['resolved'],status='official_report')
    elif summary is not None:
        if instance in summary['empty_patch_ids']:metadata.update(resolved=False,status='official_empty_patch')
        elif instance in summary['error_ids']:metadata.update(resolved=False,status='official_evaluation_error')
        elif instance in summary['unresolved_ids']:metadata.update(resolved=False,status='official_unresolved')
        elif instance in summary['resolved_ids']:metadata.update(resolved=True,status='official_resolved')
    # The per-instance report is sufficient when present. Keep the official
    # summary for empty patches and cases without that report.
    if summary is not None and not report.exists():
        (directory/'official-summary.json').write_text(json.dumps(summary,indent=2))
    (directory/'evaluation.json').write_text(json.dumps(metadata,indent=2))
    print(json.dumps({'instance_id':instance,'arm':arm,**metadata}),flush=True)
    return metadata


def main():
    parser=argparse.ArgumentParser();parser.add_argument('--root',type=Path,default=BASE)
    parser.add_argument('--tasks-file',type=Path)
    parser.add_argument('--label',default='v02');parser.add_argument('--watch',action='store_true')
    args=parser.parse_args();tasks=json.loads((args.tasks_file or args.root/'tasks.json').read_text())['tasks']
    submitted=set();pending={}
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        while True:
            for task in tasks:
                for arm in 'ABC':
                    directory=args.root/task['instance_id']/arm
                    if directory in submitted:continue
                    path=directory/'result.json'
                    if not path.exists():continue
                    try:result=json.loads(path.read_text())
                    except json.JSONDecodeError:continue # runner is publishing a new result
                    if 'executionStatus' not in result:continue
                    submitted.add(directory)
                    if (directory/'evaluation.json').exists():continue
                    pending[pool.submit(evaluate,directory,args.label)]=directory
            for future in list(pending):
                if future.done():future.result();del pending[future]
            if not args.watch and not pending:break
            if args.watch and len(submitted)==3*len(tasks) and not pending:break
            time.sleep(5)


if __name__=='__main__':main()
