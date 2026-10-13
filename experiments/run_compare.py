"""Run fixed A/B/C tasks with native dsh in clean CPU containers, then grade with SWE-bench."""
import argparse
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

from observer import Observer
from local_relay import LocalRelay

DEPLOY = Path('/mnt/data2/weizhiwei/magpie-auto')
RUNTIME = DEPLOY / 'bench-runtime'
REPO = Path(__file__).resolve().parents[1]
RESULTS = REPO / 'experiments/jev-cache-20261012'
MANIFEST = RESULTS
TRIALS = RUNTIME / 'trials-controlled-jev'
NODE = Path('/mnt/data2/weizhiwei/gemini-fullstack-langgraph-quickstart/node-v22.16.0-linux-x64')
EXTENSION = RUNTIME / 'dsh-extension'
DSH = NODE / 'lib/node_modules/@deepseek-ai/dsh/lib/bin.js'
PRIVATE_DATASET = RUNTIME / 'evaluator/verified-subset.json'
MODEL = 'dsh-workbuddy/deepseek-v4.1-flash'
TASK_SECONDS = 600
REQUEST_LIMIT = 64
MODEL_BASE = 'http://127.0.0.1:3435/v1'
DECISION_ENDPOINT = 'http://127.0.0.1:3437/v1/auto/decide'
AUTO_PROMPT = 'Magpie Auto selects collaboration; use the supplied child results. '


def configure_home(home, arm):
    import yaml
    home.mkdir(parents=True, exist_ok=True)
    settings = {'agent-default-model':{'provider':'deepseek-official','model':MODEL,'reasoningEffort':'off'},
                'llm-deepseek':{'baseURL':MODEL_BASE,'apiKeyEnv':'DEEPSEEK_API_KEY','thinking':'disabled',
                               'reasoningEffort':'off','maxTokens':16384,'models':[{'id':MODEL,'name':'DeepSeek through Magpie','contextWindow':128000,'maxTokens':16384}]}}
    (home/'auto-events.jsonl').touch(mode=0o600)
    (home/'settings.yaml').write_text(yaml.safe_dump(settings,sort_keys=False))
    mode = {'A':'auto','B':'single','C':'native'}[arm]
    patch = [{'id':id,'disabled':True} for id in ['session-title-llm','tool-workflow','tool-ralph','tool-web','web-search-deepseek','repeat-tool-reminder','llm-retry']]
    for id, provider, tool in [('tool-subagent','spawn','subagent'),('tool-subagent-fork','fork','subagent_fork')]:
        patch.append({'id':id,'config':{'provider':provider,'toolName':tool,'backgroundMode':'one-shot','maxDepth':1,
                                      'agentOptions':{'provider':'deepseek-official','model':MODEL}}})
    patch.append({'id':'compaction-basic','disabled':arm=='A','config':{'thresholdRatio':0.1875,'retainTokens':8000,'compactionRetries':0,'maxOverflowRetries':0}})
    patch.append({'insert':[{'id':'magpie-auto','name':str(EXTENSION/'index.js'),'config':{
        'mode':mode,'endpoint':DECISION_ENDPOINT,'maxChildren':2,'maxConcurrent':2,
        'pressureTokens':24000,'retainTokens':8000,'recordFile':str(home/'auto-events.jsonl')}}]})
    (home/'cordis.patch.yml').write_text(yaml.safe_dump(patch,sort_keys=False))


def events(home):
    path=home/'auto-events.jsonl'
    return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []


def with_observer_provider():
    # Only this deployment's existing local connection is selected; no credentials are copied.
    path=DEPLOY/'bin/data/providers.json'
    data=json.loads(path.read_text())
    for provider in data['providers']:
        if provider['id']=='dsh-workbuddy':
            previous=provider['chat']
            provider['chat']='http://127.0.0.1:3437/v1'
            path.write_text(json.dumps(data));path.chmod(0o600)
            return previous
    raise RuntimeError('Existing dsh-workbuddy connection missing')


def restore_provider(previous):
    path=DEPLOY/'bin/data/providers.json';data=json.loads(path.read_text())
    for provider in data['providers']:
        if provider['id']=='dsh-workbuddy':provider['chat']=previous
    path.write_text(json.dumps(data));path.chmod(0o600)


