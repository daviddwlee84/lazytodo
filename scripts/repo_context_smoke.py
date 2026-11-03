#!/usr/bin/env python3
"""Verify cwd awareness in real PTYs with disposable Git repos and fake gh."""
import fcntl
import json
import os
from pathlib import Path
import select
import shutil
import struct
import subprocess
import sys
import tempfile
import termios
import time

binary = Path(sys.argv[1] if len(sys.argv)>1 else "bin/lazytodo").resolve()
git = shutil.which("git")
assert git, "Git is required for this fixture test"

class Session:
    def __init__(self, env, config, cwd):
        self.master,self.slave = os.openpty()
        self.original = termios.tcgetattr(self.slave)
        fcntl.ioctl(self.slave,termios.TIOCSWINSZ,struct.pack("HHHH",32,120,0,0))
        self.proc = subprocess.Popen([str(binary),"--config",str(config)],cwd=cwd,env=env,stdin=self.slave,stdout=self.slave,stderr=self.slave)
        self.output=bytearray()
    def pump(self, duration=0.1):
        end=time.monotonic()+duration
        while time.monotonic()<end:
            if not select.select([self.master],[],[],max(0,end-time.monotonic()))[0]: break
            data=os.read(self.master,65536)
            self.output.extend(data)
            if b"\x1b[6n" in data: os.write(self.master,b"\x1b[1;1R")
            if b"\x1b]11;?" in data: os.write(self.master,b"\x1b]11;rgb:0000/0000/0000\x1b\\")
    def wait(self,text,after=0):
        end=time.monotonic()+8
        while text.encode() not in self.output[after:]:
            assert self.proc.poll() is None,("early exit",self.proc.returncode,self.output[-1600:])
            assert time.monotonic()<end,("missing",text,self.output[-1800:])
            self.pump()
    def send(self,data):
        os.write(self.master,data)
        self.pump(0.15)
    def finish(self):
        self.send(b"q")
        self.proc.wait(timeout=5)
        self.pump()
        assert self.proc.returncode==0,(self.proc.returncode,self.output[-1500:])
        restored=termios.tcgetattr(self.slave)
        restored[3] &= ~getattr(termios,"PENDIN",0)
        self.original[3] &= ~getattr(termios,"PENDIN",0)
        assert restored==self.original,"terminal modes not restored"
    def close(self):
        if self.proc.poll() is None:
            self.proc.kill();self.proc.wait(timeout=3)
        os.close(self.master);os.close(self.slave)

