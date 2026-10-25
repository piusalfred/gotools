// Copyright (c) 2026 Pius Alfred
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package command

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	urfave "github.com/urfave/cli/v3"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// demo builds a command whose steps record themselves, so the lifecycle can be
// checked through urfave/cli.
func demo(steps_ *[]string, fail map[string]error, extra ...urfave.Flag) *urfave.Command {
	step := func(name string) func(context.Context, *urfave.Command) error {
		return func(context.Context, *urfave.Command) error {
			*steps_ = append(*steps_, name)

			return fail[name]
		}
	}

	return Spec{
		Name: "demo", Usage: "Demo.", Flags: extra,
		Before: step("before"), Run: step("run"), After: step("after"),
	}.Build(&Context{Program: "gotools"})
}

func runDemo(uc *urfave.Command, args ...string) error {
	return uc.Run(context.Background(), append([]string{"demo"}, args...))
}

func TestSpecRunsBeforeRunAfterInOrder(t *testing.T) {
	var got []string

	if err := runDemo(demo(&got, nil)); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(got, []string{"before", "run", "after"}) {
		t.Fatalf("steps = %v", got)
	}
}

// urfave/cli runs After even when Action failed; a command's after step must
// not, so a failed run is not followed by the work that assumes it succeeded.
func TestAfterRunsOnlyWhenRunSucceeded(t *testing.T) {
	boom := errors.New("boom")

	for step, want := range map[string][]string{
		"before": {"before"},
		"run":    {"before", "run"},
		"after":  {"before", "run", "after"},
	} {
		var got []string

		if err := runDemo(demo(&got, map[string]error{step: boom})); !errors.Is(err, boom) {
			t.Fatalf("%s: err = %v", step, err)
		}

		if !slices.Equal(got, want) {
			t.Errorf("a failing %s ran %v, want %v", step, got, want)
		}
	}
}

func TestMissingStepsAreSkipped(t *testing.T) {
	ran := false

	uc := Spec{Name: "demo", Run: func(context.Context, *urfave.Command) error { ran = true; return nil }}.Build(&Context{})
	if err := runDemo(uc); err != nil || !ran {
		t.Fatalf("err=%v ran=%v", err, ran)
	}

	if err := runDemo(Spec{Name: "demo"}.Build(&Context{})); err != nil {
		t.Fatal(err)
	}
}

func TestDeclareFillsInTheProgramAndWrapsTheProse(t *testing.T) {
	uc := Spec{
		Name:        "demo",
		Description: "Run {prog} " + strings.Repeat("word ", 40) + "\n\n  {prog} --example",
		Examples:    []string{"demo --x"},
	}.Build(&Context{Program: "gx"})

	if !strings.HasPrefix(uc.Description, "Run gx word") || !strings.Contains(uc.Description, "\n\n  gx --example") {
		t.Fatalf("description:\n%s", uc.Description)
	}

	for line := range strings.SplitSeq(uc.Description, "\n") {
		if len(line) > helpWidth-descriptionIndent {
			t.Errorf("line of %d: %q", len(line), line)
		}
	}

	if got := uc.Metadata["examples"]; !slices.Equal(got.([]string), []string{"gx demo --x"}) {
		t.Fatalf("examples = %v", got)
	}
}

func TestUsageErrorMapsParseFailuresToExitTwo(t *testing.T) {
	var got []string

	cases := map[string]struct {
		args []string
		want string
	}{
		"unknown flag":  {[]string{"--frobnicate"}, "Unknown argument: --frobnicate\n   Usage: demo"},
		"unknown short": {[]string{"-z"}, "Unknown argument: -z\n   Usage: demo"},
	}

	for name, c := range cases {
		err := runDemo(demo(&got, nil), c.args...)
		if ierrors.CodeOf(err) != ierrors.Usage || err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: err = %v (code %d)", name, err, ierrors.CodeOf(err))
		}
	}
}

