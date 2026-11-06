import os, platform, shutil, subprocess
missing=[]
for tool in ['go','pnpm','buf','protoc-gen-go','protoc-gen-connect-go','docker']:
    if not shutil.which(tool): missing.append(tool)
if not os.environ.get('DEV_TOKEN'): missing.append('DEV_TOKEN (.env)')
if platform.system()=='Linux':
    if not shutil.which('pkg-config') or subprocess.call(['pkg-config','--exists','gtk+-3.0','webkit2gtk-4.1']) != 0: missing.append('GTK3 and WebKitGTK 4.1 development packages')
if missing: raise SystemExit('Missing: '+', '.join(missing))
print('Core build prerequisites found. Check llama-server /v1/models before running.')
