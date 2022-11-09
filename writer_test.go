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
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	descpb "google.golang.org/protobuf/types/descriptorpb"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

//go:embed testdata/*
var testdata embed.FS

var fileset = ParseFileSet()

func ParseFileSet() *protoregistry.Files {
	data, err := testdata.ReadFile("testdata/unittest.proto.pb")
	if err != nil {
		panic(err)
	}

	fds := new(descpb.FileDescriptorSet)
	if err := proto.Unmarshal(data, fds); err != nil {
		panic(err)
	}

	files, err := protodesc.NewFiles(fds)
	if err != nil {
		panic(err)
	}

	return files
}

func GetDesc(name string) protoreflect.MessageDescriptor {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		desc, err = fileset.FindDescriptorByName(protoreflect.FullName(name))
	}
	if err != nil {
		panic(err)
	}

	return desc.(protoreflect.MessageDescriptor)
}

func TestGoldens(t *testing.T) {
	type golden struct {
		name   string
		pb     []byte
		want   string
		config string
		opts   WriterOptions
	}

	var tests []golden
	dir, err := testdata.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dir {
		if !strings.HasSuffix(d.Name(), ".golden") {
			continue
		}

		goldenBytes, err := testdata.ReadFile("testdata/" + d.Name())
		if err != nil {
			t.Fatal(err)
		}
		goldenText := string(goldenBytes)

		// Pull off the first line, which must be a comment.
		comment, rest, _ := strings.Cut(goldenText, "\n")
		goldenText = rest

		config := strings.Fields(strings.TrimPrefix(comment, "#"))

		pb, err := testdata.ReadFile("testdata/" + config[0])
		if err != nil {
			t.Fatal(err)
		}

		opts := WriterOptions{}
		v := reflect.ValueOf(&opts).Elem()
		for _, opt := range config[1:] {
			if name := strings.TrimPrefix(opt, "Schema="); name != opt {
				opts.Schema = GetDesc(name)
				continue
			}

			v.FieldByName(opt).SetBool(true)
		}

		tests = append(tests, golden{
			name:   d.Name(),
			pb:     pb,
			want:   goldenText,
			config: comment,
			opts:   opts,
		})
	}

	if _, ok := os.LookupEnv("REGEN_GOLDENS"); ok {
		for _, tt := range tests {
			got := Write(tt.pb, tt.opts)
			f, _ := os.Create("testdata/" + tt.name)
			defer f.Close()

			fmt.Fprintln(f, tt.config)
			fmt.Fprint(f, got)
		}
		return
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Write(tt.pb, tt.opts)
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Fatal("output mismatch (-want, +got):", d)
			}
		})
	}
}
