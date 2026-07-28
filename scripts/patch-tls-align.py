#!/usr/bin/env python3
"""Raise PT_TLS p_align to 64 in an ELF64 binary.

ARM64 bionic's linker aborts on executables whose TLS segment is aligned
below 64 ("TLS segment is underaligned: alignment is 8 ... needs to be at
least 64 for ARM64 Bionic" — the Rust sibling hit this on real devices, its
PR #9). Go's internal linker emits a 16-byte PT_TLS with align 8 on
linux/arm64 PIE builds; raising the alignment constraint is loader-safe and
costs nothing. No-op (exit 0) when no underaligned PT_TLS exists.
"""
import struct
import sys

PT_TLS = 7
WANT = 64

path = sys.argv[1]
with open(path, "r+b") as f:
    ehdr = f.read(64)
    if ehdr[:4] != b"\x7fELF" or ehdr[4] != 2:
        sys.exit(f"{path}: not an ELF64 file")
    (e_phoff,) = struct.unpack_from("<Q", ehdr, 0x20)
    (e_phentsize,) = struct.unpack_from("<H", ehdr, 0x36)
    (e_phnum,) = struct.unpack_from("<H", ehdr, 0x38)
    patched = 0
    for i in range(e_phnum):
        off = e_phoff + i * e_phentsize
        f.seek(off)
        ph = f.read(e_phentsize)
        (p_type,) = struct.unpack_from("<I", ph, 0)
        if p_type != PT_TLS:
            continue
        (p_offset,) = struct.unpack_from("<Q", ph, 0x08)
        (p_vaddr,) = struct.unpack_from("<Q", ph, 0x10)
        (p_align,) = struct.unpack_from("<Q", ph, 0x30)
        if p_align >= WANT:
            continue
        if p_vaddr % WANT != p_offset % WANT:
            sys.exit(
                f"{path}: PT_TLS vaddr/offset not congruent mod {WANT}; refusing to patch"
            )
        f.seek(off + 0x30)
        f.write(struct.pack("<Q", WANT))
        patched += 1
print(f"{path}: raised {patched} PT_TLS header(s) to align {WANT}")
