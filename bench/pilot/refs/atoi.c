#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint32_t n = input_len(), v = 0;
    bool in_num = false, over = false;
    for (uint32_t i = 0; i <= n; i++) {
        uint8_t c = i < n ? input_byte(i) : ' ';
        if (c >= '0' && c <= '9') {
            in_num = true;
            uint32_t d = c - '0';
            if (!over) {
                if (v > 429496729u || (v == 429496729u && d > 5)) over = true;
                else v = v * 10 + d;
            }
        } else if (c == ' ' || c == ',') {
            if (in_num) { if (over) emit(-1); else emitu(v); }
            in_num = false;
            over = false;
            v = 0;
        } else {
            emit(-2);
            return 0;
        }
    }
    return 0;
}
