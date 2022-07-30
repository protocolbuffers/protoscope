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
	"bytes"
	"testing"
)

var desc = GetDesc("unittest.TestAllTypes")

func FuzzRoundTrip(f *testing.F) {
	f.Fuzz(func(t *testing.T, in []byte) {
		if len(in) == 0 {
			return
		}
		useSchema := in[0]&1 == 0
		in = in[1:]

		var opts WriterOptions
		if useSchema {
			opts.Schema = desc
		}

		text := Write(in, opts)
		out, err := NewScanner(text).Exec()

		if err != nil {
			t.Fatalf("%x: scan of %q failed: %s", in, text, err)
		}
		if !bytes.Equal(in, out) {
			t.Fatalf("%x: not equal after round trip through %q: %x", in, text, out)
		}
	})
}
