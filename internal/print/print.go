// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://wwp.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// package print contains printing helpers used by the Protoscope disassembler.

package print

import (
	"bytes"
	"fmt"
	"unicode/utf8"
)

// Stack is a wrapper over a slice type that provides helpers for pushing and
// popping elements.
//
// Exported because of utility in the disassembler itself.
type Stack[T any] []T

// Push pushes an element.
func (s *Stack[T]) Push(x T) {
	*s = append(*s, x)
}

// Pop pops an element; panics if the stack is empty.
func (s *Stack[T]) Pop() T {
	popped := (*s)[len(*s)-1]
	*s = (*s)[:len(*s)-1]
	return popped
}

// Pop pops the top n elements off the stack and returns a slice containing
// copies. Panics if the stack is too small
func (s *Stack[T]) PopN(n int) []T {
	popped := make([]T, n)
	copy(popped, (*s)[len(*s)-n:])
	*s = (*s)[:len(*s)-n]
	return popped
}

// Peek returns a pointer to the top of the stack, or nil if the stack is
// empty.
func (s *Stack[T]) Peek() *T {
	if len(*s) == 0 {
		return nil
	}
	return &s.PeekN(1)[0]
}

// Peek returns the top n elements of the stack as another stack.
//
// Returns nil if the stack is too small.
func (s *Stack[T]) PeekN(n int) Stack[T] {
	if len(*s) < n {
		return nil
	}
	return (*s)[len(*s)-n:]
}

// Line represents a single line in the output stream. A Printer buffers on a
// line-by-line basis to be able to do indentation and brace collapse with
// minimal difficulty.
type Line struct {
	// This line's in-progress text buffer.
	bytes.Buffer

	remarks []string
	indent  int
	folds   int
}

// Printer is an intelligent indentation and codeblock aware printer.
type Printer struct {
	// The number of spaces to use per indentation level.
	Indent int
	// The number of nested folded blocks allowed, < 0 means infinity.
	MaxFolds int

	lines  Stack[Line]
	blocks Stack[BlockInfo]
}

// Current returns the current line being processed.
func (p *Printer) Current() *Line {
	return p.Prev(0)
}

// Discards the current line
func (p *Printer) DiscardLine() {
	p.lines.Pop()
}

type Mark int

// Makes a mark on the line buffer.
func (p *Printer) Mark() Mark {
	return Mark(len(p.lines))
}

// Discards all lines after the mark.
func (p *Printer) Reset(m Mark) {
	p.lines = p.lines[:m]
}

// Prev returns the nth most recent line.
//
// Returns nil if there are not enough lines.
func (p *Printer) Prev(n int) *Line {
	return &p.lines.PeekN(n + 1)[0]
}

// NewLine pushes a new line.
func (p *Printer) NewLine() {
	p.lines.Push(Line{})
}

// Writes to the current line's buffer with Fprint.
func (p *Printer) Write(args ...any) {
	fmt.Fprint(p.Current(), args...)
}

// Writes to the current line's buffer with Fprintf.
func (p *Printer) Writef(f string, args ...any) {
	fmt.Fprintf(p.Current(), f, args...)
}

// Adds a new remark made from stringifying args.
func (p *Printer) Remark(args ...any) {
	l := p.Current()
	l.remarks = append(l.remarks, fmt.Sprint(args...))
}

// Adds a new remark made from stringifying args.
func (p *Printer) Remarkf(f string, args ...any) {
	l := p.Current()
	l.remarks = append(l.remarks, fmt.Sprintf(f, args...))
}

// Finish dumps the entire contents of the Printer into a byte array.
func (p *Printer) Finish() []byte {
	if len(p.blocks) != 0 {
		panic("called Finish() without closing all blocks")
	}

	var out bytes.Buffer
	indent := 0
	commentCol := -1
	commentColUntil := -1
	for i, line := range p.lines {
		if len(line.remarks) != 0 && commentColUntil < i {
			// Comments are aligned to the same column if they are contiguous, unless
			// crossing an indentation boundary would cause the remark column to be
			// further than it would have been without crossing the boundary.
			//
			// This allows the column finding algorithm to be linear.
			indent2 := indent
			commentCol = -1
			for j, line := range p.lines[i:] {
				if len(line.remarks) == 0 {
					commentColUntil = j + i
					break
				}

				lineLen := indent2*p.Indent + utf8.RuneCount(line.Bytes())
				indent2 += line.indent
				if lineLen > commentCol {
					if j > 1 && line.indent != 0 {
						commentColUntil = j + i
						break
					}
					commentCol = lineLen
				}
			}
			if extra := commentCol % p.Indent; extra != 0 {
				commentCol += p.Indent - extra
			}
		}

		for i := 0; i < indent*p.Indent; i++ {
			out.WriteString(" ")
		}

		out.Write(line.Bytes())
		if len(line.remarks) > 0 {
			needed := commentCol - indent*p.Indent - line.Len()
			for i := 0; i < needed; i++ {
				out.WriteString(" ")
			}

			out.WriteString("  # ")
			for i, remark := range line.remarks {
				if i != 0 {
					out.WriteString(", ")
				}
				out.WriteString(remark)
			}
		}

		indent += line.indent
		out.WriteString("\n")
	}

	return out.Bytes()
}

