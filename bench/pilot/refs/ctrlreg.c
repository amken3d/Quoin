#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

#define CTRL (*(volatile uint32_t *)0x10370000u)

int main(void) {
    CTRL = 0;
    uint32_t n = input_len();
    for (uint32_t i = 0; i + 1 < n; i += 2) {
        uint8_t f = input_byte(i);
        uint32_t v = input_byte(i + 1);
        uint32_t shift, width;
        switch (f) {
        case 0: shift = 0; width = 1; break;
        case 1: shift = 4; width = 2; break;
        case 2: shift = 8; width = 12; v *= 16; break;
        case 3: shift = 31; width = 1; break;
        default: emit(-1); continue;
        }
        if (v >= (1u << width)) { emit(-1); continue; }
        uint32_t mask = ((1u << width) - 1) << shift;
        CTRL = (CTRL & ~mask) | (v << shift);
        emitu(CTRL);
    }
    return 0;
}
