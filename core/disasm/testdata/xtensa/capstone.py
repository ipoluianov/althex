# Decodes instructions with Capstone for the differential tests of the
# disassemblers: capstone.py <capstone dir> <arch> <mode> <in> <out> <address>; each
# line of <in> is the hex bytes of one instruction; each line of <out> is
# "hex<TAB>size<TAB>mnemonic<TAB>operands", size 0 when it is not decoded
import sys
sys.path.insert(0, sys.argv[1])
import capstone

addr = int(sys.argv[6], 0)
md = capstone.Cs(getattr(capstone, sys.argv[2]), getattr(capstone, sys.argv[3]))
with open(sys.argv[4]) as f, open(sys.argv[5], "w") as out:
    for line in f:
        h = line.strip()
        if not h:
            continue
        ins = next(md.disasm(bytes.fromhex(h), addr), None)
        if ins is None:
            out.write("%s\t0\t\t\n" % h)
        else:
            out.write("%s\t%d\t%s\t%s\n" % (h, ins.size, ins.mnemonic, ins.op_str))
