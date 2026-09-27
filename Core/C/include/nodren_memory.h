#ifndef NODREN_MEMORY_H
#define NODREN_MEMORY_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct NodrenMemoryStats {
    uint64_t allocations;
    uint64_t frees;
    uint64_t live_bytes;
    uint64_t peak_bytes;
} NodrenMemoryStats;

typedef struct NodrenArena {
    uint8_t* base;
    size_t capacity;
    size_t offset;
} NodrenArena;

int nodren_memory_init(void);
void nodren_memory_shutdown(void);

void* nodren_alloc(size_t size);
void* nodren_aligned_alloc(size_t alignment, size_t size);
void nodren_free(void* ptr);

int nodren_arena_init(NodrenArena* arena, size_t capacity, size_t alignment);
void* nodren_arena_alloc(NodrenArena* arena, size_t size, size_t alignment);
void nodren_arena_reset(NodrenArena* arena);
void nodren_arena_destroy(NodrenArena* arena);

void nodren_memory_stats(NodrenMemoryStats* out);

#ifdef __cplusplus
}
#endif

#endif
