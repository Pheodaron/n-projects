import os, platform, shutil, subprocess
missing=[]
for tool in ['go','pnpm','protoc','goose','protoc-gen-go','protoc-gen-connect-go','docker']:
    if not shutil.which(tool): missing.append(tool)
if not os.environ.get('DEV_TOKEN'): missing.append('DEV_TOKEN (.env)')
if platform.system()=='Linux':
    if not shutil.which('pkg-config') or subprocess.call(['pkg-config','--exists','gtk4','webkitgtk-6.0']) != 0: missing.append('GTK4 and WebKitGTK 6.0 development packages')
if missing: raise SystemExit('Missing: '+', '.join(missing))
print('Core build prerequisites found. Check llama-server /v1/models before running.')
