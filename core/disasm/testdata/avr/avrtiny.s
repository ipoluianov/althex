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
	add r20, r18
	add r24, r19
	add r31, r30
	add r31, r28
	add r22, r19
	add r31, r16
	adc r28, r29
	adc r16, r30
	adc r24, r23
	adc r19, r26
	adc r16, r16
	adc r16, r16
	sub r28, r22
	sub r29, r16
	sub r23, r30
	sub r31, r23
	sub r27, r23
	sub r23, r30
	sbc r25, r16
	sbc r29, r19
	sbc r21, r25
	sbc r19, r26
	sbc r29, r22
	sbc r25, r25
	and r31, r28
	and r17, r31
	and r23, r28
	and r29, r21
	and r27, r27
	and r18, r30
	or r19, r21
	or r28, r27
	or r31, r16
	or r31, r17
	or r25, r28
	or r21, r21
	eor r23, r16
	eor r22, r23
	eor r28, r27
	eor r27, r30
	eor r24, r16
	eor r28, r20
	mov r22, r29
	mov r17, r31
	mov r27, r22
	mov r29, r31
	mov r27, r29
	mov r27, r16
	cp r26, r30
	cp r16, r23
	cp r21, r21
	cp r18, r24
	cp r17, r18
	cp r18, r16
	cpc r30, r16
	cpc r24, r23
	cpc r24, r19
	cpc r21, r27
	cpc r25, r18
	cpc r21, r21
	cpse r24, r21
	cpse r24, r25
	cpse r30, r26
	cpse r31, r31
	cpse r19, r16
	cpse r25, r28
	ldi r24, 0
	ldi r19, 1
	ldi r24, 127
	ldi r22, 128
	ldi r29, 255
	ldi r16, 175
	ldi r23, 215
	ldi r16, 96
	cpi r21, 0
	cpi r30, 1
	cpi r29, 127
	cpi r23, 128
	cpi r30, 255
	cpi r23, 203
	cpi r16, 74
	cpi r28, 18
	subi r25, 0
	subi r20, 1
	subi r22, 127
	subi r17, 128
	subi r25, 255
	subi r18, 164
	subi r18, 218
	subi r25, 30
	sbci r24, 0
	sbci r20, 1
	sbci r16, 127
	sbci r17, 128
	sbci r22, 255
	sbci r30, 152
	sbci r21, 81
	sbci r17, 213
	ori r19, 0
	ori r22, 1
	ori r29, 127
	ori r22, 128
	ori r31, 255
	ori r19, 193
	ori r28, 102
	ori r25, 177
	andi r28, 0
	andi r25, 1
	andi r16, 127
	andi r21, 128
	andi r22, 255
	andi r26, 255
	andi r20, 8
	andi r26, 166
	com r29
	com r22
	com r24
	com r19
	neg r28
	neg r27
	neg r31
	neg r23
	swap r18
	swap r17
	swap r18
	swap r20
	inc r21
	inc r21
	inc r22
	inc r24
	dec r26
	dec r24
	dec r27
	dec r26
	asr r26
	asr r19
	asr r25
	asr r23
	lsr r31
	lsr r20
	lsr r19
	lsr r26
	ror r17
	ror r29
	ror r18
	ror r28
	push r20
	push r20
	push r26
	push r19
	pop r28
	pop r18
	pop r23
	pop r18
	bld r24, 5
	bld r25, 1
	bld r30, 4
	bld r19, 0
	bld r25, 0
	bld r16, 1
	bst r29, 1
	bst r17, 3
	bst r23, 6
	bst r21, 1
	bst r30, 2
	bst r23, 2
	sbrc r19, 6
	sbrc r28, 4
	sbrc r24, 7
	sbrc r26, 1
	sbrc r22, 5
	sbrc r17, 0
	sbrs r16, 4
	sbrs r26, 7
	sbrs r28, 5
	sbrs r28, 1
	sbrs r18, 5
	sbrs r30, 1
	sbi 0, 4
	sbi 31, 2
	sbi 16, 3
	sbi 13, 4
	sbi 30, 3
	sbi 22, 3
	cbi 0, 7
	cbi 31, 1
	cbi 23, 5
	cbi 5, 3
	cbi 17, 6
	cbi 5, 4
	sbic 0, 4
	sbic 31, 3
	sbic 2, 5
	sbic 20, 1
	sbic 11, 1
	sbic 20, 3
	sbis 0, 1
	sbis 31, 4
	sbis 14, 1
	sbis 1, 1
	sbis 15, 0
	sbis 25, 0
	in r25, 45
	in r31, 60
	in r20, 12
	in r26, 9
	in r21, 22
	in r20, 18
	in r26, 39
	in r19, 37
	out 16, r22
	out 18, r17
	out 40, r22
	out 22, r25
	out 55, r21
	out 6, r23
	out 32, r18
	out 57, r29
	rjmp .+0
	rjmp .+2
	rjmp .-2
	rjmp .+4094
	rjmp .-4096
	rjmp .+2
	rjmp .+3102
	rjmp .+3330
	rjmp .-3918
	rcall .+0
	rcall .+2
	rcall .-2
	rcall .+4094
	rcall .-4096
	rcall .+2386
	rcall .+1452
	rcall .-1286
	rcall .+130
	brcs .+0
	brcs .-128
	brcs .+126
	brcs .+120
	brcc .+0
	brcc .-128
	brcc .+126
	brcc .-116
	breq .+0
	breq .-128
	breq .+126
	breq .+84
	brne .+0
	brne .-128
	brne .+126
	brne .-120
	brmi .+0
	brmi .-128
	brmi .+126
	brmi .-98
	brpl .+0
	brpl .-128
	brpl .+126
	brpl .+52
	brvs .+0
	brvs .-128
	brvs .+126
	brvs .-58
	brvc .+0
	brvc .-128
	brvc .+126
	brvc .-64
	brlt .+0
	brlt .-128
	brlt .+126
	brlt .-58
	brge .+0
	brge .-128
	brge .+126
	brge .+4
	brhs .+0
	brhs .-128
	brhs .+126
	brhs .+12
	brhc .+0
	brhc .-128
	brhc .+126
	brhc .+74
	brts .+0
	brts .-128
	brts .+126
	brts .+76
	brtc .+0
	brtc .-128
	brtc .+126
	brtc .-40
	brie .+0
	brie .-128
	brie .+126
	brie .-84
	brid .+0
	brid .-128
	brid .+126
	brid .-10
	ld r31, X
	st X, r16
	ld r21, X
	st X, r26
	ld r30, X
	st X, r23
	ld r23, X+
	st X+, r26
	ld r31, X+
	st X+, r31
	ld r23, X+
	st X+, r29
	ld r26, -X
	st -X, r24
	ld r23, -X
	st -X, r17
	ld r18, -X
	st -X, r27
	ld r21, Y
	st Y, r22
	ld r25, Y
	st Y, r25
	ld r25, Y
	st Y, r27
	ld r21, Y+
	st Y+, r30
	ld r18, Y+
	st Y+, r19
	ld r28, Y+
	st Y+, r21
	ld r20, -Y
	st -Y, r24
	ld r29, -Y
	st -Y, r22
	ld r17, -Y
	st -Y, r31
	ld r28, Z
	st Z, r27
	ld r28, Z
	st Z, r21
	ld r17, Z
	st Z, r18
	ld r24, Z+
	st Z+, r19
	ld r24, Z+
	st Z+, r18
	ld r20, Z+
	st Z+, r18
	ld r30, -Z
	st -Z, r23
	ld r28, -Z
	st -Z, r29
	ld r28, -Z
	st -Z, r21
	lds r19, 64
	sts 64, r29
	lds r29, 191
	sts 191, r19
	lds r25, 128
	sts 128, r24
	lds r23, 147
	sts 147, r28
	lds r16, 176
	sts 176, r22
	lds r30, 96
	sts 96, r16
	lds r16, 188
	sts 188, r23
	lds r24, 118
	sts 118, r22
