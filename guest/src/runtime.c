#include "agent_runtime_v1.h"

#define PY_SSIZE_T_CLEAN
#include <Python.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

#ifdef AGENT_RUNTIME_EXTENSION_PROFILE
#include "builtin-registry.h"
#include "wasm_extension_finder.h"
#endif

#define AGENT_RUNTIME_REQUEST_MAX (16 * 1024 * 1024)
#define AGENT_RUNTIME_RESPONSE_MAX (16 * 1024 * 1024)
#define AGENT_RUNTIME_TOOL_RESPONSE_MAX (1024 * 1024)
#define AGENT_RUNTIME_MATERIALIZED_RESPONSE_MAX 256
#define AGENT_RUNTIME_PREPARED_DESCRIPTOR_MAX 4096
#define AGENT_RUNTIME_PREPARED_BODY_MAX (8 * 1024 * 1024)

static uint8_t response_buffer[AGENT_RUNTIME_RESPONSE_MAX + 4];
static PyObject *runtime_module = NULL;
static PyObject *allowed_import_names = NULL;
static PyObject *import_receipts = NULL;
static int import_policy_sealed = 0;
static int audit_hook_registered = 0;

static int agent_runtime_audit_hook(const char *event, PyObject *args,
                                    void *user_data) {
    (void)user_data;
    if (!import_policy_sealed || strcmp(event, "agent_runtime.import") != 0) {
        return 0;
    }
    if (!PyTuple_Check(args) || PyTuple_GET_SIZE(args) != 1) {
        PyErr_SetString(PyExc_RuntimeError, "invalid Pysolate import audit event");
        return -1;
    }
    PyObject *name = PyTuple_GET_ITEM(args, 0);
    if (!PyUnicode_Check(name) || allowed_import_names == NULL) {
        PyErr_SetString(PyExc_ImportError, "Pysolate import policy is unavailable");
        return -1;
    }
    int admitted = PySet_Contains(allowed_import_names, name);
    if (admitted < 0) {
        return -1;
    }
    if (import_receipts == NULL || PyList_GET_SIZE(import_receipts) >= 1024) {
        PyErr_SetString(PyExc_RuntimeError, "Pysolate import receipt bound exceeded");
        return -1;
    }
    PyObject *receipt = Py_BuildValue("(OO)", name,
                                      admitted ? Py_True : Py_False);
    if (receipt == NULL || PyList_Append(import_receipts, receipt) < 0) {
        Py_XDECREF(receipt);
        return -1;
    }
    Py_DECREF(receipt);
    if (!admitted) {
        PyErr_Format(PyExc_ImportError,
                     "module is outside the sealed Pysolate import set: %U",
                     name);
        return -1;
    }
    return 0;
}

static void write_u32_le(uint8_t *dst, uint32_t value) {
    dst[0] = (uint8_t)(value & 0xffu);
    dst[1] = (uint8_t)((value >> 8) & 0xffu);
    dst[2] = (uint8_t)((value >> 16) & 0xffu);
    dst[3] = (uint8_t)((value >> 24) & 0xffu);
}

static uint32_t write_internal_error(void) {
    static const char payload[] =
        "{\"status\":\"error\",\"result\":null,\"receipts\":[],"
        "\"metrics\":{\"capability_calls\":0,\"result_bytes\":0},"
        "\"error\":{\"code\":\"guest_internal\","
        "\"message\":\"guest runtime call failed\"}}";
    const uint32_t length = (uint32_t)(sizeof(payload) - 1);
    write_u32_le(response_buffer, length);
    memcpy(response_buffer + 4, payload, length);
    return (uint32_t)(uintptr_t)response_buffer;
}

static uint32_t write_python_unicode(PyObject *result) {
    if (result == NULL) {
        PyErr_Clear();
        return write_internal_error();
    }
    Py_ssize_t length = 0;
    const char *payload = PyUnicode_AsUTF8AndSize(result, &length);
    if (payload == NULL || length < 0 || length > AGENT_RUNTIME_RESPONSE_MAX) {
        Py_DECREF(result);
        PyErr_Clear();
        return write_internal_error();
    }
    write_u32_le(response_buffer, (uint32_t)length);
    memcpy(response_buffer + 4, payload, (size_t)length);
    Py_DECREF(result);
    return (uint32_t)(uintptr_t)response_buffer;
}

