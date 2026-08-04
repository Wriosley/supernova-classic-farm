package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"math/bits"
)

// This is a small, self-contained Argon2id implementation for the local
// prototype. Keeping it here avoids changing the shared go.mod owned by the
// infrastructure task. The encoded parameters are retained with each account.
const (
	argonMemoryKiB = uint32(19 * 1024)
	argonTime      = uint32(2)
	argonThreads   = uint32(1)
	argonKeyLen    = uint32(32)
)

type passwordHash struct {
	Salt       []byte
	Digest     []byte
	MemoryKiB  uint32
	Iterations uint32
	Threads    uint32
	Version    uint32
}

func hashPassword(password string) (passwordHash, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return passwordHash{}, err
	}
	digest := argon2idKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return passwordHash{
		Salt: salt, Digest: digest, MemoryKiB: argonMemoryKiB,
		Iterations: argonTime, Threads: argonThreads, Version: 0x13,
	}, nil
}

func verifyPassword(password string, encoded passwordHash) bool {
	if encoded.Version != 0x13 || encoded.Threads == 0 || encoded.MemoryKiB < 8*encoded.Threads ||
		encoded.Iterations == 0 || len(encoded.Salt) < 16 || len(encoded.Digest) < 32 {
		return false
	}
	actual := argon2idKey([]byte(password), encoded.Salt, encoded.Iterations, encoded.MemoryKiB, encoded.Threads, uint32(len(encoded.Digest)))
	return subtle.ConstantTimeCompare(actual, encoded.Digest) == 1
}

type argonBlock [128]uint64

func argon2idKey(password, salt []byte, timeCost, memory uint32, threads uint32, keyLen uint32) []byte {
	if threads == 0 || timeCost == 0 || keyLen == 0 {
		panic("invalid Argon2id parameters")
	}
	memory = memory / (4 * threads) * (4 * threads)
	if memory < 8*threads {
		memory = 8 * threads
	}
	laneLen := memory / threads
	segmentLen := laneLen / 4

	h0 := make([]byte, 0, 128+len(password)+len(salt))
	h0 = appendU32(h0, threads, keyLen, memory, timeCost, 0x13, 2)
	h0 = appendBytes(h0, password)
	h0 = appendBytes(h0, salt)
	h0 = appendU32(h0, 0, 0) // secret and associated data
	initial := blake2bSum(h0, 64)

	blocks := make([]argonBlock, memory)
	for lane := uint32(0); lane < threads; lane++ {
		for i := uint32(0); i < 2; i++ {
			seed := append(append([]byte{}, initial...), byte(i), 0, 0, 0, byte(lane), byte(lane>>8), byte(lane>>16), byte(lane>>24))
			blockBytes := argonHPrime(seed, 1024)
			for word := range blocks[lane*laneLen+i] {
				blocks[lane*laneLen+i][word] = binary.LittleEndian.Uint64(blockBytes[word*8:])
			}
		}
	}

	for pass := uint32(0); pass < timeCost; pass++ {
		for slice := uint32(0); slice < 4; slice++ {
			for lane := uint32(0); lane < threads; lane++ {
				fillSegment(blocks, pass, slice, lane, memory, timeCost, threads, laneLen, segmentLen)
			}
		}
	}

	final := blocks[laneLen-1]
	for lane := uint32(1); lane < threads; lane++ {
		for i := range final {
			final[i] ^= blocks[lane*laneLen+laneLen-1][i]
		}
	}
	finalBytes := make([]byte, 1024)
	for i, word := range final {
		binary.LittleEndian.PutUint64(finalBytes[i*8:], word)
	}
	return argonHPrime(finalBytes, keyLen)
}

