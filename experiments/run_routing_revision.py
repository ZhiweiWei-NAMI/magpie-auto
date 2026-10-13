"""Targeted diagnosis on fixed existing tasks; not a new unbiased benchmark.

Reuse the CPU/Docker, isolation, observation and official-evaluation pipeline.
The source record of the earlier experiment remains unchanged.
"""
import json
import sys
import argparse
import subprocess
import os
from pathlib import Path

import yaml
import run_compare as common

ROOT=Path(__file__).resolve().parent/'jev-routing-revision'/'diagnostic'
TASK_IDS=['pytest-dev__pytest-10051','pytest-dev__pytest-7236']


def configure(home,arm):
    original_configure(home,arm)
    settings=yaml.safe_load((home/'settings.yaml').read_text())
    settings['agent-default-model']['reasoningEffort']='high'
    settings['llm-deepseek'].update(thinking='enabled',reasoningEffort='high')
    (home/'settings.yaml').write_text(yaml.safe_dump(settings,sort_keys=False))
    patch=yaml.safe_load((home/'cordis.patch.yml').read_text())
    for item in patch:
        if item.get('id') in ('tool-subagent','tool-subagent-fork'):
            item['config']['backgroundMode']='continuable'
            item['config']['agentOptions']['reasoningEffort']='high'
        for plugin in item.get('insert',[]):
            if plugin.get('id')=='magpie-auto':
                plugin['config'].update(maxChildRequests=16,maxChildTurnRequests=8,maxDecisions=12,
                    pressureTokens=24000,retainTokens=8000,nativeThresholdRatio=0.1875)
    (home/'cordis.patch.yml').write_text(yaml.safe_dump(patch,sort_keys=False))


original_configure=common.configure_home


if __name__=='__main__':
    os.environ.update(CUDA_VISIBLE_DEVICES='',USE_TF='0')
    parser=argparse.ArgumentParser()
    parser.add_argument('--results-dir',type=Path,default=ROOT)
    parser.add_argument('--trial-dir',type=Path,default=common.RUNTIME/'routing-revision/swe-trials')
    parser.add_argument('--plugin-dir',type=Path,default=common.EXTENSION)
    args=parser.parse_args();ROOT=args.results_dir
    common.EXTENSION=args.plugin_dir.resolve()
    plugin_revision=subprocess.check_output(['git','rev-parse','HEAD'],cwd=common.EXTENSION,text=True).strip()
    ROOT.mkdir(parents=True,exist_ok=True)
    previous=common.MANIFEST
    manifest=json.loads((previous/'tasks.json').read_text())
    manifest['tasks']=[t for t in manifest['tasks'] if t['instance_id'] in TASK_IDS]
    manifest['selection']='Two prespecified diagnostic tasks: 10051 (all arms solved, excess Auto output) and 7236 (Auto failed, controls solved). Chosen from prior results to investigate mechanisms, not estimate general improvement.'
    (ROOT/'tasks.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n')
    config=json.loads((previous/'run-config.json').read_text())
    config.update(plugin_version='see plugin source revision',dsh_auto_source_revision=plugin_revision,
        magpie_source_revision=subprocess.check_output(['git','rev-parse','HEAD'],cwd=common.REPO,text=True).strip(),execution_reasoning_effort='high',
        dsh_auto_source_modified=bool(subprocess.check_output(['git','status','--porcelain'],cwd=common.EXTENSION,text=True)),
        magpie_source_modified=bool(subprocess.check_output(['git','status','--porcelain'],cwd=common.REPO,text=True)),
        thinking_note='All arms send thinking enabled and reasoning_effort high. The old fixed low provider override was removed from this dedicated Magpie connection.',
        max_child_requests=16,max_child_turn_requests=8,max_decision_requests=12,
        child_lifecycle='Native continuable children; cached identity survives completed activations.',
        selection=manifest['selection'],jev_context_control='Atomic relevance and ownership at observed-input-calibrated pressure, with complete raw journals retained.')
    (ROOT/'run-config.json').write_text(json.dumps(config,ensure_ascii=False,indent=2)+'\n')
    common.configure_home=configure
    common.AUTO_PROMPT='Propose useful independent subtasks with native tools when appropriate; Magpie Auto evaluates delegation and profile selection. Collect any child results. '
    common.MANIFEST=ROOT
    sys.argv=[sys.argv[0],'--limit','2','--results-dir',str(ROOT),
        '--trial-dir',str(args.trial_dir)]
    common.main()
