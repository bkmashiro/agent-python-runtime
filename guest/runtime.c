#define PY_SSIZE_T_CLEAN
#include <Python.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#ifdef PYSOLATE_NUMPY
#include "builtin-registry.h"
#endif

#define EXPORT(name) __attribute__((export_name(name)))
#define MAX_MESSAGE (1 << 20)

__attribute__((import_module("pysolate"), import_name("call")))
int32_t host_call(const char *request, int32_t size, char *out, int32_t capacity);
__attribute__((import_module("pysolate"), import_name("prepare")))
uint32_t host_prepare(const char *request, int32_t size);
__attribute__((import_module("pysolate"), import_name("resolve")))
int32_t host_resolve(uint32_t id, const char *request, int32_t size, char *out, int32_t capacity);

// Python string → Host JSON request → Python bytes. Buffer lives only for this call.
static PyObject *python_call(PyObject *self, PyObject *args) {
    const char *request;
    Py_ssize_t size;
    if (!PyArg_ParseTuple(args, "s#", &request, &size)) return NULL;
    if (size > MAX_MESSAGE) { PyErr_SetString(PyExc_ValueError, "tool request exceeds 1 MiB"); return NULL; }
    char *buffer = malloc(MAX_MESSAGE);
    if (!buffer) return PyErr_NoMemory();
    int32_t n = host_call(request, (int32_t)size, buffer, MAX_MESSAGE);
    PyObject *result = NULL;
    if (n < 0 || n > MAX_MESSAGE) PyErr_SetString(PyExc_RuntimeError, "Host tool response exceeds buffer or ABI bounds");
    else result = PyBytes_FromStringAndSize(buffer, n);
    free(buffer);
    return result;
}

static PyObject *python_prepare(PyObject *self, PyObject *args) {
    const char *request;
    Py_ssize_t size;
    if (!PyArg_ParseTuple(args, "s#", &request, &size)) return NULL;
    if (size > MAX_MESSAGE) return PyLong_FromUnsignedLong(0);
    return PyLong_FromUnsignedLong(host_prepare(request, (int32_t)size));
}

static PyObject *python_resolve(PyObject *self, PyObject *args) {
    unsigned int id;
    const char *request;
    Py_ssize_t size;
    if (!PyArg_ParseTuple(args, "Is#", &id, &request, &size)) return NULL;
    if (size > MAX_MESSAGE) { PyErr_SetString(PyExc_ValueError, "tool request exceeds 1 MiB"); return NULL; }
    char *buffer = malloc(MAX_MESSAGE);
    if (!buffer) return PyErr_NoMemory();
    int32_t n = host_resolve(id, request, (int32_t)size, buffer, MAX_MESSAGE);
    PyObject *result = NULL;
    if (n < 0 || n > MAX_MESSAGE) PyErr_SetString(PyExc_RuntimeError, "Host tool response exceeds buffer or ABI bounds");
    else result = PyBytes_FromStringAndSize(buffer, n);
    free(buffer);
    return result;
}

static PyMethodDef methods[] = {
    {"call", python_call, METH_VARARGS, "Call a registered Host tool."},
    {"prepare", python_prepare, METH_VARARGS, "Start an allowed read, returning a run-local handle."},
    {"resolve", python_resolve, METH_VARARGS, "Consume a read at its original call position."},
    {NULL, NULL, 0, NULL}
};
static struct PyModuleDef module = {PyModuleDef_HEAD_INIT, "_pysolate", NULL, -1, methods};
static PyObject *init_pysolate(void) { return PyModule_Create(&module); }
static PyObject *execute_fn, *prefix_begin_fn, *prefix_feed_fn;

EXPORT("init") int32_t init(void) {
    if (PyImport_AppendInittab("_pysolate", init_pysolate) != 0) return -1;
#ifdef PYSOLATE_NUMPY
    if (register_selected_builtins() != 0) return -1;
#endif
    PyConfig config;
    PyConfig_InitIsolatedConfig(&config);
    config.site_import = 0;
    config.write_bytecode = 0;
    config.module_search_paths_set = 1;
    PyStatus status = PyWideStringList_Append(&config.module_search_paths, L"/usr/lib/python3.14");
    if (!PyStatus_Exception(status)) status = PyWideStringList_Append(&config.module_search_paths, L"/usr/lib/python3.14/site-packages");
    if (!PyStatus_Exception(status)) status = Py_InitializeFromConfig(&config);
    if (PyStatus_Exception(status)) {
        fprintf(stderr, "CPython init: %s\n", status.err_msg ? status.err_msg : "unknown error");
        PyConfig_Clear(&config);
        return -1;
    }
    PyConfig_Clear(&config);
    PyObject *bootstrap = PyImport_ImportModule("pysolate_bootstrap");
    if (!bootstrap) { PyErr_Print(); return -1; }
    execute_fn = PyObject_GetAttrString(bootstrap, "execute");
    prefix_begin_fn = PyObject_GetAttrString(bootstrap, "prefix_begin");
    prefix_feed_fn = PyObject_GetAttrString(bootstrap, "prefix_feed");
    Py_DECREF(bootstrap);
    if (!execute_fn || !prefix_begin_fn || !prefix_feed_fn) { PyErr_Print(); return -1; }
    return 0;
}

// Prefix calls only parse received source and issue eligible reads, never execute it.
static int32_t prefix_step(PyObject *fn, const char *data, int32_t size) {
    PyObject *result = PyObject_CallFunction(fn, "s#", data, (Py_ssize_t)size);
    if (!result) { PyErr_Print(); return -1; }
    Py_DECREF(result);
    return 0;
}
EXPORT("prefix_begin") int32_t prefix_begin(const char *data, int32_t size) {
    return prefix_step(prefix_begin_fn, data, size);
}
EXPORT("prefix_feed") int32_t prefix_feed(const char *data, int32_t size) {
    return prefix_step(prefix_feed_fn, data, size);
}

EXPORT("alloc") void *alloc(uint32_t size) { return malloc(size); }
EXPORT("release") void release(void *ptr) { free(ptr); }

// Caller owns returned malloc buffer; upper 32 bits length, lower 32 bits pointer.
EXPORT("execute") uint64_t execute(const char *request, int32_t size) {
    PyObject *result = PyObject_CallFunction(execute_fn, "s#", request, (Py_ssize_t)size);
    if (!result) { PyErr_Print(); return 0; }
    char *data;
    Py_ssize_t length;
    if (PyBytes_AsStringAndSize(result, &data, &length) < 0) { Py_DECREF(result); PyErr_Print(); return 0; }
    char *copy = malloc(length);
    if (!copy) { Py_DECREF(result); return 0; }
    memcpy(copy, data, length);
    Py_DECREF(result);
    return ((uint64_t)(uint32_t)length << 32) | (uint32_t)(uintptr_t)copy;
}
