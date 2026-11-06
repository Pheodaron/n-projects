import os
from pathlib import Path
model = Path(os.environ.get('LLAMA_MODEL_FILE', '')).expanduser()
if not model.is_file():
    raise SystemExit('Set LLAMA_MODEL_FILE to a compatible GGUF file')
binary = os.environ.get('LLAMA_SERVER', 'llama-server')
os.execvp(binary, [binary, '-m', str(model), '--alias', os.environ.get('LLAMA_MODEL', 'local'), '--host', '127.0.0.1', '--port', '8081', '-c', '4096', '-ngl', os.environ.get('LLAMA_GPU_LAYERS', '0')])
