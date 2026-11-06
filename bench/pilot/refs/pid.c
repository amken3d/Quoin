#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint32_t n = input_len();
    int32_t integ = 0, prev = 0;
    for (uint32_t i = 0; i + 1 < n; i += 2) {
        int32_t e = (int32_t)input_byte(i) - (int32_t)input_byte(i + 1);
        integ += e;
        if (integ > 1000) integ = 1000;
        if (integ < -1000) integ = -1000;
        int32_t out = 3 * e + integ / 4 + 2 * (e - prev);
        if (out > 255) out = 255;
        if (out < -255) out = -255;
        emit(out);
        prev = e;
    }
    return 0;
}
