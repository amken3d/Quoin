#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint32_t n = input_len(), i = 0;
    while (i < n) {
        uint32_t len = input_byte(i);
        if (len > 32) { emit(-2); return 0; }
        if (i + len + 2 > n) { emit(-3); return 0; }
        uint32_t sum = 0;
        uint8_t x = 0;
        for (uint32_t k = 0; k < len; k++) {
            uint8_t b = input_byte(i + 1 + k);
            sum += b;
            x ^= b;
        }
        if (x == input_byte(i + 1 + len)) emitu(sum); else emit(-1);
        i += len + 2;
    }
    return 0;
}