def run_case(client, observer, task, arm, image, preparation_seconds, network):
    case=RESULTS/task['instance_id']/arm;case.mkdir(parents=True,exist_ok=False)
    home=TRIALS/task['instance_id']/arm
    if home.exists():raise RuntimeError('A clean, new native task home is required')
    configure_home(home,arm)
    observer.output=case/'requests.json';observer.begin(task['instance_id'],arm)
    prompt='Resolve this issue in the repository. Use real tools to inspect and edit code, and run appropriate tests. '
    if arm=='B':prompt+='This run permits only the main agent; do not delegate. '
    elif arm=='C':prompt+='Native subagents are optional; decide whether they help and collect their results. '
    else:prompt+=AUTO_PROMPT
    prompt+='Do not read evaluation answers, later releases, fixed upstream tests or reference patches. Only the initial commit and local model service are available. Complete the code fix and report what was tested.\n\n'+task['problem_statement']
    volumes={str(NODE):{'bind':str(NODE),'mode':'ro'},str(EXTENSION):{'bind':str(EXTENSION),'mode':'ro'},str(home):{'bind':str(home),'mode':'rw'}}
    container=client.containers.run(image,command=['sleep','infinity'],detach=True,network=network.name,runtime='runc',
        nano_cpus=4_000_000_000,mem_limit='8g',working_dir='/testbed',volumes=volumes)
    outcome={'instance_id':task['instance_id'],'arm':arm,'image':image,'sharedImagePreparationSeconds':preparation_seconds}
    (case/'result.json').write_text(json.dumps(outcome,indent=2))
    case_started=time.perf_counter()
    try:
        reset=container.exec_run(['git','reset','--hard',task['base_commit']],workdir='/testbed')
        if reset.exit_code:raise RuntimeError('Could not reset clean task repository to base_commit')
        clean=container.exec_run(['git','clean','-fd'],workdir='/testbed')
        if clean.exit_code:raise RuntimeError('Could not clean task repository')
        # Retain exactly the original base commit/tree; discard other refs,
        # unreachable future objects and reflogs in this disposable container.
        clone=container.exec_run(['git','clone','--no-local','--no-tags','--depth','1','--no-checkout',
                                  'file:///testbed','/tmp/magpie-base'],workdir='/testbed')
        if clone.exit_code:raise RuntimeError('Could not isolate the initial Git objects')
        replace=container.exec_run(['python','-c',"import shutil; shutil.rmtree('/testbed/.git'); shutil.move('/tmp/magpie-base/.git','/testbed/.git'); shutil.rmtree('/tmp/magpie-base')"],workdir='/testbed')
        if replace.exit_code:raise RuntimeError('Could not install the isolated Git metadata')
        index=container.exec_run(['git','reset','--mixed',task['base_commit']],workdir='/testbed')
        commits=container.exec_run(['git','rev-list','--all'],workdir='/testbed')
        if index.exit_code or commits.exit_code or commits.output.decode().strip()!=task['base_commit']:
            raise RuntimeError('Isolated repository does not contain exactly base_commit')
        outcome['taskEnvironmentPreparationSeconds']=round(time.perf_counter()-case_started,4)
        case_started=time.perf_counter()
        command=['docker','exec','-w','/testbed',container.id,'env',f'DSH_HOME={home}','DEEPSEEK_API_KEY=magpie',
                 'DSH_PERMISSION_MODE=danger-full-access','DSH_DISABLE_TELEMETRY=1','CUDA_VISIBLE_DEVICES=',
                 f'PATH={NODE}/bin:/opt/miniconda3/envs/testbed/bin:/opt/miniconda3/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin',
                 str(NODE/'bin/node'),str(DSH),'--profile','headless',prompt]
        started=time.perf_counter()
        with (home/'agent-output.txt').open('w') as out:
            try:
                result=subprocess.run(command,stdout=out,stderr=subprocess.STDOUT,timeout=TASK_SECONDS)
                outcome['agentExitCode']=result.returncode
                outcome['executionStatus']='finished' if result.returncode==0 else 'agent_error'
            except subprocess.TimeoutExpired:
                outcome['executionStatus']='timeout'
        outcome['executionSeconds']=round(time.perf_counter()-started,4)
        # Include newly created files in the prediction while keeping all evaluation answers private.
        container.exec_run(['git','add','-N','.'],workdir='/testbed')
        diff=container.exec_run(['git','diff','--binary'],workdir='/testbed')
        if diff.exit_code:raise RuntimeError('Could not export actual code patch')
        patch=diff.output.decode('utf-8')
        (case/'patch.diff').write_text(patch)
        outcome['patchBytes']=len(diff.output)
    finally:
        container.exec_run(['chown','-R',f'{os.getuid()}:{os.getgid()}',str(home)])
        container.remove(force=True)
        time.sleep(.1)
        outcome['solveAndExportSeconds']=round(time.perf_counter()-case_started,4)
        observer.flush()
        lifecycle=events(home)
        (case/'decisions-and-children.json').write_text(json.dumps(lifecycle,indent=2))
        outcome['childrenStarted']=len({r['id'] for r in lifecycle if r['kind']=='child_start'})
        outcome['childActivations']=sum(r['kind']=='child_start' for r in lifecycle)
        collections=[r for r in lifecycle if r['kind']=='child_collected']
        outcome['childrenCollected']=len(collections) if collections else sum(
            r['kind']=='native_child_tool_result' and not r['isError'] and r['value']['kind']=='foreground' for r in lifecycle)
        outcome['maxChildConcurrency']=max((r['active'] for r in lifecycle if 'active' in r),default=0)
        (case/'result.json').write_text(json.dumps(outcome,indent=2))
    print(json.dumps(outcome),flush=True)
    return {'instance_id':task['instance_id'],'model_name_or_path':f'magpie-{arm}-deepseek-v4.1-flash','model_patch':patch}


