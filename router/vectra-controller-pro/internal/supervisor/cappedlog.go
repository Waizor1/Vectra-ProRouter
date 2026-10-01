package supervisor

import (
	"bytes"
	"os"
	"strings"
	"sync"

	"vectra-controller-pro/internal/redact"
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
	mu           sync.Mutex
	path         string
	max          int64
	f            *os.File
	size         int64
	pending      []byte
	dropping     bool
	privateBlock bool
}

func newCappedLog(path string, max int64) *cappedLog {
	return &cappedLog{path: path, max: max}
}

func (c *cappedLog) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// exec.Copy may split a credential across writes. Persist complete lines only.
	consumed := len(p)
	var safe []byte
	for _, b := range p {
		if b == '\n' {
			if c.dropping {
				safe = append(safe, []byte("[xray oversized log line omitted]\n")...)
			} else {
				line := string(c.pending)
				if strings.Contains(line, "-----BEGIN ") && strings.Contains(line, "PRIVATE KEY-----") {
					c.privateBlock = true
				}
				if c.privateBlock {
					safe = append(safe, "[xray private key material omitted]\n"...)
					if strings.Contains(line, "-----END ") && strings.Contains(line, "PRIVATE KEY-----") {
						c.privateBlock = false
					}
				} else {
					safe = append(safe, redact.Text(line)...)
					safe = append(safe, '\n')
				}
			}
			clear(c.pending)
			c.pending = c.pending[:0]
			c.dropping = false
		} else if !c.dropping {
			if len(c.pending) >= 16<<10 {
				clear(c.pending)
				c.pending = nil
				c.dropping = true
			} else {
				c.pending = append(c.pending, b)
			}
		}
	}
	p = safe
	if len(p) == 0 {
		return consumed, nil
	}
	// A single write may contain many lines: retain only the capped tail.
	if int64(len(p)) > c.max && c.max > 0 {
		p = p[len(p)-int(c.max):]
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			p = p[i+1:]
		} else {
			p = nil
		}
	}
	if c.f == nil {
		f, err := os.OpenFile(c.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, logMode)
		if err != nil {
			return consumed, nil
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
			return consumed, nil
		}
		c.f, c.size = f, 0
	}
	n, _ := c.f.Write(p)
	c.size += int64(n)
	return consumed, nil
}
