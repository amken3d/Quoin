/* Host harness for testdata/run: prints values, turns traps into exit 70. */
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
void emit(int32_t v) { printf("%ld\n", (long)v); fflush(stdout); }
void emitu(uint32_t v) { printf("%lu\n", (unsigned long)v); fflush(stdout); }
void emit64(int64_t v) { printf("%lld\n", (long long)v); fflush(stdout); }
void ql_trap(void) { printf("trap\n"); fflush(stdout); exit(70); }
