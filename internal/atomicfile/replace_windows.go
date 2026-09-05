// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

//go:build windows

package atomicfile

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// A reader that has the destination open without FILE_SHARE_DELETE, which is
// how Go opens files for reading, makes MoveFileEx fail with a sharing
// violation or access denied until it closes. Readers are short-lived, so the
// replace is retried for a bounded window rather than failed at once.
const (
	replaceRetryFor   = 5 * time.Second
	replaceRetryStart = time.Millisecond
	replaceRetryMax   = 50 * time.Millisecond
)

func replace(oldPath, newPath string) error {
	from, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(replaceRetryFor)
	wait := replaceRetryStart
	for {
		err = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if err == nil || !transient(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(wait)
		if wait *= 2; wait > replaceRetryMax {
			wait = replaceRetryMax
		}
	}
}

func transient(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// Transient reports whether an error from opening or replacing a published
// file is a passing sharing conflict worth retrying. On Windows a reader can
// be refused for the instant the destination is being swapped, and a writer
// can be refused while a reader still holds the old file; neither is a
// partial publication, and both clear once the other side finishes.
func Transient(err error) bool { return transient(err) }

// MOVEFILE_WRITE_THROUGH waits for the move to be flushed before returning.
func syncDirectory(string) error { return nil }
