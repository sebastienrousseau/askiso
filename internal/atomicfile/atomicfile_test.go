// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package atomicfile

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestConcurrentReadersOnlyObserveCompletePublications(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "publication.json")
	first := append([]byte(`{"generation":1,"payload":"`), bytes.Repeat([]byte("A"), 1<<16)...)
	first = append(first, []byte(`"}`)...)
	second := append([]byte(`{"generation":2,"payload":"`), bytes.Repeat([]byte("B"), 1<<16)...)
	second = append(second, []byte(`"}`)...)
	if err := Write(path, first, 0o600); err != nil {
		t.Fatal(err)
	}

	// Windows readers hold the destination open without FILE_SHARE_DELETE, so
	// a writer can only swap the file in the gaps between reads. Thirty-two
	// readers spinning leave no gap at all; a few readers that pause between
	// reads still exercise every interleaving that matters.
	readers, pause := 32, time.Duration(0)
	if runtime.GOOS == "windows" {
		readers, pause = 4, time.Millisecond
	}
	const writes = 100
	stop := make(chan struct{})
	errs := make(chan string, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := os.ReadFile(path)
				if Transient(err) {
					// Windows refuses the open for the instant of the swap.
					// That is not a partial read, which is what this test
					// is about; the next attempt sees a complete file.
					continue
				}
				if err != nil {
					errs <- err.Error()
					return
				}
				if !bytes.Equal(got, first) && !bytes.Equal(got, second) {
					errs <- "reader observed a partial or mixed publication"
					return
				}
				if pause > 0 {
					time.Sleep(pause)
				}
			}
		}()
	}
	for i := range writes {
		data := first
		if i%2 == 0 {
			data = second
		}
		if err := Write(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".publication.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestWriteFailureLeavesExistingDestinationIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "publication")
	if err := os.WriteFile(path, []byte("complete"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(filepath.Join(dir, "missing", "publication"), []byte("replacement"), 0o600); err == nil {
		t.Fatal("write below a missing directory succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "complete" {
		t.Fatalf("existing destination changed to %q", got)
	}
}
