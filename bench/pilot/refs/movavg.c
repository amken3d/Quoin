#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint32_t n = input_len();
    for (uint32_t i = 3; i < n; i++) {
        uint32_t s = input_byte(i - 3) + input_byte(i - 2) + input_byte(i - 1) + input_byte(i);
        emitu(s / 4);
    }
    return 0;
}
