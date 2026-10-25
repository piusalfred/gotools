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

package fsx

import "os"

// Snapshot remembers the content of some files so a step that may fail can put
// them back, or remove them if they did not exist.
type Snapshot struct {
	files []string
	data  map[string][]byte // absent key: the file did not exist
}

// TakeSnapshot reads files now. A file that cannot be read is treated as absent.
func TakeSnapshot(files ...string) *Snapshot {
	s := &Snapshot{files: files, data: map[string][]byte{}}

	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			s.data[f] = b
		}
	}

	return s
}

// Restore puts every file back as it was when the snapshot was taken, writing
// each atomically, and removes the ones that did not exist then.
func (s *Snapshot) Restore() {
	for _, f := range s.files {
		if b, ok := s.data[f]; ok {
			_ = WriteFileAtomic(f, b, 0o644)

			continue
		}

		_ = os.Remove(f)
	}
}
