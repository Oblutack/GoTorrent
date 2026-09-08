package torrent

import "testing"

func TestIsLANAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"192.168.1.5:6881", true},
		{"10.0.0.1:6881", true},
		{"172.16.5.5:6881", true},
		{"127.0.0.1:6881", true},
		{"127.0.0.1", true},
		{"8.8.8.8:6881", false},
		{"93.184.216.34:6881", false},
		{"[::1]:6881", true},
		{"not-an-ip:6881", false},
	}
	for _, c := range cases {
		if got := isLANAddr(c.addr); got != c.want {
			t.Errorf("isLANAddr(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}
