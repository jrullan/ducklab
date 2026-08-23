#!/bin/sh
# Build, extract, and invoke the MCPB using the command declared in its manifest.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
make -C "$root" mcpb >/dev/null
unzip -q "$root/dist/ducklab-mcp.mcpb" -d "$tmp"
python3 - "$tmp" <<'PY'
import json, os, subprocess, sys
root = sys.argv[1]
with open(os.path.join(root, "manifest.json"), encoding="utf-8") as f:
    m = json.load(f)
assert m["manifest_version"]
server = m["server"]
assert server["type"] == "stdio"
entry = server["entry_point"]
assert not os.path.isabs(entry) and ".." not in entry
binary = os.path.join(root, entry)
assert os.access(binary, os.X_OK)
with open(binary, "rb") as f:
    data = f.read(20)
assert data[:4] == b"\x7fELF" and data[4:6] == b"\x02\x01"
config = server["mcp_config"]
command = config["command"].replace("${__dirname}", root)
assert os.path.realpath(command) == os.path.realpath(binary)
env = os.environ.copy()
env["XDG_STATE_HOME"] = os.path.join(root, "state")
proc = subprocess.run([command] + config["args"], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
assert proc.returncode == 9, proc.returncode
assert b"engine not running" in proc.stdout, proc.stdout
PY
echo "MCPB e2e check passed"
