import { createHash } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';

const [inputPath, outputPath] = process.argv.slice(2);
if (!inputPath || !outputPath) throw new Error('usage: node scripts/project-provider-summary.mjs <private-provider-debug.json> <public-summary.json>');

const raw = await readFile(inputPath);
const source = JSON.parse(raw);
if (source.schema_version !== 'pysolate.lab-provider-debug.v1' || source.events?.length !== 14) throw new Error('unexpected private provider trace');

const planning = source.harness_result.planning;
const candidates = Object.fromEntries(source.harness_result.candidates.map((candidate) => [candidate.candidate_id, candidate]));
const executions = Object.fromEntries(source.harness_result.executions.map((execution) => [execution.candidate_id, execution.output]));
const outputs = source.events.filter((event) => event.type === 'model.output');
if (outputs.length !== 4) throw new Error('expected four model outputs');
const outputMetadata = outputs.map((event) => ({
  model: event.body?.model,
  token_usage: {
    prompt: event.body?.usage?.prompt_tokens,
    completion: event.body?.usage?.completion_tokens,
    reasoning_tokens: event.body?.usage?.completion_tokens_details?.reasoning_tokens,
  },
}));

const calls = [
  {
    id: 'candidate-brighton', ordinal: 1, actor: 'brighton', phase: 'candidate-generation', purpose: 'Generate constrained Brighton Python',
    ...outputMetadata[0],
    input: { planning, candidate_id: 'brighton', required_tool_calls: ['travel.weather("brighton")', 'travel.rail("brighton", travellers=2)', 'travel.attractions("brighton")'], output_contract: 'pysolate.day-trip-candidate.v1' },
    thinking_summary: ['Check the candidate-only JSON contract.', 'Emit each allowlisted travel read exactly once.', 'Compute the two-traveller total in Python instead of inventing fixture values.'],
    output: candidates.brighton,
  },
  {
    id: 'candidate-oxford', ordinal: 2, actor: 'oxford', phase: 'candidate-generation', purpose: 'Generate constrained Oxford Python',
    ...outputMetadata[1],
    input: { planning, candidate_id: 'oxford', required_tool_calls: ['travel.weather("oxford")', 'travel.rail("oxford", travellers=2)', 'travel.attractions("oxford")'], output_contract: 'pysolate.day-trip-candidate.v1' },
    thinking_summary: ['Check the candidate-only JSON contract.', 'Emit each allowlisted travel read exactly once.', 'Leave observed values to the deterministic runtime fixture.'],
    output: candidates.oxford,
  },
  {
    id: 'main-selection', ordinal: 3, actor: 'main', phase: 'selection', purpose: 'Select one observed candidate',
    ...outputMetadata[2],
    input: { budget_gbp: 100, travellers: 2, candidate_outputs: executions },
    thinking_summary: ['Compare the observed candidate totals against the fixed £100 budget.', 'Brighton is £118.40 and is rejected.', 'Oxford is £78.00 and remains within budget.'],
    output: source.harness_result.selection,
  },
  {
    id: 'main-final', ordinal: 4, actor: 'main', phase: 'final-response', purpose: 'Format the selected itinerary',
    ...outputMetadata[3],
    input: { selection: source.harness_result.selection, selected_observation: executions.oxford },
    thinking_summary: ['Use only the selected Oxford observation.', 'Preserve the observed rail times, Saturday opening hours, and £78.00 total.', 'Return the bounded final-response contract.'],
    output: source.harness_result.final,
  },
];

const projection = {
  schema_version: 'pysolate.lab-provider-summary.v1',
  source_trace_sha256: `sha256:${createHash('sha256').update(raw).digest('hex')}`,
  disclosure: 'Processed public projection. Raw provider request IDs, headers, context bodies, and reasoning are omitted.',
  calls,
};
const forbidden = /reasoning_content|request_id|authorization|bearer|system_fingerprint|trace_id/i;
const encoded = `${JSON.stringify(projection, null, 2)}\n`;
if (forbidden.test(encoded)) throw new Error('public provider summary contains a forbidden field');
await writeFile(outputPath, encoded);
console.log(`wrote ${outputPath} (${calls.length} calls)`);
