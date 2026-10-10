@ ARMv7E-M DSP extension
.syntax unified
.thumb
	sadd16 r0, r1, r2
	sasx r0, r1, r2
	ssax r0, r1, r2
	ssub16 r0, r1, r2
	sadd8 r0, r1, r2
	ssub8 r0, r1, r2
	qadd16 r0, r1, r2
	qasx r0, r1, r2
	qsax r0, r1, r2
	qsub16 r0, r1, r2
	qadd8 r0, r1, r2
	qsub8 r0, r1, r2
	shadd16 r0, r1, r2
	shasx r0, r1, r2
	shsax r0, r1, r2
	shsub16 r0, r1, r2
	shadd8 r0, r1, r2
	shsub8 r0, r1, r2
	uadd16 r0, r1, r2
	uasx r0, r1, r2
	usax r0, r1, r2
	usub16 r0, r1, r2
	uadd8 r0, r1, r2
	usub8 r0, r1, r2
	uqadd16 r0, r1, r2
	uqasx r0, r1, r2
	uqsax r0, r1, r2
	uqsub16 r0, r1, r2
	uqadd8 r0, r1, r2
	uqsub8 r0, r1, r2
	uhadd16 r0, r1, r2
	uhasx r0, r1, r2
	uhsax r0, r1, r2
	uhsub16 r0, r1, r2
	uhadd8 r0, r1, r2
	uhsub8 r0, r1, r2
	qadd r0, r1, r2
	qdadd r0, r1, r2
	qsub r0, r1, r2
	qdsub r0, r1, r2
	sel r0, r1, r2
	smlabb r0, r1, r2, r3
	smlabt r0, r1, r2, r3
	smlatb r0, r1, r2, r3
	smlatt r0, r1, r2, r3
	smulbb r0, r1, r2
	smulbt r0, r1, r2
	smultb r0, r1, r2
	smultt r0, r1, r2
	smlad r0, r1, r2, r3
	smladx r0, r1, r2, r3
	smuad r0, r1, r2
	smuadx r0, r1, r2
	smlawb r0, r1, r2, r3
	smlawt r0, r1, r2, r3
	smulwb r0, r1, r2
	smulwt r0, r1, r2
	smlsd r0, r1, r2, r3
	smlsdx r0, r1, r2, r3
	smusd r0, r1, r2
	smusdx r0, r1, r2
	smmla r0, r1, r2, r3
	smmlar r0, r1, r2, r3
	smmul r0, r1, r2
	smmulr r0, r1, r2
	smmls r0, r1, r2, r3
	smmlsr r0, r1, r2, r3
	usad8 r0, r1, r2
	usada8 r0, r1, r2, r3
	smlalbb r0, r1, r2, r3
	smlalbt r0, r1, r2, r3
	smlaltb r0, r1, r2, r3
	smlaltt r0, r1, r2, r3
	smlald r0, r1, r2, r3
	smlaldx r0, r1, r2, r3
	smlsld r0, r1, r2, r3
	smlsldx r0, r1, r2, r3
	umaal r0, r1, r2, r3
	sxtab16 r0, r1, r2, ror #8
	pkhtb r0, r1, r2
