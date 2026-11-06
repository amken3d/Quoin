# Quoin
A quoin is the wedge a printer drives into the chase to lock the type so nothing can shift while the press runs. That's the language in one object: memory that never moves, bounds locked at compile time, every cycle set before it runs.

Quoin is a no-heap language for firmware written by AI models. It compiles to plain C99 for vendor toolchains, and the compiler reports the worst-case stack each entry point needs. The design is in [docs/design.md](docs/design.md), the language as implemented in [docs/language.md](docs/language.md), and what v0 does and doesn't cover in [docs/status.md](docs/status.md).

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
$ quoinc check examples/uart_pack.qn
STACK main=56B data=72B total=128B src=est
CHECKS runtime=2 push=1 where=1
```

## Build

Needs Go 1.24+.

```
go build -o quoinc ./cmd/quoinc
go test ./...      # host C compiler for run tests; arm-none-eabi-gcc for cross tests (skipped if absent)
```

## Use

```
quoinc check   prog.qn            # diagnostics, STACK and CHECKS lines
quoinc build   prog.qn -o build   # build/prog.c, build/quoin_rt.h, build/quoin_stack.ld
quoinc stack   prog.qn --su build # exact call-stack bound from gcc -fstack-usage
quoinc explain E107               # what a diagnostic code means
```

Flags: `--json` (one object for tools and agents), `--budget N` (fail with E202 over N bytes), `-v` (worst-case paths), `--checks` (list every runtime check), `--no-line` (no `#line` in the C), `--exc-frame N`.

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
| `internal/diag` | Structured diagnostics and the code catalog |
| `examples` | STM32F4 UART, STM32F4 UART interrupt with a queue, RP2040 blink |
| `testdata` | `errors/` (expected diagnostics), `run/` (programs run on the host and checked against expected output) |

## License

MIT for the compiler. Generated code includes `quoin_rt.h` (MIT).
