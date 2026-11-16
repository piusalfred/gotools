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

package versioncheck_test

import (
	"testing"

	"github.com/piusalfred/gotools/internal/versioncheck"
)

func TestCompare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		running, recorded string
		want              versioncheck.Drift
	}{
		{"same", "v0.7.1", "v0.7.1", versioncheck.None},
		{"older patch recorded", "v0.7.2", "v0.7.1", versioncheck.WrittenByOlder},
		{"newer patch recorded", "v0.7.1", "v0.7.2", versioncheck.WrittenByNewer},
		{"newer minor recorded", "v0.7.1", "v0.8.0", versioncheck.WrittenByNewer},
		{"older minor recorded", "v0.8.0", "v0.7.9", versioncheck.WrittenByOlder},
		{"older major recorded", "v2.0.0", "v1.9.0", versioncheck.WrittenByOlder},
		{"prerelease is before its release", "v0.8.0-rc.1", "v0.8.0", versioncheck.WrittenByNewer},
		{"dev build running", "dev", "v0.8.0", versioncheck.None},
		{"nothing recorded", "v0.8.0", "", versioncheck.None},
		{"garbage recorded", "v0.8.0", "banana", versioncheck.None},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := versioncheck.Compare(tt.running, tt.recorded); got != tt.want {
				t.Fatalf("Compare(%q, %q) = %v, want %v", tt.running, tt.recorded, got, tt.want)
			}
		})
	}
}

func TestStampNeverLowersWhatAFileRecords(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, running, recorded, want string }{
		{"first write", "v0.7.1", "", "v0.7.1"},
		{"upgrade", "v0.8.0", "v0.7.1", "v0.8.0"},
		{"older gotools keeps the later stamp", "v0.7.1", "v0.8.0", "v0.8.0"},
		{"dev build keeps what is there", "dev", "v0.7.1", "v0.7.1"},
		{"dev build with nothing", "dev", "", ""},
		{"garbage recorded is replaced", "v0.7.1", "banana", "v0.7.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := versioncheck.Stamp(tt.running, tt.recorded); got != tt.want {
				t.Fatalf("Stamp(%q, %q) = %q, want %q", tt.running, tt.recorded, got, tt.want)
			}
		})
	}
}
