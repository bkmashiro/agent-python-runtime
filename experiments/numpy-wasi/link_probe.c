#define PY_SSIZE_T_CLEAN
#include <Python.h>
#include "builtin-registry.h"

static int numpy_registered = 0;
static int numpy_imported = 0;

static const char *NUMPY_FINDER_SCRIPT =
    "import sys as _sys, importlib.util as _ilu, importlib.machinery as _ilm, _imp as _imp\n"
    "_SITE = '/usr/lib/python3.14/site-packages'\n"
    "class _WasiVFSFinder:\n"
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
    "_sys.meta_path.append(_WasiVFSFinder())\n"
    "_sys.meta_path.append(_ilm.PathFinder)\n";

static const char *NUMPY_NUMERIC_SCRIPT =
    "import numpy as _np\n"
    "_a = _np.array([1, 2, 3], dtype=_np.int64)\n"
    "assert _a.tolist() == [1, 2, 3]\n"
    "assert int(_a.sum()) == 6\n"
    "_one = _np.longdouble('1')\n"
    "_ld = _np.longdouble('1.0000000000000000000000000000000002')\n"
    "assert _ld > _one\n"
    "assert float(_ld) == 1.0\n"
    "_matrix = _np.array([[1.0, 2.0], [3.0, 4.0]])\n"
    "_det = _np.linalg.det(_matrix)\n"
    "assert abs(float(_det) + 2.0) < 1e-12\n";

static const char *NUMPY_RANDOM_SCRIPT =
    "import numpy as _np\n"
    "_rng1 = _np.random.default_rng(123456789)\n"
    "_rng2 = _np.random.default_rng(123456789)\n"
    "_r1 = _rng1.integers(0, 2147483647, size=32, dtype=_np.int64)\n"
    "_r2 = _rng2.integers(0, 2147483647, size=32, dtype=_np.int64)\n"
    "assert _np.array_equal(_r1, _r2)\n"
    "for _bitgen in (_np.random.MT19937, _np.random.PCG64, _np.random.Philox, _np.random.SFC64):\n"
    "    _g1 = _np.random.Generator(_bitgen(24680))\n"
    "    _g2 = _np.random.Generator(_bitgen(24680))\n"
    "    assert _np.array_equal(_g1.random(8), _g2.random(8))\n"
    "_legacy1 = _np.random.RandomState(13579)\n"
    "_legacy2 = _np.random.RandomState(13579)\n"
    "assert _np.array_equal(_legacy1.randint(0, 1000, size=8), _legacy2.randint(0, 1000, size=8))\n";

static const char *NUMPY_ENTROPY_SCRIPT =
    "import numpy as _np\n"
    "_entropy_bytes = _np.random.default_rng().bytes(32)\n"
    "assert len(_entropy_bytes) == 32\n"
    "assert any(_entropy_bytes)\n";

/* Records the initializer pointers without executing NumPy or Python imports. */
__attribute__((export_name("numpy_register_probe")))
int numpy_register_probe(void) {
    if (numpy_registered) {
        return 0;
    }
    int result = register_selected_builtins();
    if (result != 0) {
        return result;
    }
    numpy_registered = 1;
    return 0;
}

/*
 * Initializes isolated CPython against the packed read-only VFS, installs the
 * bounded dotted-builtin/VFS finder, and performs a real top-level NumPy import.
 * Return codes are diagnostic phases, not a stable product ABI.
 */
__attribute__((export_name("numpy_import_probe")))
int numpy_import_probe(void) {
    if (!numpy_registered) {
        return 10;
    }
    if (!Py_IsInitialized()) {
        PyStatus status;
        PyConfig config;
        PyConfig_InitIsolatedConfig(&config);
        config.use_environment = 0;
        config.user_site_directory = 0;
        config.site_import = 0;
        config.write_bytecode = 0;
        config.module_search_paths_set = 1;
        status = PyWideStringList_Append(&config.module_search_paths,
                                         L"/usr/lib/python3.14");
        if (!PyStatus_Exception(status)) {
            status = PyWideStringList_Append(&config.module_search_paths,
                                             L"/usr/lib/python3.14/site-packages");
        }
        if (!PyStatus_Exception(status)) {
            status = Py_InitializeFromConfig(&config);
        }
        PyConfig_Clear(&config);
        if (PyStatus_Exception(status)) {
            return 20;
        }
    }
    if (PyRun_SimpleString(NUMPY_FINDER_SCRIPT) != 0) {
        PyErr_Print();
        return 30;
    }
    PyObject *numpy = PyImport_ImportModule("numpy");
    if (numpy == NULL) {
        PyErr_Print();
        return 40;
    }
    PyObject *version = PyObject_GetAttrString(numpy, "__version__");
    if (version == NULL || !PyUnicode_Check(version)) {
        Py_XDECREF(version);
        Py_DECREF(numpy);
        PyErr_Print();
        return 50;
    }
    int matches = PyUnicode_CompareWithASCIIString(version, "2.5.1") == 0;
    Py_DECREF(version);
    Py_DECREF(numpy);
    if (!matches) {
        return 60;
    }
    numpy_imported = 1;
    return 0;
}

/* Executes bounded deterministic numeric checks after a successful import. */
__attribute__((export_name("numpy_numeric_probe")))
int numpy_numeric_probe(void) {
    if (!numpy_imported) {
        return 70;
    }
    if (PyRun_SimpleString(NUMPY_NUMERIC_SCRIPT) != 0) {
        PyErr_Print();
        return 80;
    }
    return 0;
}

/* Executes explicit-seed random checks after a successful import. */
__attribute__((export_name("numpy_random_probe")))
int numpy_random_probe(void) {
    if (!numpy_imported) {
        return 90;
    }
    if (PyRun_SimpleString(NUMPY_RANDOM_SCRIPT) != 0) {
        PyErr_Print();
        return 100;
    }
    return 0;
}

/* Executes unseeded generation only with Host-provided entropy. */
__attribute__((export_name("numpy_entropy_probe")))
int numpy_entropy_probe(void) {
    if (!numpy_imported) {
        return 110;
    }
    if (PyRun_SimpleString(NUMPY_ENTROPY_SCRIPT) != 0) {
        PyErr_Print();
        return 120;
    }
    return 0;
}

__attribute__((export_name("numpy_python_initialized_probe")))
int numpy_python_initialized_probe(void) {
    return Py_IsInitialized();
}
