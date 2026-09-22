#!/usr/bin/env python3
"""Run with the Guest build's host CPython; compile, never import, selected modules."""
from pathlib import Path
import py_compile
import os
import sys

if sys.version_info[:2] != (3, 14):
    raise SystemExit('Use the host CPython from the same 3.14 build as the Guest')
sys.pycache_prefix = None
stage = Path(sys.argv[1]).resolve()
modules = ('encodings codecs json re enum copy copyreg weakref ast _ast_unparse types collections '
           'functools operator contextlib _collections_abc pysolate_bootstrap plm '
           'site-packages/yaml').split()
scientific = os.environ.get('PYSOLATE_PRECOMPILE_SCIENTIFIC', '0')
if scientific not in ('0', '1'):
    raise SystemExit('PYSOLATE_PRECOMPILE_SCIENTIFIC must be 0 or 1')
if scientific == '1':
    # Observed NumPy/CSV import hot paths; compile without importing.
    modules += ('site-packages/numpy csv inspect typing pickle threading '
                'pathlib dis annotationlib platform ctypes random _py_warnings glob '
                'tokenize base64 textwrap hmac numbers _compat_pickle linecache '
                '_opcode_metadata fnmatch _weakrefset opcode token').split()
files = set()
for name in modules:
    source = stage / (name + '.py')
    package = stage / name
    if source.is_file():
        files.add(source)
    elif package.is_dir():
        files.update(p for p in package.rglob('*.py')
                     if not {'test', 'tests', '__pycache__'}.intersection(p.relative_to(stage).parts))
size = 0
for source in sorted(files):
    cache = py_compile.compile(str(source),
        dfile='/usr/lib/python3.14/' + source.relative_to(stage).as_posix(),
        doraise=True, optimize=0,
        invalidation_mode=py_compile.PycInvalidationMode.CHECKED_HASH)
    assert cache is not None
    size += Path(cache).stat().st_size
print(f'Precompiled {len(files)} modules, {size} bytecode bytes')
