.text
.intel_syntax noprefix

.globl nodren_asm_sum_i32
.type nodren_asm_sum_i32, @function

# SysV ABI:
# RDI = int32_t* values
# RSI = count
# Returns int64_t in RAX.
nodren_asm_sum_i32:
    test rsi, rsi
    je .zero

    vxorpd ymm0, ymm0, ymm0
    vxorpd ymm1, ymm1, ymm1
    xor r8, r8

.loop8:
    lea r10, [r8 + 8]
    cmp r10, rsi
    ja .tail

    vmovdqu ymm2, [rdi + r8*4]

    vpmovsxdq ymm3, xmm2
    vextracti128 xmm4, ymm2, 1
    vpmovsxdq ymm4, xmm4

    vpaddq ymm0, ymm0, ymm3
    vpaddq ymm1, ymm1, ymm4

    add r8, 8
    jmp .loop8

.tail:
    vpaddq ymm0, ymm0, ymm1
    vextracti128 xmm1, ymm0, 1
    vpaddq xmm0, xmm0, xmm1
    vpshufd xmm1, xmm0, 0x4E
    vpaddq xmm0, xmm0, xmm1
    movq rax, xmm0

    cmp r8, rsi
    jae .done

.tail_loop:
    movsxd r9, dword ptr [rdi + r8*4]
    add rax, r9
    inc r8
    cmp r8, rsi
    jb .tail_loop

.done:
    vzeroupper
    ret

.zero:
    xor eax, eax
    ret

.section .note.GNU-stack,"",@progbits
