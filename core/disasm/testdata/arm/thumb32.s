@ 32-bit Thumb-2 (ARMv7-M, ARMv7E-M)
.syntax unified
.thumb
start:
	mov.w r0, #1
	mov.w r0, #0x3fc00
	movs.w r1, #0xff00ff00
	mov.w r1, #0xffffffff
	mvn r2, #5
	mvns r2, #0x10000
	and r0, r1, #0xff
	ands r0, r1, #0x100
	bic r0, r1, #3
	orr r0, r1, #0x80000000
	orn r0, r1, #2
	eor r3, r4, #0xab00ab00
	add.w r0, r1, #4
	adds.w r0, r1, #4
	add.w sp, sp, #256
	sub.w sp, sp, #256
	add.w r0, sp, #1024
	adc r0, r1, #1
	sbc r0, r1, #1
	sub.w r0, r1, #0x400
	subs.w r0, r1, #8
	rsb r0, r1, #0
	rsbs r0, r1, #10
	tst.w r0, #1
	teq r0, #0x100
	cmn.w r0, #4
	cmp.w r0, #0x1000
	addw r0, r1, #4095
	subw r0, r1, #1
	addw r0, sp, #4
	subw sp, sp, #12
	adr.w r0, start2
	adr.w r0, start
	movw r0, #0x1234
	movw r0, #0
	movt r0, #0x4002
	movt r1, #0xffff
	ssat r0, #8, r1
	ssat r0, #1, r1, lsl #3
	ssat r0, #32, r1, asr #31
	usat r0, #8, r1
	usat r0, #0, r1, asr #4
	ssat16 r0, #8, r1
	usat16 r0, #15, r1
	sbfx r0, r1, #3, #4
	ubfx r0, r1, #0, #32
	ubfx r2, r3, #31, #1
	bfi r0, r1, #4, #8
	bfc r0, #0, #16
	and.w r0, r1, r2
	ands.w r0, r1, r2, lsl #3
	bic.w r0, r1, r2, lsr #32
	orr.w r0, r1, r2, asr #2
	orrs.w r0, r1, r2, ror #2
	orr.w r0, r1, r2, rrx
	orn r0, r1, r2
	eor.w r0, r1, r2
	add.w r0, r1, r2, lsl #2
	adds.w r0, r1, r2
	add.w r0, sp, r2
	adc.w r0, r1, r2
	sbc.w r0, r1, r2
	sub.w r0, r1, r2, lsl #1
	subs.w r0, r1, r2
	rsb r0, r1, r2
	rsbs r0, r1, r2, lsl #4
	tst.w r0, r1
	teq.w r0, r1, lsl #1
	cmn.w r0, r1
	cmp.w r0, r1
	cmp.w r0, r1, lsr #8
	mov.w r0, r1
	movs.w r0, r1
	mov.w r8, r9
	lsl.w r0, r1, #3
	lsls.w r0, r1, #3
	lsr.w r0, r1, #32
	asr.w r0, r1, #1
	ror.w r0, r1, #8
	rrx r0, r1
	rrxs r0, r1
	mvn.w r0, r1
	mvns.w r0, r1, lsl #2
	pkhbt r0, r1, r2
	pkhbt r0, r1, r2, lsl #8
	pkhtb r0, r1, r2, asr #16
	lsl.w r0, r1, r2
	lsls.w r0, r1, r2
	lsr.w r0, r1, r2
	asr.w r0, r1, r2
	ror.w r0, r1, r2
	sxth.w r0, r1
	sxth r0, r1, ror #8
	sxtah r0, r1, r2
	uxth.w r0, r1
	uxtah r0, r1, r2, ror #16
	sxtb16 r0, r1
	sxtab16 r0, r1, r2
	uxtb16 r0, r1, ror #24
	uxtab16 r0, r1, r2
	sxtb.w r0, r1
	sxtab r0, r1, r2
	uxtb.w r0, r1
	uxtab r0, r1, r2, ror #8
	clz r0, r1
	rbit r0, r1
	rev.w r0, r1
	rev16.w r0, r1
	revsh.w r0, r1
	mul.w r0, r1, r2
	mul r8, r1, r2
	mla r0, r1, r2, r3
	mls r0, r1, r2, r3
	smull r0, r1, r2, r3
	umull r0, r1, r2, r3
	smlal r0, r1, r2, r3
	umlal r0, r1, r2, r3
	sdiv r0, r1, r2
	udiv r0, r1, r2
	ldr.w r0, [r1, #4]
	ldr.w r0, [r1, #4095]
	ldr r0, [r1, #-4]
	ldr r0, [r1, #-255]
	ldr r0, [r1, #4]!
	ldr r0, [r1, #-4]!
	ldr r0, [r1], #4
	ldr r0, [r1], #-4
	ldr.w r0, [r1, r2]
	ldr.w r0, [r1, r2, lsl #2]
	ldrt r0, [r1, #4]
	ldrt r0, [r1]
	ldr.w r0, start2
	ldr.w r0, start
	ldr.w r9, [pc, #2000]
	ldr.w pc, [sp], #4
	ldr.w r0, [sp, #-4]!
	ldrb.w r0, [r1, #4]
	ldrb r0, [r1, #-4]
	ldrb r0, [r1], #1
	ldrb.w r0, [r1, r2, lsl #1]
	ldrbt r0, [r1, #3]
	ldrb.w r0, start2
	ldrsb.w r0, [r1, #4]
	ldrsb r0, [r1, #-4]
	ldrsb.w r0, [r1, r2]
	ldrsb.w r0, start
	ldrsbt r0, [r1, #3]
	ldrh.w r0, [r1, #4]
	ldrh r0, [r1, #-4]!
	ldrh.w r0, [r1, r2]
	ldrh.w r0, start2
	ldrht r0, [r1]
	ldrsh.w r0, [r1, #4]
	ldrsh r0, [r1], #-2
	ldrsh.w r0, [r1, r2, lsl #3]
	ldrsh.w r0, start2
	ldrsht r0, [r1, #2]
	pld [r0, #4]
	pld [r0, #-4]
	pld [r0, r1, lsl #2]
	pld start2
	pli [r0, #4]
	pli [r0, #-8]
	pli [r0, r1]
	pli start
	str.w r0, [r1, #4]
	str.w r0, [r1, #4095]
	str r0, [r1, #-4]
	str r0, [r1, #4]!
	str r0, [r1], #-4
	str.w r0, [sp, #-4]!
	str.w r0, [r1, r2, lsl #2]
	strt r0, [r1, #8]
	strb.w r0, [r1, #4]
	strb r0, [r1, #-4]
	strb r0, [r1], #1
	strb.w r0, [r1, r2]
	strbt r0, [r1]
	strh.w r0, [r1, #4]
	strh r0, [r1, #-2]!
	strh.w r0, [r1, r2, lsl #1]
	strht r0, [r1, #2]
	ldrd r0, r1, [r2]
	ldrd r0, r1, [r2, #8]
	ldrd r0, r1, [r2, #-8]
	ldrd r0, r1, [r2, #8]!
	ldrd r0, r1, [r2], #-8
	ldrd r0, r1, start2
	ldrd r0, r1, [pc, #-16]
	strd r0, r1, [r2]
	strd r0, r1, [r2, #1020]
	strd r0, r1, [sp, #-8]!
	strd r0, r1, [r2], #8
	ldrex r0, [r1]
	ldrex r0, [r1, #8]
	strex r0, r1, [r2]
	strex r0, r1, [r2, #1020]
	ldrexb r0, [r1]
	ldrexh r0, [r1]
	strexb r0, r1, [r2]
	strexh r0, r1, [r2]
	clrex
	tbb [r0, r1]
	tbh [r0, r1, lsl #1]
	tbb [pc, r1]
	ldm.w r0, {r1, r2, r8}
	ldm r0!, {r1, r2, r8}
	ldmdb r0, {r1, r2}
	ldmdb r0!, {r1, lr}
	stm.w r0, {r1, r2, r8}
	stm r0!, {r1, r2, r8}
	stmdb r0!, {r1, r2}
	stmdb r0, {r1, r2}
	push.w {r4-r11, lr}
	push.w {r8}
	pop.w {r4-r11, pc}
	pop.w {r8}
	ldmia.w sp!, {r0, r1}
	ldm.w sp, {r0, r1}
	b.w start
	b.w start2
	beq.w start
	bne.w start2
	bgt.w start2
	bl start
	bl start2
	blx start2
	mrs r0, apsr
	mrs r0, iapsr
	mrs r0, eapsr
	mrs r0, xpsr
	mrs r0, ipsr
	mrs r0, epsr
	mrs r0, iepsr
	mrs r0, msp
	mrs r0, psp
	mrs r0, primask
	mrs r0, basepri
	mrs r0, basepri_max
	mrs r0, faultmask
	mrs r0, control
	msr apsr_nzcvq, r0
	msr apsr_g, r0
	msr apsr_nzcvqg, r0
	msr iapsr_nzcvq, r0
	msr eapsr_nzcvq, r0
	msr xpsr_nzcvq, r0
	msr msp, r0
	msr psp, r1
	msr primask, r0
	msr basepri, r0
	msr basepri_max, r0
	msr faultmask, r0
	msr control, r0
	dmb sy
	dmb ish
	dmb
	dsb sy
	dsb
	isb sy
	isb
	dmb #0
	nop.w
	yield.w
	wfe.w
	wfi.w
	sev.w
	dbg #5
	udf.w #0
	udf.w #65535
	it eq
	addeq r0, r1
	itt ne
	movne r0, #1
	movne.w r1, #2
	ite gt
	addgt.w r0, r1, r2
	suble r0, r1, r2
	itete cc
	ldrcc r0, [r1]
	ldrcs.w r0, [r1, #4]
	strcc r0, [r1, r2]
	strcs.w r0, [r1, r2]
	ittt mi
	movmi r0, r1
	addmi r0, sp, #4
	bmi start2
	it eq
	beq.w start
	it ne
	bxne lr
	it hi
	pophi {r4, pc}
	itttt vs
	cmpvs r0, #1
	lslvs r0, r1, #2
	addvs r0, r1, r2
	negvs r0, r1
	it lt
	movlt r0, r1
	ite pl
	ldmiapl r0!, {r1}
	svcmi 4
	it ge
	nopge
	it le
	movwle r0, #5
	ssat r0, #2, r1
	qadd r0, r1, r2
	.align 2
start2:
	nop
	nop
