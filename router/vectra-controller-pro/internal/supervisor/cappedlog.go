package supervisor

import (
	"os"
	"sync"
)

// XrayLogMaxBytes caps xray.log; one previous generation is kept as
// xray.log.1, so the pair never holds more than twice this in tmpfs.
const XrayLogMaxBytes = 512 << 10

// logMode is xray's log's: root's alone.
const logMode = 0o600

// cappedLog is an append-only log that rotates to <path>.1 when a write would
// take it past max. It never fails a write: xray must not stall or die
// because its log could not be written. Both generations are root's alone
// (logMode): xray's errors quote the config they choke on — a user id, a
// password.
type cappedLog struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func newCappedLog(path string, max int64) *cappedLog {
	return &cappedLog{path: path, max: max}
}

func (c *cappedLog) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == nil {
		f, err := os.OpenFile(c.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, logMode)
		if err != nil {
			return len(p), nil
		}
		c.f = f
		if st, err := f.Stat(); err == nil {
			c.size = st.Size()
		}
		// A log an older version left readable to all is made root's too.
		_ = f.Chmod(logMode)
		_ = os.Chmod(c.path+".1", logMode)
	}
	if c.size > 0 && c.size+int64(len(p)) > c.max {
		_ = c.f.Close()
		_ = os.Rename(c.path, c.path+".1")
		f, err := os.OpenFile(c.path, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, logMode)
		if err != nil {
			c.f = nil
			return len(p), nil
		}
		c.f, c.size = f, 0
	}
	n, _ := c.f.Write(p)
	c.size += int64(n)
	return len(p), nil
}
