package utp

import "testing"

func TestSeqLessOrdinaryCase(t *testing.T) {
	if !seqLess(5, 10) {
		t.Fatal("5 should be less than 10")
	}
	if seqLess(10, 5) {
		t.Fatal("10 should not be less than 5")
	}
	if seqLess(5, 5) {
		t.Fatal("a value is never less than itself")
	}
}

func TestSeqLessWrapsAroundCorrectly(t *testing.T) {
	// 65534 and 65535 are both "before" 2 once the space wraps.
	if !seqLess(65534, 2) {
		t.Fatal("65534 should be before 2 (wraps around 65536)")
	}
	if !seqLess(65535, 2) {
		t.Fatal("65535 should be before 2 (wraps around 65536)")
	}
	if seqLess(2, 65534) {
		t.Fatal("2 should not be before 65534 once the space has wrapped")
	}
}

func TestSeqLessEq(t *testing.T) {
	if !seqLessEq(5, 5) {
		t.Fatal("a value must be <= itself")
	}
	if !seqLessEq(5, 6) {
		t.Fatal("5 <= 6")
	}
	if seqLessEq(6, 5) {
		t.Fatal("6 is not <= 5")
	}
}

func TestSeqDiffOrdinaryCase(t *testing.T) {
	if got := seqDiff(5, 10); got != 5 {
		t.Fatalf("seqDiff(5, 10) = %d, want 5", got)
	}
	if got := seqDiff(10, 5); got != -5 {
		t.Fatalf("seqDiff(10, 5) = %d, want -5", got)
	}
}

func TestSeqDiffWrapsAroundCorrectly(t *testing.T) {
	if got := seqDiff(65534, 2); got != 4 {
		t.Fatalf("seqDiff(65534, 2) = %d, want 4 (65534 -> 65535 -> 0 -> 1 -> 2)", got)
	}
}
