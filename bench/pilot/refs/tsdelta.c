#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

static uint32_t ts(uint32_t i) {
    uint32_t b = 4 * i;
    return input_byte(b) | (uint32_t)input_byte(b + 1) << 8 | (uint32_t)input_byte(b + 2) << 16 | (uint32_t)input_byte(b + 3) << 24;
}

int main(void) {
    uint32_t count = input_len() / 4, over = 0, big = 0;
    for (uint32_t k = 1; k < count; k++) {
        uint32_t e = ts(k) - ts(k - 1);
        emitu(e);
        if (e > 1000000) over++;
        if (e > big) big = e;
    }
    emitu(over);
    emitu(big);
    return 0;
}
