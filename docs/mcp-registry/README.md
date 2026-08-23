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

## 4. Official MCP registry (release artifact ready)

registry.modelcontextprotocol.io only lists servers installable from a
trusted source: npm/PyPI/NuGet/OCI, a remote URL, or an **MCPB bundle
attached to a GitHub release**. Releases now attach `ducklab-mcp.mcpb`,
built by `make mcpb`, and `server.json` points at the versioned release
asset. For a new release, update its version and asset URL together, then

```bash
mcp-publisher login github     # proves io.github.jrullan namespace
mcp-publisher publish docs/mcp-registry/server.json
```
