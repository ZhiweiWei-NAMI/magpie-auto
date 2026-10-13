"""Reproduce a small real native workflow (reuse, context, or short-N-A/B/C).
Requires the existing dedicated server configuration; uses no account credentials.
Run sequentially with other observed experiments because port 3437 is shared.
"""
import sys,json,os,subprocess,time,signal,shutil
from pathlib import Path
import run_compare as common
from observer import Observer
import yaml
base=Path('/mnt/data2/weizhiwei/magpie-auto/bench-runtime/routing-revision');out=Path(__file__).resolve().parent/'jev-routing-revision/native-continuation'
case=sys.argv[1] if len(sys.argv)>1 else 'current'
arm=sys.argv[2] if len(sys.argv)>2 else 'A'
if case!='initial':
 base=base/case;out=out.parent/('native-continuation-'+case)
base.mkdir(exist_ok=True);out.mkdir(parents=True,exist_ok=False)
workspace=base/'native-work';workspace.mkdir(exist_ok=False)
home=common.RUNTIME/'routing-revision/install-home' if case=='reuse-installed' else base/'native-home'
common.configure_home(home,arm)
settings=yaml.safe_load((home/'settings.yaml').read_text());settings['agent-default-model']['reasoningEffort']='high';settings['llm-deepseek'].update(thinking='enabled',reasoningEffort='high');(home/'settings.yaml').write_text(yaml.safe_dump(settings,sort_keys=False))
patch=yaml.safe_load((home/'cordis.patch.yml').read_text())
for item in patch:
 if item.get('id') in ('tool-subagent','tool-subagent-fork'):item['config']['backgroundMode']='continuable';item['config']['agentOptions']['reasoningEffort']='high'
 for plugin in item.get('insert',[]):
  if plugin.get('id')=='magpie-auto':
   plugin['config'].update(maxChildRequests=16,maxChildTurnRequests=8,maxDecisions=12,pressureTokens=64000,nativeThresholdRatio=.8)
   if case=='reuse-installed':plugin['name']='dsh-auto'
(home/'cordis.patch.yml').write_text(yaml.safe_dump(patch,sort_keys=False))
(workspace/'window.py').write_text('def parse_window(text):\n    raise NotImplementedError\n')
(workspace/'labels.py').write_text('def normalize_label(text):\n    raise NotImplementedError\n')
(workspace/'test_ops.py').write_text('''import unittest
from window import parse_window
from labels import normalize_label
class Checks(unittest.TestCase):
 def test_window(self):
  self.assertEqual(parse_window('2:5'),(2,5));self.assertEqual(parse_window(' 7 '),(7,7))
 def test_invalid(self):
  for value in ['5:2','-1','1:2:3','x']:
   with self.assertRaises(ValueError):parse_window(value)
 def test_label(self):
  self.assertEqual(normalize_label(' A  B '),'a-b');self.assertEqual(normalize_label(''),'')
''')
prompt='''Complete this real coding task using the native subagent tools. Delegate window.py to one implementation child in the background: parse_window accepts an ASCII nonnegative integer n as (n,n), or a:b when a <= b; trim outer whitespace and raise ValueError for invalid inputs. While it runs, implement normalize_label in labels.py yourself: trim, lowercase, join whitespace runs with a hyphen. Collect the child result. Then propose a follow-up through subagent for the same window.py responsibility: reject boolean and non-string inputs with ValueError and add those regression tests. Reuse its conversation if Auto selects that route. Do not edit the child's owned file while it works. Wait for its actual result, run python -m unittest -v, and finish with the code and test result. This validates actual native continuation, so perform both phases. The task budget is 64 total model requests; finish promptly.'''
if case.startswith('reuse'):
 prompt += ' For phase two, invoke subagent with the follow-up proposal; do not bypass the Jev reuse decision by sending a direct message. This requirement applies only to this integration probe.'