static PyObject *python_host_call(PyObject *self, PyObject *args) {
    (void)self;
    const char *request = NULL;
    Py_ssize_t request_len = 0;
    if (!PyArg_ParseTuple(args, "s#:call", &request, &request_len)) {
        return NULL;
    }
    if (request_len < 0 || request_len > AGENT_RUNTIME_REQUEST_MAX) {
        PyErr_SetString(PyExc_ValueError, "Host call request exceeds the guest bound");
        return NULL;
    }
    char *response = malloc(AGENT_RUNTIME_TOOL_RESPONSE_MAX);
    if (response == NULL) {
        return PyErr_NoMemory();
    }
    int32_t response_len = agent_runtime_host_call(
        request,
        (int32_t)request_len,
        response,
        AGENT_RUNTIME_TOOL_RESPONSE_MAX);
    if (response_len < 0) {
        free(response);
        PyErr_SetString(PyExc_RuntimeError, "Host capability bridge rejected the call");
        return NULL;
    }
    if (response_len > AGENT_RUNTIME_TOOL_RESPONSE_MAX) {
        free(response);
        PyErr_SetString(PyExc_RuntimeError, "Host capability response exceeds the guest bound");
        return NULL;
    }
    PyObject *result = PyUnicode_DecodeUTF8(response, response_len, "strict");
    free(response);
    return result;
}

static PyObject *python_materialize_value(PyObject *self, PyObject *args) {
    (void)self;
    const char *decision = NULL;
    Py_ssize_t decision_len = 0;
    if (!PyArg_ParseTuple(args, "s#:materialize_value", &decision, &decision_len)) {
        return NULL;
    }
    if (decision_len != 71) {
        PyErr_SetString(PyExc_RuntimeError, "prepared region decision identity is invalid");
        return NULL;
    }
    char response[AGENT_RUNTIME_MATERIALIZED_RESPONSE_MAX];
    int32_t response_len = agent_runtime_materialize_value(
        decision, (int32_t)decision_len, response,
        AGENT_RUNTIME_MATERIALIZED_RESPONSE_MAX);
    if (response_len <= 0 || response_len > AGENT_RUNTIME_MATERIALIZED_RESPONSE_MAX) {
        PyErr_SetString(PyExc_RuntimeError, "Host prepared region claim failed");
        return NULL;
    }
    return PyUnicode_DecodeUTF8(response, response_len, "strict");
}

static PyObject *python_seal_imports(PyObject *self, PyObject *names) {
    (void)self;
    if (import_policy_sealed) {
        PyErr_SetString(PyExc_RuntimeError, "Pysolate import policy is already sealed");
        return NULL;
    }
    PyObject *sequence = PySequence_Fast(names, "import names must be a sequence");
    if (sequence == NULL) {
        return NULL;
    }
    PyObject *allowed = PySet_New(NULL);
    if (allowed == NULL) {
        Py_DECREF(sequence);
        return NULL;
    }
    Py_ssize_t count = PySequence_Fast_GET_SIZE(sequence);
    for (Py_ssize_t index = 0; index < count; index++) {
        PyObject *name = PySequence_Fast_GET_ITEM(sequence, index);
        if (!PyUnicode_Check(name) || PyUnicode_GET_LENGTH(name) == 0 ||
            PyUnicode_GET_LENGTH(name) > 256 || PySet_Add(allowed, name) < 0) {
            Py_DECREF(allowed);
            Py_DECREF(sequence);
            if (!PyErr_Occurred()) {
                PyErr_SetString(PyExc_ValueError, "invalid sealed import name");
            }
            return NULL;
        }
    }
    Py_DECREF(sequence);
    PyObject *receipts = PyList_New(0);
    if (receipts == NULL) {
        Py_DECREF(allowed);
        return NULL;
    }
    allowed_import_names = allowed;
    import_receipts = receipts;
    import_policy_sealed = 1;
    Py_RETURN_NONE;
}

static PyObject *python_import_receipts(PyObject *self, PyObject *unused) {
    (void)self;
    (void)unused;
    if (!import_policy_sealed || import_receipts == NULL) {
        PyErr_SetString(PyExc_RuntimeError,
                        "Pysolate import policy is not sealed");
        return NULL;
    }
    return PyList_GetSlice(import_receipts, 0,
                           PyList_GET_SIZE(import_receipts));
}

static PyMethodDef agent_runtime_host_methods[] = {
    {"call", python_host_call, METH_VARARGS, "Perform a bounded Host capability call."},
    {"materialize_value", python_materialize_value, METH_VARARGS, "Claim one exact prepared scalar value."},
    {"seal_imports", python_seal_imports, METH_O, "Seal the exact per-Run import set."},
    {"import_receipts", python_import_receipts, METH_NOARGS, "Read bounded native import receipts."},
    {NULL, NULL, 0, NULL},
};

