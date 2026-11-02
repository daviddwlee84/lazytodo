#!/usr/bin/env python3
"""Verify config edit hands a real PTY to an editor, using only disposable files."""
import json
import os
from pathlib import Path
import select
import subprocess
import sys
import tempfile
import termios
import time

binary = Path(sys.argv[1] if len(sys.argv)>1 else "bin/lazytodo").resolve()
with tempfile.TemporaryDirectory(prefix="lazytodo-editor-") as temp:
    root = Path(temp)
    editor = root / "editor with spaces"
    editor.write_text(f'''#!{sys.executable}
import os, sys
from pathlib import Path
assert os.isatty(0) and os.isatty(1)
assert sys.argv[1] == "--wait"
path = Path(sys.argv[2])
assert path.is_file()
print("EDITOR_READY", flush=True)
action = input().strip()
if action == "save":
    path.write_text("# editor comment retained\\nrefresh_interval='0s'\\n")
elif action == "invalid":
    path.write_text("refresh_interval='invalid-duration'\\n")
elif action == "fail":
    sys.exit(9)
''')
    editor.chmod(0o700)
    env = {"HOME": str(root), "PATH": os.environ.get("PATH", ""),
           "TERM": "xterm-256color", "VISUAL": f'"{editor}" --wait',
           "EDITOR": "definitely-not-the-selected-editor"}
    for key in ("XDG_CONFIG_HOME","XDG_CACHE_HOME","XDG_DATA_HOME","XDG_STATE_HOME"):
        env[key] = str(root / key)
    env_target = root / "env.toml"
    env["LAZYTODO_CONFIG"] = str(env_target)

    def run(args, action=None, expected=0):
        master, slave = os.openpty()
        original = termios.tcgetattr(slave)
        proc = subprocess.Popen([str(binary), *args], env=env, stdin=slave, stdout=slave, stderr=slave)
        output = bytearray()
        sent = False
        deadline = time.monotonic()+8
        try:
            while proc.poll() is None:
                assert time.monotonic()<deadline, ("editor timed out", output)
                ready,_,_ = select.select([master],[],[],0.1)
                if ready:
                    output.extend(os.read(master,65536))
                if action is not None and not sent and b"EDITOR_READY" in output:
                    os.write(master, action.encode()+b"\n")
                    sent = True
            while select.select([master],[],[],0.05)[0]:
                output.extend(os.read(master,65536))
            assert proc.returncode==expected,(proc.returncode,output)
            if action is not None:
                assert sent, output
            else:
                assert b"EDITOR_READY" not in output
            restored = termios.tcgetattr(slave)
            original[3] &= ~getattr(termios,"PENDIN",0)
            restored[3] &= ~getattr(termios,"PENDIN",0)
            assert restored==original, "terminal modes changed"
            return bytes(output).decode()
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.wait(timeout=3)
            os.close(master)
            os.close(slave)

    selected = root / "chosen folder" / "config.toml"
    selected.parent.mkdir()
    selected.write_text("broken = [")
    args = ["--config",str(selected),"config","edit"]
    output = run(args,"save")
    assert "Configuration validated" in output
    assert selected.read_text().startswith("# editor comment retained")
    assert not env_target.exists(), "explicit flag lost to env"
    output = run(args,"invalid",expected=2)
    assert "not reverted" in output and "invalid-duration" in selected.read_text()
    run(args,"save")
    run(["config","edit"],"noop")
    assert "[[views]]" in env_target.read_text(), "missing file did not get example"
    del env["LAZYTODO_CONFIG"]
    default = Path(env["XDG_CONFIG_HOME"]) / "lazytodo/config.toml"
    run(["config","edit"],"noop")
    assert default.is_file()
    blocked = root / "must-not-exist" / "config.toml"
    output = run(["--config",str(blocked),"config","edit","--json"],expected=2)
    assert json.loads(output)["error"]["code"]=="usage"
    assert not blocked.parent.exists()
    assert not Path(env["XDG_CACHE_HOME"]).exists(), "config edit initialized cache"
    assert not Path(env["XDG_DATA_HOME"]).exists(), "config edit initialized pins"
    print("Config edit PTY OK: editor arguments/TTY, repair, validation failure preserves edits, flag/env/XDG paths, JSON guard, no cache writes")
