#include <stdio.h>

int main(void) {
    int *p = 0;
    *p = 42; /* null dereference */
    printf("%d\n", *p);
    return 0;
}