func fillSegment(memory []argonBlock, pass, slice, lane, memoryBlocks, timeCost, threads, laneLen, segmentLen uint32) {
	dataIndependent := pass == 0 && slice < 2
	var address, input, zero argonBlock
	input[0], input[1], input[2] = uint64(pass), uint64(lane), uint64(slice)
	input[3], input[4], input[5] = uint64(memoryBlocks), uint64(timeCost), 2

	start := uint32(0)
	if pass == 0 && slice == 0 {
		start = 2
		if dataIndependent {
			nextAddresses(&address, &input, &zero)
		}
	}
	for index := start; index < segmentLen; index++ {
		current := lane*laneLen + slice*segmentLen + index
		previous := current - 1
		if current%laneLen == 0 {
			previous += laneLen
		}

		var pseudo uint64
		if dataIndependent {
			if index%128 == 0 {
				nextAddresses(&address, &input, &zero)
			}
			pseudo = address[index%128]
		} else {
			pseudo = memory[previous][0]
		}
		refLane := uint32(pseudo>>32) % threads
		if pass == 0 && slice == 0 {
			refLane = lane
		}
		refIndex := argonReferenceIndex(pass, slice, index, pseudo, refLane == lane, laneLen, segmentLen)
		reference := refLane*laneLen + refIndex
		fillBlock(&memory[previous], &memory[reference], &memory[current], pass != 0)
	}
}

func nextAddresses(address, input, zero *argonBlock) {
	input[6]++
	var tmp argonBlock
	fillBlock(zero, input, &tmp, false)
	fillBlock(zero, &tmp, address, false)
}

func argonReferenceIndex(pass, slice, index uint32, pseudo uint64, sameLane bool, laneLen, segmentLen uint32) uint32 {
	var area, start uint32
	if pass == 0 {
		if slice == 0 {
			area = index - 1
		} else if sameLane {
			area = slice*segmentLen + index - 1
		} else {
			area = slice * segmentLen
			if index == 0 {
				area--
			}
		}
	} else {
		if sameLane {
			area = laneLen - segmentLen + index - 1
		} else {
			area = laneLen - segmentLen
			if index == 0 {
				area--
			}
		}
		start = ((slice + 1) * segmentLen) % laneLen
	}
	relative := uint64(uint32(pseudo))
	relative = relative * relative >> 32
	relative = uint64(area-1) - (uint64(area) * relative >> 32)
	return (start + uint32(relative)) % laneLen
}

func fillBlock(previous, reference, destination *argonBlock, xorOld bool) {
	var r, z argonBlock
	for i := range r {
		r[i] = previous[i] ^ reference[i]
		z[i] = r[i]
		if xorOld {
			r[i] ^= destination[i]
		}
	}
	for i := 0; i < 8; i++ {
		argonRound(z[i*16 : i*16+16])
	}
	for i := 0; i < 8; i++ {
		var column [16]uint64
		for j := 0; j < 8; j++ {
			column[2*j] = z[16*j+2*i]
			column[2*j+1] = z[16*j+2*i+1]
		}
		argonRound(column[:])
		for j := 0; j < 8; j++ {
			z[16*j+2*i] = column[2*j]
			z[16*j+2*i+1] = column[2*j+1]
		}
	}
	for i := range destination {
		destination[i] = r[i] ^ z[i]
	}
}

func argonRound(v []uint64) {
	argonG(v, 0, 4, 8, 12)
	argonG(v, 1, 5, 9, 13)
	argonG(v, 2, 6, 10, 14)
	argonG(v, 3, 7, 11, 15)
	argonG(v, 0, 5, 10, 15)
	argonG(v, 1, 6, 11, 12)
	argonG(v, 2, 7, 8, 13)
	argonG(v, 3, 4, 9, 14)
}

func argonG(v []uint64, a, b, c, d int) {
	v[a] = blamka(v[a], v[b])
	v[d] = bits.RotateLeft64(v[d]^v[a], -32)
	v[c] = blamka(v[c], v[d])
	v[b] = bits.RotateLeft64(v[b]^v[c], -24)
	v[a] = blamka(v[a], v[b])
	v[d] = bits.RotateLeft64(v[d]^v[a], -16)
	v[c] = blamka(v[c], v[d])
	v[b] = bits.RotateLeft64(v[b]^v[c], -63)
}

