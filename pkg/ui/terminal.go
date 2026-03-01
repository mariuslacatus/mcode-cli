package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

var (
	isRawMode atomic.Bool
	isPaused  atomic.Bool
	rawModeMu sync.Mutex
	oldState  *term.State
	outputMu  sync.Mutex
	
	// ErrInterrupted is returned when the user presses Escape or Ctrl+C
	ErrInterrupted = fmt.Errorf("interrupted by user")
)

// PauseInterruptMonitor temporarily stops the monitor from reading stdin
func PauseInterruptMonitor() {
	isPaused.Store(true)
}

// ResumeInterruptMonitor resumes reading from stdin
func ResumeInterruptMonitor() {
	isPaused.Store(false)
}

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

	// Use a wait group to ensure the goroutine has finished before restoring
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		defer cancel()
		
		buf := make([]byte, 1)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if isPaused.Load() {
					time.Sleep(50 * time.Millisecond)
					continue
				}

				// Check if data is available to read using select
				if inputAvailableShort(fd) {
					n, err := os.Stdin.Read(buf)
					if err != nil {
						continue
					}
					if n > 0 {
						if buf[0] == 27 { // Escape
							// If it's just Escape (no following chars in short time), it's an interrupt
							if !inputAvailableShort(fd) {
								return
							}
							// Otherwise, it's an escape sequence, consume it
							for inputAvailableShort(fd) {
								os.Stdin.Read(buf)
							}
							continue
						}
						// Also handle Ctrl+C (ETX)
						if buf[0] == 3 {
							return
						}
					}
				}
				// Small sleep to prevent busy loop but keep it responsive
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	return ctx, func() {
		cancel()
		wg.Wait()
		
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
	outputMu.Lock()
	defer outputMu.Unlock()
	s := fmt.Sprint(a...)
	if isRawMode.Load() {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	fmt.Print(s)
	os.Stdout.Sync()
}

// PrintlnSafe prints line handling newlines for raw mode
func PrintlnSafe(a ...interface{}) {
	outputMu.Lock()
	defer outputMu.Unlock()
	s := fmt.Sprint(a...)
	if isRawMode.Load() {
		s = strings.ReplaceAll(s, "\n", "\r\n") + "\r\n"
		fmt.Print(s)
	} else {
		fmt.Println(s)
	}
	os.Stdout.Sync()
}

// PrintfSafe prints formatted string handling newlines for raw mode
func PrintfSafe(format string, a ...interface{}) {
	outputMu.Lock()
	defer outputMu.Unlock()
	s := fmt.Sprintf(format, a...)
	if isRawMode.Load() {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	fmt.Print(s)
	os.Stdout.Sync()
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

// UpdateStatusDisplay updates the fixed header at the top of the terminal
func UpdateStatusDisplay(modelName string, tokens int) {
	// Format token string
	usageStr := fmt.Sprintf("%d", tokens)
	if tokens >= 1000 {
		usageStr = fmt.Sprintf("%.1fk", float64(tokens)/1000.0)
	}

	// Update window title using ANSI escape sequence: \033]0;TITLE\007
	// This shows status in the terminal tab/window title instead of a sticky header
	title := fmt.Sprintf("MCode | %s | %s tokens", modelName, usageStr)
	fmt.Printf("\033]0;%s\007", title)
}
