#ifndef NODREN_C_API_H
#define NODREN_C_API_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct NodrenCoreConfig {
    size_t worker_count;
    size_t queue_capacity;
} NodrenCoreConfig;

typedef struct NodrenCoreTaskResult {
    uint64_t task_id;
    int64_t value;
    int state;
} NodrenCoreTaskResult;

void* nodren_core_create(const NodrenCoreConfig* config);
int nodren_core_initialize(void* engine);
void nodren_core_destroy(void* engine);

int nodren_core_submit_sum(
    void* engine,
    uint64_t task_id,
    const int32_t* values,
    size_t count);

int nodren_core_execute_sum(
    void* engine,
    uint64_t task_id,
    const int32_t* values,
    size_t count,
    NodrenCoreTaskResult* out);

void nodren_core_wait_idle(void* engine);

#ifdef __cplusplus
}
#endif

#endif
