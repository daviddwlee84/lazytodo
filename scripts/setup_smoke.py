#!/usr/bin/env python3
"""Real PTY setup checks with fictional native CLI metadata and isolated XDG."""
import fcntl
import json
import os
from pathlib import Path
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

binary = Path(sys.argv[1] if len(sys.argv)>1 else "bin/lazytodo").resolve()

class Session:
    def __init__(self, args, env):
        self.master, self.slave = os.openpty()
        self.original = termios.tcgetattr(self.slave)
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack("HHHH",32,120,0,0))
        self.proc = subprocess.Popen([str(binary), *args], env=env, stdin=self.slave, stdout=self.slave, stderr=self.slave)
        self.output = bytearray()
    def pump(self, duration=0.1):
        end = time.monotonic()+duration
        while time.monotonic()<end:
            if not select.select([self.master],[],[],max(0,end-time.monotonic()))[0]:
                break
            data = os.read(self.master,65536)
            self.output.extend(data)
            if b"\x1b[6n" in data:
                os.write(self.master,b"\x1b[1;1R")
            if b"\x1b]11;?" in data:
                os.write(self.master,b"\x1b]11;rgb:0000/0000/0000\x1b\\")
    def wait(self, text, after=0):
        deadline = time.monotonic()+8
        while text.encode() not in self.output[after:]:
            assert self.proc.poll() is None, ("unexpected exit",self.proc.returncode,self.output[-1500:])
            assert time.monotonic()<deadline, ("missing",text,self.output[-1800:])
            self.pump()
    def send(self, data):
        os.write(self.master,data)
        self.pump(0.15)
    def resize(self, width, height):
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack("HHHH",height,width,0,0))
        self.proc.send_signal(signal.SIGWINCH)
        self.pump(0.15)
    def finish(self, expected=0):
        self.proc.wait(timeout=8)
        self.pump()
        assert self.proc.returncode==expected,(self.proc.returncode,self.output[-1500:])
        restored = termios.tcgetattr(self.slave)
        restored[3] &= ~getattr(termios,"PENDIN",0)
        original = list(self.original)
        original[3] &= ~getattr(termios,"PENDIN",0)
        assert restored==original,"terminal modes not restored"
    def close(self):
        if self.proc.poll() is None:
            self.proc.kill()
            self.proc.wait(timeout=3)
        os.close(self.master)
        os.close(self.slave)

