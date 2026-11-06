#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint32_t n = input_len(), i = 0;
    while (i < n) {
        uint8_t c = input_byte(i);
        uint32_t j = i;
        while (j < n && input_byte(j) == c && j - i < 255) j++;
        emitu(j - i);
        emitu(c);
        i = j;
    }
    return 0;
}
