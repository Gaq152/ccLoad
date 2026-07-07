import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

function extractFunction(source, name) {
  const start = source.indexOf(`function ${name}(`);
  if (start === -1) {
    throw new Error(`function ${name} not found`);
  }

  const bodyStart = source.indexOf('{', start);
  let depth = 0;
  for (let i = bodyStart; i < source.length; i++) {
    if (source[i] === '{') depth++;
    if (source[i] === '}') depth--;
    if (depth === 0) {
      return source.slice(start, i + 1);
    }
  }

  throw new Error(`function ${name} was not closed`);
}

function loadParseSSEResponse() {
  const source = fs.readFileSync('web/assets/js/monitor.js', 'utf8');
  const fnSource = extractFunction(source, 'parseSSEResponse');
  return vm.runInNewContext(`(${fnSource})`);
}

test('parseSSEResponse extracts Codex function calls from streamed arguments', () => {
  const parseSSEResponse = loadParseSSEResponse();
  const responseBody = [
    'event: response.output_item.added',
    'data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"shell_command","arguments":""}}',
    '',
    'event: response.function_call_arguments.delta',
    'data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\\"command\\":\\"git"}',
    '',
    'event: response.function_call_arguments.delta',
    'data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":" status\\",\\"timeout_ms\\":10000}"}',
    '',
    'event: response.function_call_arguments.done',
    'data: {"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\\"command\\":\\"git status\\",\\"timeout_ms\\":10000}"}',
    '',
    'event: response.output_item.done',
    'data: {"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"shell_command","arguments":"{\\"command\\":\\"git status\\",\\"timeout_ms\\":10000}"}}',
    ''
  ].join('\n');

  const result = parseSSEResponse(responseBody);

  assert.deepEqual(JSON.parse(JSON.stringify(result.toolCalls)), [{
    name: 'shell_command',
    id: 'call_1',
    input: '{"command":"git status","timeout_ms":10000}'
  }]);
  assert.equal(result.reply, '');
  assert.equal(result.thinking, '');
});
