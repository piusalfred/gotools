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

package command_test

import (
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
)

// Every command is documented, and its examples start with its own name.
func TestEveryCommandIsDocumented(t *testing.T) {
	seen := map[string]bool{}

	for _, cmd := range command.All() {
		uc := cmd.Build(&command.Context{Program: "gotools"})

		if uc.Name == "" || uc.Name != cmd.Name() {
			t.Errorf("%T: Name() %q, declared %q", cmd, cmd.Name(), uc.Name)
		}

		if uc.Usage == "" || uc.Description == "" || uc.Action == nil {
			t.Errorf("%s is not fully declared: %+v", uc.Name, uc)
		}

		examples, _ := uc.Metadata["examples"].([]string)
		if len(examples) == 0 {
			t.Errorf("%s has no examples", uc.Name)
		}

		for _, e := range examples {
			if !strings.HasPrefix(e, "gotools "+uc.Name) && !strings.HasPrefix(e, "gotools exec ") {
				t.Errorf("%s: example %q does not start with the command", uc.Name, e)
			}
		}

		if seen[uc.Name] {
			t.Errorf("%s is declared twice", uc.Name)
		}

		seen[uc.Name] = true
	}
}

func TestNamesAreTheConstants(t *testing.T) {
	want := map[string]bool{command.NameInit: true, command.NameInstall: true, command.NameSync: true, command.NameMigrate: true, command.NameVersion: true, command.NameInfo: true, command.NameCheck: true, command.NameConfig: true, command.NameRemove: true, command.NameRename: true, command.NameUpgrade: true, command.NameList: true, command.NameExec: true, command.NameUninstall: true, command.NamePurge: true, command.NameDoctor: true, command.NameSelfUpdate: true}

	for _, cmd := range command.All() {
		if !want[cmd.Name()] {
			t.Errorf("%q is not one of the Name constants", cmd.Name())
		}

		delete(want, cmd.Name())
	}

	if len(want) != 0 {
		t.Errorf("no command is named %v", want)
	}
}

// Every flag a command reads is one it declares.
func TestReadFlagsAreDeclared(t *testing.T) {
	declared := func(cmd command.Command) map[string]bool {
		out := map[string]bool{}
		for _, f := range cmd.Build(&command.Context{}).Flags {
			out[f.Names()[0]] = true
		}

		return out
	}

	init := declared(command.NewInit())
	for _, name := range []string{command.FlagStrategy, command.FlagDir, command.FlagGo, command.FlagPrefix, command.FlagNoMigrate, command.FlagDryRun} {
		if !init[name] {
			t.Errorf("init reads --%s but does not declare it", name)
		}
	}

	install := declared(command.NewInstall())
	for _, name := range []string{command.FlagForce, command.FlagOffline} {
		if !install[name] {
			t.Errorf("install reads --%s but does not declare it", name)
		}
	}
}

func TestSyncReadsOnlyDeclaredFlags(t *testing.T) {
	declared := map[string]bool{}
	for _, f := range command.NewSync().Build(&command.Context{}).Flags {
		declared[f.Names()[0]] = true
	}

	for _, name := range []string{command.FlagDryRun, command.FlagOffline, command.FlagJobs} {
		if !declared[name] {
			t.Errorf("sync reads --%s but does not declare it", name)
		}
	}
}

func TestMigrateReadsOnlyDeclaredFlags(t *testing.T) {
	declared := map[string]bool{}
	for _, f := range command.NewMigrate().Build(&command.Context{}).Flags {
		declared[f.Names()[0]] = true
	}

	if !declared[command.FlagDryRun] {
		t.Error("migrate reads --dry-run but does not declare it")
	}
}

func TestVersionReadsOnlyDeclaredFlags(t *testing.T) {
	declared := map[string]bool{}
	for _, f := range command.NewVersion().Build(&command.Context{}).Flags {
		declared[f.Names()[0]] = true
	}

	for _, name := range []string{command.FlagFormat, command.FlagJSON, command.FlagText} {
		if !declared[name] {
			t.Errorf("version reads --%s but does not declare it", name)
		}
	}
}

func TestInfoReadsOnlyDeclaredFlags(t *testing.T) {
	declared := map[string]bool{}
	for _, f := range command.NewInfo().Build(&command.Context{}).Flags {
		declared[f.Names()[0]] = true
	}

	for _, name := range []string{command.FlagFormat, command.FlagJSON, command.FlagText} {
		if !declared[name] {
			t.Errorf("info reads --%s but does not declare it", name)
		}
	}
}
