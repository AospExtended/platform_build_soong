// Copyright 2019 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package terminal

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"android/soong/ui/status"
)

type smartStatusOutput struct {
	writer    io.Writer
	formatter formatter

	lock sync.Mutex

	haveBlankLine bool

	termWidth       int
	sigwinch        chan os.Signal
	sigwinchHandled chan bool
}

// NewSmartStatusOutput returns a StatusOutput that represents the
// current build status similarly to Ninja's built-in terminal
// output.
func NewSmartStatusOutput(w io.Writer, formatter formatter) status.StatusOutput {
	s := &smartStatusOutput{
		writer:    w,
		formatter: formatter,

		haveBlankLine: true,

		sigwinch: make(chan os.Signal),
	}

	s.updateTermSize()

	s.startSigwinch()

	return s
}

func (s *smartStatusOutput) Message(level status.MsgLevel, message string) {
	if level < status.StatusLvl {
		return
	}

	str := s.formatter.message(level, message)

	s.lock.Lock()
	defer s.lock.Unlock()

	if level > status.StatusLvl {
		s.print(str)
	} else {
		s.statusLine(str)
	}
}

func (s *smartStatusOutput) StartAction(action *status.Action, counts status.Counts) {
	str := action.Description
	if str == "" {
		str = action.Command
	}

	progress := s.formatter.progress(counts)

	s.lock.Lock()
	defer s.lock.Unlock()

	s.statusLine(progress + str)
}

func (s *smartStatusOutput) FinishAction(result status.ActionResult, counts status.Counts) {
	str := result.Description
	if str == "" {
		str = result.Command
	}

	progress := s.formatter.progress(counts) + str

	output := s.formatter.result(result)

	s.lock.Lock()
	defer s.lock.Unlock()

	if output != "" {
		s.statusLine(progress)
		s.requestLine()
		s.print(output)
	} else {
		s.statusLine(progress)
	}
}

func (s *smartStatusOutput) Flush() {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.stopSigwinch()

	s.requestLine()
}

func (s *smartStatusOutput) Write(p []byte) (int, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.print(string(p))
	return len(p), nil
}

func (s *smartStatusOutput) requestLine() {
	if !s.haveBlankLine {
		fmt.Fprintln(s.writer)
		s.haveBlankLine = true
	}
}

func (s *smartStatusOutput) print(str string) {
	if !s.haveBlankLine {
		fmt.Fprint(s.writer, "\r", "\x1b[K")
		s.haveBlankLine = true
	}
	fmt.Fprint(s.writer, str)
	if len(str) == 0 || str[len(str)-1] != '\n' {
		fmt.Fprint(s.writer, "\n")
	}
}

func (s *smartStatusOutput) statusLine(str string) {
	idx := strings.IndexRune(str, '\n')
	if idx != -1 {
		str = str[0:idx]
	}

	// Limit line width to the terminal width, otherwise we'll wrap onto
	// another line and we won't delete the previous line.
	if s.termWidth > 0 {
		str = s.elide(str)
	}

	// Move to the beginning on the line, turn on bold, print the output,
	// turn off bold, then clear the rest of the line.
	start := "\r\x1b[1m"
	end := "\x1b[0m\x1b[K"
	fmt.Fprint(s.writer, start, str, end)
	s.haveBlankLine = false
}

func (s *smartStatusOutput) elide(str string) string {
	if len(str) > s.termWidth {
		// TODO: Just do a max. Ninja elides the middle, but that's
		// more complicated and these lines aren't that important.
		str = str[:s.termWidth]
	}

	return str
}

func (s *smartStatusOutput) startSigwinch() {
	signal.Notify(s.sigwinch, syscall.SIGWINCH)
	go func() {
		for _ = range s.sigwinch {
			s.lock.Lock()
			s.updateTermSize()
			s.lock.Unlock()
			if s.sigwinchHandled != nil {
				s.sigwinchHandled <- true
			}
		}
	}()
}

func (s *smartStatusOutput) stopSigwinch() {
	signal.Stop(s.sigwinch)
	close(s.sigwinch)
}

func (s *smartStatusOutput) updateTermSize() {
	if w, ok := termWidth(s.writer); ok {
		s.termWidth = w
	}
}
