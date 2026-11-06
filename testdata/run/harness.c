/* Host harness for testdata/run: prints values, turns traps into exit 70. */
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
void emit(int64_t v) { printf("%lld\n", (long long)v); fflush(stdout); }
void emitu(uint64_t v) { printf("%llu\n", (unsigned long long)v); fflush(stdout); }
void ql_trap(void) { printf("trap\n"); fflush(stdout); exit(70); }
