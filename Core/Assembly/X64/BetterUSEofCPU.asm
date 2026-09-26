.code

; ------------------------------------------------------------
; memory_init
;
; RCX = memory state
; RDX = arena base
; R8  = arena size
;
; State:
;   [RCX+00h] = base
;   [RCX+08h] = current
;   [RCX+10h] = end
; ------------------------------------------------------------
memory_init PROC

    mov     [rcx], rdx
    mov     [rcx+08h], rdx

    add     rdx, r8
    mov     [rcx+10h], rdx

    xor     eax, eax
    ret

memory_init ENDP


; ------------------------------------------------------------
; memory_alloc
;
; RCX = memory state
; RDX = requested size
;
; Returns:
;   RAX = allocated address
;   RAX = 0 on failure
;
; Alignment: 16 bytes
; ------------------------------------------------------------
memory_alloc PROC

    lea     r8, [rdx+0Fh]
    and     r8, 0FFFFFFFFFFFFFFF0h

    mov     r9, [rcx+08h]
    lea     r10, [r9+r8]

    cmp     r10, [rcx+10h]
    ja      allocation_failed

    mov     rax, [rcx+08h]
    mov     [rcx+08h], r10

    ret

allocation_failed:

    xor     eax, eax
    ret

memory_alloc ENDP


; ------------------------------------------------------------
; memory_reset
;
; RCX = memory state
;
; current = base
; ------------------------------------------------------------
memory_reset PROC

    mov     rax, [rcx]
    mov     [rcx+08h], rax

    xor     eax, eax
    ret

memory_reset ENDP


; ------------------------------------------------------------
; memory_used
;
; RCX = memory state
;
; Returns:
;   RAX = bytes currently used
; ------------------------------------------------------------
memory_used PROC

    mov     rax, [rcx+08h]
    sub     rax, [rcx]

    ret

memory_used ENDP


; ------------------------------------------------------------
; memory_remaining
;
; RCX = memory state
;
; Returns:
;   RAX = bytes remaining
; ------------------------------------------------------------
memory_remaining PROC

    mov     rax, [rcx+10h]
    sub     rax, [rcx+08h]

    ret

memory_remaining ENDP


END