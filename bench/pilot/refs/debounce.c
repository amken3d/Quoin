#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint32_t n = input_len();
    int state = 0, run = 0;
    for (uint32_t i = 0; i < n; i++) {
        int s = input_byte(i) & 1;
        if (s != state) {
            if (++run == 3) {
                state = s;
                run = 0;
                emitu(i);
                emitu((uint32_t)state);
            }
        } else {
            run = 0;
        }
    }
    return 0;
}
