#include <iostream>

int main() {
    int *p = nullptr;
    *p = 42; // null dereference
    std::cout << *p << std::endl;
    return 0;
}
