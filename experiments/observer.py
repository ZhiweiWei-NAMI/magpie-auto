"""Observe actual execution requests between Magpie and the existing WorkBuddy service."""
import json
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Observer:
    def __init__(self, output, request_limit=64):
        self.output = output
        self.limit = request_limit
        self.task = None
        self.arm = None
        self.calls = []
        self.previous = {}
        self.account_labels = {}
        self.lock = threading.Lock()
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def begin(self, task, arm):
        with self.lock:
            self.task, self.arm, self.calls, self.previous = task, arm, [], {}

    def flush(self):
        self.output.write_text(json.dumps(self.calls, indent=2))

    def handler(self):
        owner = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = 'HTTP/1.1'

            def log_message(self, *_):
                pass

            def do_POST(self):
                raw = self.rfile.read(int(self.headers['Content-Length']))
                body = json.loads(raw)
                if self.path == '/v1/auto/decide':
                    with owner.lock:
                        if len(owner.calls) >= owner.limit:
                            self.send_response(429)
                            self.send_header('Content-Type','application/json')
                            self.send_header('Connection','close')
                            self.end_headers()
                            self.wfile.write(b'{"error":"Task model-request limit reached; Jev was not called"}')
                            self.close_connection = True
                            return
                        record={'kind':'jev_proxy','task':owner.task,'arm':owner.arm,'session':body['sessionId'],
                                'startedUnix':time.time(),'trigger':body['trigger'],'stateRequestBytes':len(raw)}
                        owner.calls.append(record)
                        case_calls,case_output=owner.calls,owner.output
                    started=time.perf_counter()
                    request=urllib.request.Request('http://127.0.0.1:3435/v1/auto/decide',data=raw,
                        headers={'Content-Type':'application/json','Authorization':'Bearer magpie'})
                    try:
                        try:response=owner.opener.open(request,timeout=60)
                        except urllib.error.HTTPError as error:response=error
                        with response:
                            response_raw=response.read()
                            record['httpStatus']=response.code
                            record['response']=json.loads(response_raw)
                            self.send_response(response.code)
                            self.send_header('Content-Type','application/json')
                            self.send_header('Connection','close')
                            self.end_headers()
                            self.wfile.write(response_raw)
                    except (OSError,ValueError,urllib.error.URLError) as error:
                        record['transportOrParseError']=type(error).__name__+': '+str(error)
                    finally:
                        record['elapsedSeconds']=round(time.perf_counter()-started,4)
                        with owner.lock:case_output.write_text(json.dumps(case_calls,indent=2))
                        self.close_connection=True
                    return
                if self.path != '/v1/chat/completions' or body.get('model') != 'deepseek-v4.1-flash':
                    self.send_error(400, 'Only the fixed DeepSeek execution model is permitted')
                    return
                session = self.headers.get('x-wb-session-id')
                if not session:
                    self.send_error(400, 'Missing native dsh session identity')
                    return
                with owner.lock:
                    if len(owner.calls) >= owner.limit:
                        self.send_error(429, 'Task model-request limit reached')
                        return
                    previous = owner.previous.get(session)
                    messages = body.get('messages', [])
                    old_messages = previous.get('messages', []) if previous else []
                    common = 0
                    for old, new in zip(old_messages, messages):
                        if old != new:
                            break
                        common += 1
                    system = [m for m in messages if m.get('role') == 'system']
                    old_system = [m for m in old_messages if m.get('role') == 'system']
                    record = {
                        'kind':'execution','task':owner.task, 'arm':owner.arm, 'session':session,
                        'requestNumber':sum(c['session'] == session and c['kind']=='execution' for c in owner.calls)+1,
                        'startedUnix':time.time(), 'requestBytes':len(raw),
                        'requestedModel':body['model'], 'maxTokens':body.get('max_tokens'), 'thinking':body.get('thinking'), 'reasoningEffort':body.get('reasoning_effort'),
                        'messageCount':len(messages), 'toolCount':len(body.get('tools', [])),
                        'toolNames':[t['function']['name'] for t in body.get('tools', [])],
                        'commonPrefixMessages':common,
                        'previousMessages':len(old_messages),
                        'systemUnchanged':system == old_system if previous is not None else None,
                        'toolsUnchanged':body.get('tools') == previous.get('tools') if previous is not None else None,
                        'appendOnlyMessages':common == len(old_messages) if previous is not None else None,
                        'purpose':'compaction' if self.headers.get('x-deepseek-harness-compact') == '1' else 'execution',
                    }
                    owner.calls.append(record)
                    owner.previous[session] = body
                    case_calls,case_output=owner.calls,owner.output
                headers = {'Content-Type':'application/json', 'x-wb-session-id':session, 'Accept':'application/json, text/event-stream'}
                request = urllib.request.Request('http://127.0.0.1:8788/v1/chat/completions', data=raw, headers=headers)
                response_raw = bytearray()
                started = time.perf_counter()
                try:
                    try:
                        response = owner.opener.open(request, timeout=180)
                    except urllib.error.HTTPError as error:
                        response = error
                    with response:
                        record['httpStatus'] = response.code
                        account = response.headers.get('x-wb-account')
                        if account is not None:
                            if account not in owner.account_labels: owner.account_labels[account] = f'account-{len(owner.account_labels)+1}'
                            record['executionAccount'] = owner.account_labels[account]
                        content_type = response.headers.get('Content-Type', 'application/json')
                        self.send_response(response.code)
                        self.send_header('Content-Type', content_type)
                        self.send_header('Connection', 'close')
                        self.end_headers()
                        while part := response.read1(65536):
                            response_raw.extend(part)
                            self.wfile.write(part)
                            self.wfile.flush()
                    if 'text/event-stream' in content_type:
                        usage = []
                        for line in response_raw.splitlines():
                            if line.startswith(b'data:') and line[5:].strip() != b'[DONE]':
                                item = json.loads(line[5:])
                                if 'model' in item:record['actualModel']=item['model']
                                if isinstance(item.get('usage'), dict):
                                    usage.append(item['usage'])
                        if usage:
                            record['usage'] = usage[-1]
                            if len(usage) > 1:
                                record['usageEvents'] = usage
                    else:
                        item = json.loads(response_raw)
                        if 'model' in item:record['actualModel']=item['model']
                        if 'usage' in item:
                            record['usage'] = item['usage']
                        if 'error' in item:
                            record['error'] = item['error']
                except (OSError, ValueError, urllib.error.URLError) as error:
                    record['transportOrParseError'] = type(error).__name__+': '+str(error)
                finally:
                    record['elapsedSeconds'] = round(time.perf_counter()-started, 4)
                    with owner.lock:
                        case_output.write_text(json.dumps(case_calls,indent=2))
                    self.close_connection = True

        return Handler

    def start(self, port=3437):
        self.server = ThreadingHTTPServer(('127.0.0.1', port), self.handler())
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()