func blamka(x, y uint64) uint64 { return x + y + 2*uint64(uint32(x))*uint64(uint32(y)) }

func argonHPrime(input []byte, outLen uint32) []byte {
	prefix := appendU32(nil, outLen)
	prefix = append(prefix, input...)
	if outLen <= 64 {
		return blake2bSum(prefix, int(outLen))
	}
	out := make([]byte, 0, outLen)
	value := blake2bSum(prefix, 64)
	out = append(out, value[:32]...)
	remaining := int(outLen) - 32
	for remaining > 64 {
		value = blake2bSum(value, 64)
		out = append(out, value[:32]...)
		remaining -= 32
	}
	return append(out, blake2bSum(value, remaining)...)
}

var blakeIV = [8]uint64{
	0x6a09e667f3bcc908, 0xbb67ae8584caa73b, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
	0x510e527fade682d1, 0x9b05688c2b3e6c1f, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
}

var blakeSigma = [12][16]uint8{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
	{11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4},
	{7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8},
	{9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13},
	{2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9},
	{12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11},
	{13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10},
	{6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5},
	{10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0},
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
}

func blake2bSum(message []byte, outLen int) []byte {
	if outLen < 1 || outLen > 64 {
		panic(errors.New("invalid BLAKE2b output length"))
	}
	h := blakeIV
	h[0] ^= 0x01010000 ^ uint64(outLen)
	var counter uint64
	for len(message) > 128 {
		counter += 128
		blakeCompress(&h, message[:128], counter, false)
		message = message[128:]
	}
	var final [128]byte
	copy(final[:], message)
	counter += uint64(len(message))
	blakeCompress(&h, final[:], counter, true)
	out := make([]byte, outLen)
	var full [64]byte
	for i := range h {
		binary.LittleEndian.PutUint64(full[i*8:], h[i])
	}
	copy(out, full[:outLen])
	return out
}

func blakeCompress(h *[8]uint64, block []byte, counter uint64, last bool) {
	var m [16]uint64
	for i := range m {
		m[i] = binary.LittleEndian.Uint64(block[i*8:])
	}
	var v [16]uint64
	copy(v[:8], h[:])
	copy(v[8:], blakeIV[:])
	v[12] ^= counter
	if last {
		v[14] = ^v[14]
	}
	for round := 0; round < 12; round++ {
		s := blakeSigma[round]
		blakeG(&v, 0, 4, 8, 12, m[s[0]], m[s[1]])
		blakeG(&v, 1, 5, 9, 13, m[s[2]], m[s[3]])
		blakeG(&v, 2, 6, 10, 14, m[s[4]], m[s[5]])
		blakeG(&v, 3, 7, 11, 15, m[s[6]], m[s[7]])
		blakeG(&v, 0, 5, 10, 15, m[s[8]], m[s[9]])
		blakeG(&v, 1, 6, 11, 12, m[s[10]], m[s[11]])
		blakeG(&v, 2, 7, 8, 13, m[s[12]], m[s[13]])
		blakeG(&v, 3, 4, 9, 14, m[s[14]], m[s[15]])
	}
	for i := range h {
		h[i] ^= v[i] ^ v[i+8]
	}
}

func blakeG(v *[16]uint64, a, b, c, d int, x, y uint64) {
	v[a] += v[b] + x
	v[d] = bits.RotateLeft64(v[d]^v[a], -32)
	v[c] += v[d]
	v[b] = bits.RotateLeft64(v[b]^v[c], -24)
	v[a] += v[b] + y
	v[d] = bits.RotateLeft64(v[d]^v[a], -16)
	v[c] += v[d]
	v[b] = bits.RotateLeft64(v[b]^v[c], -63)
}

func appendU32(dst []byte, values ...uint32) []byte {
	for _, value := range values {
		dst = append(dst, byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
	}
	return dst
}

func appendBytes(dst, value []byte) []byte {
	dst = appendU32(dst, uint32(len(value)))
	return append(dst, value...)
}
