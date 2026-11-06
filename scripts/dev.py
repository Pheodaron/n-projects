import os, signal, subprocess, time, urllib.request
children = []
def start(task):
    p = subprocess.Popen(['mise', 'run', task], start_new_session=True)
    children.append(p)
    return p
def wait(url, timeout=120):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if any(p.poll() is not None for p in children):
            raise RuntimeError('A component exited before readiness')
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == 200: return
        except Exception: time.sleep(.5)
    raise RuntimeError('Timed out waiting for ' + url)
def interrupted(*_): raise KeyboardInterrupt
signal.signal(signal.SIGTERM, interrupted)
try:
    subprocess.run(['mise', 'run', 'infra'], check=True)
    if os.environ.get('LLAMA_MODEL_FILE'): start('dev:llama')
    start('dev:server')
    wait(os.environ.get('BACKEND_URL','http://127.0.0.1:8080')+'/readyz')
    desktop = start('dev:desktop')
    while desktop.poll() is None:
        if any(p.poll() is not None for p in children[:-1]): raise RuntimeError('A server component exited')
        time.sleep(.5)
    if desktop.returncode != 0: raise RuntimeError('Desktop exited with code '+str(desktop.returncode))
finally:
    for p in reversed(children):
        if p.poll() is None: os.killpg(p.pid, signal.SIGTERM)
    for p in children:
        try: p.wait(timeout=10)
        except subprocess.TimeoutExpired: os.killpg(p.pid, signal.SIGKILL)
