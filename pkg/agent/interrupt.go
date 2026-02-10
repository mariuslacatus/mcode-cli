package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

var (
	isRawMode atomic.Bool
	rawModeMu sync.Mutex
	oldState  *term.State
)

// StartInterruptMonitor puts terminal in raw mode and cancels context on Escape.
// Returns a derived context and a restore function.
func StartInterruptMonitor(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)

	rawModeMu.Lock()
	fd := int(os.Stdin.Fd())
	alreadyRaw := isRawMode.Load()
	if !alreadyRaw {
		state, err := term.MakeRaw(fd)
		if err == nil {
			oldState = state
			isRawMode.Store(true)
		}
	}
	rawModeMu.Unlock()

	go func() {
		defer cancel()
		
		buf := make([]byte, 1)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Check if data is available to read using select
				if inputAvailable(fd) {
					n, err := os.Stdin.Read(buf)
					if err != nil {
						// Only return on EOF, ignore other errors to avoid spurious cancellation
						if err == io.EOF {
							f, _ := os.OpenFile("/tmp/mcode_interrupt.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
							fmt.Fprintf(f, "Cancellation due to EOF\n")
							f.Close()
							return
						}
						continue
					}
					if n > 0 {
						if buf[0] == 27 { // Escape
							// Check if more characters are available immediately (escape sequence like arrow keys)
							if inputAvailableShort(fd) {
								continue
							}
							return // Standalone Escape
						}
						// Also handle Ctrl+C (ETX)
						if buf[0] == 3 {
							return
						}
					}
				}
			}
		}
	}()

	return ctx, func() {
		cancel()
		
		rawModeMu.Lock()
		defer rawModeMu.Unlock()
		
		// Only restore if we were the ones who set it and it's still set
		if !alreadyRaw && isRawMode.Load() && oldState != nil {
			term.Restore(fd, oldState)
			isRawMode.Store(false)
		}
	}
}

func inputAvailable(fd int) bool {
	return inputAvailableTimeout(fd, 100000) // 100ms
}

func inputAvailableShort(fd int) bool {
	return inputAvailableTimeout(fd, 10000) // 10ms
}

func inputAvailableTimeout(fd int, usec int) bool {
	var fds unix.FdSet
	// Set bit for fd
	fds.Bits[fd/32] |= 1 << (uint(fd) % 32)
	
	tv := unix.Timeval{Sec: 0, Usec: int32(usec)}
	
	n, err := unix.Select(fd+1, &fds, nil, nil, &tv)
	return n > 0 && err == nil
}

// PrintSafe prints text handling newlines for raw mode
func PrintSafe(a ...interface{}) {
	s := fmt.Sprint(a...)
	if isRawMode.Load() {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	fmt.Print(s)
}

// PrintlnSafe prints line handling newlines for raw mode
func PrintlnSafe(a ...interface{}) {
	s := fmt.Sprint(a...)
	if isRawMode.Load() {
		s = strings.ReplaceAll(s, "\n", "\r\n") + "\r\n"
		fmt.Print(s)
	} else {
		fmt.Println(s)
	}
}

// PrintfSafe prints formatted string handling newlines for raw mode
func PrintfSafe(format string, a ...interface{}) {
	s := fmt.Sprintf(format, a...)
	if isRawMode.Load() {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	fmt.Print(s)
}

// ReadConfirmation reads a single key for confirmation.
// Returns the string representation (e.g., "y", "n", "i") or "i" if Escape.
func ReadConfirmation() string {
	fd := int(os.Stdin.Fd())
	
	// Set raw mode temporarily
	state, err := term.MakeRaw(fd)
	if err != nil {
		return ""
	}
	defer term.Restore(fd, state)
	
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return ""
		}
		
		key := buf[0]
		if key == 27 { // Escape
			// Check if it's a sequence
			if inputAvailableShort(fd) {
				// Read and discard the rest of the sequence
				for inputAvailableShort(fd) {
					os.Stdin.Read(buf)
				}
				continue // Ignore sequence and wait for another key
			}
			return "i" // Standalone Escape -> interrupt
		}
		if key == 3 { // Ctrl+C
			return "i"
		}
		
		// Map other keys
		return strings.ToLower(string(key))
	}
}
