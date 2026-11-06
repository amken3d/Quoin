#include <stdint.h>
#include <stdbool.h>

void emit(int32_t v);
void emitu(uint32_t v);
uint32_t input_len(void);
uint8_t input_byte(uint32_t i);

static const int32_t COEF[8] = {-3000, 6000, 12000, 18000, 18000, 12000, 6000, -3000};

static int32_t sample(uint32_t i) {
    return (int16_t)(uint16_t)(input_byte(2 * i) | (input_byte(2 * i + 1) << 8));
}

static int64_t sat32(int64_t v) {
    return v > INT32_MAX ? INT32_MAX : (v < INT32_MIN ? INT32_MIN : v);
}

int main(void) {
    uint32_t count = input_len() / 2;
    for (uint32_t n = 7; n < count; n++) {
        int64_t acc = 0;
        for (uint32_t k = 0; k < 8; k++)
            acc = sat32(acc + (int64_t)COEF[k] * sample(n - k));
        acc = sat32(acc + 16384);
        int32_t y = (int32_t)acc >> 15;
        if (y > 32767) y = 32767;
        if (y < -32768) y = -32768;
        emit(y);
    }
    return 0;
}
