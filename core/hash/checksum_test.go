package checksum

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"hash/crc64"
	"io"
	"math"
	"math/rand/v2"
	"sync/atomic"
	"testing"
)

// The digests of "", "abc" and "123456789" ("" - not checked). The hashes
// are those of the standards (FIPS 180-4, FIPS 202, RFC 1321); the CRCs of
// "123456789" are the check values of the CRC catalogue.
var known = map[string][3]string{
	"crc8":              {"00", "", "f4"},
	"crc16-arc":         {"0000", "", "bb3d"},
	"crc16-modbus":      {"ffff", "", "4b37"},
	"crc16-xmodem":      {"0000", "", "31c3"},
	"crc16-ccitt-false": {"ffff", "", "29b1"},
	"crc32":             {"00000000", "352441c2", "cbf43926"},
	"crc32c":            {"00000000", "", "e3069283"},
	"crc64-ecma":        {"0000000000000000", "", "6c40df5f0b497347"},
	"crc64-xz":          {"0000000000000000", "", "995dc9bbdf1939fa"},
	"crc64-iso":         {"0000000000000000", "", "b90956c775a41001"},
	"adler32":           {"00000001", "024d0127", "091e01de"},
	"xor8":              {"00", "60", "31"},
	"sum8":              {"00", "26", "dd"},
	"sum16":             {"0000", "0126", "01dd"},
	"sum32":             {"00000000", "00000126", "000001dd"},
	"md5":               {"d41d8cd98f00b204e9800998ecf8427e", "900150983cd24fb0d6963f7d28e17f72", "25f9e794323b453885f5181f1b624d0b"},
	"sha1":              {"da39a3ee5e6b4b0d3255bfef95601890afd80709", "a9993e364706816aba3e25717850c26c9cd0d89d", "f7c3bc1d808e04732adf679965ccc34ca7ae3441"},
	"sha224": {"d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f",
		"23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7",
		"9b3e61bf29f17c75572fae2e86e17809a4513d07c8a18152acf34521"},
	"sha256": {"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"15e2b0d3c33891ebb0f1ef609ec419420c20e320ce94c65fbc8c3312448eb225"},
	"sha384": {"38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da274edebfe76f65fbd51ad2f14898b95b",
		"cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7",
		"eb455d56d2c1a69de64e832011f3393d45f3fa31d6842f21af92d2fe469c499da5e3179847334a18479c8d1dedea1be3"},
	"sha512": {"cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
		"ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f",
		"d9e6762dd1c8eaf6d61b3c6192fc408d4d6d5f1176d0c29169bc24e71c3f274ad27fcd5811b313d681f7e55ec02d73d499c95455b6b5bb503acf574fba8ffe85"},
	"sha512-256": {"c672b8d1ef56ed28ab87c3622c5114069bdd3ad7b8f9737498d0c01ecef0967a",
		"53048e2681941ef99b2e29b76b4c7dabe4c2d0c634fc6d46e0e2f13107e7af23",
		"1877345237853a31ad79e14c1fcb0ddcd3df9973b61af7f906e4b4d052cc9416"},
	"sha3-224": {"6b4e03423667dbb73b6e15454f0eb1abd4597f9a1b078e3f5b5a6bc7",
		"e642824c3f8cf24ad09234ee7d3c766fc9a3a5168d0c94ad73b46fdf",
		"5795c3d628fd638c9835a4c79a55809f265068c88729a1a3fcdf8522"},
	"sha3-256": {"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a",
		"3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532",
		"87cd084d190e436f147322b90e7384f6a8e0676c99d21ef519ea718e51d45f9c"},
	"sha3-384": {"0c63a75b845e4f7d01107d852e4c2485c51a50aaaa94fc61995e71bbee983a2ac3713831264adb47fb6bd1e058d5f004",
		"ec01498288516fc926459f58e2c6ad8df9b473cb0fc08c2596da7cf0e49be4b298d88cea927ac7f539f1edf228376d25",
		"8b90ede4d095409f1a12492c2520599683a9478dc70b7566d23b3e41ece8538c6cde92382a5e38786490375c54672abf"},
	"sha3-512": {"a69f73cca23a9ac5c8b567dc185a756e97c982164fe25859e0d1dcc1475c80a615b2123af1f5f94c11e3e9402c3ac558f500199d95b6d3e301758586281dcd26",
		"b751850b1a57168a5693cd924b6b096e08f621827444f70d884f5d0240d2712e10e116e9192af3c91a7ec57647e3934057340b4cf408d5a56592f8274eec53f0",
		"e1e44d20556e97a180b6dd3ed7ae5c465cafd553fa8747dca038fb95635b77a37318f7ddf7aec1f6c3c14bb160ba2497007decf38dd361cab199e3b8c8fe1f5c"},
	"blake3": {"af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262",
		"6437b3ac38465133ffb63b75273a8db548c558465d79db03fd359c6cd5bd9d85", ""},
}

func TestKnownDigests(t *testing.T) {
	inputs := []string{"", "abc", "123456789"}
	seen := map[string]bool{}
	for _, a := range Algorithms() {
		if seen[a.ID] {
			t.Errorf("%s: the ID is twice", a.ID)
		}
		seen[a.ID] = true
		if b, ok := ByID(a.ID); !ok || b.Name != a.Name {
			t.Errorf("ByID(%s) = %v %v", a.ID, b.Name, ok)
		}
		want, ok := known[a.ID]
		if !ok {
			t.Errorf("%s: no known digests", a.ID)
			continue
		}
		for i, in := range inputs {
			if want[i] == "" {
				continue
			}
			h := a.New()
			h.Write([]byte(in))
			if got := hex.EncodeToString(h.Sum(nil)); got != want[i] {
				t.Errorf("%s(%q) = %s, want %s", a.ID, in, got, want[i])
			}
			if h.Size() != len(want[i])/2 {
				t.Errorf("%s: Size %d", a.ID, h.Size())
			}
		}
	}
	if _, ok := ByID("nope"); ok {
		t.Error("ByID found an unknown ID")
	}
}

func TestCRCPresets(t *testing.T) {
	// Presets and a few more of the catalogue for odd widths and for
	// RefIn != RefOut
	ps := append(CRCPresets(),
		CRCParams{Name: "CRC-3/GSM", Width: 3, Poly: 0x3, XorOut: 0x7, Check: 0x4},
		CRCParams{Name: "CRC-5/USB", Width: 5, Poly: 0x05, Init: 0x1F, RefIn: true, RefOut: true, XorOut: 0x1F, Check: 0x19},
		CRCParams{Name: "CRC-12/UMTS", Width: 12, Poly: 0x80F, RefOut: true, Check: 0xDAF},
		CRCParams{Name: "CRC-40/GSM", Width: 40, Poly: 0x0004820009, XorOut: 0xFFFFFFFFFF, Check: 0xD4164FC646},
		CRCParams{Name: "width 0 is 64", Width: 0, Poly: 0x42F0E1EBA9EA3693, Check: 0x6C40DF5F0B497347},
	)
	for _, p := range ps {
		h := NewCRC(p)
		h.Write([]byte("1234"))
		h.Write([]byte("56789"))
		if got := h.(interface{ Sum64() uint64 }).Sum64(); got != p.Check {
			t.Errorf("%s: %x, want %x", p.Name, got, p.Check)
		}
		w := p.Width
		if w == 0 {
			w = 64
		}
		sum := h.Sum([]byte{0xAA})
		if len(sum) != 1+(w+7)/8 || sum[0] != 0xAA {
			t.Errorf("%s: Sum %x", p.Name, sum)
		}
		var v uint64
		for _, b := range sum[1:] {
			v = v<<8 | uint64(b)
		}
		if v != p.Check {
			t.Errorf("%s: Sum %x", p.Name, sum)
		}
		h.Reset()
		h.Write([]byte("123456789"))
		if got := h.(interface{ Sum64() uint64 }).Sum64(); got != p.Check {
			t.Errorf("%s after Reset: %x", p.Name, got)
		}
	}
	if _, ok := CRCPreset("CRC-16/MODBUS"); !ok {
		t.Error("no CRC-16/MODBUS")
	}
}

// The table code of NewCRC agrees with the standard library on long data
// of all the lengths around the 8 byte steps
func TestCRCMatchesStdlib(t *testing.T) {
	data := randBytes(1, 3000)
	p32, _ := CRCPreset("CRC-32")
	p64, _ := CRCPreset("CRC-64/XZ")
	pISO, _ := CRCPreset("CRC-64/GO-ISO")
	tISO := crc64.MakeTable(crc64.ISO)
	tECMA := crc64.MakeTable(crc64.ECMA)
	for n := 0; n < 40; n++ {
		for _, d := range [][]byte{data[:n], data[n : 2000+n]} {
			if got, want := sum64(NewCRC(p32), d), uint64(crc32.ChecksumIEEE(d)); got != want {
				t.Fatalf("crc32 len %d: %x, want %x", len(d), got, want)
			}
			if got, want := sum64(NewCRC(p64), d), crc64.Checksum(d, tECMA); got != want {
				t.Fatalf("crc64 len %d: %x, want %x", len(d), got, want)
			}
			if got, want := sum64(NewCRC(pISO), d), crc64.Checksum(d, tISO); got != want {
				t.Fatalf("crc64 iso len %d: %x, want %x", len(d), got, want)
			}
		}
	}
	// Non-reflected: compare the 8 byte steps with byte by byte
	pB, _ := CRCPreset("CRC-32/BZIP2")
	for n := 0; n < 40; n++ {
		d := data[n : 1000+n]
		h := NewCRC(pB)
		for i := range d {
			h.Write(d[i : i+1])
		}
		if got, want := sum64(NewCRC(pB), d), h.(interface{ Sum64() uint64 }).Sum64(); got != want {
			t.Fatalf("bzip2 len %d: %x, want %x", len(d), got, want)
		}
	}
}

func sum64(h interface{ Write([]byte) (int, error) }, d []byte) uint64 {
	h.Write(d)
	return h.(interface{ Sum64() uint64 }).Sum64()
}

// From the official test_vectors.json: the input is i%251 for i < len,
// default hash mode, the first 32 bytes of output
var blake3Vectors = []struct {
	n    int
	hash string
}{
	{0, "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262"},
	{1, "2d3adedff11b61f14c886e35afa036736dcd87a74d27b5c1510225d0f592e213"},
	{2, "7b7015bb92cf0b318037702a6cdd81dee41224f734684c2c122cd6359cb1ee63"},
	{3, "e1be4d7a8ab5560aa4199eea339849ba8e293d55ca0a81006726d184519e647f"},
	{8, "2351207d04fc16ade43ccab08600939c7c1fa70a5c0aaca76063d04c3228eaeb"},
	{63, "e9bc37a594daad83be9470df7f7b3798297c3d834ce80ba85d6e207627b7db7b"},
	{64, "4eed7141ea4a5cd4b788606bd23f46e212af9cacebacdc7d1f4c6dc7f2511b98"},
	{65, "de1e5fa0be70df6d2be8fffd0e99ceaa8eb6e8c93a63f2d8d1c30ecb6b263dee"},
	{127, "d81293fda863f008c09e92fc382a81f5a0b4a1251cba1634016a0f86a6bd640d"},
	{128, "f17e570564b26578c33bb7f44643f539624b05df1a76c81f30acd548c44b45ef"},
	{129, "683aaae9f3c5ba37eaaf072aed0f9e30bac0865137bae68b1fde4ca2aebdcb12"},
	{1023, "10108970eeda3eb932baac1428c7a2163b0e924c9a9e25b35bba72b28f70bd11"},
	{1024, "42214739f095a406f3fc83deb889744ac00df831c10daa55189b5d121c855af7"},
	{1025, "d00278ae47eb27b34faecf67b4fe263f82d5412916c1ffd97c8cb7fb814b8444"},
	{2048, "e776b6028c7cd22a4d0ba182a8bf62205d2ef576467e838ed6f2529b85fba24a"},
	{2049, "5f4d72f40d7a5f82b15ca2b2e44b1de3c2ef86c426c95c1af0b6879522563030"},
	{3072, "b98cb0ff3623be03326b373de6b9095218513e64f1ee2edd2525c7ad1e5cffd2"},
	{3073, "7124b49501012f81cc7f11ca069ec9226cecb8a2c850cfe644e327d22d3e1cd3"},
	{4096, "015094013f57a5277b59d8475c0501042c0b642e531b0a1c8f58d2163229e969"},
	{4097, "9b4052b38f1c5fc8b1f9ff7ac7b27cd242487b3d890d15c96a1c25b8aa0fb995"},
	{5120, "9cadc15fed8b5d854562b26a9536d9707cadeda9b143978f319ab34230535833"},
	{5121, "628bd2cb2004694adaab7bbd778a25df25c47b9d4155a55f8fbd79f2fe154cff"},
	{6144, "3e2e5b74e048f3add6d21faab3f83aa44d3b2278afb83b80b3c35164ebeca205"},
	{6145, "f1323a8631446cc50536a9f705ee5cb619424d46887f3c376c695b70e0f0507f"},
	{7168, "61da957ec2499a95d6b8023e2b0e604ec7f6b50e80a9678b89d2628e99ada77a"},
	{7169, "a003fc7a51754a9b3c7fae0367ab3d782dccf28855a03d435f8cfe74605e7817"},
	{8192, "aae792484c8efe4f19e2ca7d371d8c467ffb10748d8a5a1ae579948f718a2a63"},
	{8193, "bab6c09cb8ce8cf459261398d2e7aef35700bf488116ceb94a36d0f5f1b7bc3b"},
	{16384, "f875d6646de28985646f34ee13be9a576fd515f76b5b0a26bb324735041ddde4"},
	{31744, "62b6960e1a44bcc1eb1a611a8d6235b6b4b78f32e7abc4fb4c6cdcce94895c47"},
	{102400, "bc3e3d41a1146b069abffad3c0d44860cf664390afce4d9661f7902e7943e085"},
}

func blake3Input(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

func TestBlake3Vectors(t *testing.T) {
	for _, v := range blake3Vectors {
		in := blake3Input(v.n)
		h := NewBlake3()
		h.Write(in)
		if got := hex.EncodeToString(h.Sum(nil)); got != v.hash {
			t.Errorf("len %d: %s, want %s", v.n, got, v.hash)
		}
		// Odd write sizes, and Sum in the middle does not change the state
		for _, step := range []int{1, 7, 63, 65, 1000, 1025, 4097} {
			h.Reset()
			for p := 0; p < len(in); p += step {
				h.Write(in[p:min(p+step, len(in))])
				if p == 0 {
					h.Sum(nil)
				}
			}
			if got := hex.EncodeToString(h.Sum(nil)); got != v.hash {
				t.Errorf("len %d by %d: %s, want %s", v.n, step, got, v.hash)
			}
		}
	}
}

// The extended output, the whole 131 bytes of the vectors
func TestBlake3XOF(t *testing.T) {
	for _, v := range []struct {
		n    int
		hash string
	}{
		{0, "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262e00f03e7b69af26b7faaf09fcd333050338ddfe085b8cc869ca98b206c08243a26f5487789e8f660afe6c99ef9e0c52b92e7393024a80459cf91f476f9ffdbda7001c22e159b402631f277ca96f2defdf1078282314e763699a31c5363165421cce14d"},
		{102400, "bc3e3d41a1146b069abffad3c0d44860cf664390afce4d9661f7902e7943e085e01c59dab908c04c3342b816941a26d69c2605ebee5ec5291cc55e15b76146e6745f0601156c3596cb75065a9c57f35585a52e1ac70f69131c23d611ce11ee4ab1ec2c009012d236648e77be9295dd0426f29b764d65de58eb7d01dd42248204f45f8e"},
	} {
		h := NewBlake3()
		h.Write(blake3Input(v.n))
		out := make([]byte, len(v.hash)/2)
		h.XOF(out)
		if got := hex.EncodeToString(out); got != v.hash {
			t.Errorf("len %d: %s", v.n, got)
		}
	}
}

func randBytes(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 0))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func TestComputeEqualsOneShot(t *testing.T) {
	data := randBytes(2, 3<<20+12345)
	r := bytes.NewReader(data)
	algs := append(Algorithms(), CRCAlgorithm(CRCParams{Name: "CRC-5/USB", Width: 5, Poly: 5, Init: 0x1F, RefIn: true, RefOut: true, XorOut: 0x1F}))
	for _, c := range []struct{ off, n int64 }{
		{0, 0}, {7, 1}, {0, int64(len(data))}, {3, 1<<20 + 1}, {12345, 2<<20 + 777}, {1, int64(len(data)) - 1},
	} {
		var last atomic.Int64
		calls := 0
		sums, err := Compute(context.Background(), r, c.off, c.n, algs, func(done int64) {
			calls++
			if done <= last.Load() || done > c.n {
				t.Errorf("progress %d after %d of %d", done, last.Load(), c.n)
			}
			last.Store(done)
		})
		if err != nil {
			t.Fatal(err)
		}
		if c.n > 0 && last.Load() != c.n {
			t.Errorf("off %d n %d: progress ended at %d", c.off, c.n, last.Load())
		}
		for i, a := range algs {
			h := a.New()
			h.Write(data[c.off : c.off+c.n])
			if !bytes.Equal(sums[i], h.Sum(nil)) {
				t.Errorf("off %d n %d %s: %x, want %x", c.off, c.n, a.ID, sums[i], h.Sum(nil))
			}
		}
	}
}

func TestComputeErrors(t *testing.T) {
	data := randBytes(3, 1000)
	r := bytes.NewReader(data)
	algs := []Algorithm{algorithms[0]}
	if _, err := Compute(context.Background(), r, 10, 1000, algs, nil); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("past the end: %v", err)
	}
	if _, err := Compute(context.Background(), r, -1, 10, algs, nil); err != ErrRange {
		t.Errorf("negative: %v", err)
	}
}

