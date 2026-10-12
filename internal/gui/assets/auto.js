// Auto provides Jev decisions and inference history; dsh executes tools. Leaving this page never cancels a run.
(() => {
  const page = document.querySelector('#view-auto');
  if (!page) return;
  let data, task, selected = '', busy = false, stopping = '', detailTicket = 0, listTicket = 0;
  let route = '', effort = '', polling = false, preferencesFor = '', inspectionKey = '';
  const drafts = new Map(), taskNodes = new Map(), eventNodes = new Map(), folds = new Set();
  const make = (tag, cls, text) => el(tag, cls, text);
  const button = (label, fn, cls = 'text') => {
    const node = make('button', cls, t(label)); node.type = 'button'; node.onclick = fn; return node;
  };
  const active = value => ['queued', 'deciding', 'running', 'stopping'].includes(value);
  const states = {idle:'Ready', queued:'Queued', deciding:'Deciding', running:'running', stopping:'Stopping', completed:'Completed', failed:'Failed', cancelled:'Stopped', interrupted:'Interrupted', missing:'Error'};
  const stateName = value => t(states[value] || 'Ready');
  const header = make('div', 'auto-head'), notice = make('p', 'auto-muted');
  header.append(make('h2', '', t('Auto')), button('Settings', () => {
    config.hidden = !config.hidden; if (!config.hidden) fillConfig();
  }));
  notice.setAttribute('role', 'status');
  const config = make('form', 'auto-config'); config.hidden = true;
  const configHelp = 'Choose Jev Free and a Console key file. dsh executes tools and subagents.';
  config.append(make('p', 'auto-muted', t(configHelp)));
  const fields = make('div', 'auto-config-fields'), configInputs = {}, routeEditors = [];
  function field(parent, label, value, type = 'text') {
    const line = make('label', 'auto-field'), input = make('input'); input.type = type;
    if (type === 'checkbox') input.checked = !!value; else input.value = value ?? '';
    input.autocomplete = 'off'; line.append(make('span', '', t(label)), input); parent.append(line); return input;
  }
  for (const [key, label, type] of [['enabled','Enable','checkbox'], ['decider','Jev decider','text'], ['jevKeyFile','Console key file','text'], ['maxConcurrent','Requests at once','number'], ['contextBytes','Context bytes','number']]) {
    const input = configInputs[key] = field(fields, label, '', type);
    if (type === 'number') { input.required = true; input.step = '1'; input.min = key === 'contextBytes' ? '1024' : '1'; input.max = key === 'contextBytes' ? '64000' : '4'; }
  }
  configInputs.enabled.onchange = () => {
    configInputs.decider.required = configInputs.enabled.checked;
    configInputs.jevKeyFile.required = configInputs.enabled.checked;
    for (const entry of routeEditors) entry.inputs.id.required = entry.inputs.model.required = configInputs.enabled.checked;
  };
  const routeFields = make('div', 'auto-routes');
  function addRoute(value = {}) {
    const row = make('div', 'auto-route'), inputs = {}, entry = {row, inputs, adapter:'gateway'};
    for (const [key, label] of [['id','Agent'], ['label','Name'], ['model','Model'], ['description','Task role'], ['efforts','Effort']]) {
      inputs[key] = field(row, label, key === 'efforts' ? (value.efforts || ['low','medium','high']).join(', ') : value[key]);
      inputs[key].required = configInputs.enabled.checked && (key === 'id' || key === 'model');
    }
    row.append(make('p', 'auto-muted', t('Inference only; this route cannot read or write workspace files.'))); routeEditors.push(entry);
    row.append(button('Remove', () => { row.remove(); routeEditors.splice(routeEditors.indexOf(entry), 1); })); routeFields.append(row);
  }
  config.append(fields, make('h3', '', t('Configured agents')), routeFields, button('Add', () => addRoute()), button('Save', () => config.requestSubmit(), 'text primary'));
  function fillConfig() {
    const current = data?.config || {enabled:false, decider:'', maxConcurrent:1, contextBytes:16000};
    for (const [key, input] of Object.entries(configInputs)) {
      if (key === 'enabled') input.checked = !!current.enabled; else input.value = current[key] ?? (key === 'decider' ? 'jev-1.13-free' : key === 'jevKeyFile' ? '' : key === 'contextBytes' ? 16000 : 1);
    }
    routeEditors.length = 0; routeFields.replaceChildren();
    for (const value of current.routes || []) addRoute(value);
    if (!routeEditors.length) addRoute();
    configInputs.enabled.onchange();
  }
  config.onsubmit = event => {
    event.preventDefault(); action(async () => {
      const next = {
        enabled:configInputs.enabled.checked, decider:configInputs.decider.value.trim(), jevKeyFile:configInputs.jevKeyFile.value.trim(),
        maxConcurrent:Number(configInputs.maxConcurrent.value), contextBytes:Number(configInputs.contextBytes.value)
      };
      next.routes = routeEditors.filter(({inputs}) => ['id','label','model','description'].some(key => inputs[key].value.trim())).map(({inputs, adapter}) => {
        const route = {adapter};
        for (const [key, input] of Object.entries(inputs)) {
          route[key] = input.type === 'checkbox' ? input.checked : key === 'efforts' ? input.value.split(',').map(value => value.trim()).filter(Boolean) : input.value.trim();
        }
        if (adapter === 'gateway') { delete route.workspace; delete route.allowWrites; }
        return route;
      });
      await api('auto/config', next); config.hidden = true;
    });
  };
  const workspace = make('div', 'auto-workspace'), rail = make('aside', 'auto-rail');
  const railHead = make('div', 'auto-rail-head'), taskList = make('div', 'auto-task-list');
  railHead.append(make('h3', '', t('Tasks')), button('New task', () => select(''))); rail.append(railHead, taskList);
  const conversation = make('section', 'auto-conversation'), taskHead = make('div', 'auto-task-head');
  const goal = make('h3', '', t('New task')), runInfo = make('p', 'auto-run-info');
  const detailsButton = button('Details', () => showDetails(inspector.hidden));
  taskHead.append(goal, detailsButton);
  const timeline = make('div', 'auto-timeline'), empty = make('p', 'auto-muted', t('Jev selects a configured model route when you choose Start.'));
  timeline.setAttribute('role', 'log'); timeline.setAttribute('aria-live', 'polite'); timeline.append(empty);
  const composer = make('form', 'auto-composer'), message = make('textarea', 'auto-message');
  message.rows = 3; message.placeholder = t('Describe a task or continue the conversation'); message.setAttribute('aria-label', message.placeholder);
  function saveDraft() { if (message.value) drafts.set(selected, message.value); else drafts.delete(selected); }
  message.oninput = saveDraft;
  const controls = make('div', 'auto-controls');
  const routePick = button('Automatic', () => openProtoMenu(routePick, [{v:'',name:'Automatic'}, ...(data?.config?.routes || []).map(r => ({v:r.id,name:r.label || r.id,literalName:true}))], route, value => {
    route = value;
    if (effort && route && !data.config.routes.find(r => r.id === route)?.efforts?.includes(effort)) effort = '';
    showChoices();
  }, 'Agent'));
  const effortPick = button('Automatic', () => openProtoMenu(effortPick, ['', ...new Set((data?.config?.routes || []).filter(r => !route || r.id === route).flatMap(r => r.efforts || []))].map(v => ({v,name:v || 'Automatic'})), effort, value => { effort = value; showChoices(); }, 'Effort'));
  function showChoices() {
    routePick.textContent = t('Agent') + ': ' + ((data?.config?.routes || []).find(r => r.id === route)?.label || route || t('Automatic'));
    effortPick.textContent = t('Effort') + ': ' + t(effort || 'Automatic');
  }
  showChoices();
  routePick.setAttribute('aria-label', t('Agent')); effortPick.setAttribute('aria-label', t('Effort'));
  const stop = button('Stop', () => action(async () => {
    const id = selected; stopping = id; updateControls(); await api('auto/tasks/' + encodeURIComponent(id) + '/cancel', {});
  }));
  const send = button('Create', () => composer.requestSubmit(), 'text primary');
  controls.append(routePick, effortPick, make('span', 'grow'), stop, send); composer.append(message, controls);
  composer.onsubmit = event => {
    event.preventDefault(); if (busy || active(task?.status) || selected && !task || !selected && !message.value.trim()) return;
    const id = selected, sent = message.value, preferences = readPreferences();
    action(async () => {
      const path = id ? 'auto/tasks/' + encodeURIComponent(id) + (task.runs?.length ? '/continue' : '/start') : 'auto/tasks';
      const body = id ? {message:sent.trim(), routeId:route, effort, preferences} : {goal:sent.trim(), preferences};
      const result = await api(path, body);
      // Consume only the submitted draft; typing or switching tasks during a request stays intact.
      if (drafts.get(id) === sent) drafts.delete(id);
      if (selected === id && message.value === sent) { message.value = ''; if (!id) await select(result.task?.id || result.id); }
    });
  };
  conversation.append(taskHead, runInfo, timeline, composer);
  const inspector = make('aside', 'auto-inspector'); inspector.id = 'auto-inspector'; inspector.hidden = true;
  detailsButton.setAttribute('aria-controls', inspector.id); detailsButton.setAttribute('aria-expanded', 'false');
  function showDetails(open) { inspector.hidden = !open; detailsButton.setAttribute('aria-expanded', String(open)); }
  const inspection = make('div'), pref = make('div', 'auto-preferences'), feedback = make('div', 'auto-feedback');
  inspector.append(button('Close', () => showDetails(false)), inspection, pref, feedback);
  pref.append(make('h3', '', t('Preferences')), make('p', 'auto-muted', t("Preferences are saved here. Feedback adjusts this task's route scores.")));
  let saved = {}; try { saved = JSON.parse(localStorage.getItem('magpie.autoPreferences') || '{}'); } catch {}
  const prefInputs = {};
  const readPreferences = () => Object.fromEntries(Object.entries(prefInputs).map(([key, input]) => [key, Number(input.value)]));
  for (const [key, label] of [['quality','Quality'], ['cost','Cost'], ['speed','Speed']]) {
    const line = make('label', 'auto-pref'), input = make('input'), value = make('output');
    input.type = 'range'; input.min = '0'; input.max = '1'; input.step = '.1'; input.value = saved?.[key] ?? .5; value.textContent = input.value;
    input.oninput = () => { value.textContent = input.value; saved = readPreferences(); try { localStorage.setItem('magpie.autoPreferences', JSON.stringify(saved)); } catch {} };
    line.append(make('span', '', t(label)), input, value); prefInputs[key] = input; pref.append(line);
  }
  const feedbackText = make('input'); feedbackText.placeholder = t('Feedback'); feedbackText.setAttribute('aria-label', t('Feedback'));
  function rate(rating) {
    const id = selected, text = feedbackText.value, runId = task?.runs?.at(-1)?.id;
    action(async () => {
      await api('auto/tasks/' + encodeURIComponent(id) + '/feedback', {runId, rating, text, preferences:readPreferences()});
      if (selected === id && feedbackText.value === text) feedbackText.value = ''; status(t('Feedback saved'), 'ok');
    });
  }
  feedback.append(make('h3', '', t('Feedback')), feedbackText, button('Helpful', () => rate(1)), button('Needs improvement', () => rate(-1)));
  workspace.append(rail, conversation, inspector); page.append(header, notice, config, workspace);
  function updateControls() {
    const running = active(task?.status);
    const isStopping = !!selected && stopping === selected;
    send.textContent = t(!selected ? 'Create' : task?.runs?.length ? 'Continue' : 'Start');
    send.disabled = busy || running || !!selected && (!task || !data?.configured || !data?.config?.enabled);
    stop.hidden = !running && !isStopping; stop.disabled = busy || isStopping; stop.textContent = t(isStopping ? 'Stopping' : 'Stop');
    routePick.disabled = effortPick.disabled = busy || running;
    feedback.querySelectorAll('button').forEach(node => node.disabled = busy || running || !task?.runs?.length);
  }
  function drawList() {
    const ids = new Set((data?.tasks || []).map(item => item.id));
    for (const [id, node] of taskNodes) if (!ids.has(id)) { node.remove(); taskNodes.delete(id); }
    for (const item of data?.tasks || []) {
      let node = taskNodes.get(item.id);
      if (!node) { node = button('New task', () => select(item.id), 'auto-task'); node.replaceChildren(make('span', 'auto-task-goal'), make('small', 'auto-muted')); taskNodes.set(item.id, node); taskList.append(node); }
      node.firstChild.textContent = item.goal; node.lastChild.textContent = stateName(item.status); node.classList.toggle('on', selected === item.id); node.setAttribute('aria-pressed', String(selected === item.id));
    }
  }
  function drawTask() {
    const follow = !eventNodes.size || timeline.scrollHeight - timeline.scrollTop - timeline.clientHeight < 48;
    const run = task?.runs?.at(-1), decision = run?.decision;
    goal.textContent = task?.goal || t('New task'); empty.hidden = !!task;
    runInfo.textContent = task ? [stateName(task.status), run?.route?.label || decision?.routeId, run?.route?.model, decision?.effort && t(decision.effort)].filter(Boolean).join(' · ') : '';
    const raw = task?.events || [], finished = new Set(raw.filter(e => e.kind === 'assistant').map(e => e.runId)), streams = new Map();
    const events = raw.filter(e => ['created','user','assistant','feedback','status','reasoning','tool_call','tool_result'].includes(e.kind));
    for (const event of raw) if (['assistant_delta','reasoning_delta'].includes(event.kind) && (event.kind === 'reasoning_delta' || !finished.has(event.runId))) {
      const key = event.runId + ':' + event.kind;
      const stream = streams.get(key) || {...event, id:key, text:''}; stream.text += event.text || ''; streams.set(key, stream);
    }
    events.push(...streams.values()); events.sort((a, b) => (a.at || '').localeCompare(b.at || ''));
    const visible = new Set(events.map(event => event.id));
    for (const [id, node] of eventNodes) if (!visible.has(id)) { node.remove(); eventNodes.delete(id); }
    for (const event of events) {
      let node = eventNodes.get(event.id);
      const thinking = event.kind.startsWith('reasoning') || event.kind.startsWith('tool_');
      if (!node) { node = make(thinking ? 'details' : 'article', 'auto-event'); node.append(make(thinking ? 'summary' : 'div', 'auto-event-meta'), make('div', 'auto-event-text')); eventNodes.set(event.id, node); timeline.append(node); }
      node.firstChild.textContent = t(({created:'Goal',user:'You',assistant:'Assistant',assistant_delta:'Assistant',reasoning:'reasoning',reasoning_delta:'reasoning',feedback:'Feedback',status:'Progress',tool_call:'Tool',tool_result:'Tool'})[event.kind]);
      const parts = (event.text || '').split(': '), code = parts.shift();
      node.lastChild.textContent = event.kind === 'status' ? states[code] ? stateName(code) + (parts.join(': ') ? ': ' + parts.join(': ') : '') : t(event.text || '') : event.text || '';
      if (event.kind.startsWith('tool_')) {
        let tool; try { tool = JSON.parse(event.text); } catch {}
        let args; try { if (tool?.function?.arguments) args = JSON.parse(tool.function.arguments); } catch {}
        node.firstChild.textContent = t(tool?.error ? 'Error' : 'Tool') + ': ' + [tool?.name || tool?.function?.name,tool?.path || args?.path].filter(Boolean).join(' · ');
        node.lastChild.textContent = tool?.error ?? tool?.result ?? (args ? JSON.stringify(args, null, 2) : event.text);
      }
      node.classList.toggle('auto-event-user', ['created','user'].includes(event.kind));
    }
    const key = JSON.stringify([decision, run?.route, run?.decider, run?.decisionRequestId, run?.error, run?.requestId, run?.usage, run?.calls, task?.state, task?.sources, task?.artifacts]);
    if (key !== inspectionKey) {
      inspectionKey = key;
      for (const fold of inspection.querySelectorAll('details')) { if (fold.open) folds.add(fold.dataset.source); else folds.delete(fold.dataset.source); }
      inspection.replaceChildren(make('h3', '', t('Details')));
      if (run?.route?.adapter === 'workspace') inspection.append(make('p', 'auto-reason', run.route.workspace + ' · ' + t(run.route.allowWrites ? 'Allow file writes' : 'Read-only')));
      if (decision?.reason) inspection.append(make('p', 'auto-reason', decision.reason));
      if (run?.error) inspection.append(make('p', 'auto-reason', run.error));
      if (run?.requestId) inspection.append(make('p', 'auto-muted', t('Request ID') + ': ' + run.requestId));
      if (run?.decider) inspection.append(make('p', 'auto-muted', t('Jev decider') + ': ' + run.decider + (run.decisionRequestId ? ' · ' + run.decisionRequestId : '')));
      const usage = run?.usage;
      if (run?.calls?.length) inspection.append(make('p', 'auto-muted', t('{n} requests', {n:run.calls.length})));
      if (usage) inspection.append(make('h4', '', t('Latest request')));
      for (const [label, value] of [['Input',usage?.prompt_tokens ?? usage?.input_tokens], ['Output tokens',usage?.completion_tokens ?? usage?.output_tokens], ['Cached tokens',usage?.prompt_tokens_details?.cached_tokens ?? usage?.input_tokens_details?.cached_tokens]]) {
        if (typeof value === 'number') inspection.append(make('p', 'auto-muted', t(label) + ': ' + value.toLocaleString()));
      }
      if (task?.state?.currentResult?.text) inspection.append(make('h4', '', t('Current result')), make('p', 'auto-record', task.state.currentResult.text), make('p', 'auto-muted', task.state.currentResult.sourceId));
      if (task?.state?.pendingDependencies?.length) {
        inspection.append(make('h4', '', t('Dependencies')));
        for (const dependency of task.state.pendingDependencies) inspection.append(button('Open', () => select(dependency.taskId)), make('p', 'auto-muted', dependency.taskId + ' · ' + stateName(dependency.status)));
      }
      inspection.append(make('h4', '', t('Context')));
      const included = run?.contextSourceIds || decision?.sourceIds || [];
      for (const source of (task?.sources || []).filter(source => included.includes(source.id))) {
        const node = make('details', 'auto-source'); node.dataset.source = source.id; node.open = folds.has(source.id);
        node.append(make('summary', '', source.id + (source.status ? ' · ' + stateName(source.status) : '')), make('p', 'auto-muted', [source.taskId,source.runId,source.reason].filter(Boolean).join(' · ')), make('pre', '', source.text || '')); inspection.append(node);
      }
      if (task?.artifacts?.length) inspection.append(make('h4', '', t('Artifacts')));
      for (const artifact of task?.artifacts || []) {
        const path = make('p', 'text auto-artifact', artifact.path);
        inspection.append(path, make('p', 'auto-muted', [artifact.runId,artifact.sourceId].filter(Boolean).join(' · ')));
      }
    }
    if (stopping === selected && !active(task?.status)) stopping = ''; updateControls();
    if (follow) timeline.scrollTop = timeline.scrollHeight;
  }
  async function select(id) {
    saveDraft(); selected = id || ''; task = null; ++detailTicket; preferencesFor = selected;
    folds.clear(); inspectionKey = ''; feedbackText.value = ''; eventNodes.clear(); timeline.querySelectorAll('.auto-event').forEach(node => node.remove());
    message.value = drafts.get(selected) || ''; route = effort = ''; showChoices(); drawTask(); drawList();
    if (selected) try { await loadDetail(); } catch (error) { if (selected === id) { notice.textContent = error.message; status(error.message, 'err'); } }
    else for (const [key, input] of Object.entries(prefInputs)) { input.value = saved?.[key] ?? .5; input.nextSibling.textContent = input.value; }
  }
  async function loadDetail() {
    const id = selected, ticket = ++detailTicket; if (!id) return;
    const result = await api('auto/tasks/' + encodeURIComponent(id)); if (ticket !== detailTicket || selected !== id) return;
    task = result.task || result;
    if (preferencesFor === id) {
      for (const [key, input] of Object.entries(prefInputs)) { input.value = task.preferences?.[key] ?? saved?.[key] ?? .5; input.nextSibling.textContent = input.value; } preferencesFor = '';
    }
    drawTask();
  }
  async function load() {
    const ticket = ++listTicket, result = await api('auto'); if (ticket !== listTicket) return;
    data = result; notice.textContent = data.error || t(!data.config?.enabled ? 'Off' : data.configured ? 'Ready' : configHelp);
    drawList(); if (selected) await loadDetail(); else updateControls();
  }
  async function action(fn) {
    if (busy) return; busy = true; ++listTicket; ++detailTicket; updateControls();
    try { await fn(); await load(); } catch (error) { notice.textContent = error.message; status(error.message, 'err'); stopping = ''; }
    finally { busy = false; updateControls(); }
  }
  window.loadAuto = load;
  if (view === 'auto') load().catch(error => { notice.textContent = error.message; });
  setInterval(async () => {
    if (view !== 'auto' || busy || polling || document.hidden) return;
    polling = true; try { await load(); } catch (error) { notice.textContent = error.message; } finally { polling = false; }
  }, 1800);
})();
