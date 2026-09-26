#ifndef THREADPOOL_H
#define THREADPOOL_H

typedef struct ThreadPool ThreadPool;

ThreadPool *thread_pool_create(int worker_count);
void thread_pool_destroy(ThreadPool *pool);
int thread_pool_submit(ThreadPool *pool, void (*task)(void *), void *arg);

#endif
