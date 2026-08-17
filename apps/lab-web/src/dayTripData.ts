export type DestinationID = 'brighton' | 'oxford';

export interface DayTripToolContract {
  name: string;
  kind: string;
  latency_ms: number;
}

export interface DayTripWeather {
  forecast: string;
  high_c: number;
  rain_chance_pct: number;
}

export interface DayTripLeg {
  departure: string;
  arrival: string;
  cost_gbp: number;
}

export interface DayTripRail {
  outbound: DayTripLeg;
  return: DayTripLeg;
  total_cost_gbp: number;
  currency: 'GBP';
}

export interface DayTripAttraction {
  name: string;
  open_saturday: boolean;
  opening_hours: string;
  entry_cost_gbp: number;
}

export interface DayTripApiFixture {
  schema_version: 'pysolate.day-trip-api-fixture.v1';
  weather: Record<DestinationID, DayTripWeather>;
  rail: Record<DestinationID, DayTripRail>;
  attractions: Record<DestinationID, DayTripAttraction>;
  delays: Record<DestinationID, number>;
  api_latency_ms: {
    attractions: number;
    rail: number;
    weather: number;
  };
}

export interface DayTripWorkspaceRequest {
  schema_version: 'pysolate.day-trip-workspace-request.v1';
  origin: string;
  day: string;
  travellers: number;
  budget_gbp: number;
  candidate_ids: DestinationID[];
  required_checks: string[];
}

export interface DayTripWorkspaceSnapshot {
  request: DayTripWorkspaceRequest;
  api_fixture: DayTripApiFixture;
}

export interface DayTripInput {
  task_summary: string;
  public_system_instructions: string;
  skills: Array<{ id: string; body: string }>;
  tool_contracts: DayTripToolContract[];
  workspace_snapshot: DayTripWorkspaceSnapshot;
  private_fields_withheld: string[];
}

export interface DayTripModelOutput {
  summary: string;
  python_source: string;
  capture: string;
}

export interface DayTripApiWait {
  capability: 'travel.weather' | 'travel.rail' | 'travel.attractions';
  latency_ms: number;
  observed: DayTripWeather | DayTripRail | DayTripAttraction;
}

export interface DayTripObservedOutput {
  candidate_id: DestinationID;
  weather: DayTripWeather;
  rail: DayTripRail;
  attraction: DayTripAttraction;
  total_cost_gbp: number;
}

export interface DayTripRuntime {
  execution: 'fresh isolated WASI Guest';
  api_waits: [DayTripApiWait, DayTripApiWait, DayTripApiWait];
  observed_output: DayTripObservedOutput;
  workspace_sha256: string;
}

export interface DayTripAgent {
  id: DestinationID;
  label: string;
  role: 'candidate';
  model_output: DayTripModelOutput;
  runtime: DayTripRuntime;
  disposition: 'selected' | 'discarded';
}

export interface DayTripGroup {
  id: 'input' | 'candidates' | 'runtime' | 'decision' | 'output';
  label: string;
  icon: 'inbox' | 'split' | 'terminal' | 'check' | 'flag';
  summary: string;
}

export interface DayTripDecision {
  model_output: {
    schema_version: 'pysolate.day-trip-selection.v1';
    selected_candidate_id: DestinationID;
    justification: string;
  };
  selected_candidate_id: DestinationID;
  discarded_candidate_ids: [DestinationID];
  selected_root_sha256: string;
}

export interface DayTripFinalOutput {
  schema_version: 'pysolate.day-trip-final.v1';
  selected_candidate_id: DestinationID;
  itinerary: string;
  total_cost_gbp: number;
}

export interface DayTripPrivacy {
  public_projection: string;
  private_recording: string;
  credentials: string;
}

export interface DayTripSnapshot {
  schema_version: 'pysolate.public-day-trip.v1';
  source_commit: string;
  title: string;
  subtitle: string;
  provider: {
    name: string;
    model: string;
    candidate_outputs: string;
    selection_and_final: string;
    raw_envelopes: 'withheld';
    reasoning_content: 'withheld';
  };
  input: DayTripInput;
  groups: [DayTripGroup, DayTripGroup, DayTripGroup, DayTripGroup, DayTripGroup];
  agents: [DayTripAgent, DayTripAgent];
  decision: DayTripDecision;
  final_output: DayTripFinalOutput;
  privacy: DayTripPrivacy;
  artifact_sha256: string;
}