if case.startswith('short'):
 prompt = 'Inspect window.py, labels.py and test_ops.py, implement both stubs and run python -m unittest -v. parse_window accepts ASCII nonnegative n or ordered a:b, trimming outer whitespace; invalid values including non-strings raise ValueError. normalize_label trims, lowercases and joins whitespace runs with a hyphen. Complete this small coding task promptly.'
 with (workspace/'test_ops.py').open('a') as file:
  file.write(''' def test_ascii_and_type_boundaries(self):
  self.assertEqual(parse_window('00:02'),(0,2))
  for value in ['', '  ', '٢', True, None, 3]:
   with self.assertRaises(ValueError):parse_window(value)
 def test_tabs(self):
  self.assertEqual(normalize_label(' A\\t  B '),'a-b')
''')
if case.startswith('context'):
 (workspace/'archive.txt').write_text('Old unrelated catalogue entries, superseded before this task.\n'+''.join(f'Archived stock reference ITEM{i:04d}: blue paper boxes, obsolete shelf counts.\n' for i in range(900)))
 for item in patch:
  for plugin in item.get('insert',[]):
   if plugin.get('id')=='magpie-auto':plugin['config'].update(pressureTokens=4096,retainTokens=1024)
 (home/'cordis.patch.yml').write_text(yaml.safe_dump(patch,sort_keys=False))
 prompt='Read archive.txt once with bash cat; it is an unrelated historical input retained for this context-management check. Then inspect the two stub modules and test_ops.py and fix both yourself. parse_window accepts ASCII nonnegative n or ordered a:b, trimming surrounding whitespace; invalid values raise ValueError. normalize_label trims, lowercases and joins runs of whitespace with a hyphen. Do not delegate this small task. Run python -m unittest -v and report the result. The final requirement is important: tabs and multiple spaces must both normalize to one hyphen. The archived catalogue is not a source of requirements and should not influence the code.'
(out/'task.txt').write_text(prompt+'\n')
(out/'run-config.json').write_text(json.dumps({'settings':settings,'plugins':patch},ensure_ascii=False,indent=2)+'\n')
observer=Observer(out/'requests.json',64);observer.begin(case,arm);observer.start();previous=common.with_observer_provider()
started=time.perf_counter();result={'purpose':'Native tool integration / short repeated task; separate from SWE-bench','case':case,'arm':arm,'configuredModel':common.MODEL,'timeoutSeconds':240}
try:
 env=os.environ.copy();env.update(DSH_HOME=str(home),DEEPSEEK_API_KEY='magpie',DSH_PERMISSION_MODE='danger-full-access',DSH_DISABLE_TELEMETRY='1',CUDA_VISIBLE_DEVICES='',PATH=str(common.NODE/'bin')+':'+os.environ['PATH'])
 with (base/'native-output.txt').open('w') as log:
  proc=subprocess.Popen([str(common.NODE/'bin/node'),str(common.DSH),'--profile','headless',prompt],cwd=workspace,env=env,stdout=log,stderr=log,start_new_session=True)
  try:result['agentExitCode']=proc.wait(timeout=240);result['executionStatus']='finished' if proc.returncode==0 else 'agent_error'
  except subprocess.TimeoutExpired:
   result['executionStatus']='timeout';os.killpg(proc.pid,signal.SIGTERM)
   try:proc.wait(timeout=5)
   except subprocess.TimeoutExpired:os.killpg(proc.pid,signal.SIGKILL);proc.wait()
 result['solveSeconds']=time.perf_counter()-started
 test=subprocess.run([sys.executable,'-m','unittest','-v'],cwd=workspace,capture_output=True,text=True,timeout=15)
 result['testExitCode']=test.returncode;(out/'tests.txt').write_text(test.stdout+test.stderr)
finally:
 common.restore_provider(previous);observer.flush();observer.close()
 for file in workspace.glob('*.py'):shutil.copyfile(file,out/file.name)
 if (home/'auto-events.jsonl').exists():
  events=[json.loads(line) for line in (home/'auto-events.jsonl').read_text().splitlines()];(out/'events.json').write_text(json.dumps(events,ensure_ascii=False,indent=2)+'\n')
  result['distinctChildSessions']=len({e['id'] for e in events if e['kind']=='child_start'})
  result['completedActivations']=sum(e['kind']=='child_end' and e['stopReason']=='completed' for e in events)
  result['collectedActivations']=len({e['runId'] for e in events if e['kind']=='child_collected' and e.get('runId') is not None})
  result['contextReplacements']=sum(e['kind']=='context_replaced' for e in events)
  if case.startswith('reuse'):
   result['jevSelectedReuse']=any(e['kind']=='child_reused' for e in events)
   result['continuationValidated']=result.get('testExitCode')==0 and result['distinctChildSessions']==1 and result['completedActivations']>=2 and result['collectedActivations']>=2 and result['jevSelectedReuse']
 (out/'result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result),flush=True)
