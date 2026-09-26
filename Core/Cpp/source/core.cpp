#include "source.hpp"
#include <iostream>

int Core::run() {
    std::cout << "Core service is running." << std::endl;
    return 0;
}

int main() {
    Core core;
    return core.run();
}
