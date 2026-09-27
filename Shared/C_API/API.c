#include "nodren_c_api.h"

int API_execute_sum(
    void* engine,
    uint64_t task_id,
    const int32_t* values,
    size_t count,
    NodrenCoreTaskResult* out)
{
    return nodren_core_execute_sum(
        engine,
        task_id,
        values,
        count,
        out
    );
}