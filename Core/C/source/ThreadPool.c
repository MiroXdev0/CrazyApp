#include "ThreadPool.h"
#include <stdlib.h>

struct ThreadPool {
    int worker_count;
};

ThreadPool *thread_pool_create(int worker_count) {
    ThreadPool *pool = calloc(1, sizeof(ThreadPool));
    if (!pool) {
        return NULL;
    }

    pool->worker_count = worker_count > 0 ? worker_count : 1;
    return pool;
}

void thread_pool_destroy(ThreadPool *pool) {
    free(pool);
}

int thread_pool_submit(ThreadPool *pool, void (*task)(void *), void *arg) {
    (void)pool;
    (void)task;
    (void)arg;
    return 0;
}
