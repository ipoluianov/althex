@ ARMv8-M additions
.syntax unified
.thumb
	sg
	tt r0, r1
	ttt r0, r1
	tta r0, r1
	ttat r2, r3
	bxns lr
	blxns r3
	lda r0, [r1]
	ldab r0, [r1]
	ldah r0, [r1]
	stl r0, [r1]
	stlb r0, [r1]
	stlh r0, [r1]
	ldaex r0, [r1]
	ldaexb r0, [r1]
	ldaexh r0, [r1]
	stlex r0, r1, [r2]
	stlexb r0, r1, [r2]
	stlexh r0, r1, [r2]
	mrs r0, msp_ns
	msr psp_ns, r0
	mrs r0, msplim
	msr psplim, r0
	mrs r0, control_ns
	movw r0, #1
	movt r0, #2
	b.w lbl
	cbz r0, lbl
lbl:
	nop
