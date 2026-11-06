#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

static uint32_t bit(uint32_t pos) {
    return (input_byte(pos / 8) >> (7 - pos % 8)) & 1;
}

int main(void) {
    uint32_t total = input_len() * 8, pos = 0;
    while (total - pos >= 5) {
        uint32_t w = 0;
        for (int k = 0; k < 5; k++) w = (w << 1) | bit(pos++);
        if (total - pos < w + 1) { emit(-1); return 0; }
        uint32_t v = 0;
        for (uint32_t k = 0; k < w + 1; k++) v = (v << 1) | bit(pos++);
        emitu(v);
    }
    return 0;
}
