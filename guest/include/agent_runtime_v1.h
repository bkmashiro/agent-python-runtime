#ifndef AGENT_RUNTIME_V1_H
#define AGENT_RUNTIME_V1_H

#include <stdint.h>

#if defined(__wasm__)
#define AGENT_RUNTIME_EXPORT(name) __attribute__((export_name(name)))
#define AGENT_RUNTIME_IMPORT(module_name, import_name_value) \
    __attribute__((import_module(module_name), import_name(import_name_value)))
#else
#define AGENT_RUNTIME_EXPORT(name)
#define AGENT_RUNTIME_IMPORT(module_name, import_name_value)
#endif

AGENT_RUNTIME_IMPORT("agent_runtime_v1", "host_call")
int32_t agent_runtime_host_call(const char *request,
                                int32_t request_len,
                                char *response,
                                int32_t response_cap);

AGENT_RUNTIME_IMPORT("agent_runtime_v1", "materialize_value")
int32_t agent_runtime_materialize_value(const char *decision,
                                        int32_t decision_len,
                                        char *response,
                                        int32_t response_cap);

AGENT_RUNTIME_EXPORT("runtime_init")
int32_t runtime_init(const char *config, int32_t config_len);

AGENT_RUNTIME_EXPORT("runtime_validate_source")
int32_t runtime_validate_source(const char *request, int32_t request_len);

AGENT_RUNTIME_EXPORT("runtime_analyze_source")
uint32_t runtime_analyze_source(const char *request, int32_t request_len);

AGENT_RUNTIME_EXPORT("runtime_emit_prepared_region_patch")
uint32_t runtime_emit_prepared_region_patch(const char *request, int32_t request_len);

AGENT_RUNTIME_EXPORT("runtime_execute_prepared_region_scratch")
uint32_t runtime_execute_prepared_region_scratch(const char *request, int32_t request_len);

AGENT_RUNTIME_EXPORT("runtime_select_prepared_region_execution")
int32_t runtime_select_prepared_region_execution(const char *request, int32_t request_len);

AGENT_RUNTIME_EXPORT("runtime_prepare_numpy_ndarray")
int32_t runtime_prepare_numpy_ndarray(const char *descriptor,
                                      int32_t descriptor_len,
                                      const uint8_t *body,
                                      int32_t body_len);

AGENT_RUNTIME_EXPORT("runtime_prepare")
int32_t runtime_prepare(const char *source, int32_t source_len);

AGENT_RUNTIME_EXPORT("alloc")
void *alloc(int32_t size);

AGENT_RUNTIME_EXPORT("dealloc")
void dealloc(void *ptr);

AGENT_RUNTIME_EXPORT("execute")
uint32_t execute(const char *request, int32_t request_len);

#endif
