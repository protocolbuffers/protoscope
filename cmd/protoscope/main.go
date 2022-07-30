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

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	_ "embed"

	"github.com/protocolbuffers/protoscope"
)

var (
	outPath  = flag.String("o", "", "output file to use (defaults to stdout)")
	assemble = flag.Bool("s", false, "whether to treat the input as a Protoscope source file")
	spec     = flag.Bool("spec", false, "opens the Protoscope spec in $PAGER")

	noQuotedStrings        = flag.Bool("no-quoted-strings", false, "assume no fields in the input proto are strings")
	allFieldsAreMessages   = flag.Bool("all-fields-are-messages", false, "try really hard to disassemble all fields as messages")
	explicitWireTypes      = flag.Bool("explicit-wire-types", false, "include an explicit wire type for every field")
	noGroups               = flag.Bool("no-groups", false, "do not try to disassemble groups")
	explicitLengthPrefixes = flag.Bool("explicit-length-prefixes", false, "emit literal length prefixes instead of braces")
)

func main() {
	if err := Main(); err != nil {
		fmt.Fprintln(os.Stderr, "protoscope:", err)
		os.Exit(1)
	}
}

func Main() error {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [-s] [OPTION...] [INPUT]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Assemble a Protoscope file to binary, or inspect binary data as Protoscope text.\n")
		fmt.Fprintf(os.Stderr, "Run with -spec to learn more about the Protoscope language.\n\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(1)
	}

	if *spec {
		pager := os.Getenv("PAGER")
		if pager == "" {
			return fmt.Errorf("%s", protoscope.LanguageTxt)
			return nil
		}

		cmd := exec.Command(pager)
		cmd.Stdout = os.Stdout
		cmd.Stdin = strings.NewReader(protoscope.LanguageTxt)
		if err := cmd.Run(); err != nil {
			return err
		}
		return nil
	}

	inPath := ""
	inFile := os.Stdin
	if flag.NArg() == 1 {
		inPath = flag.Arg(0)
		var err error
		inFile, err = os.Open(inPath)
		if err != nil {
			return err
		}
		defer inFile.Close()
	}

	inBytes, err := io.ReadAll(inFile)
	if err != nil {
		return err
	}

	var outBytes []byte
	if *assemble {
		scanner := protoscope.NewScanner(string(inBytes))
		scanner.SetFile(inPath)

		outBytes, err = scanner.Exec()
		if err != nil {
			return fmt.Errorf("syntax error: %s\n", err)
			os.Exit(1)
		}
	} else {
		outBytes = []byte(protoscope.Write(inBytes, protoscope.WriterOptions{
			NoQuotedStrings:        *noQuotedStrings,
			AllFieldsAreMessages:   *allFieldsAreMessages,
			ExplicitWireTypes:      *explicitWireTypes,
			NoGroups:               *noGroups,
			ExplicitLengthPrefixes: *explicitLengthPrefixes,
		}))
	}

	outFile := os.Stdout
	if *outPath != "" {
		var err error
		outFile, err = os.Create(*outPath)
		if err != nil {
			return err
		}
		defer outFile.Close()
	}

	_, err = outFile.Write(outBytes)
	return err
}
