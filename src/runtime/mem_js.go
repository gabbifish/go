// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build js,wasm

package runtime

import (
	"runtime/internal/sys"
	"unsafe"
)

// Don't split the stack as this function may be invoked without a valid G,
// which prevents us from allocating more stack.
//go:nosplit
func sysAlloc(n uintptr, sysStat *uint64) unsafe.Pointer {
	p := sysReserve(nil, n)
	//println("sysAlloc", n, p)
	sysMap(p, n, sysStat)
	return p
}

func sysUnused(v unsafe.Pointer, n uintptr) {
	println("sysUnused", unsafe.Pointer(v), unsafe.Pointer(n))
}

func sysUsed(v unsafe.Pointer, n uintptr) {
	println("sysUsed", unsafe.Pointer(v), unsafe.Pointer(n))
}

// A node in a free list. The first node is at lastmoduledatap.end.
//
// TODO(twifkak): Maybe make a buddy allocator instead of a free list. Not sure
// how to do this cleanly atop an unbounded memory pool. Maybe free list is
// fine -- malloc.go minimizes the calls it makes to
// sysReserve/sysFree, so... not too much fragmentation should occur?
// TODO(twifkak): Do these need to be aligned? I'm not trying to align them
// anywhere.
//go:notinheap
type Free struct {
	size uintptr
	next uintptr  // 0 == nullptr
}
var exampleFree = Free{}
var freeSize = func() uintptr { return unsafe.Sizeof(exampleFree) }

func printFreeNode(node *Free) {
	println(" + ", node, "\tsize", unsafe.Pointer(node.size), "\tnext", unsafe.Pointer(node.next))
}

func printFreeList() {
	cur := (*Free)(unsafe.Pointer(lastmoduledatap.end))
	printFreeNode(cur)
	for cur.next != 0 {
		cur = (*Free)(unsafe.Pointer(cur.next))
		printFreeNode(cur)
	}
	println("end", unsafe.Pointer(uintptr(reserveEnd)))
}

// TODO(twifkak): Do I need to synchronize sysFree & sysReserve? I hope not.
// "Concurrent GC" doesn't mean concurrent with itself, I assume.

// Don't split the stack as this function may be invoked without a valid G,
// which prevents us from allocating more stack.
//go:nosplit
func sysFree(v unsafe.Pointer, n uintptr, sysStat *uint64) {
	println("sysFree", v, unsafe.Pointer(n))
	mSysStatDec(sysStat, n)
	if reserveEnd < lastmoduledatap.end {
		println("sysFee called before sysReserve; weird")
		return
	}
	// TODO(twifkak): Do tiny frees happen? How to handle?
	if n < freeSize() {
		return
	}
	prev := (*Free)(unsafe.Pointer(lastmoduledatap.end))
	for prev.next != 0 && prev.next < uintptr(v) {
		prev = (*Free)(unsafe.Pointer(prev.next))
	}
	cur := (*Free)(unsafe.Pointer(v))
	cur.size = n
	cur.next = prev.next
	prev.next = uintptr(v)
	// TODO(twifkak): Join adjacent free blocks.
	printFreeList()
	println()
}

func sysFault(v unsafe.Pointer, n uintptr) {
}

var reserveEnd uintptr

func growEnough() bool /*success*/ {
	current := currentMemory()
	needed := int32(reserveEnd/sys.DefaultPhysPageSize + 1)
	if current < needed {
		return growMemory(needed-current) != -1
	}
	return true
}

// TODO(twifkak): Does this need go:nosplit?
func sysReserve(v unsafe.Pointer, n uintptr) unsafe.Pointer {
	// TODO(neelance): maybe unify with mem_plan9.go, depending on how https://github.com/WebAssembly/design/blob/master/FutureFeatures.md#finer-grained-control-over-memory turns out

	println("sysReserve", v, unsafe.Pointer(n))

	if reserveEnd < lastmoduledatap.end {
		println("lastmoduledatap.end", unsafe.Pointer(lastmoduledatap.end))
		reserveEnd = lastmoduledatap.end + freeSize()
		if !growEnough() {
			println("can't grow")
			return nil
		}
		println("freeSize", freeSize())
		free := (*Free)(unsafe.Pointer(lastmoduledatap.end))
		free.size = 0  // The first entry cannot be allocated over.
		free.next = 0
	} else {
		printFreeList()
	}

	// TODO(twifkak): If v == nil, allocate at first available.
	prev := (*Free)(unsafe.Pointer(lastmoduledatap.end))
	cur := (*Free)(unsafe.Pointer(prev.next))
	if cur != nil {
		for cur.next != 0 && cur.next < uintptr(v) {
			prev = cur
			cur = (*Free)(unsafe.Pointer(cur.next))
		}
	}

	if cur != nil && uintptr(unsafe.Pointer(cur)) < uintptr(v) {
		println("trying to split")
		curPos := uintptr(unsafe.Pointer(cur))
		roomBefore := uintptr(v) - curPos
		roomAfter := curPos + cur.size - (uintptr(v) + n)
		next := cur.next
		if roomAfter >= freeSize() {
			println("splitting")
			// Split the free list around [v, v+n).
			after := (*Free)(unsafe.Pointer(uintptr(v) + n))
			after.size = roomAfter
			after.next = next
			afterPos := uintptr(unsafe.Pointer(after))
			if roomBefore >= freeSize() {
				cur.size = roomBefore
				cur.next = afterPos
			} else {
				prev.next = afterPos
			}
			printFreeList()
			println("returning", v)
			return v
		}
	}

	// Allocate at the end.
	println("allocating at end")
	v = unsafe.Pointer(reserveEnd)
	reserveEnd += n
	if !growEnough() {
		println("can't grow")
		return nil
	}
	printFreeList()
	println("returning", v)
	println()
	return v
}

func currentMemory() int32
func growMemory(pages int32) int32

func sysMap(v unsafe.Pointer, n uintptr, sysStat *uint64) {
	println("sysUsed", unsafe.Pointer(v), unsafe.Pointer(n))
	mSysStatInc(sysStat, n)
}
