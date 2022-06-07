// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package protoscope

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// WriterOptions represents options that can be passed to control the writer's
// decoding heuristics.
type WriterOptions struct {
	// Disables treating any fields as containing UTF-8.
	NoQuotedStrings bool
	// Treats every length-prefixed field as being a message, printing hex if
	// an error is hit.
	AllFieldsAreMessages bool
}

func Write(src []byte, opts WriterOptions) string {
	w := writer{opts: opts}

	for len(src) > 0 {
		w.newLine()
		rest, ok := w.decodeField(src)
		if !ok {
			w.lines = w.lines[:len(w.lines)-1]
			break
		}
		src = rest
	}

	if len(src) > 0 {
		w.newLine()
		w.dumpHexString(src)
	}

	var out strings.Builder
	for _, line := range w.lines {
		for i := 0; i < line.indent; i++ {
			fmt.Fprint(&out, "  ")
		}
		fmt.Fprint(&out, line.text.String())
		if comment := line.comment.String(); comment != "" {
			fmt.Fprint(&out, "  # ", comment)
		}
		fmt.Fprintln(&out)
	}

	return out.String()
}

type line struct {
	text, comment *strings.Builder
	indent 				int
}

type writer struct {
	opts   WriterOptions
	indent int
	lines  []line
}

func (w *writer) write(args ...any) {
	fmt.Fprint(w.lines[len(w.lines) - 1].text, args...)
}

func (w *writer) writef(f string, args ...any) {
	fmt.Fprintf(w.lines[len(w.lines) - 1].text, f, args...)
}

func (w *writer) commentf(f string, args ...any) {
	fmt.Fprintf(w.lines[len(w.lines) - 1].comment, f, args...)
}

func (w *writer) newLine() {
	w.lines = append(w.lines, line {
		text: new(strings.Builder),
		comment: new(strings.Builder),
		indent: w.indent,
	})
}

func (w *writer) dumpHexString(src []byte) {
	w.write("`")

	for i, b := range src {
		if i > 0 && i%40 == 0 {
			w.write("`")
			w.newLine()
			w.write("`")
		}
		w.writef("%02x", b)
	}
	w.write("`")
}

func (w *writer) decodeField(src []byte) ([]byte, bool) {
	rest, value, extra, ok := decodeVarint(src)
	if !ok {
		return nil, false
	}
	src = rest

	// 0 is never a valid field number, so this probably isn't a message.
	if value>>3 == 0 && !w.opts.AllFieldsAreMessages {
		return nil, false
	}

	if extra > 0 {
		w.writef("long-form:%d ", extra)
	}
	w.writef("%d:", value>>3)

	switch value & 0x7 {
	case 0:
		rest, value, extra, ok := decodeVarint(src)
		if !ok {
			return nil, false
		}
		src = rest

		if extra > 0 {
			w.writef(" long-form:%d", extra)
		}
		w.writef(" %d", int64(value))
	case 3:
		w.writef("SGROUP")
	case 4:
		w.writef("EGROUP")

	case 1:
		// Assume this is a float by default.
		if len(src) < 8 {
			return nil, false
		}
		bits := binary.LittleEndian.Uint64(src)
		src = src[8:]
		value := math.Float64frombits(bits)

		if math.IsInf(value, 1) {
			w.write(" inf64")
		} else if math.IsInf(value, -1) {
			w.write(" -inf64")
		} else if math.IsNaN(value) {
			w.writef(" 0x%xi64", bits)
		} else {
			if s := ftoa(bits); s != "" {
				w.writef(" %s", s)
				w.commentf("%#xi64", int64(bits))
			} else {
				w.writef(" %di64", int64(bits))
			}
		}
	case 5:
		// Assume this is a float by default.
		if len(src) < 8 {
			return nil, false
		}
		bits := binary.LittleEndian.Uint32(src)
		src = src[4:]
		value := float64(math.Float32frombits(bits))

		if math.IsInf(value, 1) {
			w.write(" inf32")
		} else if math.IsInf(value, -1) {
			w.write(" -inf32")
		} else if math.IsNaN(value) {
			w.writef(" 0x%xi32", bits)
		} else {
			if s := ftoa(bits); s != "" {
				w.writef(" %si32", s)
				w.commentf("%#xi32", int32(bits))

			} else {
				w.writef(" %di32", int32(bits))
			}
		}

	case 2:
		rest, value, extra, ok := decodeVarint(src)
		if !ok {
			return nil, false
		}
		src = rest

		if uint64(len(src)) < value {
			return nil, false
		}

		delimited := src[:int(value)]
		src = src[int(value):]

		if extra > 0 {
			w.writef(" long-form:%d", extra)
		}
		w.write(" {")

		w.indent++

		// First, assume this is a message.
		startLine := len(w.lines)
		src2 := delimited
		for len(src2) > 0 {
			w.newLine()
			s, ok := w.decodeField(src2)
			if !ok {
				// Clip off an incompletely printed line.
				w.lines = w.lines[:len(w.lines)-1]
				break
			}
			src2 = s
		}

		if len(src2) == 0 || (w.opts.AllFieldsAreMessages && len(src2) < len(delimited)) {
			oneLiner := len(w.lines) == startLine + 1 && len(src2) == 0 
			if oneLiner {
				line := w.lines[startLine]
				w.lines = w.lines[:startLine]
				w.writef(" %s ", line.text.String())
				w.commentf("%s", line.comment.String())
			} else if len(src2) > 0 {
				w.newLine()
				w.dumpHexString(src2)
			}

			w.indent--
			if !oneLiner {
				w.newLine()
			}
			w.write("}")
			return src, true
		} else {
			w.lines = w.lines[:startLine]
		}

		// Otherwise, maybe it's a UTF-8 string.
		if !w.opts.NoQuotedStrings && utf8.Valid(delimited) {
			runes := utf8.RuneCount(delimited)

			s := string(delimited)
			unprintable := 0
			for _, r := range s {
				if !unicode.IsGraphic(r) {
					unprintable++
				}
			}
			if float64(unprintable)/float64(runes) > 0.3 {
				goto justBytes
			}

			if runes > 80 {
				w.newLine()
			}
			w.write("\"")

			for i, r := range s {
				if i != 0 && i%80 == 0 {
					w.write("\"")
					w.newLine()
					w.write("\"")
				}

				switch r {
				case '\n':
					w.write("\\n")
				case '\\':
					w.write("\\\\")
				case '"':
					w.write("\\\"")
				default:
					if !unicode.IsGraphic(r) {
						enc := make([]byte, 4)
						enc = enc[:utf8.EncodeRune(enc, r)]
						for _, b := range enc {
							w.writef("\\x%02x", b)
						}
					} else {
						w.writef("%c", r)
					}
				}
			}

			w.write("\"")
			w.indent--
			if runes > 80 {
				w.newLine()
			}
			w.write("}")
			return src, true
		}

		// Who knows what it is? Bytes or something.
	justBytes:
		if len(delimited) > 40 {
			w.newLine()
		}
		w.dumpHexString(delimited)
		w.indent--
		if len(delimited) > 40 {
			w.newLine()
		}
		w.write("}")
	case 6, 7:
		return nil, false
	}
	return src, true
}

