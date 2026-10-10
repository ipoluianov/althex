package disasm

// EffectKind is what an instruction does that the analysis follows: the
// constants put into registers and the memory reached through them, so an
// access like "str r1, [r0, #0x18]" after "ldr r0, =0x40021000" is known to
// be 0x40021018 (FW-8)
type EffectKind int

const (
	// SetConst: Dst = Imm
	SetConst EffectKind = iota + 1
	// SetLow16: the low 16 bits of Dst = Imm, the high ones cleared (ARM movw)
	SetLow16
	// SetHigh16: the high 16 bits of Dst = Imm, the low ones kept (ARM movt)
	SetHigh16
	// AddImm: Dst = Src + Imm (also with Src = Dst; Src may be the PC: then
	// the value is Addr, already computed by the decoder: RISC-V auipc)
	AddImm
	// LoadLiteral: Dst = the Size bytes in memory at Addr (ARM ldr rX, [pc,
	// #n], Xtensa l32r): the analysis reads them from the image
	LoadLiteral
	// MemAccess: a load or a store of Size bytes at Base + Imm, or at Addr
	// when Base is -1 (AVR lds/sts, a known address); a store has the
	// register stored in Src (-1 when not one), a load the register loaded in Dst
	MemAccess
	// Clobber: Dst gets a value that is not known
	Clobber
	// JumpReg: the jump or the call goes to Src + Imm (ARM blx r3, RISC-V
	// jalr, Xtensa callx8; AVR icall: the word address in Z, Src = 30); the
	// analysis follows it when the register is known
	JumpReg
)

// Effect is one effect of an instruction
type Effect struct {
	Kind  EffectKind
	Dst   int // a register number of the architecture, -1 none
	Src   int
	Base  int
	Imm   int64
	Addr  uint64
	Size  int
	Store bool
}

// Modes of architectures with more than one instruction set
const (
	ModeDefault = 0
	ModeThumb   = 1 // ARM: the target or the code is Thumb
	ModeARM     = 2 // ARM: the target is ARM (A32)
)
