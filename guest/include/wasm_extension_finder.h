#ifndef AGENT_RUNTIME_WASM_EXTENSION_FINDER_H
#define AGENT_RUNTIME_WASM_EXTENSION_FINDER_H

static const char *AGENT_RUNTIME_WASM_EXTENSION_FINDER_SCRIPT =
    "import sys as _sys, importlib.util as _ilu, importlib.machinery as _ilm, _imp as _imp\n"
    "_SITE = '/usr/lib/python3.14/site-packages'\n"
    "class _AgentRuntimeWasiVFSFinder:\n"
    "    @staticmethod\n"
    "    def _exists(path):\n"
    "        try:\n"
    "            open(path, 'rb').close()\n"
    "            return True\n"
    "        except OSError:\n"
    "            return False\n"
    "    def find_spec(self, fullname, path, target=None):\n"
    "        if _imp.is_builtin(fullname):\n"
    "            return _ilu.spec_from_loader(fullname, _ilm.BuiltinImporter)\n"
    "        base = _SITE + '/' + '/'.join(fullname.split('.'))\n"
    "        init = base + '/__init__.py'\n"
    "        if self._exists(init):\n"
    "            loader = _ilm.SourceFileLoader(fullname, init)\n"
    "            return _ilu.spec_from_file_location(fullname, init, loader=loader, submodule_search_locations=[base])\n"
    "        source = base + '.py'\n"
    "        if self._exists(source):\n"
    "            loader = _ilm.SourceFileLoader(fullname, source)\n"
    "            return _ilu.spec_from_file_location(fullname, source, loader=loader)\n"
    "        return None\n"
    "if _SITE not in _sys.path:\n"
    "    _sys.path.insert(0, _SITE)\n"
    "_sys.meta_path = [finder for finder in _sys.meta_path if finder is not _ilm.PathFinder]\n"
    "_sys.meta_path.append(_AgentRuntimeWasiVFSFinder())\n"
    "_sys.meta_path.append(_ilm.PathFinder)\n";

#endif
