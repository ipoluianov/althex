@ 16-bit Thumb (ARMv6-M / ARMv7-M)
.syntax unified
.thumb
start:
	lsls r0, r1, #0
	movs r0, r1
	lsls r1, r2, #5
	lsrs r3, r4, #32
	lsrs r3, r4, #1
	asrs r5, r6, #32
	asrs r5, r6, #7
	adds r0, r1, r2
	subs r7, r6, r5
	adds r0, r1, #7
	subs r0, r1, #0
	adds r0, r1, #0
	movs r7, #255
	movs r0, #0
	cmp r3, #200
	adds r2, #100
	subs r2, #1
	ands r0, r1
	eors r0, r1
	lsls r0, r1
	lsrs r0, r1
	asrs r0, r1
	adcs r0, r1
	sbcs r0, r1
	rors r0, r1
	tst r0, r1
	negs r0, r1
	cmp r0, r1
	cmn r0, r1
	orrs r0, r1
	muls r0, r1, r0
	bics r0, r1
	mvns r0, r1
	add r0, r8
	add r8, r0
	add sp, r0
	add r0, sp, r0
	add pc, r1
	cmp r8, r0
	cmp r0, r9
	mov r8, r0
	mov r0, r8
	mov sp, r1
	mov lr, pc
	mov r0, r1
	bx lr
	bx r0
	blx r3
	ldr r0, [pc, #0]
	ldr r1, [pc, #1020]
	str r0, [r1, r2]
	strh r0, [r1, r2]
	strb r0, [r1, r2]
	ldrsb r0, [r1, r2]
	ldr r0, [r1, r2]
	ldrh r0, [r1, r2]
	ldrb r0, [r1, r2]
	ldrsh r0, [r1, r2]
	str r0, [r1, #0]
	str r0, [r1, #124]
	ldr r2, [r3, #4]
	strb r0, [r1, #31]
	ldrb r0, [r1, #1]
	strh r0, [r1, #62]
	ldrh r0, [r1, #2]
	str r0, [sp, #0]
	str r0, [sp, #1020]
	ldr r7, [sp, #8]
	adr r0, start2
	add r0, sp, #0
	add r0, sp, #1020
	add sp, #508
	sub sp, #508
	add sp, #0
	cbz r0, start2
	cbnz r7, start2
	sxth r0, r1
	sxtb r0, r1
	uxth r0, r1
	uxtb r0, r1
	push {r0}
	push {r4-r7, lr}
	push {r0, r2, r4, r6}
	push {lr}
	pop {pc}
	pop {r4-r7, pc}
	pop {r0-r7}
	rev r0, r1
	rev16 r0, r1
	revsh r0, r1
	bkpt 0
	bkpt 255
	cpsie i
	cpsid i
	cpsie f
	cpsid f
	cpsie if
	nop
	yield
	wfe
	wfi
	sev
	stmia r0!, {r1, r2}
	ldmia r0!, {r1, r2}
	ldmia r0, {r0, r1}
	stmia r7!, {r0-r7}
	beq start
	bne start2
	bcs start
	bcc start
	bmi start
	bpl start
	bvs start
	bvc start
	bhi start
	bls start
	bge start
	blt start
	bgt start
	ble start
	udf #0
	udf #254
	svc 0
	svc 255
	b start
	b start2
	.align 2
start2:
	nop
	nop
	.short 0xffff
	.short 0xffff
