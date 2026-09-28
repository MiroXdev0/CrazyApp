#ifndef NEXUS_RUNTIME_H
#define NEXUS_RUNTIME_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

int nexus_runtime_init(void);
void nexus_runtime_shutdown(void);
void *nexus_alloc(size_t size);
void nexus_free(void *ptr);

#ifdef __cplusplus
}
#endif

#endif