func ftoa[I uint32 | uint64](bits I) string {
	var mantLen, expLen, bitLen int
	var value float64
	switch b := any(bits).(type) {
	case uint32:
		bitLen = 32
		expLen = 8
		value = float64(math.Float32frombits(b))
	case uint64:
		bitLen = 64
		expLen = 11
		value = math.Float64frombits(b)
	}
	mantLen = bitLen - expLen - 1

	if bits == 0 {
		return "0.0"
	} else if bits == 1<<(bitLen-1) {
		return "-0.0"
	}

	exp := int64((bits >> mantLen) & ((1 << expLen) - 1))
	exp -= (1 << (expLen - 1)) - 1
	absExp := exp
	if absExp < 0 {
		absExp = -absExp
	}
	bigExp := int64(1)<<(expLen-1) - 1

	if absExp >= bigExp {
		// Very large or very small exponents indicate this probably isn't actually
		// a float.
		return ""
	}

	// Only print floats in decimal if it can be round-tripped.
	decimal := strconv.FormatFloat(value, 'g', -1, bitLen)

	roundtrip, _ := strconv.ParseFloat(decimal, bitLen)
	var bits2 I
	switch any(bits).(type) {
	case uint32:
		bits2 = I(math.Float32bits(float32(roundtrip)))
	case uint64:
		bits2 = I(math.Float64bits(roundtrip))
	}

	if bits2 != bits {
		decimal = strconv.FormatFloat(value, 'x', -1, bitLen)
	}

	// Discard a + after the exponent.
	decimal = strings.Replace(decimal, "+", "", -1)

	// Insert a decimal point if necessary.
	if !strings.Contains(decimal, ".") {
		if strings.Contains(decimal, "e") {
			decimal = strings.Replace(decimal, "e", ".0e", -1)
		} else {
			decimal += ".0"
		}
	}

	return decimal
}

func decodeVarint(src []byte) (rest []byte, value uint64, extraBytes int, ok bool) {
	count := 0
	for {
		if len(src) == 0 {
			ok = false
			return
		}

		var b byte
		b, src = src[0], src[1:]
		if count == 9 && b > 1 {
			// The tenth byte has a special upper limit: it may only be 0 or 1.
			ok = false
			return
		}

		value |= uint64(b&0x7f) << (count * 7)
		count++

		if b&0x7f == 0 {
			extraBytes++
		} else {
			extraBytes = 0
		}

		if b&0x80 == 0 {
			break
		}
	}

	if value == 0 {
		extraBytes--
	}
	rest = src
	ok = true
	return
}
