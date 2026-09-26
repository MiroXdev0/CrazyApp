#ifndef CRAZYAPP_MEMORY_H
#define CRAZYAPP_MEMORY_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct crazyapp_memory_stats {
    size_t live_bytes;
    size_t peak_bytes;
    size_t total_allocations;
    size_t total_frees;
} crazyapp_memory_stats_t;

int crazyapp_memory_init(void);
void crazyapp_memory_shutdown(void);
void *crazyapp_alloc(size_t size);
void crazyapp_free(void *ptr);
void *crazyapp_aligned_alloc(size_t alignment, size_t size);
void crazyapp_get_memory_stats(crazyapp_memory_stats_t *stats);

#ifdef __cplusplus
}
#endif

#endif
