# MCP registry submissions — the send kit

Prepared 2026-08-21. Each entry says what is ready and what pressing send
looks like.

## 1. awesome-mcp-servers (ready — one click)

Branch `add-ducklab` is pushed to the jrullan fork with a one-line diff
under **Coding Agents**, alphabetical slot, house legend (🎖️ official,
🏎️ Go, 🏠 local, 🐧 Linux). Open the PR:

https://github.com/punkpeye/awesome-mcp-servers/compare/main...jrullan:awesome-mcp-servers:add-ducklab?expand=1

## 2. Glama (ready — authenticate and claim)

Glama indexes public GitHub repos; `glama.json` at the repo root names the
maintainer. To claim the listing (controls metadata, gives usage reports):
sign in at https://glama.ai with GitHub and claim the server. You can also
submit the repo directly at https://glama.ai/mcp/servers (Add server).

## 3. mcp.so (form)

Submit the repo URL at https://mcp.so (Submit button). Suggested blurb:
the repository description, verbatim.

## 4. Official MCP registry (blocked on an artifact — tracked)

registry.modelcontextprotocol.io only lists servers installable from a
trusted source: npm/PyPI/NuGet/OCI, a remote URL, or an **MCPB bundle
attached to a GitHub release**. Ducklab's MCP server is `ducklab mcp
serve` — a locally built Go binary — so none exist yet. The path of least
resistance is attaching an MCPB bundle to the next release; tracked on the
board. `server.json` here is the ready draft: fill the release URL, then

```bash
mcp-publisher login github     # proves io.github.jrullan namespace
mcp-publisher publish docs/mcp-registry/server.json
```
