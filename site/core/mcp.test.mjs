import assert from "node:assert/strict";
import { test } from "node:test";
import { createMcpHandler, readConfig, VERSION } from "./mcp.mjs";

const CONFIG = readConfig({ HOME_API_URL: "https://homie.example/", HOME_API_KEY: "secret-key-0123456789" });

const READ = { readOnlyHint: true, destructiveHint: false, openWorldHint: false };
const WRITE = { readOnlyHint: false, destructiveHint: false, idempotentHint: true, openWorldHint: false };
const MANIFEST = {
  instructions: "Living room devices.",
  tools: [
    { name: "tv_status", description: "Current state of the TV.", inputSchema: { type: "object", properties: {} }, annotations: READ, extra: "dropped" },
    { name: "tv_set_volume", description: "Set the TV volume.", inputSchema: { type: "object", properties: { level: { type: "integer" } } }, annotations: WRITE },
  ],
};

const json = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body });

// What frps serves when no home is connected: an HTML page, so the body is not JSON.
const offlinePage = (status) => ({ ok: false, status, json: async () => { throw new SyntaxError("Unexpected token '<'"); } });

// A fake backend: records the requests, answers from a table of "METHOD /path".
function backend(table, calls = []) {
  const fetchImpl = async (url, options = {}) => {
    calls.push({ url, method: options.method ?? "GET", headers: options.headers, body: options.body });
    const key = `${options.method ?? "GET"} ${new URL(url).pathname}`;
    const answer = table[key];
    if (answer instanceof Error) throw answer;
    if (typeof answer === "function") return answer(options);
    return answer ?? json(404, { error: "unknown tool" });
  };
  return { fetchImpl, calls };
}

const handler = (table, extra = {}) => {
  const fake = backend(table, extra.calls);
  return createMcpHandler({ config: extra.config ?? CONFIG, fetchImpl: fake.fetchImpl, now: extra.now ?? (() => 0), cache: {} });
};
const rpc = (method, params, id = 1) => ({ jsonrpc: "2.0", id, method, params });
const GOOD = { "GET /api/v1/tools": json(200, MANIFEST) };

test("the settings are trimmed and the url loses its trailing slash", () => {
  assert.deepEqual(readConfig({ HOME_API_URL: " https://x.example// ", HOME_API_KEY: "k", HOME_LANGUAGE: " ru " }), { apiUrl: "https://x.example", apiKey: "k", language: "ru" });
  assert.deepEqual(readConfig({}), { apiUrl: "", apiKey: "", language: "" });
});

test("initialize negotiates the protocol, carries the instructions and a version that fingerprints the tools", async () => {
  const first = (await handler(GOOD)(rpc("initialize", { protocolVersion: "2025-06-18" }))).result;
  assert.equal(first.protocolVersion, "2025-06-18");
  assert.equal(first.instructions, "Living room devices.");
  assert.match(first.serverInfo.version, new RegExp(`^${VERSION.replaceAll(".", "\\.")}\\+[0-9a-f]{8}$`));
  assert.deepEqual(first.capabilities, { tools: {} });
  const unknown = (await handler(GOOD)(rpc("initialize", { protocolVersion: "1999-01-01" }))).result;
  assert.equal(unknown.protocolVersion, "2025-06-18");
  const changed = { ...MANIFEST, tools: [{ ...MANIFEST.tools[0], description: "Current state of the TV, reworded." }, MANIFEST.tools[1]] };
  const second = (await handler({ "GET /api/v1/tools": json(200, changed) })(rpc("initialize", {}))).result;
  assert.notEqual(first.serverInfo.version, second.serverInfo.version);
});

test("the language setting is added to the instructions", async () => {
  const config = readConfig({ HOME_API_URL: "https://homie.example", HOME_API_KEY: "k".repeat(32), HOME_LANGUAGE: "ru" });
  const result = (await handler(GOOD, { config })(rpc("initialize", {}))).result;
  assert.equal(result.instructions, "Living room devices. Speak to the user in ru.");
});

test("tools/list hands over each tool as the service describes it and nothing else", async () => {
  const { tools } = (await handler(GOOD)(rpc("tools/list"))).result;
  assert.deepEqual(tools.map((t) => t.name), ["tv_status", "tv_set_volume"]);
  assert.equal(tools[0].annotations.readOnlyHint, true);
  assert.equal(tools[1].annotations.readOnlyHint, false);
  assert.equal("extra" in tools[0], false);
});

test("the service is asked with the bearer key and the manifest is cached for a minute", async () => {
  const calls = [];
  let clock = 0;
  const handle = handler(GOOD, { calls, now: () => clock });
  await handle(rpc("tools/list"));
  await handle(rpc("tools/list"));
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "https://homie.example/api/v1/tools");
  assert.equal(calls[0].headers.authorization, "Bearer secret-key-0123456789");
  clock = 61_000;
  await handle(rpc("initialize", {}));
  assert.equal(calls.length, 2);
});

