# Quoin
A quoin is the wedge a printer drives into the chase to lock the type so nothing can shift while the press runs. That's the language in one object: memory that never moves, bounds locked at compile time, every cycle set before it runs.

Quoin is a no-heap language for firmware written by AI models. On the RP2350 it compiles straight to Thumb-2 and proves each function's worst-case stack and cycles. For any other part it compiles to plain C99 for the vendor toolchain, with stack bounds. The design is in [docs/design.md](docs/design.md), the language as implemented in [docs/language.md](docs/language.md), what v0 covers in [docs/status.md](docs/status.md), and how it is measured in [docs/benchmark.md](docs/benchmark.md).

The compiler, `quoinc`, is written in Go and ships as one static binary. Quoin programs have no Go in them: no runtime, no GC, no Go toolchain needed to build or run them.

```
error Timeout

fn wait_txe(u: Uart) !Timeout
  for i in 0..100000
    if u.SR.TXE
      return
  return Timeout

fn send(u: Uart, buf: []u8) !Timeout
  for b in buf
    wait_txe(u)?
    u.DR = b

fn main()
  GPIOA.MODER.P5 = .Out   // register as type: no masks, no shifts
  ...
```

```
$ quoinc check --target rp2350 examples/rp2350_blink.qn
STACK main=32B data=0B total=32B src=exact
CYCLES main=4000081 model=m33-uncalibrated
CHECKS runtime=0
```

Functions can claim bounds, and the compiler proves them or says what the real number is:

```
fn toggle() where cycles <= 20, stack <= 16
  SIO.GPIO_OUT_XOR = u32(1) << LED
```

## Build

Needs Go 1.24+ to build the compiler. The native targets need GNU `arm-none-eabi-as`/`ld`; the C target needs any C99 compiler.

```
go build -o quoinc ./cmd/quoinc
go test ./...      # tests skip cleanly when a tool is missing
```

Full test coverage uses a host C compiler, `arm-none-eabi-gcc`/binutils, `qemu-system-arm`, and Python with `unicorn` and `pyelftools` (for the RP2350 bus-level test).

## Use

```
quoinc check   prog.qn                    # diagnostics, STACK and CHECKS lines
quoinc build   --target rp2350 prog.qn    # build/prog.s, .elf and .uf2 (SRAM image, BOOTSEL)
quoinc run     prog.qn                    # native build for QEMU's Cortex-M33, run, report measured stack
quoinc build   prog.qn -o build           # C target: build/prog.c, quoin_rt.h, quoin_stack.ld
quoinc stack   prog.qn --su build         # C target: exact call-stack bound from gcc -fstack-usage
quoinc explain E301                       # what a diagnostic code means
```

Flags: `--target c|rp2350|m33-qemu`, `--json` (one object for tools and agents), `--budget N` (fail with E202 over N bytes), `-v` (worst-case paths and every function's cycles), `--checks` (list every runtime check), `--insns` (run: count executed instructions), `--no-line`, `--no-link`, `--exc-frame N`.

`go run ./cmd/quoin-bench` measures proof coverage, bound tightness on QEMU, footprint and instruction counts against GCC -O2. Results are kept in [bench/results](bench/results).

Exact stack bound for an RP2040:

```
quoinc build prog.qn -o build
arm-none-eabi-gcc -std=c99 -O2 -mcpu=cortex-m0plus -mthumb -fstack-usage -c build/prog.c -o build/prog.o
quoinc stack prog.qn --su build
```

The data stack is a static array of exactly the worst-case size, so RAM overflow fails the link. `quoin_stack.ld` exposes `__quoin_call_stack_bytes` for an `ASSERT` against your linker script's stack region.

## Layout

| Path | Contents |
| --- | --- |
| `cmd/quoinc` | CLI |
| `internal/lexer`, `parser`, `ast` | Indentation-aware lexer, recursive-descent parser, syntax tree |
| `internal/check` | Names, types, constant folding, interval facts, data-stack frame layout, runtime-check marking |
| `internal/stack` | Call graph, recursion check, worst-case bounds, `.su` ingestion, shared-state and queue rules |
| `internal/cgen` | C99 emitter and `quoin_rt.h` |
| `internal/thumb` | Native Thumb-2 backend for the Cortex-M33: code, exact frames, cycle/instruction bounds, RP2350 and QEMU runtimes, UF2 |
| `cmd/quoin-bench` | Benchmark runner |
| `tools/mmio_rp2350.py` | Runs an RP2350 image in Unicorn and records its peripheral writes |
| `internal/diag` | Structured diagnostics and the code catalog |
| `examples` | RP2350 blink with claims, STM32F4 UART, STM32F4 UART interrupt with a queue, RP2040 blink |
| `testdata` | `errors/` (expected diagnostics), `run/` (programs run on the host and on QEMU, checked against expected output), `kernels/` (bounded kernels for bound and benchmark measurements) |

## License

MIT for the compiler. Generated code includes `quoin_rt.h` (MIT).
