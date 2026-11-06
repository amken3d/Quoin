#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

static uint8_t counts[256];

int main(void) {
    uint32_t n = input_len(), i = 0;
    while (i < n) {
        uint32_t len = input_byte(i);
        if (len == 0) { emit(-1); return 0; }
        if (i + 1 + len > n) { emit(-2); return 0; }
        for (int v = 0; v < 256; v++) counts[v] = 0;
        for (uint32_t k = 0; k < len; k++) counts[input_byte(i + 1 + k)]++;
        uint32_t target = (len - 1) / 2, seen = 0;
        for (uint32_t v = 0; v < 256; v++) {
            seen += counts[v];
            if (seen > target) { emitu(v); break; }
        }
        i += 1 + len;
    }
    return 0;
}