static struct PyModuleDef agent_runtime_host_module = {
    PyModuleDef_HEAD_INIT,
    "_agent_runtime_host",
    NULL,
    -1,
    agent_runtime_host_methods,
};

PyMODINIT_FUNC PyInit__agent_runtime_host(void) {
    return PyModule_Create(&agent_runtime_host_module);
}

static int32_t ensure_interpreter(void) {
    if (Py_IsInitialized()) {
        return 0;
    }

    if (PyImport_AppendInittab("_agent_runtime_host", PyInit__agent_runtime_host) != 0) {
        return -1;
    }
    if (!audit_hook_registered) {
        if (PySys_AddAuditHook(agent_runtime_audit_hook, NULL) != 0) {
            return -1;
        }
        audit_hook_registered = 1;
    }
#ifdef AGENT_RUNTIME_EXTENSION_PROFILE
    if (register_selected_builtins() != 0) {
        return -1;
    }
#endif

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
        return -1;
    }
#ifdef AGENT_RUNTIME_EXTENSION_PROFILE
    if (PyRun_SimpleString(AGENT_RUNTIME_WASM_EXTENSION_FINDER_SCRIPT) != 0) {
        PyErr_Print();
        return -1;
    }
#endif

    runtime_module = PyImport_ImportModule("agent_runtime");
    if (runtime_module == NULL) {
        PyErr_Print();
        return -1;
    }
    return 0;
}

static PyObject *call_with_utf8(const char *function_name,
                                const char *bytes,
                                int32_t length) {
    if (runtime_module == NULL || bytes == NULL || length < 0 ||
        length > AGENT_RUNTIME_REQUEST_MAX) {
        return NULL;
    }
    PyObject *function = PyObject_GetAttrString(runtime_module, function_name);
    if (function == NULL) {
        return NULL;
    }
    PyObject *argument = PyUnicode_DecodeUTF8(bytes, (Py_ssize_t)length, "strict");
    if (argument == NULL) {
        Py_DECREF(function);
        return NULL;
    }
    PyObject *result = PyObject_CallOneArg(function, argument);
    Py_DECREF(argument);
    Py_DECREF(function);
    return result;
}

int32_t runtime_init(const char *config, int32_t config_len) {
    if (ensure_interpreter() != 0) {
        return -1;
    }
    PyObject *result = call_with_utf8("_initialize", config, config_len);
    if (result == NULL) {
        PyErr_Print();
        return -1;
    }
    Py_DECREF(result);
    return 0;
}

int32_t runtime_validate_source(const char *request, int32_t request_len) {
    if (!Py_IsInitialized() || runtime_module == NULL) {
        return -1;
    }
    PyObject *result = call_with_utf8("_validate_request_source", request,
                                     request_len);
    if (result == NULL) {
        PyErr_Print();
        return -1;
    }
    long status = PyLong_AsLong(result);
    Py_DECREF(result);
    if (status < 0 || status > 2 || PyErr_Occurred()) {
        PyErr_Clear();
        return -1;
    }
    return (int32_t)status;
}


uint32_t runtime_analyze_source(const char *request, int32_t request_len) {
    if (!Py_IsInitialized() || runtime_module == NULL || request == NULL ||
        request_len < 0 || request_len > AGENT_RUNTIME_REQUEST_MAX) {
        return write_internal_error();
    }
    PyObject *module = PyImport_ImportModule("agent_runtime.semantic");
    if (module == NULL) {
        return write_python_unicode(NULL);
    }
    PyObject *function = PyObject_GetAttrString(module, "analyze_request_json");
    Py_DECREF(module);
    if (function == NULL) {
        return write_python_unicode(NULL);
    }
    PyObject *argument = PyUnicode_DecodeUTF8(request, (Py_ssize_t)request_len,
                                              "strict");
    if (argument == NULL) {
        Py_DECREF(function);
        return write_python_unicode(NULL);
    }
    PyObject *result = PyObject_CallOneArg(function, argument);
    Py_DECREF(argument);
    Py_DECREF(function);
    return write_python_unicode(result);
}


uint32_t runtime_emit_prepared_region_patch(const char *request, int32_t request_len) {
    if (!Py_IsInitialized() || runtime_module == NULL || request == NULL ||
        request_len < 0 || request_len > AGENT_RUNTIME_REQUEST_MAX) {
        return write_internal_error();
    }
    PyObject *module = PyImport_ImportModule("agent_runtime.prepared_region");
    if (module == NULL) {
        return write_python_unicode(NULL);
    }
    PyObject *function = PyObject_GetAttrString(module, "emit_prepared_region_patch_request_json");
    Py_DECREF(module);
    if (function == NULL) {
        return write_python_unicode(NULL);
    }
    PyObject *argument = PyUnicode_DecodeUTF8(request, (Py_ssize_t)request_len,
                                              "strict");
    if (argument == NULL) {
        Py_DECREF(function);
        return write_python_unicode(NULL);
    }
    PyObject *result = PyObject_CallOneArg(function, argument);
    Py_DECREF(argument);
    Py_DECREF(function);
    return write_python_unicode(result);
}


