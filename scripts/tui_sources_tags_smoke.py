#!/usr/bin/env python3
"""Exercise TUI editor handoff/reload and tag filters with disposable data."""
import fcntl
import json
import os
from pathlib import Path
import select
import struct
import subprocess
import sys
import tempfile
import termios
import time

binary=Path(sys.argv[1] if len(sys.argv)>1 else "bin/lazytodo").resolve()
repo=Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="lazytodo-edit-tags-") as temp:
    root=Path(temp)
    snapshot=json.loads((repo/"examples/snapshot.json").read_text())
    snapshot["items"][0]["labels"]=[{"name":"bug","color":"ff0000"},{"name":"P1","color":"#ffff00"},{"name":"needs review"}]
    (root/"snapshot.json").write_text(json.dumps(snapshot,ensure_ascii=False))
    config=root/"config.toml"
    globals='repo_context=false\ncolor="never"\nrefresh_interval="1s"\n'
    source='[[sources]]\nid="demo"\nkind="snapshot"\npath="snapshot.json"\n'
    initial=globals+source
    custom=initial+'priority_labels=["needs review"]\n'
    disabled=custom+'disabled=true\n'
    invalid=custom.replace('refresh_interval="1s"','refresh_interval="invalid"')
    config.write_text(initial)
    for name,value in {"custom":custom,"invalid":invalid,"disabled":disabled,"empty":globals,"restore":custom}.items():
        (root/(name+".toml")).write_text(value)
    editor=root/"fake editor"
    editor.write_text(f'''#!{sys.executable}
import os,sys,termios
from pathlib import Path
assert os.isatty(0) and os.isatty(1)
mode=termios.tcgetattr(0)
assert mode[3] & termios.ICANON and mode[3] & termios.ECHO, "outer TUI retained raw input"
assert sys.argv[1] == "--wait"
print("EDITOR_READY",flush=True)
action=input().strip()
root=Path(os.environ["FIXTURE_ROOT"])
Path(sys.argv[2]).write_text((root/(action+".toml")).read_text())
''')
    editor.chmod(0o700)
    env={"HOME":str(root),"PATH":os.environ.get("PATH",""),"TERM":"xterm-256color","NO_COLOR":"1","LANG":"en_US.UTF-8","VISUAL":f'"{editor}" --wait',"FIXTURE_ROOT":str(root)}
    for key in ("XDG_CONFIG_HOME","XDG_CACHE_HOME","XDG_DATA_HOME","XDG_STATE_HOME"):env[key]=str(root/key)
    base=[str(binary),"--config",str(config),"--refresh-interval","0s","--color","never"]
    def cli(*args):
        p=subprocess.run([*base,*args,"--json"],env=env,capture_output=True,timeout=10)
        assert p.returncode==0,(p.returncode,p.stdout,p.stderr)
        return json.loads(p.stdout)["data"]
    cli("fetch")
    original_attempt=cli("sources")[0]["last_attempt"]
    master,slave=os.openpty()
    original=termios.tcgetattr(slave)
    fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack("HHHH",32,140,0,0))
    proc=subprocess.Popen(base,env=env,stdin=slave,stdout=slave,stderr=slave)
    output=bytearray()
    def pump(duration=0.1):
        end=time.monotonic()+duration
        while time.monotonic()<end:
            if not select.select([master],[],[],max(0,end-time.monotonic()))[0]:break
            data=os.read(master,65536)
            output.extend(data)
            if b"\x1b[6n" in data:os.write(master,b"\x1b[1;1R")
            if b"\x1b]11;?" in data:os.write(master,b"\x1b]11;rgb:0000/0000/0000\x1b\\")
    def wait(text,after=0):
        end=time.monotonic()+8
        while text.encode() not in output[after:]:
            assert proc.poll() is None,("early exit",proc.returncode,output[-1400:])
            assert time.monotonic()<end,("missing",text,output[-1600:])
            pump()
    def send(data):
        os.write(master,data)
        pump(0.15)
    def edit(action,valid=True):
        start=len(output)
        send(b"e")
        wait("EDITOR_READY",start)
        send(action.encode()+b"\n")
        wait("Press Enter to return",start)
        # The result remains in the normal terminal until acknowledgement.
        assert b"\x1b[?1049l" in output[start:],"TUI did not release alternate screen"
        ack=len(output)
        send(b"\r")
        wait("Configuration reloaded" if valid else "refresh paused",ack)
        assert b"\x1b[?1049h" in output[ack:],"TUI did not reacquire alternate screen"
    try:
        wait("Priority labels: P1")
        wait("[P1]")
        send(b"t")
        wait("Tags")
        send(b"j\r") # Native label order: bug, P1, needs review.
        wait('label:"P1"')
        wait("1 of 1")
        send(b"s")
        edit("custom")
        send(b"\x1b")
        wait("Priority labels: needs review")
        assert cli("sources")[0]["last_attempt"]==original_attempt,"reload fetched despite explicit interval0"
        send(b"s")
        edit("invalid",valid=False)
        send(b"\x1b")
        send(b"r") # Must not refresh under invalid edited settings.
        assert cli("sources")[0]["last_attempt"]==original_attempt,"invalid config allowed refresh"
        assert "invalid" in config.read_text(),"invalid edits were discarded"
        send(b"s")
        edit("restore")
        edit("disabled")
        wait("disabled")
        # Disabled sources remain listed and editable; removing the last source works too.
        edit("empty")
        wait("No sources configured")
        edit("restore")
        send(b"\x1b")
        wait("Priority labels: needs review")
        assert cli("sources")[0]["last_attempt"]==original_attempt,"config operations fetched source data"
        send(b"q")
        proc.wait(timeout=5)
        pump()
        assert proc.returncode==0,(proc.returncode,output[-1400:])
        restored=termios.tcgetattr(slave)
        restored[3]&=~getattr(termios,"PENDIN",0)
        original[3]&=~getattr(termios,"PENDIN",0)
        assert restored==original,"terminal modes not restored"
        print("TUI edit/tags PTY OK: label badges/exact filter, editor handoff + acknowledgement, live reload, per-source priority override, invalid-config pause, disable/remove/re-add last source, explicit flags and terminal cleanup")
    finally:
        if proc.poll() is None:
            proc.terminate()
            try:proc.wait(timeout=3)
            except subprocess.TimeoutExpired:proc.kill();proc.wait(timeout=3)
        os.close(master);os.close(slave)
