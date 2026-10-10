package fs

// LZNT1, the compression of NTFS: the data is cut into chunks of 4096
// bytes; each chunk has a header of 2 bytes (the size of its data - 1 in
// the low 12 bits, bit 15 - compressed) and, when compressed, groups of a
// flag byte and 8 tokens: a literal byte for a 0 bit, a back reference of
// 2 bytes for a 1 bit. The split of the reference between the offset and
// the length depends on how far into the chunk it is.

const ntfsChunk = 4096

// lznt1Decompress decompresses src into dst, which is filled from the
// start; it returns the number of bytes made. A chunk that makes less than
// 4096 bytes is followed by zeros up to the next chunk, so the result of a
// whole compression unit keeps its offsets. A damaged stream is an error.
func lznt1Decompress(dst, src []byte) (int, error) {
	out := 0
	for in := 0; in+2 <= len(src) && out < len(dst); {
		h := int(xLE16(src[in:]))
		if h == 0 {
			break // the end of the data
		}
		in += 2
		n := h&0xFFF + 1
		if in+n > len(src) {
			return out, xDamaged("LZNT1 chunk of %d bytes past the end", n)
		}
		chunk := src[in : in+n]
		in += n
		start := out
		end := min(start+ntfsChunk, len(dst))
		if h&0x8000 == 0 {
			out += copy(dst[out:end], chunk)
			out = end
			continue
		}
		for c := 0; c < len(chunk) && out < end; {
			flags := chunk[c]
			c++
			for bit := 0; bit < 8 && c < len(chunk) && out < end; bit++ {
				if flags&(1<<bit) == 0 {
					dst[out] = chunk[c]
					out++
					c++
					continue
				}
				if c+2 > len(chunk) {
					return out, xDamaged("LZNT1 token cut")
				}
				tok := int(xLE16(chunk[c:]))
				c += 2
				pos := out - start
				if pos == 0 {
					return out, xDamaged("LZNT1 reference at the start of a chunk")
				}
				// The offset takes as many bits as the position needs, at
				// least 4; the length the rest of the 16
				lg := 0
				for i := pos - 1; i >= 0x10; i >>= 1 {
					lg++
				}
				back := tok>>(12-lg) + 1
				length := tok&(0xFFF>>lg) + 3
				if back > pos {
					return out, xDamaged("LZNT1 reference before the chunk")
				}
				for ; length > 0 && out < end; length-- {
					dst[out] = dst[out-back] // may overlap: byte by byte
					out++
				}
			}
		}
		// A short chunk is followed by zeros (dst is zeroed by the caller)
		out = end
	}
	return out, nil
}
