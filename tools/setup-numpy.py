#!/usr/bin/env python3
"""Build pinned NumPy static archives using the already prepared CPython/WASI inputs."""
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess

root = Path(__file__).resolve().parents[1]
if platform.system() != 'Linux' or platform.machine() != 'x86_64':
    raise SystemExit('NumPy build requires Linux x86_64')
inputs = Path(os.environ.get('PYSOLATE_BUILD_INPUTS', root / 'build/inputs')).resolve()
work = root / 'build/numpy'
downloads = work / 'downloads'
downloads.mkdir(parents=True, exist_ok=True)
lock = root / 'guest/build/profiles/numpy-core.lock.json'
filenames = {'numpy-source': 'numpy-source.tar.gz', 'cython-source': 'cython-source.tar.gz', 'setuptools-wheel': 'setuptools-71.1.0-py3-none-any.whl'}

def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()

for source in json.loads(lock.read_text())['sources']:
    target = downloads / filenames[source['id']]
    if target.exists() and digest(target) == source['sha256']:
        continue
    partial = target.with_suffix('.part')
    subprocess.run(['curl', '-fL', '--retry', '2', '--connect-timeout', '20', '--max-time', '900', '-o', str(partial), source['url']], check=True)
    if digest(partial) != source['sha256']:
        raise SystemExit(f'Checksum mismatch: {partial}')
    partial.replace(target)

env = os.environ | {
    'ROOT_DIR': str(root), 'WORK_DIR': str(work), 'DOWNLOAD_DIR': str(downloads),
    'CPYTHON_DIR': str(inputs / 'cpython'), 'WASI_SDK_PATH': str(inputs / 'tools/wasi-sdk'),
    'WASI_BUILD_DIR': str(inputs / 'cpython/cross-build/wasm32-wasip1'),
    'SOURCE_DATE_EPOCH': '1', 'PACKAGE_PROFILE_LOCK': str(lock),
}
subprocess.run(['bash', str(root / 'guest/build/recipes/numpy-static-v1.sh')], env=env, check=True)
print('NumPy inputs ready; run bash build-guest.sh')