test("a call is forwarded with its arguments and comes back as text plus structured content", async () => {
  const calls = [];
  const table = { ...GOOD, "POST /api/v1/tools/tv_status": json(200, { power: "on", volume: 12 }) };
  const reply = (await handler(table, { calls })(rpc("tools/call", { name: "tv_status", arguments: { a: 1 } }))).result;
  assert.deepEqual(reply.structuredContent, { power: "on", volume: 12 });
  assert.equal(reply.content[0].type, "text");
  assert.deepEqual(JSON.parse(reply.content[0].text), { power: "on", volume: 12 });
  assert.equal(reply.isError, undefined);
  assert.equal(calls[0].method, "POST");
  assert.equal(calls[0].body, JSON.stringify({ a: 1 }));
});

test("a tool name is encoded in the path and arguments default to an empty object", async () => {
  const calls = [];
  const table = { "POST /api/v1/tools/a%2Fb": json(200, {}) };
  await handler(table, { calls })(rpc("tools/call", { name: "a/b" }));
  assert.equal(new URL(calls[0].url).pathname, "/api/v1/tools/a%2Fb");
  assert.equal(calls[0].body, "{}");
});

test("every failure of the service is a readable tool error, never a crash and never the key", async () => {
  const cases = [
    [json(404, { error: "unknown tool" }), /outdated tool list/],
    [json(401, { error: "unauthorized" }), /refused this Site's key/],
    [json(400, { error: "level must be 0-100" }), /refused: level must be 0-100/],
    [json(500, {}), /could not be reached/],
    [json(422, { error: "The TV is switched off." }), /^The TV is switched off\.$/],
    [offlinePage(404), /home hub is offline/],
    [new TypeError("fetch failed to https://homie.example with secret-key-0123456789"), /could not be reached/],
  ];
  for (const [answer, expected] of cases) {
    const table = { ...GOOD, "POST /api/v1/tools/tv_status": answer };
    const reply = (await handler(table)(rpc("tools/call", { name: "tv_status" }))).result;
    assert.equal(reply.isError, true);
    assert.match(reply.content[0].text, expected);
    assert.equal(reply.content[0].text.includes("secret-key-0123456789"), false);
  }
});

test("a failed change says it is unknown whether it took effect; a failed read does not", async () => {
  const down = new TypeError("fetch failed");
  const table = { ...GOOD, "POST /api/v1/tools/tv_set_volume": down, "POST /api/v1/tools/tv_status": down };
  const handle = handler(table);
  await handle(rpc("tools/list")); // learns which tools change something
  const write = (await handle(rpc("tools/call", { name: "tv_set_volume", arguments: { level: 20 } }))).result;
  assert.match(write.content[0].text, /Unknown whether it took effect/);
  const read = (await handle(rpc("tools/call", { name: "tv_status" }))).result;
  assert.doesNotMatch(read.content[0].text, /Unknown whether/);
});

test("a call that outlasts its deadline is aborted and reported", async () => {
  const slow = async (url, options) => new Promise((resolve, reject) => options.signal.addEventListener("abort", () => reject(Object.assign(new Error("aborted"), { name: "AbortError" }))));
  const handle = createMcpHandler({ config: CONFIG, fetchImpl: (url, options) => (url.endsWith("/api/v1/tools") ? Promise.resolve(json(200, MANIFEST)) : slow(url, options)), cache: {} });
  const original = globalThis.setTimeout;
  globalThis.setTimeout = (fn, ms, ...rest) => original(fn, ms > 1000 ? 5 : ms, ...rest); // 45 s becomes 5 ms
  try {
    const reply = (await handle(rpc("tools/call", { name: "tv_status" }))).result;
    assert.equal(reply.isError, true);
    assert.match(reply.content[0].text, /did not answer in time/);
  } finally {
    globalThis.setTimeout = original;
  }
});

test("an unreachable service at initialize is an error ChatGPT can retry, not an empty tool list", async () => {
  const response = await handler({ "GET /api/v1/tools": new TypeError("down") })(rpc("initialize", {}));
  assert.equal(response.result, undefined);
  assert.equal(response.error.code, -32603);
});

test("a Site without its settings says what to configure", async () => {
  const handle = handler(GOOD, { config: readConfig({}) });
  assert.match((await handle(rpc("initialize", {}))).error.message, /HOME_API_URL and HOME_API_KEY/);
  assert.deepEqual((await handle(rpc("ping"))).result, {});
});

test("protocol housekeeping: notifications get no body, bad messages and unknown methods are errors", async () => {
  const handle = handler(GOOD);
  assert.equal(await handle({ jsonrpc: "2.0", method: "notifications/initialized" }), null);
  assert.equal((await handle({ method: "tools/list" })).error.code, -32600);
  assert.equal((await handle(rpc("resources/list"))).error.code, -32601);
  assert.equal((await handle(rpc("tools/call", { name: "x", arguments: [1] }))).error.code, -32602);
  assert.equal((await handle(rpc("tools/call", { arguments: {} }))).error.code, -32602);
  assert.deepEqual((await handle(rpc("ping", undefined, 7))), { jsonrpc: "2.0", id: 7, result: {} });
});

test("a 422 is the tool's own failure: its text comes through as is, and it is not a hub problem", async () => {
  const table = {
    ...GOOD,
    "POST /api/v1/tools/tv_status": json(422, { error: "The TV is not paired. Pair it from the admin first." }),
    "POST /api/v1/tools/tv_set_volume": json(422, { error: "The TV is switched off." }),
  };
  const handle = handler(table);
  await handle(rpc("tools/list")); // learns which tools change something
  for (const [name, text] of [["tv_status", "The TV is not paired. Pair it from the admin first."], ["tv_set_volume", "The TV is switched off."]]) {
    const reply = (await handle(rpc("tools/call", { name }))).result;
    assert.equal(reply.isError, true);
    assert.equal(reply.content[0].text, text); // not "could not be reached", and no "unknown whether it took effect"
  }
  const empty = (await handler({ ...GOOD, "POST /api/v1/tools/tv_status": json(422, {}) })(rpc("tools/call", { name: "tv_status" }))).result;
  assert.equal(empty.isError, true);
  assert.match(empty.content[0].text, /device reported a failure/);
});

test("a 404 that is not JSON is the relay's offline page: the home hub is offline, not an outdated tool list", async () => {
  const table = { ...GOOD, "POST /api/v1/tools/tv_status": offlinePage(404), "POST /api/v1/tools/tv_set_volume": offlinePage(404) };
  const handle = handler(table);
  await handle(rpc("tools/list"));
  for (const name of ["tv_status", "tv_set_volume"]) {
    const reply = (await handle(rpc("tools/call", { name }))).result;
    assert.equal(reply.isError, true);
    assert.match(reply.content[0].text, /^The home hub is offline right now \(the home computer may be asleep or without internet\)\. Try again later\.$/);
    assert.doesNotMatch(reply.content[0].text, /outdated|Unknown whether/); // nothing was sent to a device, so no doubt about an effect
  }
});

test("an offline home at initialize or tools/list is a retryable error that names the cause", async () => {
  for (const method of ["initialize", "tools/list"]) {
    const response = await handler({ "GET /api/v1/tools": offlinePage(404) })(rpc(method, {}));
    assert.equal(response.result, undefined);
    assert.equal(response.error.code, -32603);
    assert.match(response.error.message, /home hub is offline/);
  }
});

test("a JSON 404 stays 'unknown tool', even without an error text", async () => {
  for (const answer of [json(404, { error: "unknown tool" }), json(404, {})]) {
    const reply = (await handler({ ...GOOD, "POST /api/v1/tools/tv_status": answer })(rpc("tools/call", { name: "tv_status" }))).result;
    assert.match(reply.content[0].text, /outdated tool list/);
  }
});

test("rate limit, a stopped plugin and a hub-side timeout each get their own message", async () => {
  const cases = [
    [json(429, { error: "rate limited" }), /rate limiting/],
    [json(503, { error: "plugin unavailable" }), /device service on the home hub is not running/],
    [json(504, { error: "timeout" }), /did not answer in time/],
  ];
  for (const [answer, expected] of cases) {
    const table = { ...GOOD, "POST /api/v1/tools/tv_status": answer, "POST /api/v1/tools/tv_set_volume": answer };
    const handle = handler(table);
    await handle(rpc("tools/list"));
    const read = (await handle(rpc("tools/call", { name: "tv_status" }))).result;
    assert.equal(read.isError, true);
    assert.match(read.content[0].text, expected);
    assert.doesNotMatch(read.content[0].text, /Unknown whether/);
  }
  // a stopped plugin or a timeout leaves a change in doubt; a rate limit means the call never ran
  const write = async (answer) => {
    const handle = handler({ ...GOOD, "POST /api/v1/tools/tv_set_volume": answer });
    await handle(rpc("tools/list"));
    return (await handle(rpc("tools/call", { name: "tv_set_volume", arguments: { level: 5 } }))).result.content[0].text;
  };
  assert.match(await write(json(504, { error: "timeout" })), /Unknown whether it took effect/);
  assert.match(await write(json(503, { error: "plugin unavailable" })), /Unknown whether it took effect/);
});

test("a plain text result of the hub comes back as the text itself, structured content stays as sent", async () => {
  const table = {
    "POST /api/v1/tools/tv_power_on": json(200, { text: "Living room TV is on." }),
    "POST /api/v1/tools/tv_status": json(200, { text: "on", volume: 12 }),
  };
  const handle = handler(table);
  const plain = (await handle(rpc("tools/call", { name: "tv_power_on" }))).result;
  assert.equal(plain.content[0].text, "Living room TV is on.");
  assert.deepEqual(plain.structuredContent, { text: "Living room TV is on." });
  const rich = (await handle(rpc("tools/call", { name: "tv_status" }))).result;
  assert.deepEqual(JSON.parse(rich.content[0].text), { text: "on", volume: 12 });
});
