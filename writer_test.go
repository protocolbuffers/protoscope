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
	"embed"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// To regenerate these tests, go to this directory and run
// for f in *.pb; do ../protoscope $f > $f.golden; done
//go:embed testdata/*
var testdata embed.FS

func TestGoldens(t *testing.T) {
	type golden struct {
		name string
		pb   []byte
		want string
	}

	var tests []golden
	dir, err := testdata.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dir {
		if !strings.HasSuffix(d.Name(), ".pb") {
			continue
		}

		pb, err := testdata.ReadFile("testdata/" + d.Name())
		if err != nil {
			t.Fatal(err)
		}
		goldenText, err := testdata.ReadFile("testdata/" + d.Name() + ".golden")
		if err != nil {
			t.Fatal(err)
		}

		tests = append(tests, golden{d.Name(), pb, string(goldenText)})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Write(tt.pb, WriterOptions{})
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Fatal("output mismatch (-want, +got):", d)
			}
		})
	}
}
