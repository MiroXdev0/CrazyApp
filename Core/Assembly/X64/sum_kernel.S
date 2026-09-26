    .intel_syntax noprefix
    .text
    .globl crazyapp_asm_sum

crazyapp_asm_sum:
    xor r8, r8
    xor rcx, rcx

.loop:
    cmp rcx, rsi
    jae .done
    mov eax, DWORD PTR [rdi + rcx * 4]
    add r8, rax
    inc rcx
    jmp .loop

.done:
    mov rax, r8
    ret
