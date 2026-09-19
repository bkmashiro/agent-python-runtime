#!/usr/bin/env python3
"""Separate RandomState long and Generator int64 symbols in a static link."""
from pathlib import Path
import re
import sys

root = Path(sys.argv[1])
header = root / 'numpy/core/include/numpy/random/distributions.h'
text = header.read_text()
text = re.sub(r'^#define random_\w+ pysolate_legacy_random_\w+\n', '', text, flags=re.M)
marker = '#define RAND_INT_TYPE long'
if text.count(marker) != 1:
    raise SystemExit('Unsupported NumPy distributions header')
source = (root / 'numpy/random/src/distributions/distributions.c').read_text()
names = sorted(set(re.findall(r'\b(random_[A-Za-z0-9_]+)\s*\(', text + source)))
aliases = '\n'.join(f'#define {name} pysolate_legacy_{name}' for name in names)
header.write_text(text.replace(marker, aliases + '\n' + marker))
