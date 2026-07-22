#define PY_SSIZE_T_CLEAN
#include <Python.h>

extern PyObject *PyInit__multiarray_umath(void);
extern PyObject *PyInit__umath_linalg(void);

static int numpy_registered = 0;

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

/* Records the initializer pointers without executing NumPy or Python imports. */
__attribute__((export_name("numpy_register_probe")))
int numpy_register_probe(void) {
    if (numpy_registered) {
        return 0;
    }
    if (PyImport_AppendInittab("numpy._core._multiarray_umath",
                               PyInit__multiarray_umath) != 0) {
        return 1;
    }
    if (PyImport_AppendInittab("numpy.linalg._umath_linalg",
                               PyInit__umath_linalg) != 0) {
        return 2;
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
    return 0;
}

__attribute__((export_name("numpy_python_initialized_probe")))
int numpy_python_initialized_probe(void) {
    return Py_IsInitialized();
}
