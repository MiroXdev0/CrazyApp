#ifndef NODREN_NATIVE_TASK_BRIDGE_H
#define NODREN_NATIVE_TASK_BRIDGE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

void *nodren_native_create_engine(void);
void nodren_native_destroy_engine(void *engine);
long long nodren_native_submit_task(void *engine, const char *task_id, const int32_t *payload, size_t payload_len);

#ifdef __cplusplus
}
#endif

#endif
