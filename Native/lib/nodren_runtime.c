#include "nodren_runtime.h"
#include <stdlib.h>

int nexus_runtime_init(void) {
    return 0;
}

void nexus_runtime_shutdown(void) {
}

void *nexus_alloc(size_t size) {
    if (size == 0) {
        return NULL;
    }
    return calloc(1, size);
}

void nexus_free(void *ptr) {
    free(ptr);
}
