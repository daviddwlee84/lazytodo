#!/usr/bin/env python3
"""Run fictional demo data using disposable HOME/XDG directories."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile

binary = Path(sys.argv[1] if len(sys.argv)>1 else "bin/lazytodo").resolve()
repo = Path(__file__).resolve().parents[1]
if not binary.is_file():
    raise SystemExit("Build first: go build -o bin/lazytodo ./cmd/lazytodo")
with tempfile.TemporaryDirectory(prefix="lazytodo-demo-") as temp:
    env = dict(os.environ)
    env["HOME"] = temp
    for key in ("XDG_CONFIG_HOME","XDG_CACHE_HOME","XDG_DATA_HOME","XDG_STATE_HOME"):
        env[key] = str(Path(temp) / key)
    for key in ("LAZYTODO_CONFIG","LAZYTODO_COLOR","LAZYTODO_REFRESH_INTERVAL"):
        env.pop(key, None)
    args = [str(binary), "--config", str(repo / "examples/demo.toml")]
    subprocess.run([*args,"fetch"], env=env, check=True)
    try:
        code = subprocess.run(args, env=env).returncode
    except KeyboardInterrupt:
        code = 130
    raise SystemExit(code)
