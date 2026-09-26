#include "memory.h"
#include <stdlib.h>

void *safe_alloc(size_t size) {
    if (size == 0) {
        return NULL;
    }
    return calloc(1, size);
}

void safe_free(void *ptr) {
    free(ptr);
}
