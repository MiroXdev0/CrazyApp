#include "../include/nodren_memory.h"

#include <stdatomic.h>
#include <stdint.h>

#if defined(_WIN32)
    #define WIN32_LEAN_AND_MEAN
    #include <windows.h>
#else
    #include <sys/mman.h>
    #include <unistd.h>
#endif

typedef struct NodrenAllocHeader {
    size_t size;
    size_t mapping_size;
    void* base;
} NodrenAllocHeader;

static _Atomic uint64_t g_allocations = 0;
static _Atomic uint64_t g_frees = 0;
static _Atomic uint64_t g_live_bytes = 0;
static _Atomic uint64_t g_peak_bytes = 0;

static void* nodren_raw_reserve(size_t bytes) {
#if defined(_WIN32)
    return VirtualAlloc(NULL, bytes, MEM_RESERVE | MEM_COMMIT, PAGE_READWRITE);
#else
    void* ptr = mmap(NULL, bytes, PROT_READ | PROT_WRITE,
                     MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    return ptr == MAP_FAILED ? NULL : ptr;
#endif
}

static void nodren_raw_release(void* ptr, size_t bytes) {
    if (!ptr) return;
#if defined(_WIN32)
    (void)bytes;
    VirtualFree(ptr, 0, MEM_RELEASE);
#else
    munmap(ptr, bytes);
#endif
}

static void nodren_peak_update(uint64_t current) {
    uint64_t observed = atomic_load_explicit(&g_peak_bytes, memory_order_relaxed);
    while (current > observed &&
           !atomic_compare_exchange_weak_explicit(
               &g_peak_bytes, &observed, current,
               memory_order_relaxed, memory_order_relaxed)) {
    }
}

int nodren_memory_init(void) {
    atomic_store(&g_allocations, 0);
    atomic_store(&g_frees, 0);
    atomic_store(&g_live_bytes, 0);
    atomic_store(&g_peak_bytes, 0);
    return 0;
}

void nodren_memory_shutdown(void) {
}

void* nodren_alloc(size_t size) {
    if (size == 0) return NULL;

    const size_t total = sizeof(NodrenAllocHeader) + size;
    NodrenAllocHeader* header =
        (NodrenAllocHeader*)nodren_raw_reserve(total);
    if (!header) return NULL;

    header->size = size;
    header->mapping_size = total;
    header->base = header;

    const uint64_t live = atomic_fetch_add_explicit(
        &g_live_bytes, (uint64_t)size, memory_order_relaxed) + size;
    atomic_fetch_add_explicit(&g_allocations, 1, memory_order_relaxed);
    nodren_peak_update(live);

    return (uint8_t*)header + sizeof(NodrenAllocHeader);
}

void* nodren_aligned_alloc(size_t alignment, size_t size) {
    if (size == 0 || alignment < sizeof(void*) ||
        (alignment & (alignment - 1u)) != 0) {
        return NULL;
    }

    const size_t total = sizeof(NodrenAllocHeader) + size + alignment - 1u;
    uint8_t* raw = (uint8_t*)nodren_raw_reserve(total);
    if (!raw) return NULL;

    uintptr_t start = (uintptr_t)(raw + sizeof(NodrenAllocHeader));
    uintptr_t aligned =
        (start + alignment - 1u) & ~(uintptr_t)(alignment - 1u);

    NodrenAllocHeader* header =
        (NodrenAllocHeader*)(aligned - sizeof(NodrenAllocHeader));

    header->size = size;
    header->mapping_size = total;
    header->base = raw;

    const uint64_t live = atomic_fetch_add_explicit(
        &g_live_bytes, (uint64_t)size, memory_order_relaxed) + size;
    atomic_fetch_add_explicit(&g_allocations, 1, memory_order_relaxed);
    nodren_peak_update(live);

    return (void*)aligned;
}

void nodren_free(void* ptr) {
    if (!ptr) return;

    NodrenAllocHeader* header =
        (NodrenAllocHeader*)((uint8_t*)ptr - sizeof(NodrenAllocHeader));

    const size_t size = header->size;
    const size_t mapping_size = header->mapping_size;
    void* base = header->base;

    uint64_t old = atomic_load_explicit(&g_live_bytes, memory_order_relaxed);
    while (!atomic_compare_exchange_weak_explicit(
        &g_live_bytes, &old,
        old >= size ? old - size : 0,
        memory_order_relaxed, memory_order_relaxed)) {
    }

    atomic_fetch_add_explicit(&g_frees, 1, memory_order_relaxed);
    nodren_raw_release(base, mapping_size);
}

int nodren_arena_init(NodrenArena* arena, size_t capacity, size_t alignment) {
    if (!arena || capacity == 0) return -1;
    if (alignment < 64) alignment = 64;

    arena->base = (uint8_t*)nodren_aligned_alloc(alignment, capacity);
    if (!arena->base) return -1;

    arena->capacity = capacity;
    arena->offset = 0;
    return 0;
}

void* nodren_arena_alloc(NodrenArena* arena, size_t size, size_t alignment) {
    if (!arena || !arena->base || size == 0) return NULL;
    if (alignment == 0 || (alignment & (alignment - 1u)) != 0) return NULL;

    uintptr_t current = (uintptr_t)(arena->base + arena->offset);
    uintptr_t aligned =
        (current + alignment - 1u) & ~(uintptr_t)(alignment - 1u);

    const size_t new_offset =
        (size_t)(aligned - (uintptr_t)arena->base) + size;

    if (new_offset > arena->capacity) return NULL;

    arena->offset = new_offset;
    return (void*)aligned;
}

void nodren_arena_reset(NodrenArena* arena) {
    if (arena) arena->offset = 0;
}

void nodren_arena_destroy(NodrenArena* arena) {
    if (!arena || !arena->base) return;
    nodren_free(arena->base);
    arena->base = NULL;
    arena->capacity = 0;
    arena->offset = 0;
}

void nodren_memory_stats(NodrenMemoryStats* out) {
    if (!out) return;

    out->allocations =
        atomic_load_explicit(&g_allocations, memory_order_relaxed);
    out->frees =
        atomic_load_explicit(&g_frees, memory_order_relaxed);
    out->live_bytes =
        atomic_load_explicit(&g_live_bytes, memory_order_relaxed);
    out->peak_bytes =
        atomic_load_explicit(&g_peak_bytes, memory_order_relaxed);
}
