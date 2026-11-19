// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Command askiso-cabi builds the AskISO C-ABI shared library (-buildmode=c-shared)
// consumed by polyrepo language SDKs (Python, Java, Rust) for zero-drift
// offline ISO 20022 validation, linting, and translation.
package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"unsafe"

	"github.com/sebastienrousseau/askiso/pkg/iso20022/ffi"
)

//export askiso_abi_version
func askiso_abi_version() *C.char {
	return C.CString(ffi.ABIVersion)
}

//export askiso_call
func askiso_call(op *C.char, payload *C.char) *C.char {
	var goOp string
	if op != nil {
		goOp = C.GoString(op)
	}
	var goPayload []byte
	if payload != nil {
		goPayload = []byte(C.GoString(payload))
	}
	res := ffi.Dispatch(goOp, goPayload)
	return C.CString(string(res))
}

//export askiso_free
func askiso_free(ptr *C.char) {
	if ptr != nil {
		C.free(unsafe.Pointer(ptr))
	}
}

func main() {}