def main():
    global RESULTS,TRIALS,MODEL_BASE,DECISION_ENDPOINT
    import docker
    from swebench.harness.test_spec.test_spec import make_test_spec
    parser=argparse.ArgumentParser();parser.add_argument('--limit',type=int,default=12)
    parser.add_argument('--results-dir',type=Path,default=RESULTS)
    parser.add_argument('--trial-dir',type=Path,default=TRIALS);args=parser.parse_args()
    RESULTS,TRIALS=args.results_dir,args.trial_dir
    RESULTS.mkdir(parents=True,exist_ok=True)
    if RESULTS.resolve()!=MANIFEST.resolve():
        shutil.copyfile(MANIFEST/'tasks.json',RESULTS/'tasks.json')
        shutil.copyfile(MANIFEST/'run-config.json',RESULTS/'run-config.json')
    public=json.loads((MANIFEST/'tasks.json').read_text())['tasks'][:args.limit]
    private={r['instance_id']:r for r in json.loads(PRIVATE_DATASET.read_text())}
    client=docker.from_env()
    network=client.networks.create('magpie-jev-controlled-20261012',driver='bridge',internal=True)
    network.reload();gateway=network.attrs['IPAM']['Config'][0]['Gateway']
    MODEL_BASE=f'http://{gateway}:3435/v1'
    DECISION_ENDPOINT=f'http://{gateway}:3437/v1/auto/decide'
    observer=Observer(RESULTS/'inflight-requests.json',REQUEST_LIMIT);observer.start()
    relays=[LocalRelay(gateway,port) for port in (3435,3437)]
    (RESULTS/'solver-isolation.json').write_text(json.dumps({'network':network.name,'internal':True,
        'model_base':MODEL_BASE,'decision_endpoint':DECISION_ENDPOINT,'git':'Only base_commit/tree objects; no other refs or reflog'},indent=2)+'\n')
    previous=with_observer_provider()
    predictions={arm:[] for arm in 'ABC'}
    order=[]
    try:
        for index, task in enumerate(public):
            image=make_test_spec(private[task['instance_id']],namespace='swebench').instance_image_key
            start=time.perf_counter()
            if not client.images.list(name=image):
                subprocess.run([sys.executable,str(Path(__file__).parent/'pull_oci.py'),image],check=True)
            preparation=round(time.perf_counter()-start,4)
            rotation='ABC'[index%3:]+'ABC'[:index%3]
            for arm in rotation:
                order.append({'instance_id':task['instance_id'],'arm':arm})
                (RESULTS/'execution-order.json').write_text(json.dumps(order,indent=2))
                prediction=run_case(client,observer,task,arm,image,preparation,network)
                predictions[arm].append(prediction)
                (RESULTS/f'predictions-{arm}.jsonl').write_text(''.join(json.dumps(r)+'\n' for r in predictions[arm]))
    finally:
        restore_provider(previous);observer.close()
        for relay in relays:relay.close()
        network.remove()


if __name__=='__main__':main()
