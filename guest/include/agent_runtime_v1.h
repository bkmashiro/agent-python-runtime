#ifndef AGENT_RUNTIME_V1_H
#define AGENT_RUNTIME_V1_H

#include <stdint.h>

#if defined(__wasm__)
#define AGENT_RUNTIME_EXPORT(name) __attribute__((export_name(name)))
#else
#define AGENT_RUNTIME_EXPORT(name)
#endif

AGENT_RUNTIME_EXPORT("runtime_init")
int32_t runtime_init(const char *config, int32_t config_len);

AGENT_RUNTIME_EXPORT("runtime_prepare")
int32_t runtime_prepare(const char *source, int32_t source_len);

AGENT_RUNTIME_EXPORT("alloc")
void *agent_runtime_alloc(int32_t size);

AGENT_RUNTIME_EXPORT("dealloc")
void agent_runtime_dealloc(void *ptr);

AGENT_RUNTIME_EXPORT("execute")
uint32_t agent_runtime_execute(const char *request, int32_t request_len);

#endif
