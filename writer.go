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
	w := writer{opts: opts, dest: new(strings.Builder)}

	for len(src) > 0 {
		rest, ok := w.decodeField(src)
		if !ok {
			break
		}
		src = rest
	}

	if len(src) > 0 {
		w.dumpHexString(src)
	}

	fmt.Fprintln(w.dest)
	return strings.TrimPrefix(w.dest.String(), "\n")
}

type writer struct {
	opts   WriterOptions
	indent int
	dest   *strings.Builder
}

func (w *writer) newLine() {
	fmt.Fprint(w.dest, "\n")
	for i := 0; i < w.indent; i++ {
		fmt.Fprint(w.dest, "  ")
	}
}

func (w *writer) dumpHexString(src []byte) {
	fmt.Fprint(w.dest, "`")

	for i, b := range src {
		if i > 0 && i%40 == 0 {
			fmt.Fprint(w.dest, "`")
			w.newLine()
			fmt.Fprint(w.dest, "`")
		}
		fmt.Fprintf(w.dest, "%02x", b)
	}
	fmt.Fprint(w.dest, "`")
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

	w.newLine()
	if extra > 0 {
		fmt.Fprintf(w.dest, "long-form:%d ", extra)
	}
	fmt.Fprintf(w.dest, "%d:", value>>3)

	switch value & 0x7 {
	case 0:
		rest, value, extra, ok := decodeVarint(src)
		if !ok {
			return nil, false
		}
		src = rest

		if extra > 0 {
			fmt.Fprintf(w.dest, " long-form:%d", extra)
		}
		fmt.Fprintf(w.dest, " %d", int64(value))
	case 3:
		fmt.Fprintf(w.dest, "SGROUP")
	case 4:
		fmt.Fprintf(w.dest, "EGROUP")

	case 1:
		// Assume this is a float by default.
		if len(src) < 8 {
			return nil, false
		}
		bits := binary.LittleEndian.Uint64(src)
		src = src[8:]
		value := math.Float64frombits(bits)

		if math.IsInf(value, 1) {
			fmt.Fprint(w.dest, " inf64")
		} else if math.IsInf(value, -1) {
			fmt.Fprint(w.dest, " -inf64")
		} else if math.IsNaN(value) {
			fmt.Fprintf(w.dest, " 0x%x", bits)
		} else {
			if s := ftoa(bits); s != "" {
				fmt.Fprintf(w.dest, " %s  # %#xi64", s, int64(bits))
			} else {
				fmt.Fprintf(w.dest, " %di64", int64(bits))
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
			fmt.Fprint(w.dest, " inf32")
		} else if math.IsInf(value, -1) {
			fmt.Fprint(w.dest, " -inf32")
		} else if math.IsNaN(value) {
			fmt.Fprintf(w.dest, " 0x%x", bits)
		} else {
			if s := ftoa(bits); s != "" {
				fmt.Fprintf(w.dest, " %si32  # %#xi32", s, int32(bits))
			} else {
				fmt.Fprintf(w.dest, " %di32", int32(bits))
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
			fmt.Fprintf(w.dest, " long-form:%d", extra)
		}
		fmt.Fprint(w.dest, " {")

		w.indent++

		// First, assume this is a message.
		var builder, perLoop strings.Builder
		old := w.dest
		if w.opts.AllFieldsAreMessages {
			// Make sure to capture the contents of every run individually, so that
			// we avoid getting half of a printout for a bad field.
			w.dest = &perLoop
		} else {
			w.dest = &builder
		}

		src2 := delimited
		for len(src2) > 0 {
			s, ok := w.decodeField(src2)
			if !ok {
				break
			}

			if w.opts.AllFieldsAreMessages {
				builder.WriteString(perLoop.String())
				perLoop.Reset()
			}
			src2 = s
		}
		w.dest = old

		if len(src2) == 0 || (w.opts.AllFieldsAreMessages && len(src2) < len(delimited)) {
			fields := builder.String()
			lines := strings.Count(fields, "\n")
			if lines <= 1 && len(src2) == 0 {
				fields = strings.TrimSpace(fields)
			}

			fmt.Fprint(w.dest, fields)
			if len(src2) > 0 {
				w.newLine()
				w.dumpHexString(src2)
			}

			w.indent--
			if lines > 1 || len(src2) > 0 {
				w.newLine()
			}
			fmt.Fprint(w.dest, "}")
			return src, true
		}

		// Otherwise, maybe it's a UTF-8 string.
		if !w.opts.NoQuotedStrings {
			runes := utf8.RuneCount(delimited)
			if runes == -1 {
				goto justBytes
			}

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
			fmt.Fprint(w.dest, "\"")

			for i, r := range s {
				if i != 0 && i%80 == 0 {
					fmt.Fprint(w.dest, "\"")
					w.newLine()
					fmt.Fprint(w.dest, "\"")
				}

				switch r {
				case '\n':
					fmt.Fprint(w.dest, "\\n")
				case '\\':
					fmt.Fprint(w.dest, "\\\\")
				case '"':
					fmt.Fprint(w.dest, "\\\"")
				default:
					if !unicode.IsGraphic(r) {
						enc := make([]byte, 4)
						enc = enc[:utf8.EncodeRune(enc, r)]
						for _, b := range enc {
							fmt.Fprintf(w.dest, "\\x%02x", b)
						}
					} else {
						fmt.Fprintf(w.dest, "%c", r)
					}
				}
			}

			fmt.Fprint(w.dest, "\"")
			w.indent--
			if runes > 80 {
				w.newLine()
			}
			fmt.Fprint(w.dest, "}")
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
		fmt.Fprint(w.dest, "}")
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
