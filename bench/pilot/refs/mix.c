#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint32_t n = input_len();
    for (uint32_t i = 0; i + 1 < n; i += 2) {
        int32_t s = (int8_t)input_byte(i) * 200 + (int8_t)input_byte(i + 1) * 200;
        if (s > 32767) s = 32767;
        if (s < -32768) s = -32768;
        emit(s);
    }
    return 0;
}
