#include "../header/resource_manager.hpp"

#include <thread>

#if defined(_WIN32)
#include <windows.h>
#elif defined(__unix__) || defined(__APPLE__)
#include <unistd.h>
#endif

ResourceSnapshot ResourceManager::query() const {
    ResourceSnapshot snapshot;

    snapshot.logical_threads = static_cast<int>(std::thread::hardware_concurrency());
    if (snapshot.logical_threads <= 0) {
        snapshot.logical_threads = 1;
    }
    snapshot.cpu_cores = snapshot.logical_threads;

#if defined(_WIN32)
    MEMORYSTATUSEX mem{};
    mem.dwLength = sizeof(mem);
    if (GlobalMemoryStatusEx(&mem)) {
        snapshot.total_ram_mb = static_cast<long long>(mem.ullTotalPhys / (1024ULL * 1024ULL));
        snapshot.available_ram_mb = static_cast<long long>(mem.ullAvailPhys / (1024ULL * 1024ULL));
    }
    snapshot.platform = "windows";
#elif defined(__linux__)
    long pages = sysconf(_SC_PHYS_PAGES);
    long page_size = sysconf(_SC_PAGE_SIZE);
    snapshot.total_ram_mb = (pages * page_size) / (1024LL * 1024LL);
    snapshot.available_ram_mb = snapshot.total_ram_mb / 2LL;
    snapshot.platform = "linux";
#elif defined(__APPLE__)
    long pages = sysconf(_SC_PHYS_PAGES);
    long page_size = sysconf(_SC_PAGE_SIZE);
    snapshot.total_ram_mb = (pages * page_size) / (1024LL * 1024LL);
    snapshot.available_ram_mb = snapshot.total_ram_mb / 2LL;
    snapshot.platform = "apple";
#else
    snapshot.total_ram_mb = 0;
    snapshot.available_ram_mb = 0;
    snapshot.platform = "unknown";
#endif

    snapshot.avx2 = false;
#if defined(__x86_64__) || defined(_M_X64)
#if defined(__GNUC__) || defined(__clang__)
    __builtin_cpu_init();
    snapshot.avx2 = __builtin_cpu_supports("avx2");
#endif
#endif

    return snapshot;
}
