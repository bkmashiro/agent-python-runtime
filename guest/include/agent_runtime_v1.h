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

AGENT_RUNTIME_EXPORT("runtime_init")
int32_t runtime_init(const char *config, int32_t config_len);

AGENT_RUNTIME_EXPORT("runtime_validate_source")
int32_t runtime_validate_source(const char *request, int32_t request_len);

AGENT_RUNTIME_EXPORT("runtime_prepare")
int32_t runtime_prepare(const char *source, int32_t source_len);

AGENT_RUNTIME_EXPORT("runtime_warmup")
int32_t runtime_warmup(const char *profile, int32_t profile_len);

AGENT_RUNTIME_EXPORT("alloc")
void *alloc(int32_t size);

AGENT_RUNTIME_EXPORT("dealloc")
void dealloc(void *ptr);

AGENT_RUNTIME_EXPORT("execute")
uint32_t execute(const char *request, int32_t request_len);

#endif