const digest = /^sha256:[0-9a-f]{64}$/;
const commit = /^[0-9a-f]{40}$/;
const clock = /^\d{2}:\d{2}$/;
const destinationIDs: DestinationID[] = ['brighton', 'oxford'];
const groupContracts: Array<Pick<DayTripGroup, 'id' | 'label' | 'icon'>> = [
  { id: 'input', label: 'Public input', icon: 'inbox' },
  { id: 'candidates', label: 'Candidate Agents', icon: 'split' },
  { id: 'runtime', label: 'Fresh Guest execution', icon: 'terminal' },
  { id: 'decision', label: 'Main Agent decision', icon: 'check' },
  { id: 'output', label: 'Final output', icon: 'flag' },
];
const waitContracts: DayTripApiWait['capability'][] = ['travel.weather', 'travel.rail', 'travel.attractions'];
const privateMarkers = ['/users/', '/home/', '\\\\users\\\\', '.hermes', 'file://', 'private://', 'provider_request', 'provider_response', 'trace_body', 'workspace_body'];

function object(value: unknown, label: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`);
  return value as Record<string, unknown>;
}

function exactKeys(value: Record<string, unknown>, keys: string[], label: string) {
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new Error(`${label} has unknown or missing fields`);
}

function text(value: unknown, label: string): string {
  if (typeof value !== 'string' || value.length === 0) throw new Error(`${label} must be a non-empty string`);
  return value;
}

function finiteNumber(value: unknown, label: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new Error(`${label} must be a finite number`);
  return value;
}

function integer(value: unknown, label: string): number {
  const result = finiteNumber(value, label);
  if (!Number.isInteger(result)) throw new Error(`${label} must be an integer`);
  return result;
}

function stringArray(value: unknown, label: string): string[] {
  if (!Array.isArray(value) || value.some((item) => typeof item !== 'string' || item.length === 0)) throw new Error(`${label} is invalid`);
  return value as string[];
}

function exactDestinationArray(value: unknown, label: string): DestinationID[] {
  if (!Array.isArray(value) || value.length !== destinationIDs.length || value.some((item, index) => item !== destinationIDs[index])) throw new Error(`${label} must contain Brighton and Oxford exactly once`);
  return value as DestinationID[];
}

function assertDigest(value: unknown, label: string): string {
  const result = text(value, label);
  if (!digest.test(result)) throw new Error(`${label} is not a SHA-256 digest`);
  return result;
}

function assertCommit(value: unknown, label: string): string {
  const result = text(value, label);
  if (!/^[0-9a-f]{40}$/.test(result)) throw new Error(`${label} is not a commit SHA`);
  return result;
}

function parseWeather(value: unknown, label: string): DayTripWeather {
  const raw = object(value, label);
  exactKeys(raw, ['forecast', 'high_c', 'rain_chance_pct'], label);
  const high = integer(raw.high_c, `${label}.high_c`);
  const rain = integer(raw.rain_chance_pct, `${label}.rain_chance_pct`);
  if (high < -80 || high > 80 || rain < 0 || rain > 100) throw new Error(`${label} has impossible weather values`);
  return { forecast: text(raw.forecast, `${label}.forecast`), high_c: high, rain_chance_pct: rain };
}

function parseLeg(value: unknown, label: string): DayTripLeg {
  const raw = object(value, label);
  exactKeys(raw, ['departure', 'arrival', 'cost_gbp'], label);
  const departure = text(raw.departure, `${label}.departure`);
  const arrival = text(raw.arrival, `${label}.arrival`);
  const cost = finiteNumber(raw.cost_gbp, `${label}.cost_gbp`);
  if (!clock.test(departure) || !clock.test(arrival) || cost < 0) throw new Error(`${label} has invalid time or cost`);
  return { departure, arrival, cost_gbp: cost };
}

function parseRail(value: unknown, label: string): DayTripRail {
  const raw = object(value, label);
  exactKeys(raw, ['outbound', 'return', 'total_cost_gbp', 'currency'], label);
  const outbound = parseLeg(raw.outbound, `${label}.outbound`);
  const returnLeg = parseLeg(raw.return, `${label}.return`);
  const total = finiteNumber(raw.total_cost_gbp, `${label}.total_cost_gbp`);
  if (raw.currency !== 'GBP' || total < 0 || Math.abs(total - outbound.cost_gbp - returnLeg.cost_gbp) > 0.001) throw new Error(`${label} total or currency is inconsistent`);
  return { outbound, return: returnLeg, total_cost_gbp: total, currency: 'GBP' };
}

function parseAttraction(value: unknown, label: string): DayTripAttraction {
  const raw = object(value, label);
  exactKeys(raw, ['name', 'open_saturday', 'opening_hours', 'entry_cost_gbp'], label);
  const cost = finiteNumber(raw.entry_cost_gbp, `${label}.entry_cost_gbp`);
  if (typeof raw.open_saturday !== 'boolean' || cost < 0 || !clock.test(text(raw.opening_hours, `${label}.opening_hours`).split('-')[0]) || !clock.test(text(raw.opening_hours, `${label}.opening_hours`).split('-')[1] ?? '')) throw new Error(`${label} is invalid`);
  return {
    name: text(raw.name, `${label}.name`),
    open_saturday: raw.open_saturday,
    opening_hours: raw.opening_hours as string,
    entry_cost_gbp: cost,
  };
}

function parseApiFixture(value: unknown): DayTripApiFixture {
  const raw = object(value, 'workspace api fixture');
  exactKeys(raw, ['schema_version', 'weather', 'rail', 'attractions', 'delays', 'api_latency_ms'], 'workspace api fixture');
  if (raw.schema_version !== 'pysolate.day-trip-api-fixture.v1') throw new Error('workspace api fixture schema is invalid');
  const weatherRaw = object(raw.weather, 'weather');
  const railRaw = object(raw.rail, 'rail');
  const attractionsRaw = object(raw.attractions, 'attractions');
  exactKeys(weatherRaw, destinationIDs, 'weather');
  exactKeys(railRaw, destinationIDs, 'rail');
  exactKeys(attractionsRaw, destinationIDs, 'attractions');
  const weather: Record<DestinationID, DayTripWeather> = {
    brighton: parseWeather(weatherRaw.brighton, 'weather.brighton'),
    oxford: parseWeather(weatherRaw.oxford, 'weather.oxford'),
  };
  const rail: Record<DestinationID, DayTripRail> = {
    brighton: parseRail(railRaw.brighton, 'rail.brighton'),
    oxford: parseRail(railRaw.oxford, 'rail.oxford'),
  };
  const attractions: Record<DestinationID, DayTripAttraction> = {
    brighton: parseAttraction(attractionsRaw.brighton, 'attractions.brighton'),
    oxford: parseAttraction(attractionsRaw.oxford, 'attractions.oxford'),
  };
  const delaysRaw = object(raw.delays, 'delays');
  exactKeys(delaysRaw, destinationIDs, 'delays');
  const delays = { brighton: integer(delaysRaw.brighton, 'delays.brighton'), oxford: integer(delaysRaw.oxford, 'delays.oxford') };
  if (Object.values(delays).some((delay) => delay < 0)) throw new Error('delays must not be negative');
  const latencyRaw = object(raw.api_latency_ms, 'api latency');
  exactKeys(latencyRaw, ['attractions', 'rail', 'weather'], 'api latency');
  const api_latency_ms = {
    attractions: integer(latencyRaw.attractions, 'api latency attractions'),
    rail: integer(latencyRaw.rail, 'api latency rail'),
    weather: integer(latencyRaw.weather, 'api latency weather'),
  };
  if (Object.values(api_latency_ms).some((latency) => latency < 0)) throw new Error('api latency must not be negative');
  return { schema_version: 'pysolate.day-trip-api-fixture.v1', weather, rail, attractions, delays, api_latency_ms };
}

function parseWorkspace(value: unknown): DayTripWorkspaceSnapshot {
  const raw = object(value, 'workspace snapshot');
  exactKeys(raw, ['request', 'api_fixture'], 'workspace snapshot');
  const requestRaw = object(raw.request, 'workspace request');
  exactKeys(requestRaw, ['schema_version', 'origin', 'day', 'travellers', 'budget_gbp', 'candidate_ids', 'required_checks'], 'workspace request');
  if (requestRaw.schema_version !== 'pysolate.day-trip-workspace-request.v1') throw new Error('workspace request schema is invalid');
  const request: DayTripWorkspaceRequest = {
    schema_version: 'pysolate.day-trip-workspace-request.v1',
    origin: text(requestRaw.origin, 'workspace request origin'),
    day: text(requestRaw.day, 'workspace request day'),
    travellers: integer(requestRaw.travellers, 'workspace request travellers'),
    budget_gbp: finiteNumber(requestRaw.budget_gbp, 'workspace request budget'),
    candidate_ids: exactDestinationArray(requestRaw.candidate_ids, 'workspace request candidates'),
    required_checks: stringArray(requestRaw.required_checks, 'workspace request checks'),
  };
  if (request.travellers < 1 || request.budget_gbp < 0) throw new Error('workspace request has invalid constraints');
  return { request, api_fixture: parseApiFixture(raw.api_fixture) };
}

function parseInput(value: unknown): DayTripInput {
  const raw = object(value, 'public input');
  exactKeys(raw, ['task_summary', 'public_system_instructions', 'skills', 'tool_contracts', 'workspace_snapshot', 'private_fields_withheld'], 'public input');
  if (!Array.isArray(raw.skills) || raw.skills.length !== 3) throw new Error('public input must contain exactly three skills');
  const skillIDs = new Set<string>();
  const skills = raw.skills.map((item, index) => {
    const skill = object(item, `skill ${index + 1}`);
    exactKeys(skill, ['id', 'body'], 'skill');
    const id = text(skill.id, 'skill id');
    if (skillIDs.has(id)) throw new Error('skill IDs must be unique');
    skillIDs.add(id);
    return { id, body: text(skill.body, `skill ${id} body`) };
  });
  if (!Array.isArray(raw.tool_contracts) || raw.tool_contracts.length !== 3) throw new Error('public input must contain exactly three tool contracts');
  const contractIDs = new Set<string>();
  const tool_contracts = raw.tool_contracts.map((item, index) => {
    const contract = object(item, `tool contract ${index + 1}`);
    exactKeys(contract, ['name', 'kind', 'latency_ms'], 'tool contract');
    const name = text(contract.name, 'tool contract name');
    if (contractIDs.has(name)) throw new Error('tool contract names must be unique');
    contractIDs.add(name);
    const latency = integer(contract.latency_ms, `tool contract ${name} latency`);
    if (latency < 0) throw new Error('tool contract latency must not be negative');
    return { name, kind: text(contract.kind, `tool contract ${name} kind`), latency_ms: latency };
  });
  return {
    task_summary: text(raw.task_summary, 'task summary'),
    public_system_instructions: text(raw.public_system_instructions, 'public system instructions'),
    skills,
    tool_contracts,
    workspace_snapshot: parseWorkspace(raw.workspace_snapshot),
    private_fields_withheld: stringArray(raw.private_fields_withheld, 'private fields withheld'),
  };
}

function parseAgent(value: unknown, label: string): DayTripAgent {
  const raw = object(value, label);
  exactKeys(raw, ['id', 'label', 'role', 'model_output', 'runtime', 'disposition'], label);
  if (raw.id !== 'brighton' && raw.id !== 'oxford' || raw.role !== 'candidate' || raw.disposition !== 'selected' && raw.disposition !== 'discarded') throw new Error(`${label} identity is invalid`);
  const model = object(raw.model_output, `${label} model output`);
  exactKeys(model, ['summary', 'python_source', 'capture'], `${label} model output`);
  const runtimeRaw = object(raw.runtime, `${label} runtime`);
  exactKeys(runtimeRaw, ['execution', 'api_waits', 'observed_output', 'workspace_sha256'], `${label} runtime`);
  if (runtimeRaw.execution !== 'fresh isolated WASI Guest' || !Array.isArray(runtimeRaw.api_waits) || runtimeRaw.api_waits.length !== 3) throw new Error(`${label} runtime is invalid`);
  const api_waits = runtimeRaw.api_waits.map((item, index) => {
    const wait = object(item, `${label} API wait ${index + 1}`);
    exactKeys(wait, ['capability', 'latency_ms', 'observed'], `${label} API wait`);
    const capability = text(wait.capability, `${label} capability`) as DayTripApiWait['capability'];
    if (!waitContracts.includes(capability)) throw new Error(`${label} capability is invalid`);
    return { capability, latency_ms: integer(wait.latency_ms, `${label} API wait latency`), observed: wait.observed } as DayTripApiWait;
  }) as [DayTripApiWait, DayTripApiWait, DayTripApiWait];
  return {
    id: raw.id,
    label: text(raw.label, `${label} label`),
    role: 'candidate',
    model_output: { summary: text(model.summary, `${label} summary`), python_source: text(model.python_source, `${label} Python`), capture: text(model.capture, `${label} capture`) },
    runtime: {
      execution: 'fresh isolated WASI Guest',
      api_waits,
      observed_output: parseObservedOutput(runtimeRaw.observed_output, `${label} observed output`),
      workspace_sha256: assertDigest(runtimeRaw.workspace_sha256, `${label} workspace SHA`),
    },
    disposition: raw.disposition,
  };
}

function parseObservedOutput(value: unknown, label: string): DayTripObservedOutput {
  const raw = object(value, label);
  exactKeys(raw, ['candidate_id', 'weather', 'rail', 'attraction', 'total_cost_gbp'], label);
  if (raw.candidate_id !== 'brighton' && raw.candidate_id !== 'oxford') throw new Error(`${label} candidate is invalid`);
  return {
    candidate_id: raw.candidate_id,
    weather: parseWeather(raw.weather, `${label}.weather`),
    rail: parseRail(raw.rail, `${label}.rail`),
    attraction: parseAttraction(raw.attraction, `${label}.attraction`),
    total_cost_gbp: finiteNumber(raw.total_cost_gbp, `${label}.total_cost_gbp`),
  };
}

function parseGroups(value: unknown): DayTripSnapshot['groups'] {
  if (!Array.isArray(value) || value.length !== groupContracts.length) throw new Error('day-trip groups are incomplete');
  const groups = value.map((item, index) => {
    const raw = object(item, `day-trip group ${index + 1}`);
    exactKeys(raw, ['id', 'label', 'icon', 'summary'], 'day-trip group');
    const contract = groupContracts[index];
    if (raw.id !== contract.id || raw.label !== contract.label || raw.icon !== contract.icon) throw new Error('day-trip group order or icon contract drifted');
    return { id: contract.id, label: contract.label, icon: contract.icon, summary: text(raw.summary, 'day-trip group summary') };
  });
  return groups as DayTripSnapshot['groups'];
}

function assertUniqueJSONKeys(raw: string) {
  let offset = 0;
  const whitespace = () => { while (/\s/.test(raw[offset] ?? '')) offset += 1; };
  const string = () => {
    whitespace();
    if (raw[offset] !== '"') throw new Error('invalid day-trip JSON');
    const start = offset++;
    while (offset < raw.length) {
      if (raw[offset] === '\\') { offset += 2; continue; }
      if (raw[offset++] === '"') return JSON.parse(raw.slice(start, offset)) as string;
    }
    throw new Error('invalid day-trip JSON');
  };
  const value = (): void => {
    whitespace();
    if (raw[offset] === '{') { objectValue(); return; }
    if (raw[offset] === '[') { arrayValue(); return; }
    if (raw[offset] === '"') { string(); return; }
    const start = offset;
    while (offset < raw.length && !/[\s,}\]]/.test(raw[offset])) offset += 1;
    if (start === offset) throw new Error('invalid day-trip JSON');
  };
  const objectValue = () => {
    offset += 1;
    const keys = new Set<string>();
    whitespace();
    if (raw[offset] === '}') { offset += 1; return; }
    while (offset < raw.length) {
      const key = string();
      if (keys.has(key)) throw new Error(`day-trip contains duplicate JSON key: ${key}`);
      keys.add(key);
      whitespace();
      if (raw[offset++] !== ':') throw new Error('invalid day-trip JSON');
      value();
      whitespace();
      if (raw[offset] === '}') { offset += 1; return; }
      if (raw[offset++] !== ',') throw new Error('invalid day-trip JSON');
    }
    throw new Error('invalid day-trip JSON');
  };
  const arrayValue = () => {
    offset += 1;
    whitespace();
    if (raw[offset] === ']') { offset += 1; return; }
    while (offset < raw.length) {
      value();
      whitespace();
      if (raw[offset] === ']') { offset += 1; return; }
      if (raw[offset++] !== ',') throw new Error('invalid day-trip JSON');
    }
    throw new Error('invalid day-trip JSON');
  };
  value();
  whitespace();
  if (offset !== raw.length) throw new Error('invalid day-trip JSON');
}

function artifactDocument(snapshot: DayTripSnapshot): ArrayBuffer {
  const clone = JSON.parse(JSON.stringify(snapshot)) as DayTripSnapshot;
  delete (clone as Partial<DayTripSnapshot>).artifact_sha256;
  const encoded = JSON.stringify(clone).replace(/[<>&\u2028\u2029]/g, (character) => `\\u${character.charCodeAt(0).toString(16).padStart(4, '0')}`);
  const bytes = new TextEncoder().encode(encoded);
  return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
}

async function artifactSHA256(snapshot: DayTripSnapshot): Promise<string> {
  if (!globalThis.crypto?.subtle) throw new Error('Web Crypto is required to verify the day-trip fixture');
  const hash = await globalThis.crypto.subtle.digest('SHA-256', artifactDocument(snapshot));
  return `sha256:${Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, '0')).join('')}`;
}

function validateDayTripShape(value: unknown): DayTripSnapshot {
  const root = object(value, 'day-trip fixture');
  exactKeys(root, ['schema_version', 'source_commit', 'title', 'subtitle', 'provider', 'input', 'groups', 'agents', 'decision', 'final_output', 'privacy', 'artifact_sha256'], 'day-trip fixture');
  if (root.schema_version !== 'pysolate.public-day-trip.v1' || !commit.test(text(root.source_commit, 'source commit'))) throw new Error('day-trip schema or source commit is invalid');
  assertDigest(root.artifact_sha256, 'artifact SHA');
  if (privateMarkers.some((marker) => JSON.stringify(root).toLowerCase().includes(marker))) throw new Error('day-trip fixture contains a private body or path marker');
  const input = parseInput(root.input);
  const groups = parseGroups(root.groups);
  if (!Array.isArray(root.agents) || root.agents.length !== 2) throw new Error('day-trip requires exactly two candidate Agents');
  const agents = root.agents.map((item, index) => parseAgent(item, `candidate Agent ${index + 1}`)) as [DayTripAgent, DayTripAgent];
  const agentIDs = agents.map((agent) => agent.id);
  if (new Set(agentIDs).size !== agents.length || agentIDs.some((id, index) => id !== input.workspace_snapshot.request.candidate_ids[index])) throw new Error('candidate IDs must be unique and match the public request');
  const decisionRaw = object(root.decision, 'main Agent decision');
  exactKeys(decisionRaw, ['model_output', 'selected_candidate_id', 'discarded_candidate_ids', 'selected_root_sha256'], 'main Agent decision');
  const decisionModel = object(decisionRaw.model_output, 'main Agent model output');
  exactKeys(decisionModel, ['schema_version', 'selected_candidate_id', 'justification'], 'main Agent model output');
  if (decisionModel.schema_version !== 'pysolate.day-trip-selection.v1' || decisionModel.selected_candidate_id !== decisionRaw.selected_candidate_id) throw new Error('main Agent selection output is invalid');
  const selected = decisionRaw.selected_candidate_id;
  if (selected !== 'brighton' && selected !== 'oxford') throw new Error('main Agent selected candidate is unknown');
  const discarded = decisionRaw.discarded_candidate_ids;
  if (!Array.isArray(discarded) || discarded.length !== 1 || discarded[0] === selected || discarded[0] !== destinationIDs.find((id) => id !== selected)) throw new Error('selected/discarded candidate IDs are inconsistent');
  const decision: DayTripDecision = {
    model_output: { schema_version: 'pysolate.day-trip-selection.v1', selected_candidate_id: selected, justification: text(decisionModel.justification, 'main Agent justification') },
    selected_candidate_id: selected,
    discarded_candidate_ids: discarded as [DestinationID],
    selected_root_sha256: assertDigest(decisionRaw.selected_root_sha256, 'selected root SHA'),
  };
  const selectedAgents = agents.filter((agent) => agent.disposition === 'selected');
  const discardedAgents = agents.filter((agent) => agent.disposition === 'discarded');
  if (selectedAgents.length !== 1 || discardedAgents.length !== 1 || selectedAgents[0].id !== selected || discardedAgents[0].id !== discarded[0]) throw new Error('candidate selected/discarded totals are inconsistent');
  for (const agent of agents) {
    if (agent.runtime.observed_output.candidate_id !== agent.id || !agent.model_output.python_source.toLowerCase().includes(agent.id)) throw new Error(`${agent.id} model/runtime output is inconsistent`);
    const expected = input.workspace_snapshot.api_fixture.rail[agent.id].total_cost_gbp + input.workspace_snapshot.api_fixture.attractions[agent.id].entry_cost_gbp * input.workspace_snapshot.request.travellers;
    if (Math.abs(agent.runtime.observed_output.total_cost_gbp - expected) > 0.001 || Math.abs(agent.runtime.observed_output.rail.total_cost_gbp - input.workspace_snapshot.api_fixture.rail[agent.id].total_cost_gbp) > 0.001) throw new Error(`${agent.id} observed total is inconsistent`);
    if (agent.runtime.api_waits.map((wait) => wait.capability).some((capability, index) => capability !== waitContracts[index])) throw new Error(`${agent.id} API wait order is inconsistent`);
    const expectedWaits: unknown[] = [input.workspace_snapshot.api_fixture.weather[agent.id], input.workspace_snapshot.api_fixture.rail[agent.id], input.workspace_snapshot.api_fixture.attractions[agent.id]];
    agent.runtime.api_waits.forEach((wait, index) => {
      if (wait.latency_ms !== input.workspace_snapshot.api_fixture.api_latency_ms[waitContracts[index].split('.')[1] as 'weather' | 'rail' | 'attractions'] || JSON.stringify(wait.observed) !== JSON.stringify(expectedWaits[index])) throw new Error(`${agent.id} API wait/result is inconsistent`);
    });
  }
  const finalRaw = object(root.final_output, 'final output');
  exactKeys(finalRaw, ['schema_version', 'selected_candidate_id', 'itinerary', 'total_cost_gbp'], 'final output');
  const finalTotal = finiteNumber(finalRaw.total_cost_gbp, 'final output total');
  if (finalRaw.schema_version !== 'pysolate.day-trip-final.v1' || finalRaw.selected_candidate_id !== selected || finalTotal !== selectedAgents[0].runtime.observed_output.total_cost_gbp) throw new Error('final output total or candidate is inconsistent');
  const final_output: DayTripFinalOutput = { schema_version: 'pysolate.day-trip-final.v1', selected_candidate_id: selected, itinerary: text(finalRaw.itinerary, 'final itinerary'), total_cost_gbp: finalTotal };
  const privacyRaw = object(root.privacy, 'privacy projection');
  exactKeys(privacyRaw, ['public_projection', 'private_recording', 'credentials'], 'privacy projection');
  const privacy: DayTripPrivacy = { public_projection: text(privacyRaw.public_projection, 'public projection'), private_recording: text(privacyRaw.private_recording, 'private recording'), credentials: text(privacyRaw.credentials, 'credentials') };
  return {
    schema_version: 'pysolate.public-day-trip.v1',
    source_commit: assertCommit(root.source_commit, 'source commit'),
    title: text(root.title, 'day-trip title'),
    subtitle: text(root.subtitle, 'day-trip subtitle'),
    provider: (() => { const provider = object(root.provider, 'provider'); exactKeys(provider, ['name', 'model', 'candidate_outputs', 'selection_and_final', 'raw_envelopes', 'reasoning_content'], 'provider'); if (provider.raw_envelopes !== 'withheld' || provider.reasoning_content !== 'withheld') throw new Error('provider private fields must be withheld'); return { name: text(provider.name, 'provider name'), model: text(provider.model, 'provider model'), candidate_outputs: text(provider.candidate_outputs, 'candidate output provenance'), selection_and_final: text(provider.selection_and_final, 'selection provenance'), raw_envelopes: 'withheld', reasoning_content: 'withheld' }; })(),
    input,
    groups,
    agents,
    decision,
    final_output,
    privacy,
    artifact_sha256: assertDigest(root.artifact_sha256, 'artifact SHA'),
  };
}

export async function validateDayTripSnapshot(value: unknown): Promise<DayTripSnapshot> {
  const snapshot = validateDayTripShape(value);
  if (await artifactSHA256(snapshot) !== snapshot.artifact_sha256) throw new Error('day-trip artifact SHA mismatch');
  return snapshot;
}

export async function loadDayTripSnapshot(url = '/lab-data/day-trip.json'): Promise<DayTripSnapshot> {
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) throw new Error(`day-trip fixture load failed (${response.status})`);
  const raw = await response.text();
  assertUniqueJSONKeys(raw);
  return validateDayTripSnapshot(JSON.parse(raw));
}
