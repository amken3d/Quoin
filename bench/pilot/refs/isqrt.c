#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

static uint32_t isqrt(uint32_t v) {
    uint32_t lo = 0, hi = 65536;
    while (hi - lo > 1) {
        uint32_t m = (lo + hi) / 2;
        if (m <= v / m) lo = m; else hi = m;
    }
    return lo;
}

int main(void) {
    uint32_t n = input_len() / 4;
    for (uint32_t k = 0; k < n; k++) {
        uint32_t b = 4 * k;
        uint32_t v = input_byte(b) | (uint32_t)input_byte(b + 1) << 8 | (uint32_t)input_byte(b + 2) << 16 | (uint32_t)input_byte(b + 3) << 24;
        emitu(isqrt(v));
    }
    return 0;
}
