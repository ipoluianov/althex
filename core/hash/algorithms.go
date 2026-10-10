// Package checksum computes checksums, hashes and byte statistics (the
// histogram and entropy) of a file or a part of it (ANL-7, ANL-8, VIEW-8).
// The data can be of any size: it is read in parts from an io.ReaderAt, in
// the background, with progress and cancel. The directory is core/hash; the
// package is named checksum not to clash with the standard hash package.
package checksum

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	gohash "hash"
	"hash/adler32"
	"hash/crc32"
	"hash/crc64"
)

// Algorithm is a checksum or a hash
type Algorithm struct {
	ID   string // stable, for settings: "crc32", "sha256", ...
	Name string // shown: "CRC-32", "SHA-256", ...
	New  func() gohash.Hash
}

// The standard tables are made once; hash/crc32 and hash/crc64 use faster
// code than NewCRC (slicing and CPU instructions), so the CRCs they know
// are taken from there
var (
	crc32cTable = crc32.MakeTable(crc32.Castagnoli)
	crc64ECMA   = crc64.MakeTable(crc64.ECMA)
	crc64ISO    = crc64.MakeTable(crc64.ISO)
)

func presetCRC(name string) func() gohash.Hash {
	p, ok := CRCPreset(name)
	if !ok {
		panic("checksum: no CRC preset " + name)
	}
	return func() gohash.Hash { return NewCRC(p) }
}

var algorithms = []Algorithm{
	{"crc8", "CRC-8", presetCRC("CRC-8")},
	{"crc16-arc", "CRC-16/ARC", presetCRC("CRC-16/ARC")},
	{"crc16-modbus", "CRC-16/MODBUS", presetCRC("CRC-16/MODBUS")},
	{"crc16-xmodem", "CRC-16/XMODEM", presetCRC("CRC-16/XMODEM")},
	{"crc16-ccitt-false", "CRC-16/CCITT-FALSE", presetCRC("CRC-16/CCITT-FALSE")},
	{"crc32", "CRC-32", func() gohash.Hash { return crc32.NewIEEE() }},
	{"crc32c", "CRC-32C", func() gohash.Hash { return crc32.New(crc32cTable) }},
	{"crc64-ecma", "CRC-64/ECMA-182", presetCRC("CRC-64/ECMA-182")},
	{"crc64-xz", "CRC-64/XZ", func() gohash.Hash { return crc64.New(crc64ECMA) }},
	{"crc64-iso", "CRC-64/GO-ISO", func() gohash.Hash { return crc64.New(crc64ISO) }},
	{"adler32", "Adler-32", func() gohash.Hash { return adler32.New() }},
	{"xor8", "XOR-8", newSimple(true, 1, 0)},
	{"sum8", "Sum-8", newSimple(false, 1, 0)},
	{"sum16", "Sum-16", newSimple(false, 2, 0)},
	{"sum32", "Sum-32", newSimple(false, 4, 0)},
	{"md5", "MD5", md5.New},
	{"sha1", "SHA-1", sha1.New},
	{"sha224", "SHA-224", sha256.New224},
	{"sha256", "SHA-256", sha256.New},
	{"sha384", "SHA-384", sha512.New384},
	{"sha512", "SHA-512", sha512.New},
	{"sha512-256", "SHA-512/256", sha512.New512_256},
	{"sha3-224", "SHA3-224", func() gohash.Hash { return sha3.New224() }},
	{"sha3-256", "SHA3-256", func() gohash.Hash { return sha3.New256() }},
	{"sha3-384", "SHA3-384", func() gohash.Hash { return sha3.New384() }},
	{"sha3-512", "SHA3-512", func() gohash.Hash { return sha3.New512() }},
	{"blake3", "BLAKE3", func() gohash.Hash { return NewBlake3() }},
}

// Algorithms returns all the algorithms, in the order to show them
func Algorithms() []Algorithm {
	return append([]Algorithm(nil), algorithms...)
}

// ByID finds an algorithm by its ID
func ByID(id string) (Algorithm, bool) {
	for _, a := range algorithms {
		if a.ID == id {
			return a, true
		}
	}
	return Algorithm{}, false
}

// CRCAlgorithm makes an algorithm of a CRC with any parameters (a preset
// or the user's own), to pass to Compute with the others
func CRCAlgorithm(p CRCParams) Algorithm {
	name := p.Name
	if name == "" {
		name = "CRC"
	}
	return Algorithm{ID: "crc:" + name, Name: name, New: func() gohash.Hash { return NewCRC(p) }}
}