with tempfile.TemporaryDirectory(prefix="lazytodo-setup-") as temp:
    root = Path(temp)
    tools = root / "tools"
    tools.mkdir()
    log = root / "calls.jsonl"
    fail_all = root / "fail-all"
    program = f'''#!{sys.executable}
import json, os, sys
from pathlib import Path
kind = Path(sys.argv[0]).name
args = sys.argv[1:]
assert args[0] == "api" and args[args.index("--method")+1] == "GET"
endpoint = args[-1]
assert endpoint == "user" or endpoint.startswith("user/repos?") or endpoint.startswith("projects?"), endpoint
with open(os.environ["CALL_LOG"],"a") as f:
    f.write(json.dumps({{"kind":kind,"args":args}})+"\\n")
def respond(value, status="200 OK"):
    print("HTTP/2.0 "+status+"\\r\\nContent-Type: application/json\\r\\n\\r\\n"+json.dumps(value))
if endpoint == "user":
    if kind == "glab" or Path(os.environ["FAIL_ALL"]).exists():
        print("private-auth-secret-must-not-leak",file=sys.stderr)
        respond({{}},"401 Unauthorized")
        sys.exit(1)
    respond({{"login":"fixture-user"}})
else:
    assert kind == "gh"
    def repo(id,name,date,archived=False,issues=True):
        return {{"id":id,"full_name":name,"updated_at":date,"private":True,"archived":archived,"has_issues":issues}}
    respond([repo(1,"owner/older","2025-01-01T00:00:00Z"),repo(2,"owner/newest","2026-10-30T00:00:00Z"),repo(3,"owner/archived","2026-11-01T00:00:00Z",True),repo(4,"owner/no-issues","2026-11-01T00:00:00Z",False,False)])
'''
    for name in ("gh","glab"):
        path = tools / name
        path.write_text(program)
        path.chmod(0o700)
    env = {"HOME":str(root),"PATH":str(tools),"TERM":"xterm-256color","NO_COLOR":"1","LANG":"en_US.UTF-8","CALL_LOG":str(log),"FAIL_ALL":str(fail_all)}
    for key in ("XDG_CONFIG_HOME","XDG_CACHE_HOME","XDG_DATA_HOME","XDG_STATE_HOME"):
        env[key] = str(root / key)
    config = root / "chosen folder" / "config.toml"
    session = Session(["--config",str(config),"init"],env)
    try:
        session.wait("Targets")
        session.wait("installed")
        assert not log.exists(),"wizard probed before explicit discovery"
        session.send(b"je") # Select GitLab and edit its self-hosted authority.
        session.send(b"\x01\x0bgitlab.qa.example\r")
        assert session.proc.poll() is None, "host input q quit the wizard"
        session.send(b"\r")
        session.wait("Candidates")
        session.wait("owner/newest")
        session.wait("glab auth login")
        assert not config.parent.exists(),"discovery wrote configuration"
        assert b"private-auth-secret-must-not-leak" not in session.output
        session.send(b"/jkhql/?")
        assert session.proc.poll() is None,"filter key invoked navigation"
        session.send(b"\x1b")
        session.send(b" ")
        session.send(b"\r")
        session.wait("Review")
        session.wait("ADD github")
        session.wait("[Back]")
        assert not config.exists(),"review wrote before Save"
        session.send(b"\r") # Default Back is safe.
        assert not config.exists()
        session.send(b"\r") # Review again; selection persisted.
        session.send(b"\x1b[Z") # Shift+Tab: Back -> Save.
        session.wait("[Save]")
        session.send(b"\r")
        session.finish()
        assert config.is_file()
    finally:
        session.close()

    show = subprocess.run([str(binary),"--config",str(config),"config","show","--json"],env=env,capture_output=True,check=True)
    saved = json.loads(show.stdout)["data"]["config"]["sources"]
    assert len(saved)==1 and saved[0]["scope"]=="owner/newest",saved
    assert saved[0]["host"]=="github.com"
    assert not Path(env["XDG_CACHE_HOME"]).exists(),"wizard fetched issues/cache"
    assert not Path(env["XDG_DATA_HOME"]).exists(),"wizard created pin database"
    calls = log.read_text()
    assert "issues?" not in calls
    assert "gitlab.qa.example" in calls, "edited host was not used for discovery"
    print("Setup PTY OK: explicit discovery, recent-first selection, partial auth failure guidance, filter input, safe Review/Back, confirmed Save, no issue fetch")

    # No tools: show actionable setup/manual options and allow cancel without writes.
    no_tools = dict(env)
    no_tools["PATH"] = str(root / "empty-path")
    empty = root / "no-tools" / "config.toml"
    session = Session(["--config",str(empty),"init"],no_tools)
    try:
        session.wait("missing gh")
        session.wait("missing glab")
        session.wait("brew install gh glab")
        session.wait("lazytodo config edit")
        session.send(b"\r")
        session.wait("Select an installed provider")
        for width,height in ((80,24),(36,12),(1,1),(120,32)):
            session.resize(width,height)
            assert session.proc.poll() is None
        session.send(b"q")
        session.finish()
        assert not empty.parent.exists()
    finally:
        session.close()
    assert log.read_text()==calls,"missing-tool screen made API calls"
    print("Setup PTY OK: missing-tools guidance, resize, cancel leaves no files")

    # Tools present but neither authenticated: explain native login, never prompt it.
    fail_all.write_text("fail")
    auth_config = root / "no-auth" / "config.toml"
    session = Session(["--config",str(auth_config),"init"],env)
    try:
        session.wait("installed")
        session.send(b"\r")
        session.wait("gh auth login --hostname github.com")
        session.wait("glab auth login --hostname gitlab.com")
        session.send(b"q")
        session.finish()
        assert not auth_config.parent.exists()
        assert b"private-auth-secret-must-not-leak" not in session.output
    finally:
        session.close()
    print("Setup PTY OK: unauthenticated hosts give explicit native login instructions")

    guarded = root / "json-guard" / "config.toml"
    before = log.read_text()
    session = Session(["--config",str(guarded),"init","--json"],env)
    try:
        session.finish(expected=2)
        assert json.loads(session.output)["error"]["code"]=="usage"
        assert not guarded.parent.exists()
    finally:
        session.close()
    assert log.read_text()==before
    print("Setup PTY OK: JSON mode rejected before discovery or file writes")