uint32_t runtime_execute_prepared_region_scratch(const char *request, int32_t request_len) {
    if (!Py_IsInitialized() || runtime_module == NULL || request == NULL ||
        request_len < 0 || request_len > AGENT_RUNTIME_REQUEST_MAX) {
        return write_internal_error();
    }
    PyObject *module = PyImport_ImportModule("agent_runtime.prepared_region");
    if (module == NULL) {
        return write_python_unicode(NULL);
    }
    PyObject *function = PyObject_GetAttrString(module, "execute_prepared_region_scratch_request_json");
    Py_DECREF(module);
    if (function == NULL) {
        return write_python_unicode(NULL);
    }
    PyObject *argument = PyUnicode_DecodeUTF8(request, (Py_ssize_t)request_len,
                                              "strict");
    if (argument == NULL) {
        Py_DECREF(function);
        return write_python_unicode(NULL);
    }
    PyObject *result = PyObject_CallOneArg(function, argument);
    Py_DECREF(argument);
    Py_DECREF(function);
    return write_python_unicode(result);
}


int32_t runtime_select_prepared_region_execution(const char *request, int32_t request_len) {
    if (!Py_IsInitialized() || runtime_module == NULL || request == NULL ||
        request_len < 0 || request_len > AGENT_RUNTIME_REQUEST_MAX) {
        return -1;
    }
    PyObject *result = call_with_utf8("_prepare_prepared_region_execution", request,
                                      request_len);
    if (result == NULL) {
        PyErr_Print();
        return -1;
    }
    Py_DECREF(result);
    return 0;
}


int32_t runtime_prepare_numpy_ndarray(const char *descriptor,
                                      int32_t descriptor_len,
                                      const uint8_t *body,
                                      int32_t body_len) {
    if (!Py_IsInitialized() || runtime_module == NULL || descriptor == NULL ||
        body == NULL || descriptor_len <= 0 ||
        descriptor_len > AGENT_RUNTIME_PREPARED_DESCRIPTOR_MAX || body_len <= 0 ||
        body_len > AGENT_RUNTIME_PREPARED_BODY_MAX) {
        return -1;
    }
    PyObject *function = PyObject_GetAttrString(runtime_module,
                                                "_prepare_numpy_ndarray");
    if (function == NULL) {
        PyErr_Print();
        return -1;
    }
    PyObject *descriptor_value = PyUnicode_DecodeUTF8(
        descriptor, (Py_ssize_t)descriptor_len, "strict");
    PyObject *body_value = PyByteArray_FromStringAndSize(
        (const char *)body, (Py_ssize_t)body_len);
    if (descriptor_value == NULL || body_value == NULL) {
        Py_XDECREF(descriptor_value);
        Py_XDECREF(body_value);
        Py_DECREF(function);
        PyErr_Print();
        return -1;
    }
    PyObject *result = PyObject_CallFunctionObjArgs(
        function, descriptor_value, body_value, NULL);
    Py_DECREF(descriptor_value);
    Py_DECREF(body_value);
    Py_DECREF(function);
    if (result == NULL) {
        PyErr_Print();
        return -1;
    }
    Py_DECREF(result);
    return 0;
}


int32_t runtime_prepare(const char *source, int32_t source_len) {
    if (!Py_IsInitialized() || runtime_module == NULL) {
        return -1;
    }
    PyObject *result = call_with_utf8("_prepare", source, source_len);
    if (result == NULL) {
        PyErr_Print();
        return -1;
    }
    Py_DECREF(result);
    return 0;
}


void *alloc(int32_t size) {
    if (size <= 0 || size > AGENT_RUNTIME_REQUEST_MAX) {
        return NULL;
    }
    return malloc((size_t)size);
}

void dealloc(void *ptr) {
    free(ptr);
}

uint32_t execute(const char *request, int32_t request_len) {
    if (!Py_IsInitialized() || runtime_module == NULL || request == NULL ||
        request_len < 0 || request_len > AGENT_RUNTIME_REQUEST_MAX) {
        return write_internal_error();
    }
    return write_python_unicode(call_with_utf8("_execute", request,
                                               request_len));
}
