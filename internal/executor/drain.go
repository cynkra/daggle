package executor

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// stdoutMaxLineBytes caps how much of one line of step output is retained.
// Anything past it is counted and discarded, not buffered, so a step that
// never emits a newline costs a bounded amount of memory. 1MB is far more
// than any log line or ::daggle-output:: value needs.
const stdoutMaxLineBytes = 1024 * 1024

// drainLines reads r to EOF, calling handle once per line with the trailing
// newline (and a CR before it) removed.
//
// It exists because this loop must never stop early. The step's stdout is a
// pipe, and once nothing reads it the child blocks in write() as soon as the
// pipe buffer fills. RunProcess then waits for a child that is waiting for us,
// and the run hangs until someone kills it by hand.
//
// A bufio.Scanner cannot give that guarantee: it treats a line longer than its
// buffer as a fatal ErrTooLong and stops. The default buffer is 64KB, which a
// progress counter printing "\r" without ever printing "\n" reaches easily —
// that deadlocked a four-hour run before this was rewritten. Raising the buffer
// only moves the threshold, so read with a bufio.Reader instead, where a long
// line is ErrBufferFull, an ordinary signal to keep reading.
func drainLines(r io.Reader, handle func(string)) {
	br := bufio.NewReaderSize(r, 64*1024)
	var line []byte
	dropped := 0
	for {
		chunk, err := br.ReadSlice('\n')
		// chunk aliases br's buffer and is only valid until the next read.
		if len(chunk) > 0 {
			room := stdoutMaxLineBytes - len(line)
			if len(chunk) <= room {
				line = append(line, chunk...)
			} else {
				line = append(line, chunk[:room]...)
				dropped += len(chunk) - room
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue // no newline yet, the line goes on
		}
		// err == nil means a complete line, including an empty one. On EOF or a
		// read error emit whatever came before it, then stop.
		if err == nil || len(line) > 0 || dropped > 0 {
			handle(assembleLine(line, dropped))
			line, dropped = line[:0], 0
		}
		if err != nil {
			return
		}
	}
}

func assembleLine(line []byte, dropped int) string {
	line = bytes.TrimSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	if dropped == 0 {
		return string(line)
	}
	return fmt.Sprintf("%s [daggle: line too long, %d further bytes dropped]", line, dropped)
}
