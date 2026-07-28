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

#define AGENT_RUNTIME_REQUEST_MAX (1024 * 1024)
#define AGENT_RUNTIME_RESPONSE_MAX (1024 * 1024)
#define AGENT_RUNTIME_TOOL_RESPONSE_MAX (1024 * 1024)

static uint8_t response_buffer[AGENT_RUNTIME_RESPONSE_MAX + 4];
static PyObject *runtime_module = NULL;

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

static PyMethodDef agent_runtime_host_methods[] = {
    {"call", python_host_call, METH_VARARGS, "Perform a bounded Host capability call."},
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
#ifdef AGENT_RUNTIME_PREINITIALIZATION_SPIKE
    config.use_hash_seed = 1;
    config.hash_seed = 0xa9e17f5dUL;
#endif

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

#ifdef AGENT_RUNTIME_PREINITIALIZATION_SPIKE
static void preinitialize_python_or_trap(void) {
    static const char config[] = "{}";
    fprintf(stderr, "preinitialization-spike: begin\n");
    fflush(stderr);
    int32_t status = runtime_init(config, (int32_t)(sizeof(config) - 1));
    fprintf(stderr,
            "preinitialization-spike: runtime_init status=%d python_initialized=%d\n",
            status,
            Py_IsInitialized());
    fflush(stderr);
    if (status != 0) {
        __builtin_trap();
    }
}

__attribute__((export_name("runtime_preinitialize")))
void runtime_preinitialize(void) {
    preinitialize_python_or_trap();
}

__attribute__((export_name("runtime_preinitialized_initialize")))
void runtime_preinitialized_initialize(void) {}
#endif

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

__attribute__((export_name("runtime_warmup")))
int32_t runtime_warmup(const char *profile, int32_t profile_len) {
    if (!Py_IsInitialized() || runtime_module == NULL) {
        return -1;
    }
    PyObject *result = call_with_utf8("_warmup", profile, profile_len);
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

    PyObject *result = call_with_utf8("_execute", request, request_len);
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
