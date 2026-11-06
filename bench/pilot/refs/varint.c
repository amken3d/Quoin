#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint32_t n = input_len(), i = 0;
    while (i < n) {
        uint32_t v = 0;
        for (uint32_t k = 0;; k++) {
            if (i >= n) { emit(-2); return 0; }
            uint8_t b = input_byte(i++);
            if (k == 4 && ((b & 0x80) || b > 0x0F)) { emit(-1); return 0; }
            v |= (uint32_t)(b & 0x7F) << (7 * k);
            if (!(b & 0x80)) break;
        }
        emitu(v);
    }
    return 0;
}
