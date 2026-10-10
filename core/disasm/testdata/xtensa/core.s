# Instructions of the Xtensa core and of the ESP8266 (LX106) options, for
# the differential test against GNU objdump (xtensa-lx106-elf-as)
	.text
	.align 4
lit:
	.byte	0xff, 0xff, 0xff, 0xff
start:
	l32r	a2, lit
	l32r	a15, lit
	movi	a3, 0x3ff
	movi	a3, -5
	movi	a4, -2048
	movi	a5, 2047
	movi	a5, 255
	movi	a5, 256
	movi	a5, -255
	movi	a5, -256
	movi.n	a2, 95
	movi.n	a2, -32
	movi.n	a7, 0
	addi	a1, a1, -16
	addi	a2, a3, 127
	addi.n	a2, a3, -1
	addi.n	a2, a3, 15
	addmi	a2, a3, 0x7f00
	addmi	a2, a3, -32768
	add	a2, a3, a4
	add.n	a2, a3, a4
	sub	a2, a3, a4
	addx2	a2, a3, a4
	addx4	a2, a3, a4
	addx8	a2, a3, a4
	subx2	a2, a3, a4
	subx4	a2, a3, a4
	subx8	a2, a3, a4
	and	a2, a3, a4
	or	a2, a3, a4
	xor	a2, a3, a4
	or	a5, a6, a6
	mov.n	a2, a3
	neg	a2, a3
	abs	a2, a3
	moveqz	a2, a3, a4
	movnez	a2, a3, a4
	movltz	a2, a3, a4
	movgez	a2, a3, a4
	extui	a2, a3, 4, 8
	extui	a2, a3, 31, 1
	extui	a2, a3, 16, 16
	slli	a2, a3, 1
	slli	a2, a3, 31
	srli	a2, a3, 15
	srai	a2, a3, 0
	srai	a2, a3, 31
	src	a2, a3, a4
	sll	a2, a3
	srl	a2, a3
	sra	a2, a3
	ssl	a2
	ssr	a2
	ssai	0
	ssai	31
	ssa8l	a2
	ssa8b	a2
	l8ui	a2, a3, 255
	l16ui	a2, a3, 510
	l16si	a2, a3, 2
	l32i	a2, a1, 1020
	l32i.n	a2, a1, 60
	s8i	a2, a3, 0
	s16i	a2, a3, 4
	s32i	a2, a1, 16
	s32i.n	a2, a1, 0
	mul16u	a2, a3, a4
	mul16s	a2, a3, a4
	mull	a2, a3, a4
	nsa	a2, a3
	nsau	a2, a3
near:
	beq	a2, a3, near
	bne	a2, a3, near
	blt	a2, a3, near
	bge	a2, a3, near
	bltu	a2, a3, near
	bgeu	a2, a3, near
	bany	a2, a3, near
	bnone	a2, a3, near
	ball	a2, a3, near
	bnall	a2, a3, near
	bbc	a2, a3, near
	bbs	a2, a3, near
	bbci	a2, 0, near
	bbci	a2, 31, near
	bbsi	a2, 16, near
	beqi	a2, -1, near
	bnei	a2, 256, near
	blti	a2, 10, near
	bgei	a2, 12, near
	bltui	a2, 32768, near
	bgeui	a2, 65536, near
	beqz	a2, start
	bnez	a2, start
	bltz	a2, start
	bgez	a2, start
	beqz.n	a2, next
	bnez.n	a3, next
	j	start
	jx	a2
	call0	start
	callx0	a3
next:
	ret
	ret.n
	nop
	nop.n
	ill
	break	1, 2
	break.n	3
	syscall
	rfe
	rfi	3
	rsil	a2, 3
	waiti	0
	memw
	extw
	isync
	rsync
	esync
	dsync
	excw
	rsr	a2, sar
	rsr	a2, ps
	rsr	a2, epc1
	rsr	a2, exccause
	rsr	a2, vecbase
	rsr	a2, intenable
	rsr	a2, interrupt
	rsr	a2, ccount
	rsr	a2, ccompare0
	rsr	a2, litbase
	rsr	a2, excvaddr
	rsr	a2, depc
	rsr	a2, prid
	wsr	a2, sar
	wsr	a2, ps
	wsr	a2, intenable
	wsr	a2, intset
	wsr	a2, intclear
	wsr	a2, ccompare0
	wsr	a2, vecbase
	wsr	a2, litbase
	xsr	a2, excsave1
	xsr	a2, ps
	.byte	0xff
