package api

import (
	"testing"
	"time"
)

func TestAuthFailureLimiterLocksOutAfterThreshold(t *testing.T) {
	l := NewAuthFailureLimiter(3, time.Minute, time.Hour)

	for i := 0; i < 2; i++ {
		if !l.Allowed("1.2.3.4") {
			t.Fatalf("locked out after only %d failures, want 3", i)
		}
		l.RecordFailure("1.2.3.4")
	}
	if !l.Allowed("1.2.3.4") {
		t.Fatal("locked out after only 2 failures, want 3")
	}
	l.RecordFailure("1.2.3.4") // third failure trips the threshold

	if l.Allowed("1.2.3.4") {
		t.Fatal("still allowed after 3 failures within the window, want locked out")
	}
	if !l.Allowed("5.6.7.8") {
		t.Fatal("an unrelated address was locked out too")
	}
}

func TestAuthFailureLimiterExpiresLockout(t *testing.T) {
	l := NewAuthFailureLimiter(1, time.Minute, 10*time.Millisecond)
	l.RecordFailure("1.2.3.4")
	if l.Allowed("1.2.3.4") {
		t.Fatal("expected a lockout immediately after tripping the threshold")
	}
	time.Sleep(30 * time.Millisecond)
	if !l.Allowed("1.2.3.4") {
		t.Fatal("lockout did not expire after its duration elapsed")
	}
}

func TestAuthFailureLimiterSuccessClearsHistory(t *testing.T) {
	l := NewAuthFailureLimiter(2, time.Minute, time.Hour)
	l.RecordFailure("1.2.3.4")
	l.RecordSuccess("1.2.3.4")
	l.RecordFailure("1.2.3.4") // if history wasn't cleared, this would be failure #2 and trip
	if !l.Allowed("1.2.3.4") {
		t.Fatal("a success in between failures should have reset the failure count")
	}
}

func TestAuthFailureLimiterZeroMaxFailsIsANoOp(t *testing.T) {
	l := NewAuthFailureLimiter(0, time.Minute, time.Hour)
	for i := 0; i < 100; i++ {
		l.RecordFailure("1.2.3.4")
	}
	if !l.Allowed("1.2.3.4") {
		t.Fatal("maxFails=0 must disable the limiter entirely")
	}
}