with tempfile.TemporaryDirectory(prefix="lazytodo-cwd-") as temp:
    root=Path(temp)
    tools=root/"tools";tools.mkdir()
    helper=tools/"gh"
    helper.write_text(f'''#!{sys.executable}
import json,os,sys
args=sys.argv[1:]
assert args[0]=="api" and args[args.index("--method")+1]=="GET"
endpoint=args[-1]
assert endpoint.startswith("repos/owner/project/issues?"),endpoint
with open(os.environ["API_LOG"],"a") as f:f.write(endpoint+"\\n")
def issue(id,state,title):
    return {{"id":id,"number":id,"title":title,"body":"Fictional cwd fixture", "state":state,"html_url":"https://github.com/owner/project/issues/"+str(id),"labels":[],"assignees":[],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}}
print("HTTP/2.0 200 OK\\r\\n\\r\\n"+json.dumps([issue(1,"open","Current repo open issue"),issue(2,"closed","Current repo closed issue")]))
''')
    helper.chmod(0o700)
    base={"HOME":str(root),"PATH":str(tools)+os.pathsep+str(Path(git).parent),"TERM":"xterm-256color","NO_COLOR":"1","LANG":"en_US.UTF-8","GIT_CONFIG_NOSYSTEM":"1"}
    repo=root/"checkout"
    subprocess.run([git,"init","-q",str(repo)],env=base,check=True)
    subprocess.run([git,"-C",str(repo),"remote","add","origin","git@github.com:owner/project.git"],env=base,check=True)
    cwd=repo/"nested";cwd.mkdir()

    def scenario(name, sources=""):
        folder=root/name;folder.mkdir()
        env=dict(base);env["HOME"]=str(folder);env["API_LOG"]=str(folder/"api.log")
        for key in ("XDG_CONFIG_HOME","XDG_CACHE_HOME","XDG_DATA_HOME","XDG_STATE_HOME"):env[key]=str(folder/key)
        config=folder/"config.toml"
        config.write_text('refresh_interval="0s"\ncolor="never"\nrepo_context=true\n'+sources)
        return env,config
    def cli(env,config,*args):
        p=subprocess.run([str(binary),"--config",str(config),*args,"--json"],env=env,capture_output=True,timeout=10)
        assert p.returncode==0,(p.returncode,p.stdout,p.stderr)
        return json.loads(p.stdout)["data"]

    # Existing configured repo automatically scopes Open; All preserves the scope.
    env,config=scenario("known",'[[sources]]\nid="here"\nkind="github"\nscope="owner/project"\n')
    cli(env,config,"fetch")
    before=Path(env["API_LOG"]).read_text()
    session=Session(env,config,cwd)
    try:
        session.wait("(cwd)")
        session.wait("Current repo open issue")
        assert b"Current repo closed issue" not in session.output,"default included closed rows"
        session.send(b"v\x1b[H\r")
        session.wait("Current repo closed issue")
        session.send(b"s\x1b[H\r")
        session.wait("All sources")
        session.send(b"c")
        session.wait("(cwd)")
        session.wait("Esc all sources")
        # Esc is the outermost step out of the cwd scope back to every source.
        mark=len(session.output)
        session.send(b"\x1b")
        session.wait("All sources;",after=mark)
        session.finish()
    finally:session.close()
    assert Path(env["API_LOG"]).read_text()==before,"cache-only context detection performed API calls"
    print("Cwd PTY OK: nested checkout, configured repo prioritization, Open default, independent All/source switching, Esc leaves cwd scope, no detection network")

    # Missing source offers Ignore by default; neither config nor cache is written.
    env,config=scenario("ignore")
    before=config.read_bytes()
    session=Session(env,config,cwd)
    try:
        session.wait("Current repository")
        session.wait("[Ignore this time]")
        session.send(b"\r")
        session.wait("ignored for this session")
        assert config.read_bytes()==before
        assert not Path(env["API_LOG"]).exists()
        assert not Path(env["XDG_CACHE_HOME"]).exists()
        session.finish()
    finally:session.close()
    print("Cwd PTY OK: default Ignore creates no registration, cache, or API calls")

    # Only explicit Add+fetch saves the source and reads its tasks.
    env,config=scenario("add")
    session=Session(env,config,cwd)
    try:
        session.wait("[Ignore this time]")
        assert not Path(env["API_LOG"]).exists()
        session.send(b"\t")
        session.wait("[Add + fetch]")
        session.send(b"\r")
        session.wait("Current repo open issue")
        session.wait("Refresh complete")
        session.finish()
    finally:session.close()
    sources=cli(env,config,"config","show")["config"]["sources"]
    assert len(sources)==1 and sources[0]["scope"]=="owner/project",sources
    assert len(Path(env["API_LOG"]).read_text().splitlines())==1,"fetched more than the chosen source"
    assert cli(env,config,"query")["total"]==2
    print("Cwd PTY OK: confirmed registration plus targeted fetch, selected scope updated without restarting")

    env,config=scenario("disabled",'[[sources]]\nid="off"\nkind="github"\nscope="owner/project"\ndisabled=true\n')
    before=config.read_bytes()
    session=Session(env,config,cwd)
    try:
        session.wait("All sources are disabled")
        session.wait("configured but disabled")
        session.finish()
    finally:session.close()
    assert config.read_bytes()==before and not Path(env["API_LOG"]).exists()
    print("Cwd PTY OK: disabled sources are not automatically enabled")
