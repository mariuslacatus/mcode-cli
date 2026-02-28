package ui

import (
	"sync"
	"time"
)

// Spinner represents a thread-safe terminal spinner
type Spinner struct {
	mu      sync.Mutex
	done    chan bool
	cleared chan bool
	active  bool
	message string
	title   string
}

// NewSpinner creates a new spinner
func NewSpinner(message string) *Spinner {
	return &Spinner{
		message: message,
	}
}

// Start starts the spinner if it's not already running
func (s *Spinner) Start() {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return
	}

	s.done = make(chan bool)
	s.cleared = make(chan bool)
	s.active = true
	
	msg := s.message
	title := s.title
	s.mu.Unlock()

	// Print first frame and update title immediately
	go func() {
		spinnerChars := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		
		// Initial frame
		if title != "" {
			PrintfSafe("\033]0;%s\007", title)
		}
		if msg != "" {
			PrintfSafe("\033[K%s %s", spinnerChars[0], msg)
		} else {
			PrintfSafe("\033[K%s", spinnerChars[0])
		}
		i++

		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-s.done:
				// Clear the spinner completely
				PrintSafe("\033[K")
				if s.cleared != nil {
					s.cleared <- true
				}
				return
			case <-ticker.C:
				s.mu.Lock()
				msg := s.message
				title := s.title
				s.mu.Unlock()

				// Update window title if provided
				if title != "" {
					PrintfSafe("\033]0;%s\007", title)
				}

				// Update spinner line
				if msg != "" {
					PrintfSafe("\033[K%s %s", spinnerChars[i%len(spinnerChars)], msg)
				} else {
					PrintfSafe("\033[K%s", spinnerChars[i%len(spinnerChars)])
				}
				i++
			}
		}
	}()
}

// Stop stops the spinner if it is running
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	done := s.done
	cleared := s.cleared
	s.mu.Unlock()

	done <- true
	if cleared != nil {
		<-cleared
	}

	s.mu.Lock()
	s.active = false
	s.mu.Unlock()
}

// UpdateMessage updates the spinner message
func (s *Spinner) UpdateMessage(message string) {
	s.mu.Lock()
	s.message = message
	s.mu.Unlock()
}

// SetTitle updates the window title via the spinner
func (s *Spinner) SetTitle(title string) {
	s.mu.Lock()
	s.title = title
	s.mu.Unlock()
}
