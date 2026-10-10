	.text
	nop
	ret
	reti
	sleep
	break
	wdr
	ijmp
	icall
	sec
	sez
	sen
	sev
	ses
	seh
	set
	sei
	clc
	clz
	cln
	clv
	cls
	clh
	clt
	cli
	lpm
	spm
	add r8, r4
	add r16, r7
	add r31, r28
	add r30, r24
	add r13, r6
	add r31, r1
	adc r24, r27
	adc r0, r28
	adc r17, r14
	adc r6, r20
	adc r1, r1
	adc r1, r0
	sub r24, r13
	sub r27, r1
	sub r14, r28
	sub r31, r14
	sub r22, r14
	sub r14, r29
	sbc r18, r1
	sbc r26, r6
	sbc r11, r18
	sbc r7, r21
	sbc r27, r12
	sbc r19, r18
	and r31, r25
	and r2, r30
	and r15, r25
	and r26, r11
	and r23, r23
	and r5, r28
	or r6, r10
	or r25, r23
	or r31, r1
	or r30, r2
	or r19, r25
	or r10, r10
	eor r14, r0
	eor r12, r14
	eor r25, r22
	eor r22, r29
	eor r17, r0
	eor r24, r8
	mov r13, r27
	mov r3, r30
	mov r23, r12
	mov r26, r31
	mov r22, r26
	mov r22, r0
	cp r21, r29
	cp r1, r14
	cp r11, r11
	cp r5, r16
	cp r2, r4
	cp r5, r1
	cpc r28, r0
	cpc r17, r15
	cpc r17, r7
	cpc r11, r22
	cpc r18, r4
	cpc r10, r10
	cpse r16, r10
	cpse r17, r18
	cpse r29, r20
	cpse r31, r30
	cpse r7, r1
	cpse r19, r24
	mul r21, r26
	mul r12, r16
	mul r6, r16
	mul r13, r27
	mul r1, r14
	mul r1, r25
	movw r8, r2
	movw r10, r28
	movw r26, r14
	movw r28, r14
	movw r0, r24
	movw r20, r26
	muls r17, r25
	muls r20, r22
	muls r17, r25
	muls r18, r18
	muls r25, r25
	muls r21, r29
	mulsu r20, r18
	mulsu r16, r16
	mulsu r19, r23
	mulsu r18, r16
	mulsu r22, r19
	mulsu r21, r17
	fmul r19, r22
	fmul r19, r23
	fmul r17, r22
	fmul r20, r23
	fmul r16, r21
	fmul r22, r20
	fmuls r16, r18
	fmuls r19, r21
	fmuls r18, r21
	fmuls r22, r19
	fmuls r20, r17
	fmuls r22, r21
	fmulsu r23, r19
	fmulsu r17, r16
	fmulsu r17, r18
	fmulsu r18, r18
	fmulsu r19, r20
	fmulsu r21, r20
	adiw r28, 43
	adiw r28, 14
	adiw r28, 30
	adiw r30, 17
	adiw r24, 41
	adiw r24, 52
	sbiw r24, 48
	sbiw r26, 16
	sbiw r28, 14
	sbiw r30, 9
	sbiw r26, 10
	sbiw r28, 46
	ldi r24, 0
	ldi r19, 1
	ldi r17, 127
	ldi r25, 128
	ldi r16, 255
	ldi r16, 151
	ldi r18, 58
	ldi r29, 234
	cpi r23, 0
	cpi r29, 1
	cpi r21, 127
	cpi r19, 128
	cpi r30, 255
	cpi r21, 58
	cpi r23, 20
	cpi r21, 96
	subi r25, 0
	subi r24, 1
	subi r31, 127
	subi r26, 128
	subi r19, 255
	subi r22, 52
	subi r26, 222
	subi r17, 193
	sbci r26, 0
	sbci r30, 1
	sbci r28, 127
	sbci r26, 128
	sbci r28, 255
	sbci r18, 13
	sbci r18, 5
	sbci r26, 151
	ori r22, 0
	ori r31, 1
	ori r27, 127
	ori r24, 128
	ori r21, 255
	ori r22, 233
	ori r25, 57
	ori r22, 128
	andi r24, 0
	andi r18, 1
	andi r30, 127
	andi r18, 128
	andi r26, 255
	andi r23, 126
	andi r28, 184
	andi r25, 41
	com r2
	com r20
	com r11
	com r20
	neg r19
	neg r15
	neg r21
	neg r6
	swap r5
	swap r15
	swap r14
	swap r1
	inc r15
	inc r25
	inc r4
	inc r17
	dec r4
	dec r4
	dec r1
	dec r0
	asr r18
	asr r22
	asr r31
	asr r30
	lsr r9
	lsr r6
	lsr r20
	lsr r4
	ror r11
	ror r11
	ror r9
	ror r9
	push r20
	push r19
	push r6
	push r18
	pop r8
	pop r13
	pop r9
	pop r2
	bld r20, 3
	bld r11, 4
	bld r27, 2
	bld r3, 3
	bld r16, 1
	bld r28, 6
	bst r16, 7
	bst r29, 0
	bst r25, 5
	bst r10, 4
	bst r31, 0
	bst r26, 0
	sbrc r3, 5
	sbrc r8, 2
	sbrc r8, 4
	sbrc r17, 6
	sbrc r25, 2
	sbrc r5, 3
	sbrs r31, 0
	sbrs r11, 5
	sbrs r28, 3
	sbrs r15, 5
	sbrs r31, 7
	sbrs r14, 6
	sbi 0, 1
	sbi 31, 5
	sbi 21, 2
	sbi 17, 3
	sbi 14, 4
	sbi 3, 4
	cbi 0, 1
	cbi 31, 1
	cbi 19, 6
	cbi 23, 2
	cbi 10, 2
	cbi 29, 4
	sbic 0, 6
	sbic 31, 5
	sbic 27, 6
	sbic 13, 2
	sbic 3, 0
	sbic 31, 1
	sbis 0, 2
	sbis 31, 1
	sbis 16, 7
	sbis 6, 3
	sbis 17, 6
	sbis 5, 6
	in r25, 21
	in r20, 56
	in r8, 62
	in r13, 15
	in r27, 52
	in r7, 37
	in r17, 31
	in r24, 0
	out 24, r28
	out 2, r1
	out 31, r16
	out 26, r11
	out 36, r9
	out 25, r17
	out 39, r16
	out 57, r10
	rjmp .+0
	rjmp .+2
	rjmp .-2
	rjmp .+4094
	rjmp .-4096
	rjmp .+1752
	rjmp .+3944
	rjmp .+2784
	rjmp .-2102
	rcall .+0
	rcall .+2
	rcall .-2
	rcall .+4094
	rcall .-4096
	rcall .-674
	rcall .+2182
	rcall .-742
	rcall .+556
	brcs .+0
	brcs .-128
	brcs .+126
	brcs .-74
	brcc .+0
	brcc .-128
	brcc .+126
	brcc .-116
	breq .+0
	breq .-128
	breq .+126
	breq .-68
	brne .+0
	brne .-128
	brne .+126
	brne .-122
	brmi .+0
	brmi .-128
	brmi .+126
	brmi .+22
	brpl .+0
	brpl .-128
	brpl .+126
	brpl .-60
	brvs .+0
	brvs .-128
	brvs .+126
	brvs .-90
	brvc .+0
	brvc .-128
	brvc .+126
	brvc .+62
	brlt .+0
	brlt .-128
	brlt .+126
	brlt .+30
	brge .+0
	brge .-128
	brge .+126
	brge .+94
	brhs .+0
	brhs .-128
	brhs .+126
	brhs .+54
	brhc .+0
	brhc .-128
	brhc .+126
	brhc .+36
	brts .+0
	brts .-128
	brts .+126
	brts .-128
	brtc .+0
	brtc .-128
	brtc .+126
	brtc .-66
	brie .+0
	brie .-128
	brie .+126
	brie .+98
	brid .+0
	brid .-128
	brid .+126
	brid .+102
	ld r22, X
	st X, r19
	ld r25, X
	st X, r21
	ld r31, X
	st X, r7
	ld r24, X+
	st X+, r24
	ld r13, X+
	st X+, r0
	ld r17, X+
	st X+, r12
	ld r29, -X
	st -X, r26
	ld r19, -X
	st -X, r10
	ld r28, -X
	st -X, r12
	ld r23, Y
	st Y, r0
	ld r24, Y
	st Y, r27
	ld r25, Y
	st Y, r21
	ld r4, Y+
	st Y+, r31
	ld r15, Y+
	st Y+, r18
	ld r1, Y+
	st Y+, r26
	ld r9, -Y
	st -Y, r25
	ld r17, -Y
	st -Y, r11
	ld r4, -Y
	st -Y, r0
	ld r22, Z
	st Z, r16
	ld r26, Z
	st Z, r19
	ld r9, Z
	st Z, r29
	ld r16, Z+
	st Z+, r31
	ld r10, Z+
	st Z+, r29
	ld r2, Z+
	st Z+, r17
	ld r6, -Z
	st -Z, r27
	ld r4, -Z
	st -Z, r22
	ld r4, -Z
	st -Z, r28
	ldd r25, Y+0
	std Y+0, r17
	ldd r19, Y+1
	std Y+1, r13
	ldd r13, Y+63
	std Y+63, r15
	ldd r21, Y+2
	std Y+2, r17
	ldd r4, Y+21
	std Y+21, r4
	ldd r23, Y+20
	std Y+20, r29
	ldd r3, Y+11
	std Y+11, r10
	ldd r25, Z+0
	std Z+0, r25
	ldd r11, Z+1
	std Z+1, r30
	ldd r16, Z+63
	std Z+63, r21
	ldd r14, Z+38
	std Z+38, r16
	ldd r15, Z+34
	std Z+34, r1
	ldd r25, Z+45
	std Z+45, r20
	ldd r27, Z+29
	std Z+29, r15
	lds r28, 0
	sts 0, r9
	lds r16, 65535
	sts 65535, r29
	lds r10, 256
	sts 256, r8
	lds r8, 35270
	sts 35270, r28
	lds r23, 24890
	sts 24890, r19
	lds r25, 9507
	sts 9507, r15
	lds r7, 21709
	sts 21709, r13
	lpm r19, Z
	lpm r4, Z+
	lpm r6, Z
	lpm r14, Z+
	lpm r25, Z
	lpm r20, Z+
	lpm r31, Z
	lpm r6, Z+
	jmp 0
	call 0
	jmp 2
	call 2
	jmp 131070
	call 131070
	jmp 48958
	call 48958
	jmp 11790
	call 11790
	jmp 14506
	call 14506
	jmp 6102
	call 6102
	cpse r1, r2
	jmp 0x100
	sbrc r3, 1
	lds r4, 0x200
	sbis 5, 1
	call 0x300
