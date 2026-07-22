#include "agent_runtime_v1.h"

#include <Python.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

#define AGENT_RUNTIME_REQUEST_MAX (1024 * 1024)
#define AGENT_RUNTIME_RESPONSE_MAX (1024 * 1024)

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

static int32_t ensure_interpreter(void) {
    if (Py_IsInitialized()) {
        return 0;
    }

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
