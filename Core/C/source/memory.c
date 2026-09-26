#include "memory.h"

#include <stdint.h>
#include <stdlib.h>

#define CRAZYAPP_MAX_ALIGNED_BLOCKS 32

typedef struct aligned_block {
    void *aligned_ptr;
    void *base_ptr;
    size_t size;
} aligned_block_t;

static crazyapp_memory_stats_t g_memory_stats = {0};
static aligned_block_t g_aligned_blocks[CRAZYAPP_MAX_ALIGNED_BLOCKS] = {0};

static void crazyapp_track_alloc(size_t size) {
    g_memory_stats.live_bytes += size;
    if (g_memory_stats.live_bytes > g_memory_stats.peak_bytes) {
        g_memory_stats.peak_bytes = g_memory_stats.live_bytes;
    }
    g_memory_stats.total_allocations += 1;
}

static void crazyapp_track_free(size_t size) {
    if (g_memory_stats.live_bytes >= size) {
        g_memory_stats.live_bytes -= size;
    }
    g_memory_stats.total_frees += 1;
}

static void crazyapp_register_aligned_block(void *aligned_ptr, void *base_ptr, size_t size) {
    for (size_t index = 0; index < CRAZYAPP_MAX_ALIGNED_BLOCKS; ++index) {
        if (g_aligned_blocks[index].aligned_ptr == NULL) {
            g_aligned_blocks[index].aligned_ptr = aligned_ptr;
            g_aligned_blocks[index].base_ptr = base_ptr;
            g_aligned_blocks[index].size = size;
            return;
        }
    }
}

static void crazyapp_unregister_aligned_block(void *aligned_ptr) {
    for (size_t index = 0; index < CRAZYAPP_MAX_ALIGNED_BLOCKS; ++index) {
        if (g_aligned_blocks[index].aligned_ptr == aligned_ptr) {
            g_aligned_blocks[index].aligned_ptr = NULL;
            g_aligned_blocks[index].base_ptr = NULL;
            g_aligned_blocks[index].size = 0;
            return;
        }
    }
}

int crazyapp_memory_init(void) {
    g_memory_stats.live_bytes = 0;
    g_memory_stats.peak_bytes = 0;
    g_memory_stats.total_allocations = 0;
    g_memory_stats.total_frees = 0;
    for (size_t i = 0; i < CRAZYAPP_MAX_ALIGNED_BLOCKS; ++i) {
        g_aligned_blocks[i].aligned_ptr = NULL;
        g_aligned_blocks[i].base_ptr = NULL;
        g_aligned_blocks[i].size = 0;
    }
    return 0;
}

void crazyapp_memory_shutdown(void) {
    for (size_t i = 0; i < CRAZYAPP_MAX_ALIGNED_BLOCKS; ++i) {
        if (g_aligned_blocks[i].aligned_ptr != NULL) {
            free(g_aligned_blocks[i].base_ptr);
            g_aligned_blocks[i].aligned_ptr = NULL;
            g_aligned_blocks[i].base_ptr = NULL;
            g_aligned_blocks[i].size = 0;
        }
    }
    g_memory_stats.live_bytes = 0;
    g_memory_stats.peak_bytes = 0;
}

void *crazyapp_alloc(size_t size) {
    if (size == 0) {
        return NULL;
    }

    size_t *block = (size_t *)calloc(1, size + sizeof(size_t));
    if (block == NULL) {
        return NULL;
    }

    block[0] = size;
    crazyapp_track_alloc(size);
    return (void *)(block + 1);
}

void crazyapp_free(void *ptr) {
    if (ptr == NULL) {
        return;
    }

    for (size_t index = 0; index < CRAZYAPP_MAX_ALIGNED_BLOCKS; ++index) {
        if (g_aligned_blocks[index].aligned_ptr == ptr) {
            size_t size = g_aligned_blocks[index].size;
            crazyapp_track_free(size);
            free(g_aligned_blocks[index].base_ptr);
            crazyapp_unregister_aligned_block(ptr);
            return;
        }
    }

    size_t *header = ((size_t *)ptr) - 1;
    size_t size = header[0];
    crazyapp_track_free(size);
    free(header);
}

void *crazyapp_aligned_alloc(size_t alignment, size_t size) {
    if (alignment == 0 || (alignment & (alignment - 1U)) != 0U) {
        return NULL;
    }
    if (size == 0) {
        return NULL;
    }

    size_t raw_size = size + alignment + sizeof(size_t) * 2U;
    size_t *base_ptr = (size_t *)calloc(1, raw_size + sizeof(size_t));
    if (base_ptr == NULL) {
        return NULL;
    }

    base_ptr[0] = size;
    crazyapp_track_alloc(size);

    uintptr_t aligned = ((uintptr_t)base_ptr + sizeof(size_t) * 2U + alignment - 1U) & ~(uintptr_t)(alignment - 1U);
    void *aligned_ptr = (void *)aligned;
    crazyapp_register_aligned_block(aligned_ptr, base_ptr, size);
    return aligned_ptr;
}

void crazyapp_get_memory_stats(crazyapp_memory_stats_t *stats) {
    if (stats == NULL) {
        return;
    }
    *stats = g_memory_stats;
}
