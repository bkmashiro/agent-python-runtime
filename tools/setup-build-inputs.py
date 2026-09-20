#!/usr/bin/env python3
"""One-time Linux x86_64 CPython/WASI preparation; later Guest rebuilds are local.

No sudo, no remote build host. Downloads are pinned; interrupted builds can resume.
"""
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]

def main():
    if platform.system() != 'Linux' or platform.machine() != 'x86_64':
        raise SystemExit('Build inputs require Linux x86_64.')
    if sys.version_info < (3, 11):
        raise SystemExit('Use Python 3.11 or newer for the CPython WASI build driver.')
    for tool in ('cc', 'make', 'curl', 'tar', 'unzip'):
        if not shutil.which(tool):
            raise SystemExit(f'Missing {tool}; install build tools before running setup.')
    base = Path(os.environ.get('PYSOLATE_BUILD_INPUTS', ROOT / 'build/inputs')).resolve()
    base.mkdir(parents=True, exist_ok=True)
    downloads = base / 'downloads'
    downloads.mkdir(exist_ok=True)
    lock = json.loads((ROOT / 'tools/build-inputs.lock.json').read_text())
    jobs = int(os.environ.get('PYSOLATE_BUILD_JOBS', '6'))
    if jobs < 1: raise SystemExit('PYSOLATE_BUILD_JOBS must be positive')
    os.sched_setaffinity(0, sorted(os.sched_getaffinity(0))[:jobs])
    env = os.environ.copy()
    env['MAKEFLAGS'] = f'-j{jobs}'
    layout = {
        'cpython-source': ('cpython', False),
        'wasi-sdk-linux-x86_64': ('tools/wasi-sdk', False),
        'wasm-tools-linux-x86_64': ('tools/wasm-tools', False),
        'wasmtime-linux-x86_64': ('tools/wasmtime', False),
        'wasi-vfs-cli-linux-x86_64': ('tools/wasi-vfs-cli', True),
        'wasi-vfs-static-library': ('tools/wasi-vfs-lib', True),
        'pyyaml-source': ('pure/pyyaml', False),
    }
    def digest(file):
        with file.open('rb') as stream:
            return hashlib.file_digest(stream, 'sha256').hexdigest()
    for name, (directory, zipped) in layout.items():
        source = lock[name]
        archive = downloads / name
        if not archive.exists() or digest(archive) != source['sha256']:
            partial = archive.with_suffix('.part')
            print(f'Downloading {name} {source["version"]}', flush=True)
            subprocess.run(['curl', '-fL', '--retry', '2', '--connect-timeout', '20', '--max-time', '900', '-C', '-', '-o', str(partial), source['url']], check=True)
            if digest(partial) != source['sha256']:
                raise SystemExit(f'Checksum mismatch: {partial}; no extraction performed')
            partial.replace(archive)
        dest = base / directory
        if not dest.exists():
            dest.parent.mkdir(parents=True, exist_ok=True)
            with tempfile.TemporaryDirectory(dir=base, prefix='extract-') as temporary:
                if zipped:
                    subprocess.run(['unzip', '-q', str(archive), '-d', temporary], check=True)
                else:
                    subprocess.run(['tar', '-xf', str(archive), '-C', temporary, '--strip-components=1'], check=True)
                Path(temporary).rename(dest)
    for relative in ('tools/wasmtime/wasmtime', 'tools/wasm-tools/wasm-tools', 'tools/wasi-vfs-cli/wasi-vfs'):
        (base / relative).chmod(0o755)
    python = base / 'cpython'
    config = python / 'Tools/wasm/wasi/config.site-wasm32-wasi'
    text = config.read_text()
    if 'ac_cv_func_clock_nanosleep=no' not in text:
        config.write_text(text + '\n# wazero: use relative clock sleeps\nac_cv_func_clock_nanosleep=no\nac_cv_lib_rt_clock_nanosleep=no\n')
    env['WASI_SDK_PATH'] = str(base / 'tools/wasi-sdk')
    env['WASMTIME'] = str(base / 'tools/wasmtime/wasmtime')
    env['PATH'] = str(base / 'tools/wasmtime') + os.pathsep + env['PATH']
    print('Building CPython locally (incremental on repeat runs)', flush=True)
    subprocess.run([sys.executable, 'Tools/wasm/wasi', 'build', '--wasi-sdk', env['WASI_SDK_PATH']], cwd=python, env=env, check=True)
    target = python / 'cross-build/wasm32-wasip1'
    for file in ('libpython3.14.a', 'pyconfig.h', 'Modules/_decimal/libmpdec/libmpdec.a', 'Modules/expat/libexpat.a'):
        if not (target / file).is_file(): raise SystemExit(f'Missing build output: {file}')
    if not list((target / 'Modules/_hacl').glob('*.a')):
        raise SystemExit('Missing HACL archives')
    print(f'Ready: {base}\nRun bash build-guest.sh to rebuild this project\'s actual Guest.', flush=True)

if __name__ == '__main__':
    main()