type BlockInfo struct {
	// Whether this block will start and end with delimiters that do not need to
	// have spaces placed before/after them, allowing for output like {x} instead
	// of { x }.
	HasDelimiters bool
	// The maximum height of the block that will be folded into a single line.
	HeightToFoldAt int
	// The line (zero-indexed, starting from the last line) that should be the
	// final indented line. If there are not enough lines, the block is not
	// indented at all.
	UnindentAt int

	start int
}

// Starts a new indentation block.
func (p *Printer) StartBlock(bi BlockInfo) {
	if p.Current().indent != 0 {
		panic("called StartBlock() too many times; this is a bug")
	}
	bi.start = len(p.lines) - 1
	p.blocks.Push(bi)
	p.Current().indent++
}

// Discards the current block and undoes its indentation.
func (p *Printer) DropBlock() *Line {
	bi := p.blocks.Pop()
	start := &p.lines[bi.start]
	start.indent--
	return start
}

// Finishes an indentation block; a call to this function must match up to
// a corresponding previous StartBlock() call. Returns the starting line for the
// block.
//
// This function will perform folding of small blocks as appropriate.
func (p *Printer) EndBlock() *Line {
	bi := p.blocks.Pop()
	start := &p.lines[bi.start]
	height := len(p.lines) - bi.start

	// Does the unindentation operation. Because this may run after a successful
	// fold, we need to make sure that it re-computes the height.
	defer func() {
		height := len(p.lines) - bi.start
		if height <= bi.UnindentAt {
			p.lines[bi.start].indent--
		} else {
			p.lines.PeekN(bi.UnindentAt + 1)[0].indent--
		}
	}()

	// Decide whether to fold this block.
	if height > bi.HeightToFoldAt || height < 2 {
		return start
	}

	folds := 0
	remarks := 0
	for _, line := range p.lines[bi.start:] {
		folds += line.folds
		if len(line.remarks) > 0 {
			remarks++
		}
	}

	if folds > p.MaxFolds {
		return start
	}

	// Do not mix remarks from different lines.
	if remarks > 1 {
		return start
	}

	// We are ok to unindent.
	for i, line := range p.lines[bi.start+1:] {
		if (i != 0 && i != height-2) || !bi.HasDelimiters {
			start.WriteString(" ")
		}
		start.Write(line.Bytes())
		if len(line.remarks) != 0 {
			// This will execute at most once per loop.
			start.remarks = line.remarks
		}
	}

	start.folds = folds
	p.lines = p.lines[:bi.start+1]
	return start
}

// Folds the last count lines into lines with `cols` columns each.
func (p *Printer) FoldIntoColumns(cols, count int) {
	toFold := p.lines.PopN(count)
	widths := make([]int, cols)

	for len(toFold) > 0 {
		for i := range widths {
			widths[i] = 0
		}

		end := len(toFold)
		for i, line := range toFold {
			if len(line.remarks) != 0 {
				end = i
				break
			}

			len := utf8.RuneCount(line.Bytes())
			w := &widths[i%cols]
			if len > *w {
				*w = len
			}
		}
		if end == 0 {
			end = 1
		}

		for i, line := range toFold[:end] {
			if i%cols == 0 {
				p.NewLine()
			} else {
				p.Write(" ")
			}

			needed := widths[i%cols] - utf8.RuneCount(line.Bytes())
			for i := 0; i < needed; i++ {
				p.Write(" ")
			}
			p.Current().Write(line.Bytes())
			if len(line.remarks) != 0 {
				// This will execute at most once per loop.
				p.Current().remarks = line.remarks
			}
		}

		toFold = toFold[end:]
	}
}
