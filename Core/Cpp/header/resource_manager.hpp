#pragma once

#include <string>

struct ResourceSnapshot {
    int cpu_cores = 1;
    int logical_threads = 1;
    long long total_ram_mb = 0;
    long long available_ram_mb = 0;
    bool avx2 = false;
    std::string platform = "unknown";
};

class ResourceManager {
public:
    ResourceSnapshot query() const;
};
