#ifndef MEMORY_H
#define MEMORY_H

#include <stddef.h>

void *safe_alloc(size_t size);
void safe_free(void *ptr);

#endif
