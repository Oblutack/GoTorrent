package utp

// seqLess reports whether a comes strictly before b in the circular
// 16-bit sequence-number space seq_nr/ack_nr both live in — required
// since both genuinely wrap at 65536 on any long-lived or fast
// connection, and a plain `a < b` comparison silently breaks the instant
// that happens. Standard RFC 1982-style serial number comparison: the
// wraparound-safe unsigned subtraction, reinterpreted as signed, tells
// you which direction is "forward" in the circular space.
func seqLess(a, b uint16) bool {
	return int16(a-b) < 0
}

// seqLessEq is seqLess or equal.
func seqLessEq(a, b uint16) bool {
	return a == b || seqLess(a, b)
}

// seqDiff returns b-a as a signed distance in the circular sequence-number
// space (positive when b is ahead of a) — used to size the reorder buffer
// and to detect an implausibly large jump (protocol violation/garbage)
// rather than trusting an attacker-controlled seq_nr blindly.
func seqDiff(a, b uint16) int32 {
	return int32(int16(b - a))
}
