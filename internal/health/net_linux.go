package health

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// portUsage counts TCP sockets on this machine whose local port is in the
// ephemeral range (connecting sockets and TIME_WAIT ones alike, which is
// what runs out), and the size of that range.
func portUsage() (used, total uint64, ok bool) {
	b, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		return 0, 0, false
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return 0, 0, false
	}
	lo, err1 := strconv.ParseUint(f[0], 10, 16)
	hi, err2 := strconv.ParseUint(f[1], 10, 16)
	if err1 != nil || err2 != nil || hi < lo {
		return 0, 0, false
	}
	for _, p := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		n, err := countEphemeral(p, lo, hi)
		if err == nil {
			used += n
			ok = true
		}
	}
	return used, hi - lo + 1, ok
}

// countEphemeral counts non-listening sockets in a /proc/net/tcp file
// with a local port in [lo, hi].
func countEphemeral(path string, lo, hi uint64) (uint64, error) {
	fh, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer fh.Close()
	var n uint64
	sc := bufio.NewScanner(fh)
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[3] == "0A" { // 0A is LISTEN
			continue
		}
		_, port, found := strings.Cut(f[1], ":")
		if !found {
			continue
		}
		p, err := strconv.ParseUint(port, 16, 16)
		if err == nil && p >= lo && p <= hi {
			n++
		}
	}
	return n, sc.Err()
}

// ifaceCounters returns the bytes received and sent by each network
// interface except loopback, and each one's link speed in bits per
// second (0 when the kernel does not know it, as for most virtual NICs).
func ifaceCounters() map[string]ifaceCount {
	fh, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil
	}
	defer fh.Close()
	out := map[string]ifaceCount{}
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if name == "lo" || len(f) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		c := ifaceCount{rx: rx, tx: tx}
		if b, err := os.ReadFile("/sys/class/net/" + name + "/speed"); err == nil {
			if mbit, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && mbit > 0 {
				c.speed = uint64(mbit) * 1_000_000
			}
		}
		out[name] = c
	}
	return out
}
