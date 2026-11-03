#!/usr/bin/env python3
"""Exercise the real TUI in a PTY using disposable state and a fake gh binary.

No network, user credentials, source writes, or third-party Python packages.
This verifies input and cleanup, not terminal-emulator glyph appearance.
"""
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

binary = Path(sys.argv[1] if len(sys.argv) > 1 else "bin/lazytodo").resolve()
repo = Path(__file__).resolve().parents[1]
assert binary.is_file(), "build bin/lazytodo first"

with tempfile.TemporaryDirectory(prefix="lazytodo-pty-") as temp:
    root = Path(temp)
    tools = root / "tools"
    tools.mkdir()
    snapshot = root / "snapshot.json"
    snapshot.write_text((repo / "examples/snapshot.json").read_text())
    marker = root / "fail"
    started = root / "started"
    hold = root / "hold"
    fake = tools / "gh"
    # Authored helper is kept separate from fixture data. It never uses network.
    fake.write_text(f'''#!{sys.executable}
import json, os, sys, time
from pathlib import Path
Path(os.environ["FAKE_STARTED"]).write_text("started")
deadline = time.monotonic() + 15
while Path(os.environ["FAKE_HOLD"]).exists() and time.monotonic() < deadline:
    time.sleep(0.01)
if Path(os.environ["FAKE_FAILURE"]).exists():
    print("HTTP/2.0 503 Service Unavailable\\r\\n\\r\\n{{}}")
    print("private-token-must-not-appear", file=sys.stderr)
    sys.exit(1)
item = {{"id":991,"number":9,"title":"Slow source fixture","body":"Fictional provider response", "html_url":"https://github.com/fictional/fixture/issues/9","state":"open","labels":[],"assignees":[],"created_at":"2026-01-01T12:00:00Z","updated_at":"2026-01-01T12:00:00Z"}}
print("HTTP/2.0 200 OK\\r\\nContent-Type: application/json\\r\\n\\r\\n" + json.dumps([item]))
''')
    fake.chmod(0o700)
    # Fake native clipboard: drag-copy must never touch the real clipboard.
    clip = root / "clipboard.txt"
    pbcopy = tools / "pbcopy"
    pbcopy.write_text(f"#!{sys.executable}\nimport sys\nopen({str(clip)!r}, 'w').write(sys.stdin.read())\n")
    pbcopy.chmod(0o700)
    config = root / "config.toml"
    config.write_text('''refresh_interval = "0s"
repo_context = false
color = "never"
[[sources]]
id = "demo"
kind = "snapshot"
path = "snapshot.json"
[[sources]]
id = "slow"
kind = "github"
scope = "fictional/fixture"
[[views]]
name = "Open"
query = "state:open"
''')
    env = {"HOME": str(root), "PATH": str(tools) + os.pathsep + os.environ.get("PATH", ""),
           "TERM": "xterm-256color", "LANG": "en_US.UTF-8", "NO_COLOR": "1",
           "FAKE_FAILURE": str(marker), "FAKE_STARTED": str(started), "FAKE_HOLD": str(hold)}
    for key in ("XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"):
        env[key] = str(root / key)

    def cli(*args, expected=0):
        p = subprocess.run([str(binary), "--config", str(config), *args, "--json"],
                           env=env, capture_output=True, timeout=10)
        assert p.returncode == expected, (args, p.returncode, p.stdout, p.stderr)
        return json.loads(p.stdout)["data"]

    cli("fetch")
    assert cli("query")["total"] == 4
    master, slave = os.openpty()
    original = termios.tcgetattr(slave)

    def terminal_modes(attrs):
        # macOS may raise PENDIN when tcsetattr restores cooked mode. It is
        # kernel pending-input bookkeeping, not a requested mode like ECHO.
        copy = list(attrs)
        copy[3] &= ~getattr(termios, "PENDIN", 0)
        return copy

    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
    process = subprocess.Popen([str(binary), "--config", str(config)], stdin=slave,
                               stdout=slave, stderr=slave, env=env)
    transcript = bytearray()

    def pump(seconds=0.1):
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            ready, _, _ = select.select([master], [], [], max(0, end-time.monotonic()))
            if not ready:
                break
            try:
                data = os.read(master, 65536)
            except OSError:
                break
            transcript.extend(data)
            # Respond to harmless terminal capability queries, not application input.
            if b"\x1b[6n" in data:
                os.write(master, b"\x1b[1;1R")
            if b"\x1b]11;?" in data:
                os.write(master, b"\x1b]11;rgb:0000/0000/0000\x1b\\")

    def wait_text(text, timeout=5, after=0):
        end = time.monotonic()+timeout
        while text.encode() not in transcript[after:]:
            assert process.poll() is None, ("TUI exited early", process.returncode, transcript[-3000:])
            assert time.monotonic()<end, ("missing", text, transcript[-1200:])
            pump()

    def send(data):
        os.write(master, data)
        pump(0.15)

    try:
        first = time.monotonic()
        wait_text("lazytodo")
        wait_text("登入")
        first_frame = time.monotonic()-first
        send(b"/jkhql/?")
        assert process.poll() is None, "printable q escaped search"
        wait_text("No items match")
        send(b"\x1b")
        send(b"/" + "登入".encode())
        wait_text("1 of 1")
        send(b"\r")  # accept query, not open details
        send(b"\r")  # inspect
        send(b"\x1b")  # return list
        send(b"\x1b")  # clear accepted filter
        # Bracketed paste owns printable shortcuts; its newline cannot submit.
        send(b"/\x1b[200~j\nq/?\x1b[201~")
        assert process.poll() is None
        send(b"\x1b")
        send(b"?")
        wait_text("NAVIGATION")
        send(b"\x1b")
        send(b"v")
        wait_text("Views")
        send(b"\x1b[Hj\r")  # All -> Pinned view, independently of initial Open.
        send(b"v")
        send(b"\x1b[H\r")  # All
        send(b"s")
        wait_text("Sources")
        send(b"\x1b")

        # Mouse capture is on by default (cell motion + SGR coordinates).
        assert b"\x1b[?1002h" in transcript and b"\x1b[?1006h" in transcript, "mouse capture not requested"
        # SGR coordinates are 1-based: list row 2 is cell (6, 7).
        send(b"\x1b[<0;6;7M\x1b[<0;6;7m")
        send(b"\x1b[<65;6;7M\x1b[<64;6;7M")  # wheel down/up over the list
        send(b"\x1b[<65;70;8M")  # wheel over the preview
        # Drag in the preview (inner x starts at 53 1-based) copies on release.
        start = len(transcript)
        send(b"\x1b[<0;54;5M\x1b[<32;64;6M\x1b[<0;64;6m")
        # Linux CI has no native clipboard helper; the OSC 52-only notice also says Copied.
        wait_text("Copied", after=start)
        assert b"\x1b]52;c;" in transcript[start:], "drag-copy did not emit OSC 52"
        if sys.platform == "darwin":
            deadline = time.monotonic()+3
            while not (clip.exists() and clip.read_text().strip()):
                assert time.monotonic()<deadline, "native clipboard helper not invoked"
                pump(0.05)
        send(b"\x1b")  # clear selection
        send(b"\x1b")  # list focus
        # Footer hint click opens Views ("v views" sits after "tab pane  / search  ").
        start = len(transcript)
        footer = 30
        send(f"\x1b[<0;{len('↑↓/jk move  tab pane  / search  ')+2};{footer}M".encode())
        wait_text("Views", after=start)
        send(b"\x1b")
        # M releases capture for native selection, M again restores it.
        start = len(transcript)
        send(b"M")
        wait_text("Mouse capture off", after=start)
        assert b"\x1b[?1002l" in transcript[start:], "M did not release mouse capture"
        send(b"M")
        assert b"\x1b[?1002h" in transcript[start:], "M did not restore mouse capture"
        assert process.poll() is None, "mouse input crashed the TUI"

        # Begin a slow refresh; select/pin while provider I/O remains pending.
        started.unlink(missing_ok=True)
        hold.write_text("hold provider until input succeeds")
        send(b"r")
        deadline = time.monotonic()+3
        while not started.exists():
            assert time.monotonic()<deadline, "refresh did not start"
            pump(0.02)
        began = time.monotonic()
        send(b"jp")
        pins = cli("query", "pinned:true")
        assert pins["total"] == 1, "pin did not execute while fetch was pending"
        responsive = time.monotonic()-began
        assert hold.exists(), "provider must remain held during input assertion"
        hold.unlink()
        wait_text("Refresh complete")

        # A failed later refresh retains old rows and hides provider stderr.
        marker.write_text("fail")
        start = len(transcript)
        send(b"r")
        wait_text("Refresh incomplete", after=start)
        assert cli("query")["total"] == 4, "failed refresh erased rows"
        assert b"private-token-must-not-appear" not in transcript
        for width, height in ((80,24),(36,12),(1,1),(120,30)):
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH",height,width,0,0))
            process.send_signal(signal.SIGWINCH)
            pump(0.1)
            assert process.poll() is None, "resize crashed"
        send(b"q")
        process.wait(timeout=5)
        pump()
        assert process.returncode == 0, process.returncode
        restored = termios.tcgetattr(slave)
        assert terminal_modes(restored) == terminal_modes(original), "terminal modes not restored"
        assert b"\x1b[?1049l" in transcript, "alternate screen not restored"
        assert transcript.rfind(b"\x1b[?1002l") > transcript.rfind(b"\x1b[?1002h"), "mouse capture not released on exit"
        print(f"PTY OK: search/input/paste, views/help/sources, mouse click/wheel/footer/drag-copy/M toggle, pin during slow fetch ({responsive:.3f}s), failure retention, resize, quit cleanup; first observed frame {first_frame:.3f}s")

        # Raw Ctrl+C must exit cleanly and restore terminal ownership too.
        transcript.clear()
        process = subprocess.Popen([str(binary), "--config", str(config)], stdin=slave,
                                   stdout=slave, stderr=slave, env=env)
        wait_text("lazytodo")
        send(b"\x03")
        process.wait(timeout=5)
        pump()
        assert process.returncode in (0, 130), process.returncode
        assert terminal_modes(termios.tcgetattr(slave)) == terminal_modes(original)
        assert b"\x1b[?1049l" in transcript
        print("PTY OK: Ctrl+C and terminal restoration")
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)
        os.close(master)
        os.close(slave)
