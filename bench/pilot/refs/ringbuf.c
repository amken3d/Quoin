#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint8_t q[8];
    uint32_t head = 0, count = 0, n = input_len();
    for (uint32_t i = 0; i < n; i++) {
        uint8_t c = input_byte(i);
        if (c < 0x80) {
            if (count == 8) emit(-1);
            else { q[(head + count) % 8] = c; count++; }
        } else if (count == 0) {
            emit(-2);
        } else {
            emitu(q[head]);
            head = (head + 1) % 8;
            count--;
        }
    }
    emitu(count);
    return 0;
}
