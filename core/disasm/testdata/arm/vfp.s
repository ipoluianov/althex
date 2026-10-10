@ FPv4/FPv5 single and double precision
.syntax unified
.thumb
start:
	vldr s0, [r0]
	vldr s31, [r1, #1020]
	vldr d0, [r0, #-8]
	vldr d15, [sp, #16]
	vldr s2, start2
	vldr d1, start
	vstr s0, [r0, #4]
	vstr d8, [r1, #-1020]
	vpush {s16-s31}
	vpush {d8-d15}
	vpush {s0}
	vpop {s16-s31}
	vpop {d8}
	vldmia r0, {s0-s3}
	vldmia r0!, {d0-d3}
	vldmdb r0!, {s1-s2}
	vstmia r0, {s4}
	vstmia r1!, {d4-d5}
	vstmdb r1!, {d0}
	vmov s0, r1
	vmov r2, s3
	vmov s0, s1, r2, r3
	vmov r2, r3, s0, s1
	vmov d0, r2, r3
	vmov r2, r3, d0
	vmov.32 d0[0], r1
	vmov.32 d1[1], r1
	vmov.32 r1, d2[1]
	vmov.f32 s0, s1
	vmov.f64 d0, d1
	vmov.f32 s0, #1.0
	vmov.f32 s0, #-2.5
	vmov.f64 d0, #0.5
	vmov.f32 s1, #31.0
	vadd.f32 s0, s1, s2
	vadd.f64 d0, d1, d2
	vsub.f32 s0, s1, s2
	vsub.f64 d0, d1, d15
	vmul.f32 s0, s1, s2
	vmul.f64 d0, d1, d2
	vnmul.f32 s0, s1, s2
	vdiv.f32 s0, s1, s2
	vdiv.f64 d0, d1, d2
	vmla.f32 s0, s1, s2
	vmls.f32 s0, s1, s2
	vnmla.f32 s0, s1, s2
	vnmls.f64 d0, d1, d2
	vfma.f32 s0, s1, s2
	vfms.f32 s0, s1, s2
	vfnma.f32 s0, s1, s2
	vfnms.f64 d0, d1, d2
	vneg.f32 s0, s1
	vneg.f64 d0, d1
	vabs.f32 s0, s1
	vabs.f64 d0, d1
	vsqrt.f32 s0, s1
	vsqrt.f64 d0, d1
	vcmp.f32 s0, s1
	vcmpe.f32 s0, s1
	vcmp.f32 s0, #0
	vcmpe.f64 d0, #0
	vcmp.f64 d0, d1
	vcvt.f64.f32 d0, s1
	vcvt.f32.f64 s0, d1
	vcvt.f32.s32 s0, s1
	vcvt.f32.u32 s0, s1
	vcvt.f64.s32 d0, s1
	vcvt.f64.u32 d0, s1
	vcvt.s32.f32 s0, s1
	vcvt.u32.f32 s0, s1
	vcvtr.s32.f32 s0, s1
	vcvtr.u32.f64 s0, d1
	vcvt.s32.f64 s0, d1
	vcvt.s16.f32 s0, s0, #4
	vcvt.u32.f32 s0, s0, #20
	vcvt.f32.s16 s0, s0, #16
	vcvt.f32.u32 s0, s0, #1
	vcvt.f64.s32 d0, d0, #8
	vcvtb.f32.f16 s0, s1
	vcvtt.f32.f16 s0, s1
	vcvtb.f16.f32 s0, s1
	vcvtt.f16.f32 s0, s1
	vcvtb.f64.f16 d0, s1
	vcvtb.f16.f64 s0, d1
	vmrs r0, fpscr
	vmrs APSR_nzcv, fpscr
	vmsr fpscr, r0
	vmrs r1, fpexc
	vmsr fpexc, r1
	vrinta.f32 s0, s1
	vrintn.f64 d0, d1
	vrintp.f32 s0, s1
	vrintm.f32 s0, s1
	vrintr.f32 s0, s1
	vrintz.f64 d0, d1
	vrintx.f32 s0, s1
	vcvta.s32.f32 s0, s1
	vcvtn.u32.f32 s0, s1
	vcvtp.s32.f64 s0, d1
	vcvtm.u32.f64 s0, d1
	vseleq.f32 s0, s1, s2
	vselge.f64 d0, d1, d2
	vselgt.f32 s0, s1, s2
	vselvs.f32 s0, s1, s2
	vmaxnm.f32 s0, s1, s2
	vminnm.f64 d0, d1, d2
	it eq
	vaddeq.f32 s0, s1, s2
	it ne
	vmovne r0, s1
	.align 2
start2:
	nop
	nop
