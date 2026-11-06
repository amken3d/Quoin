#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint32_t n = input_len(), start = 0;
    for (uint32_t i = 0; i < n; i++) {
        if (input_byte(i) != 0) continue;
        uint32_t flen = i - start;
        if (flen == 0) {
            emit(-1);
        } else {
            uint32_t j = 0, count = 0;
            uint8_t x = 0;
            bool ok = true;
            while (j < flen) {
                uint32_t c = input_byte(start + j);
                if (j + c > flen) { ok = false; break; }
                for (uint32_t k = 1; k < c; k++) { x ^= input_byte(start + j + k); count++; }
                j += c;
                if (j < flen && c < 255) count++;
            }
            if (ok) { emitu(count); emitu(x); } else emit(-1);
        }
        start = i + 1;
    }
    return 0;
}
