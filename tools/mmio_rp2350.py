#!/usr/bin/env python3
"""Run an RP2350 image's main() in Unicorn and print its MMIO writes as JSON.

QEMU has no RP2350 model, so peripheral behaviour of rp2350 images is
checked at the bus level, as TickTrace's T1 tier does: SRAM is real memory,
peripherals are mocked (RESET_DONE mirrors RESET), and every peripheral
access is recorded. Execution starts at `main` (the startup code is the
pico-sdk/TickTrace sequence and needs RP2350-only coprocessor state).

usage: mmio_rp2350.py image.elf [max_writes]
"""
import json
import sys

from elftools.elf.elffile import ELFFile
from unicorn import Uc, UC_ARCH_ARM, UC_MODE_THUMB, UC_MODE_MCLASS, UC_HOOK_MEM_READ, UC_HOOK_MEM_WRITE
from unicorn.arm_const import UC_ARM_REG_SP, UC_ARM_REG_LR, UC_ARM_REG_PC

SRAM, SRAM_SIZE = 0x20000000, 0x82000
PERIPH = [(0x40000000, 0x100000), (0xD0000000, 0x10000)]
RESETS = 0x40020000
EXIT = 0x20081000  # a halt address in SRAM; main returning lands here

def main():
    elf_path = sys.argv[1]
    max_writes = int(sys.argv[2]) if len(sys.argv) > 2 else 64
    uc = Uc(UC_ARCH_ARM, UC_MODE_THUMB | UC_MODE_MCLASS)
    uc.mem_map(SRAM, SRAM_SIZE)
    for base, size in PERIPH:
        uc.mem_map(base, size)
    with open(elf_path, "rb") as f:
        elf = ELFFile(f)
        for seg in elf.iter_segments():
            if seg["p_type"] == "PT_LOAD" and seg["p_filesz"]:
                uc.mem_write(seg["p_paddr"], seg.data())
        syms = {s.name: s["st_value"] for s in elf.get_section_by_name(".symtab").iter_symbols()}
    regs = {RESETS + 0x00: 0x1FFFFFFF, 0x40038068: 0x196, 0x400280CC: 0x1F}
    events = []

    def on_write(uc, access, addr, size, value, _):
        events.append({"op": "w", "addr": addr, "value": value & 0xFFFFFFFF})
        regs[addr] = value & 0xFFFFFFFF
        if sum(1 for e in events if e["op"] == "w") >= max_writes:
            uc.emu_stop()

    def on_read(uc, access, addr, size, value, _):
        v = regs.get(addr, 0)
        if addr == RESETS + 0x08:  # RESET_DONE: done when out of reset
            v = ~regs.get(RESETS, 0) & 0xFFFFFFFF
        uc.mem_write(addr, v.to_bytes(4, "little")[:size])
        events.append({"op": "r", "addr": addr, "value": v})

    for base, size in PERIPH:
        uc.hook_add(UC_HOOK_MEM_WRITE, on_write, begin=base, end=base + size - 1)
        uc.hook_add(UC_HOOK_MEM_READ, on_read, begin=base, end=base + size - 1)
    uc.mem_write(EXIT, b"\xfe\xe7")  # b .
    uc.reg_write(UC_ARM_REG_SP, SRAM + 0x80000)
    uc.reg_write(UC_ARM_REG_LR, EXIT | 1)
    uc.emu_start(syms["main"] | 1, EXIT, count=50_000_000)
    print(json.dumps({"events": events, "pc": uc.reg_read(UC_ARM_REG_PC)}))

if __name__ == "__main__":
    main()
