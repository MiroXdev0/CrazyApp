bits 64

section .text

global memory_init
global memory_alloc
global memory_reset
global memory_used
global memory_remaining

memory_blob:
    incbin "Memory.bin"

memory_init       equ memory_blob + 0x00
memory_alloc      equ memory_blob + 0x11
memory_reset      equ memory_blob + 0x33
memory_used       equ memory_blob + 0x3D
memory_remaining  equ memory_blob + 0x45