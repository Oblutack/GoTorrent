package mse

import (
	"encoding/hex"
	"testing"
)

// testS and testSKEY back every known-answer test below. testS is byte i
// = i for i in [0,96) (a fixed, deterministic 96-byte value); testSKEY is
// 20 bytes of 0xAA.
func testS() []byte {
	s := make([]byte, 96)
	for i := range s {
		s[i] = byte(i)
	}
	return s
}

func testSKEY() []byte {
	skey := make([]byte, 20)
	for i := range skey {
		skey[i] = 0xAA
	}
	return skey
}

// These expected values were computed independently of this package —
// via .NET's System.Security.Cryptography.SHA1 in a throwaway PowerShell
// script, not by calling hash20 or sha1.Sum from Go at all — specifically
// so this test can catch a real bug (e.g. concatenating S+SKEY instead of
// SKEY+S, or the wrong label) rather than just mirroring whatever
// production code happens to compute, which a hardcoded sha1.Sum call in
// the test itself could never do.
func TestKnownAnswerHashes(t *testing.T) {
	s, skey := testS(), testSKEY()

	tests := []struct {
		name string
		got  []byte
		want string
	}{
		{"req1", req1(s), "ed37476cccf63496894d66db14b9806c8c9efe3e"},
		{"req2", req2(skey), "c9b0599dfa950cfbde978efc6aa0588f510fa315"},
		{"req3", req3(s), "4986fc7b9cb37334e5ab5c3034812fdbc7cd8573"},
		{"keyA", keyA(s, skey), "811e2f709105c70039156d2268cc6908c2a6faba"},
		{"keyB", keyB(s, skey), "4573f8ad56b1b961da3ff7e156a8bb0389b52fba"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := hex.DecodeString(tc.want)
			if err != nil {
				t.Fatalf("bad test literal: %v", err)
			}
			if hex.EncodeToString(tc.got) != tc.want {
				t.Fatalf("%s = %x, want %x", tc.name, tc.got, want)
			}
		})
	}
}

func TestReq2Xor3IsSelfConsistent(t *testing.T) {
	s, skey := testS(), testSKEY()
	got := req2Xor3(skey, s)
	if len(got) != 20 {
		t.Fatalf("len = %d, want 20", len(got))
	}
	// Manually XOR the two independently-verified halves and confirm it
	// matches what req2Xor3 produces — this is the one construction where
	// a swapped operand order (req3^req2 happens to equal req2^req3 for
	// XOR, so that particular swap wouldn't be caught by this) or an
	// off-by-one in the loop bound would show up.
	a, b := req2(skey), req3(s)
	want := make([]byte, 20)
	for i := range want {
		want[i] = a[i] ^ b[i]
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("req2Xor3()[%d] = %#x, want %#x", i, got[i], want[i])
		}
	}
}