// endlessReader gives zeros at any offset, for cancel
type endlessReader struct{ reads atomic.Int64 }

func (s *endlessReader) ReadAt(b []byte, off int64) (int, error) {
	s.reads.Add(1)
	clear(b)
	return len(b), nil
}

func TestComputeCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sr := &endlessReader{}
	sha, _ := ByID("sha256")
	_, err := Compute(ctx, sr, 0, 1<<50, []Algorithm{sha}, func(done int64) {
		if done >= 8<<20 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if n := sr.reads.Load(); n > 20 {
		t.Errorf("%d reads after cancel", n)
	}
	if _, err := Analyze(ctx, sr, 0, 1<<50, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Analyze: %v", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	_, err = Analyze(ctx2, sr, 0, 1<<50, func(done int64) {
		if done >= 8<<20 {
			cancel2()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Analyze: %v", err)
	}
	if _, err := EntropyBlocks(ctx, sr, 0, 1<<50, 10, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("EntropyBlocks: %v", err)
	}
}

func TestAnalyze(t *testing.T) {
	same := bytes.Repeat([]byte{0x5A}, 3<<20+5)
	s, err := Analyze(context.Background(), bytes.NewReader(same), 0, int64(len(same)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count != int64(len(same)) || s.Histogram[0x5A] != s.Count || s.Entropy() != 0 {
		t.Errorf("all equal: count %d, entropy %v", s.Count, s.Entropy())
	}

	uni := make([]byte, 256*10000)
	for i := range uni {
		uni[i] = byte(i)
	}
	s, err = Analyze(context.Background(), bytes.NewReader(uni), 0, int64(len(uni)), nil)
	if err != nil {
		t.Fatal(err)
	}
	for v, c := range s.Histogram {
		if c != 10000 {
			t.Fatalf("value %d: %d", v, c)
		}
	}
	if e := s.Entropy(); math.Abs(e-8) > 1e-9 {
		t.Errorf("uniform: entropy %v", e)
	}

	// Odd range of random data against counting by hand
	data := randBytes(4, 2<<20+99)
	off, n := int64(13), int64(2<<20+50)
	var last int64
	s, err = Analyze(context.Background(), bytes.NewReader(data), off, n, func(d int64) { last = d })
	if err != nil || last != n {
		t.Fatalf("err %v, progress %d", err, last)
	}
	var want Stats
	for _, b := range data[off : off+n] {
		want.Histogram[b]++
	}
	want.Count = n
	if s != want {
		t.Error("histogram differs")
	}
	if e := s.Entropy(); e < 7.99 || e > 8 {
		t.Errorf("random: entropy %v", e)
	}
	var empty Stats
	if empty.Entropy() != 0 {
		t.Error("empty entropy")
	}
}

func TestEntropyBlocks(t *testing.T) {
	// Half zeros, half random
	data := append(make([]byte, 1<<20), randBytes(5, 1<<20)...)
	r := bytes.NewReader(data)
	for _, sample := range []int64{0, 64 << 10, 5000, 1} {
		es, err := EntropyBlocks(context.Background(), r, 0, int64(len(data)), 8, sample)
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range es {
			if i < 4 && e != 0 {
				t.Errorf("sample %d block %d: %v", sample, i, e)
			}
			if i >= 4 && sample != 1 && e < 7 {
				t.Errorf("sample %d block %d: %v", sample, i, e)
			}
		}
	}
	// Uneven blocks over an odd range, against Analyze of each block
	off, n := int64(3), int64(1<<20+1001)
	es, err := EntropyBlocks(context.Background(), r, off, n, 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		a, b := n*int64(i)/7, n*int64(i+1)/7
		s, _ := Analyze(context.Background(), r, off+a, b-a, nil)
		if math.Abs(s.Entropy()-es[i]) > 1e-12 {
			t.Errorf("block %d: %v, want %v", i, es[i], s.Entropy())
		}
	}
	// More blocks than bytes
	es, err = EntropyBlocks(context.Background(), r, 0, 3, 10, 0)
	if err != nil || len(es) != 10 {
		t.Errorf("tiny: %v %v", es, err)
	}
}

func benchHash(b *testing.B, id string) {
	a, _ := ByID(id)
	data := randBytes(6, 1<<20)
	h := a.New()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		h.Write(data)
	}
}

func BenchmarkCRC32(b *testing.B)        { benchHash(b, "crc32") }
func BenchmarkCRC64ECMA182(b *testing.B) { benchHash(b, "crc64-ecma") }
func BenchmarkBlake3(b *testing.B)       { benchHash(b, "blake3") }
func BenchmarkSHA256(b *testing.B)       { benchHash(b, "sha256") }

func BenchmarkComputeSHA256CRC32(b *testing.B) {
	data := randBytes(7, 64<<20)
	r := bytes.NewReader(data)
	sha, _ := ByID("sha256")
	crc, _ := ByID("crc32")
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := Compute(context.Background(), r, 0, int64(len(data)), []Algorithm{sha, crc}, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAnalyze(b *testing.B) {
	data := randBytes(8, 64<<20)
	r := bytes.NewReader(data)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := Analyze(context.Background(), r, 0, int64(len(data)), nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSimpleSums(t *testing.T) {
	data := []byte{0x01, 0x02, 0xFF, 0x10}
	for id, want := range map[string]string{"xor8": "ec", "sum8": "12", "sum16": "0112", "sum32": "00000112"} {
		a, _ := ByID(id)
		h := a.New()
		h.Write(data)
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
}
