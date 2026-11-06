#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

int main(void) {
    uint32_t n = input_len();
    for (uint32_t i = 4; i < n; i++) {
        int32_t w[5];
        for (int k = 0; k < 5; k++) w[k] = (int8_t)input_byte(i - 4 + k);
        for (int a = 0; a < 5; a++) {
            int less = 0, eq = 0;
            for (int b = 0; b < 5; b++) {
                if (w[b] < w[a]) less++;
                else if (w[b] == w[a]) eq++;
            }
            if (less <= 2 && less + eq >= 3) { emit(w[a]); break; }
        }
    }
    return 0;
}