func TestAFlagValidatorFailureIsAUsageError(t *testing.T) {
	var got []string

	uc := demo(&got, nil, &urfave.StringFlag{Name: "level", Validator: func(string) error { return errors.New("too loud") }})

	err := runDemo(uc, "--level=11")
	if ierrors.CodeOf(err) != ierrors.Usage || !strings.Contains(err.Error(), "too loud") || !strings.Contains(err.Error(), "-level") {
		t.Fatalf("err = %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("steps ran after an invalid flag: %v", got)
	}
}

func TestNoExtraArgs(t *testing.T) {
	uc := Spec{Name: "demo", Before: func(_ context.Context, cmd *urfave.Command) error { return noExtraArgs(cmd) }}.Build(&Context{Program: "gotools"})

	if err := runDemo(uc); err != nil {
		t.Fatalf("no args: %v", err)
	}

	err := runDemo(uc, "extra")
	if ierrors.CodeOf(err) != ierrors.Usage || !strings.HasPrefix(err.Error(), "Unknown argument: extra\n   Usage: demo") {
		t.Fatalf("err = %v", err)
	}
}

func TestUsageLine(t *testing.T) {
	cases := []struct {
		spec Spec
		want string
	}{
		{Spec{Name: "list"}, "demo"},
		{Spec{Name: "x", Flags: []urfave.Flag{&urfave.BoolFlag{Name: "f"}}}, "demo [options]"},
		{Spec{Name: "x", ArgsUsage: "[name] <pkg>"}, "demo [name] <pkg>"},
		{Spec{Name: "x", ArgsUsage: "<tool>", Flags: []urfave.Flag{&urfave.BoolFlag{Name: "f"}}}, "demo [options] <tool>"},
	}

	for _, c := range cases {
		var line string

		uc := c.spec.Build(&Context{})
		uc.Name = "demo"
		uc.Before = func(ctx context.Context, cmd *urfave.Command) (context.Context, error) {
			line = usageLine(cmd)

			return ctx, nil
		}

		if err := runDemo(uc); err != nil || line != c.want {
			t.Errorf("usageLine = %q (%v), want %q", line, err, c.want)
		}
	}
}

// Everything a Spec declares reaches the urfave/cli command.
func TestSpecBuildCarriesEveryDeclaration(t *testing.T) {
	flag := &urfave.BoolFlag{Name: "f"}
	arg := &urfave.StringArgs{Name: "spec", Min: 1, Max: 2}

	uc := Spec{
		Name: "demo", Aliases: []string{"d"}, Usage: "Demo.", ArgsUsage: "<spec>",
		Flags: []urfave.Flag{flag}, Args: []urfave.Argument{arg},
	}.Build(&Context{Program: "gx"})

	if uc.Name != "demo" || !slices.Equal(uc.Aliases, []string{"d"}) || uc.Usage != "Demo." || uc.ArgsUsage != "<spec>" {
		t.Fatalf("names: %+v", uc)
	}

	if len(uc.Flags) != 1 || uc.Flags[0] != urfave.Flag(flag) || len(uc.Arguments) != 1 {
		t.Fatalf("flags %v, arguments %v", uc.Flags, uc.Arguments)
	}

	if uc.CustomHelpTemplate != helpTemplate || uc.OnUsageError == nil || uc.Action == nil {
		t.Fatalf("shared setup missing: %+v", uc)
	}

	if !uc.HasName("d") {
		t.Fatal("the alias does not match")
	}
}

// A step gets the parsed command, so it reads the flags and arguments the user
// gave.
func TestStepsReadFlagsAndArguments(t *testing.T) {
	var got string

	uc := Spec{
		Name:  "demo",
		Flags: []urfave.Flag{&urfave.StringFlag{Name: "level"}},
		Args:  []urfave.Argument{&urfave.StringArgs{Name: "spec", Min: 1, Max: 2}},
		Run: func(_ context.Context, cmd *urfave.Command) error {
			got = cmd.String("level") + "|" + strings.Join(cmd.StringArgs("spec"), ",")

			return nil
		},
	}.Build(&Context{})

	if err := runDemo(uc, "--level=3", "a", "b"); err != nil || got != "3|a,b" {
		t.Fatalf("err=%v got=%q", err, got)
	}
}
