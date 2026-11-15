import { env } from "cloudflare:workers";
import { createMcpHandler, readConfig } from "../../core/mcp.mjs";

// Stateless JSON-RPC endpoint of the Site's MCP server. Everything except the wiring lives in core/, which runs under
// node --test; the tools themselves live in the home hub, reached through the relay.
export async function POST(request: Request) {
  let rpc: unknown;
  try {
    rpc = await request.json();
  } catch {
    return Response.json({ jsonrpc: "2.0", id: null, error: { code: -32700, message: "Parse error" } });
  }
  const config = readConfig(env as unknown as Record<string, string | undefined>);
  const response = await createMcpHandler({ config })(rpc);
  return response ? Response.json(response) : new Response(null, { status: 202 });
}
