// Auto owns its tasks and runs. Leaving this page never cancels a run.
(() => {
  const page = document.querySelector('#view-auto');
  if (!page) return;
  let data = null, task = null, selected = '', request = 0, busy = false, stopping = '';
  const drafts = new Map(), eventNodes = new Map();
  const inspectionFolds = new Set(); let inspectionFor = '';
  const make = (tag, cls, text) => el(tag, cls, text);
  const button = (text, fn, cls = 'text') => {
    const b = make('button', cls, t(text)); b.type = 'button'; b.onclick = fn; return b;
  };
  const active = s => ['running', 'deciding', 'queued', 'starting', 'stopping', 'waiting', 'awaiting_action'].includes(s);
  const states = { pending:'Ready', ready:'Ready', running:'Running', deciding:'Deciding', queued:'Queued', waiting:'Waiting', awaiting_action:'Needs your response', starting:'Starting', stopping:'Stopping', completed:'Completed', failed:'Failed', cancelled:'Stopped', interrupted:'Interrupted', blocked:'Blocked' };
  const stateName = s => t(states[s] || 'Ready');
  const header = make('div', 'auto-head');
  const intro = make('div'); intro.append(make('h2', '', t('Auto workspace')), make('p', 'auto-muted', t('Owned tasks, bounded context, explicit execution.')));
  const configButton = button('Configure Auto', () => { config.hidden = !config.hidden; if (!config.hidden) fillConfig(); });
  header.append(intro, configButton);
  const notice = make('div', 'auto-notice'); notice.setAttribute('role', 'status');
  const config = make('form', 'auto-config'); config.hidden = true;
  const configText = make('textarea', 'auto-config-json'); configText.spellcheck = false; configText.setAttribute('aria-label', t('Auto configuration'));
  const configHelp = make('p', 'auto-muted', t('Configure Jev in Providers. Only configured Codex app-server routes can execute. No API keys belong here.'));
  const configFields = make('div', 'auto-config-fields'), configInputs = {};
  for (const [key, label] of [['enabled','Enable Auto'], ['decider','Jev decider'], ['command','Codex command'], ['cwd','Working directory'], ['model','Model']]) {
    const line = make('label', 'auto-config-field'), input = make('input'); input.type = key === 'enabled' ? 'checkbox' : 'text'; input.autocomplete = 'off';
    line.append(make('span', '', t(label)), input); configInputs[key] = input; configFields.append(line);
    input.oninput = () => { try { const current = JSON.parse(configText.value); if (key === 'enabled') current.enabled = input.checked; else if (key === 'decider') current.decider = input.value; else {current.routes ||= []; current.routes[0] ||= {id:'codex', adapter:'codex-app-server', efforts:['low','medium','high'],args:[]}; current.routes[0][key] = input.value;} configText.value = JSON.stringify(current,null,2); } catch {} };
  }
  const sandboxPick = button('Read-only', () => {
    let current; try { current = JSON.parse(configText.value); } catch (e) { status(e.message, 'err'); return; }
    openProtoMenu(sandboxPick, [{v:'read-only',name:'Read-only'}, {v:'workspace-write',name:'Workspace write'}], current.routes?.[0]?.sandbox || 'read-only', value => {
      current.routes ||= []; current.routes[0] ||= {id:'codex',adapter:'codex-app-server',efforts:['low','medium','high'],args:[]}; current.routes[0].sandbox = value;
      configText.value = JSON.stringify(current, null, 2); sandboxPick.textContent = t(value === 'workspace-write' ? 'Workspace write' : 'Read-only');
    }, 'Execution permission');
  });
  const sandboxLine = make('div', 'auto-config-field'); sandboxLine.append(make('span', '', t('Execution permission')), sandboxPick); configFields.append(sandboxLine);
  const advanced = make('details', 'auto-config-advanced'); advanced.append(make('summary', '', t('Advanced configuration')), make('p', 'auto-muted', t('Read-only is the default. Workspace write requires an exact directory and confirmation. Network access stays disabled; command escalation cannot be approved.')), configText);
  config.append(configHelp, configFields, advanced, button('Save configuration', () => config.requestSubmit(), 'text primary'));
  config.onsubmit = async e => { e.preventDefault(); await action(async () => {
    const next = JSON.parse(configText.value);
    for (const route of next.routes || []) if (route.sandbox === 'workspace-write') {
      const old = data?.config?.routes?.find(r => r.id === route.id);
      if (old?.sandbox === 'workspace-write' && old.cwd === route.cwd) continue;
      if (!route.cwd || !/^([A-Za-z]:[\\/]|\\\\|\/)/.test(route.cwd)) throw new Error(t('Set an exact absolute working directory before enabling workspace write.'));
      if (!await confirmAction(t('Allow workspace writes in {cwd}?', {cwd:route.cwd}), t('Route {route} may edit files in this directory. Network access remains disabled; command escalation cannot be approved here.', {route:route.id}), t('Allow workspace writes'))) return;
    }
    await api('auto/config', next); config.hidden = true;
  }); };
  const workspace = make('div', 'auto-workspace');
  const rail = make('aside', 'auto-rail'); rail.setAttribute('aria-label', t('Tasks'));
  const railHead = make('div', 'auto-rail-head'); railHead.append(make('h3', '', t('Tasks')), button('New task', () => select('')));
  const taskList = make('div', 'auto-task-list'); rail.append(railHead, taskList);
  const conversation = make('section', 'auto-conversation');
  const taskHead = make('div', 'auto-task-head');
  const goal = make('h3', '', t('New task')), runInfo = make('div', 'auto-run-info');
  const detailsButton = button('Details', () => { inspector.hidden = !inspector.hidden; detailsButton.setAttribute('aria-expanded', !inspector.hidden); });
  detailsButton.setAttribute('aria-expanded', 'false');
  taskHead.append(goal, detailsButton);
  const timeline = make('div', 'auto-timeline'); timeline.setAttribute('role', 'log'); timeline.setAttribute('aria-live', 'polite');
  const empty = make('div', 'auto-empty'); empty.append(make('h3', '', t('What should we work on?')), make('p', 'auto-muted', t('Describe a goal. Jev chooses among your configured executors before a run starts.')));
  timeline.append(empty);
  const composer = make('form', 'auto-composer');
  const message = make('textarea', 'auto-message'); message.rows = 3; message.placeholder = t('Describe a task or continue the conversation'); message.setAttribute('aria-label', t('Task message'));
  message.oninput = () => drafts.set(selected, message.value);
  const controls = make('div', 'auto-controls');
  let route = '', effort = '';
  const routePick = button('Automatic route', () => openProtoMenu(routePick, [{v:'', name:t('Automatic route')}, ...(data?.config?.routes || []).map(r => ({v:r.id, name:r.name || r.id}))], route, value => {
    route = value; routePick.textContent = value || t('Automatic route');
    if (effort && route && !(data?.config?.routes || []).find(r => r.id === route)?.efforts?.includes(effort)) {
      effort = ''; effortPick.textContent = t('Automatic effort');
    }
  }, 'Executor route'));
  const effortPick = button('Automatic effort', () => openProtoMenu(effortPick, ['', ...new Set((data?.config?.routes || []).filter(r => !route || r.id === route).flatMap(r => r.efforts || []))].map(v => ({v, name:v || t('Automatic effort')})), effort, value => {effort = value; effortPick.textContent = value || t('Automatic effort');}, 'Effort'));
  const stop = button('Stop', () => action(async () => { const id = selected; stopping = id; clearPending(); updateControls(); await api('auto/tasks/' + encodeURIComponent(id) + '/cancel', {}); }));
  const send = button('Create task', () => composer.requestSubmit(), 'text primary');
  controls.append(routePick, effortPick, make('span', 'grow'), stop, send); composer.append(message, controls);
  composer.onsubmit = async e => {
    e.preventDefault(); if (busy || !message.value.trim() && !selected) return;
    const id = selected, text = message.value.trim();
    await action(async () => {
      if (!id) { const created = await api('auto/tasks', {goal:text}); drafts.delete(''); await select(created.id || created.task?.id); }
      else { await api('auto/tasks/' + encodeURIComponent(id) + ((task?.runs || []).length ? '/continue' : '/start'), {message:text, routeId:route, effort, preferences:readPreferences()}); drafts.delete(id); if (selected === id) message.value = ''; }
    });
  };
  const pending = make('section', 'auto-pending'); pending.hidden = true; pending.setAttribute('aria-label', t('Pending actions'));
  const pendingNodes = new Map(), answeredActions = new Set();
  conversation.append(taskHead, runInfo, timeline, pending, composer);
  const inspector = make('aside', 'auto-inspector'); inspector.hidden = true;
  inspector.id = 'auto-inspector'; detailsButton.setAttribute('aria-controls', inspector.id);
  const inspectorHead = make('div', 'auto-inspector-head'); inspectorHead.append(make('h3', '', t('Details')), button('Close', () => { inspector.hidden = true; detailsButton.setAttribute('aria-expanded', 'false'); }));
  const inspection = make('div', 'auto-inspection');
  const pref = make('div', 'auto-preferences'); pref.append(make('h3', '', t('Run preferences')), make('p', 'auto-muted', t('These choices apply to this task. Explicit requests take priority.')));
  const prefInputs = {};
  for (const [key, label] of [['quality','Quality'], ['cost','Cost'], ['speed','Speed']]) {
    const line = make('label', 'auto-pref'); const input = make('input'); input.type = 'range'; input.min = '0'; input.max = '1'; input.step = '.1'; input.value = '.5';
    const value = make('output', '', '.5'); input.oninput = () => value.textContent = input.value;
    line.append(make('span', '', t(label)), input, value); prefInputs[key] = input; pref.append(line);
  }
  const feedback = make('div', 'auto-feedback'); const feedbackText = make('input'); feedbackText.placeholder = t('Feedback for the next run'); feedbackText.setAttribute('aria-label', t('Feedback for the next run'));
  const rate = rating => action(async () => { await api('auto/tasks/' + encodeURIComponent(selected) + '/feedback', {runId:task?.runs?.at(-1)?.id || '', rating, text:feedbackText.value, preferences:readPreferences()}); feedbackText.value = ''; status(t('Feedback saved for this task'), 'ok'); });
  feedback.append(make('h3', '', t('Feedback')), feedbackText, button('Helpful', () => rate(1)), button('Needs improvement', () => rate(-1)), make('p', 'auto-muted', t('Feedback adjusts task routing scores, not remote model weights.')));
  inspector.append(inspectorHead, inspection, pref, feedback); workspace.append(rail, conversation, inspector); page.append(header, notice, config, workspace);
  const readPreferences = () => Object.fromEntries(Object.entries(prefInputs).map(([key, input]) => [key, Number(input.value)]));
  function fillConfig() { configText.value = JSON.stringify(data?.config || {enabled:false, decider:'', routes:[{id:'codex',adapter:'codex-app-server',model:'',description:'',efforts:['low','medium','high'],command:'codex',args:[],cwd:'',sandbox:'read-only'}], maxConcurrent:1, contextBytes:24000}, null, 2); const current = JSON.parse(configText.value); for (const [key,input] of Object.entries(configInputs)) {if (key === 'enabled') input.checked = !!current.enabled; else input.value = key === 'decider' ? current.decider || '' : current.routes?.[0]?.[key] || '';} sandboxPick.textContent = t(current.routes?.[0]?.sandbox === 'workspace-write' ? 'Workspace write' : 'Read-only'); }
  function updateControls() {
    const running = active(task?.status), configured = !!data?.configured && !!data?.config?.enabled;
    send.textContent = t(!selected ? 'Create task' : (task?.runs || []).length ? 'Continue' : 'Start');
    send.disabled = busy || running || (!!selected && !configured); stop.hidden = !running && stopping !== selected;
    stop.disabled = busy || stopping === selected; stop.textContent = t(stopping === selected ? 'Stopping' : 'Stop');
    routePick.disabled = effortPick.disabled = busy || running; feedback.querySelectorAll('button').forEach(b => b.disabled = busy || !task?.runs?.length || running);
    for (const [key, record] of pendingNodes) record.node.querySelectorAll('button,input').forEach(e => e.disabled = busy || answeredActions.has(key));
  }
  function drawList() {
    taskList.replaceChildren();
    for (const item of data?.tasks || []) {
      const b = button('', () => select(item.id), 'auto-task'); b.classList.toggle('on', item.id === selected); b.setAttribute('aria-current', item.id === selected ? 'true' : 'false');
      b.append(make('span', 'auto-task-goal', item.goal), make('span', 'auto-muted', stateName(item.status))); taskList.append(b);
    }
    if (!data?.tasks?.length) taskList.append(make('p', 'auto-muted auto-list-empty', t('No tasks yet')));
  }
  function clearPending() {
    for (const record of pendingNodes.values()) { record.clear(); record.node.remove(); }
    pendingNodes.clear(); pending.hidden = true; workspace.classList.remove('auto-needs-action');
  }
  function drawPending() {
    const requests = task?.pendingActions || [], visible = new Set(requests.map(a => selected + '/' + a.id));
    for (const [key, record] of pendingNodes) if (!visible.has(key)) {record.clear(); record.node.remove(); pendingNodes.delete(key);}
    pending.hidden = !requests.length; workspace.classList.toggle('auto-needs-action', !!requests.length);
    for (const request of requests) {
      const owner = selected, key = owner + '/' + request.id; if (pendingNodes.has(key)) continue;
      const node = make('form', 'auto-action'); node.dataset.actionId = request.id;
      node.append(make('h3', '', t(request.kind === 'input' ? 'Your response is needed' : request.kind === 'file' ? 'File change approval' : 'Command escalation request')));
      if (request.reason) node.append(make('p', 'auto-action-reason', request.reason));
      node.append(make('p', 'auto-muted', t('Working directory') + ': ' + (request.cwd || t('Unknown'))));
      if (request.command) node.append(make('pre', 'auto-action-command', request.command));
      const paths = Array.isArray(request.paths) ? request.paths : [];
      if (paths.length) { const list = make('ul', 'auto-action-paths'); paths.forEach(path => list.append(make('li', '', path))); node.append(list); }
      const run = task?.runs?.find(r => r.id === request.runId) || task?.runs?.at(-1);
      const write = run?.route?.sandbox === 'workspace-write';
      node.append(make('p', 'auto-muted', t(write ? 'Workspace-write scope. Approval applies once to the listed paths only.' : 'Read-only scope. File writes cannot be approved.')));
      if (request.kind === 'command') node.append(make('p', 'auto-action-limit', t('This command requests permission beyond the sandbox. Magpie cannot guarantee that scope or network isolation. Decline it or stop the run; change the task instead of granting escalation.')));
      const inputs = new Map();
      if (request.kind === 'input') for (const question of request.questions || []) {
        const section = make('fieldset', 'auto-question'); section.append(make('legend', '', question.header || question.question || question.id));
        if (question.question && question.question !== question.header) section.append(make('p', '', question.question));
        const options = question.isSecret ? [] : question.options || [];
        for (const option of options) {
          const line = make('label', 'auto-question-option'), input = make('input'); input.type = 'radio'; input.name = 'auto-' + request.id + '-' + question.id; input.value = option.label;
          const words = make('span'); words.append(make('span', '', option.label)); if (option.description) words.append(make('small', 'auto-muted', option.description)); line.append(input, words); section.append(line);
        }
        let free;
        if (question.isSecret || question.isOther || !options.length) {
          free = make('input', 'auto-answer'); free.type = question.isSecret ? 'password' : 'text'; free.autocomplete = question.isSecret ? 'new-password' : 'off'; free.placeholder = t(options.length ? 'Other answer' : 'Enter your answer'); free.setAttribute('aria-label', question.question || question.header || question.id); section.append(free);
        }
        inputs.set(question.id, {section, free, secret:!!question.isSecret}); node.append(section);
      }
      const error = make('p', 'auto-action-error'); error.setAttribute('role', 'status');
      const bar = make('div', 'auto-action-bar');
      const clear = () => node.querySelectorAll('input').forEach(input => {input.value = input.type === 'radio' ? input.value : ''; input.checked = false;});
      const respond = response => action(async () => {
        if (answeredActions.has(key)) return;
        if (response.decision === 'accept' && !await confirmAction(t('Approve these file changes once?'), t('Working directory') + ': ' + (request.cwd || '') + '\n\n' + paths.join('\n'), t('Approve once'))) return;
        clear();
        await api('auto/tasks/' + encodeURIComponent(owner) + '/actions/' + encodeURIComponent(request.id), response);
        answeredActions.add(key); status(t('Response sent'), 'ok');
      });
      if (request.kind === 'file' && write && paths.length && request.allowedDecisions?.includes('accept')) bar.append(button('Approve once', () => respond({decision:'accept'}), 'text primary'));
      if (request.allowedDecisions?.includes('decline')) bar.append(button('Decline', () => respond({decision:'decline'})));
      if (request.allowedDecisions?.includes('cancel') || request.kind === 'input') bar.append(button('Cancel request', () => respond({decision:'cancel'})));
      if (request.kind === 'input') bar.prepend(button('Send response', () => node.requestSubmit(), 'text primary'));
      node.onsubmit = e => {
        e.preventDefault(); if (busy || answeredActions.has(key) || request.kind !== 'input') return;
        const answers = {};
        for (const [id, fields] of inputs) {
          const free = fields.free ? fields.secret ? fields.free.value : fields.free.value.trim() : '';
          const answer = free || fields.section.querySelector('input[type=radio]:checked')?.value;
          if (!answer) {error.textContent = t('Answer each question before continuing.'); return;}
          answers[id] = [answer];
        }
        respond({answers});
      };
      node.append(error, bar); pendingNodes.set(key, {node,clear}); pending.append(node);
    }
  }
  function drawRecordedState() {
    const state = task?.state; if (!state) return;
    const box = make('details', 'auto-recorded-state'); box.dataset.autoFold = 'state'; box.open = inspectionFolds.has('state'); box.append(make('summary', '', t('Recorded task state')), make('p', 'auto-muted', t('Recorded from requests and executor events.')));
    if (state.phase) box.append(make('p', 'auto-muted', t('Phase') + ': ' + stateName(state.phase)));
    const record = (label, value) => {if (!value?.text) return; box.append(make('h4', '', t(label)), make('p', 'auto-state-text', value.text), make('p', 'auto-provenance', [value.taskId,value.runId,value.sourceId].filter(Boolean).join(' · ')));};
    record('Goal', state.goal); record('Current request', state.currentRequest); record('Progress', state.progress); record('Current result', state.currentResult);
    for (const result of state.completedResults || []) record('Completed result', result);
    if (state.explicitConstraints) box.append(make('p', 'auto-muted', t('Explicit choices') + ': ' + [state.explicitConstraints.routeId,state.explicitConstraints.effort].filter(Boolean).join(' · ')));
    for (const dependency of state.pendingDependencies || []) { const line = make('p', 'auto-muted', t('Dependency') + ': ' + [dependency.taskId,dependency.runId,stateName(dependency.status)].filter(Boolean).join(' · ')); if (dependency.taskId) line.append(button('Open task', () => select(dependency.taskId))); box.append(line); }
    if (state.sourceRefs?.length) {box.append(make('h4', '', t('State provenance'))); for (const source of state.sourceRefs) box.append(make('p', 'auto-provenance', [source.kind,source.id,source.taskId,source.runId].filter(Boolean).join(' · ')));}
    inspection.append(box);
  }
  function drawTask() {
    goal.textContent = task?.goal || t('New task');
    const run = task?.runs?.at(-1);
    const decision = run?.decision;
    const model = run?.route?.model;
    runInfo.textContent = task ? [stateName(task.status), decision?.routeId, model, decision?.effort].filter(Boolean).join(' · ') : t('Auto starts only when you choose Start.');
    empty.hidden = !!task;
    const raw = task?.events || [];
    const completedRuns = new Set(raw.filter(e => e.kind === "assistant").map(e => e.runId));
    const events = raw.filter(e => ["created", "user", "assistant", "status", "progress", "error", "child"].includes(e.kind));
    const stream = new Map();
    for (const event of raw) if (event.kind === "commentary_delta" || event.kind === "message_delta" && !completedRuns.has(event.runId)) {
      const streamKey = event.runId + ':' + event.kind;
      const current = stream.get(streamKey) || { ...event, id:'stream:' + streamKey, text:'' }; current.text += event.text || ''; stream.set(streamKey, current);
    }
    events.push(...stream.values());
    const visible = new Set(events.map(e => e.id));
    for (const [key,node] of eventNodes) if (!visible.has(key)) {node.remove();eventNodes.delete(key);}
    for (const event of events) {
      const key = event.id || [event.runId, event.at, event.kind, event.text].join('|');
      let node = eventNodes.get(key);
      if (!node) { node = make('article', 'auto-event'); node.append(make('div', 'auto-event-meta'), make('div', 'auto-event-text')); eventNodes.set(key, node); timeline.append(node); }
      node.firstChild.textContent = [t(({created:'Goal',user:'You',assistant:'Assistant',message_delta:'Assistant',commentary_delta:'Progress',status:'Progress',progress:'Progress',error:'Error',child:'Child task'})[event.kind] || 'Progress'), event.at ? new Date(event.at).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'}) : ''].filter(Boolean).join(' · ');
      node.lastChild.textContent = ['status', 'progress'].includes(event.kind) ? t(event.text || '') : event.text || ''; node.classList.toggle('auto-event-user', event.kind === 'user' || event.kind === 'created');
    }
    if (inspectionFor !== selected) inspectionFolds.clear();
    else for (const fold of inspection.querySelectorAll('details[data-auto-fold]')) {if (fold.open) inspectionFolds.add(fold.dataset.autoFold); else inspectionFolds.delete(fold.dataset.autoFold);}
    inspectionFor = selected;
    inspection.replaceChildren(make('h3', '', t('Execution details')));
    inspection.append(make('p', 'auto-muted', t(run?.route?.sandbox === 'workspace-write' ? 'Workspace-write executor. Network access is disabled; command escalation cannot be approved.' : 'Read-only executor. File writes cannot be approved.')));
    if (run?.threadId) inspection.append(make('p', 'auto-muted', t('Native session') + ': ' + run.threadId));
    if (run?.childTaskId) inspection.append(button('Open child task', () => select(run.childTaskId)));
    if (run?.parentTaskId || task?.parentId) inspection.append(button('Open parent task', () => select(run?.parentTaskId || task.parentId)));
    drawRecordedState();
    if (decision?.reason) inspection.append(make('p', 'auto-reason', decision.reason));
    if (run?.error) inspection.append(make('p', 'auto-reason', run.error));
    if (task?.parentId) inspection.append(make('p', 'auto-muted', t('Parent task') + ': ' + task.parentId));
    if (task?.dependencies?.length) inspection.append(make('p', 'auto-muted', t('Dependencies') + ': ' + task.dependencies.join(', ')));
    inspection.append(make('h4', '', t('Context sources')));
    const included = run?.contextSourceIds || decision?.sourceIds;
    const sources = (task?.sources || []).filter(s => !included || included.includes(s.id));
    for (const source of sources) { const d = make('details', 'auto-source'); d.dataset.autoFold = 'source:' + source.id; d.open = inspectionFolds.has(d.dataset.autoFold); d.append(make('summary', '', [source.kind, source.id].filter(Boolean).join(' · ')), make('p', 'auto-muted', source.reason || ''), make('pre', '', source.text || '')); inspection.append(d); }
    if (!sources.length) inspection.append(make('p', 'auto-muted', t('No context selected yet')));
    inspection.append(make('h4', '', t('Artifacts')));
    for (const artifact of task?.artifacts || []) { const d = make('div', 'auto-artifact'); d.append(make('strong', '', artifact.path || artifact.id), make('p', 'auto-muted', t('Run') + ': ' + (artifact.runId || '') + ' · ' + t('Source') + ': ' + (artifact.sourceId || ''))); { const a = make('a', 'text', t('Open artifact')); a.href = '/api/auto/tasks/' + encodeURIComponent(artifact.taskId || selected) + '/artifacts/' + encodeURIComponent(artifact.id); a.download = ''; a.target = '_blank'; a.rel = 'noopener'; d.append(a); } inspection.append(d); }
    if (task?.routeScores && Object.keys(task.routeScores).length) { inspection.append(make("h4", "", t("Route scores"))); for (const [id,score] of Object.entries(task.routeScores)) inspection.append(make("p", "auto-muted", id + ": " + Number(score).toFixed(2))); }
    if (!task?.artifacts?.length) inspection.append(make('p', 'auto-muted', t('No artifacts recorded')));
    if (stopping === selected && !active(task?.status)) stopping = '';
    drawPending(); updateControls();
  }
  async function select(id) {
    drafts.set(selected, message.value); clearPending(); selected = id || ''; task = null; ++request; eventNodes.clear(); timeline.querySelectorAll('.auto-event').forEach(n => n.remove()); message.value = drafts.get(selected) || ''; route = effort = ''; routePick.textContent = t('Automatic route'); effortPick.textContent = t('Automatic effort'); drawTask(); drawList();
    if (selected) { await loadDetail(); for (const [key,input] of Object.entries(prefInputs)) {input.value = task?.preferences?.[key] ?? .5; input.nextSibling.textContent = input.value;} }
  }
  async function loadDetail() {
    const id = selected, ticket = ++request; if (!id) return;
    const result = await api('auto/tasks/' + encodeURIComponent(id)); if (ticket !== request || selected !== id) return;
    task = result.task || result; drawTask();
  }
  async function load() {
    data = await api('auto'); notice.textContent = data.error || t(!data.config?.enabled ? 'Auto is off. Normal Magpie behavior is unchanged.' : !data.configured ? 'Auto is unconfigured. Add Jev in Providers and configure an executor route.' : 'Auto is ready. Runs use only configured executors.');
    notice.classList.toggle('auto-unconfigured', !data.configured); drawList(); if (selected) await loadDetail(); else updateControls();
  }
  async function action(fn) {
    if (busy) return; busy = true; updateControls();
    try { await fn(); await load(); } catch (e) { status(e.message, 'err'); notice.textContent = e.message; stopping = ''; }
    finally { busy = false; updateControls(); }
  }
  if (view === "auto") load().catch(e => { notice.textContent = e.message; });
  window.loadAuto = () => load().catch(e => { notice.textContent = e.message; throw e; });
  setInterval(() => { if (view === 'auto' && !busy && !document.hidden) load().catch(e => { notice.textContent = e.message; }); }, 1800);
})();
