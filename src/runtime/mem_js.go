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

// A node in a free list.
//
// TODO(twifkak): Maybe make a buddy allocator instead of a free list. Not sure
// how to do this cleanly atop an unbounded memory pool. Maybe free list is
// fine -- malloc.go minimizes the calls it makes to
// sysReserve/sysFree, so... not too much fragmentation should occur?
// TODO(twifkak): Do these need to be aligned? I'm not trying to align them
// anywhere.
//go:notinheap
type Free struct {
	size uintptr  // including this header's size
	next uintptr  // 0 == nullptr
}
var exampleFree = Free{}
var freeSize = func() uintptr { return unsafe.Sizeof(exampleFree) }
var freeHead *Free  // Pointer to the first node.

func printFreeNode(node *Free) {
	if node != nil {
		println(" + ", node, "\tsize", unsafe.Pointer(node.size), "\tnext", unsafe.Pointer(node.next))
	} else {
		println("[empty]")
	}
}

func printFreeList() {
	freeSpace := (uintptr)(0)
	for cur := freeHead; cur != nil; cur = (*Free)(unsafe.Pointer(cur.next)) {
		printFreeNode(cur)
		freeSpace += cur.size
	}
	println("end", unsafe.Pointer(uintptr(reserveEnd)))
	println("wasted", freeSpace)
}

// TODO(twifkak): Do I need to synchronize sysFree & sysReserve? I hope not.
// "Concurrent GC" doesn't mean concurrent with itself, I assume.

// Don't split the stack as this function may be invoked without a valid G,
// which prevents us from allocating more stack.
// Plus, I suspect bad things would happen if this were preempted by the GC.
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
	if freeHead == nil {
		freeHead = (*Free)(v)
		freeHead.size = n
		freeHead.next = 0
	} else {
		prev := freeHead
		for prev.next != 0 && prev.next < uintptr(v) {
			prev = (*Free)(unsafe.Pointer(prev.next))
		}
		cur := (*Free)(unsafe.Pointer(v))
		cur.size = n
		cur.next = prev.next
		prev.next = uintptr(v)
		// TODO(twifkak): Join adjacent free blocks.
	}
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

// I suspect bad things would happen if this were preempted by the GC.
//go:nosplit
func sysReserve(v unsafe.Pointer, n uintptr) unsafe.Pointer {
	// TODO(neelance): maybe unify with mem_linux.go, depending on how https://github.com/WebAssembly/design/blob/master/FutureFeatures.md#finer-grained-control-over-memory turns out

	println("sysReserve", v, unsafe.Pointer(n))

	if reserveEnd < lastmoduledatap.end {
		println("lastmoduledatap.end", unsafe.Pointer(lastmoduledatap.end))
		reserveEnd = lastmoduledatap.end + freeSize()
		if !growEnough() {
			println("can't grow")
			return nil
		}
		println("freeSize", freeSize())
		//freeHead = nil
	}

	// Try to allocate where requested.
	if v != nil {
		prev := freeHead
		cur := (*Free)(nil)
		if prev != nil {
			cur = (*Free)(unsafe.Pointer(prev.next))
		}
		if cur != nil {
			for cur.next != 0 && cur.next < uintptr(v) {
				prev = cur
				cur = (*Free)(unsafe.Pointer(cur.next))
			}
		}

		if cur != nil && uintptr(unsafe.Pointer(cur)) < uintptr(v) && uintptr(unsafe.Pointer(cur)) + cur.size > uintptr(v) + n + freeSize() {
			curPos := uintptr(unsafe.Pointer(cur))
			roomBefore := uintptr(v) - curPos
			roomAfter := curPos + cur.size - (uintptr(v) + n)

			println("splitting", unsafe.Pointer(roomBefore), unsafe.Pointer(roomAfter))
			next := cur.next
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
			println()
			return v
		}
	}

	// Try to find some free space in the middle.
	// TODO(twifkak): Maybe some non-greedy algorithm, to reduce fragmentation.
	prev := freeHead
	cur := (*Free)(nil)
	if prev != nil {
		cur = (*Free)(unsafe.Pointer(prev.next))
	}
	for cur != nil && cur.size < n {
		prev = cur
		cur = (*Free)(unsafe.Pointer(cur.next))
	}
	if cur != nil {
		println("allocating in middle", unsafe.Pointer(cur))
		v = unsafe.Pointer(cur)
		next := cur.next
		if cur.size > n + freeSize() {
			println("inserting after-node")
			after := (*Free)(unsafe.Pointer(uintptr(unsafe.Pointer(cur)) + n))
			after.size = cur.size - n
			after.next = next
			// TODO(twifkak): Adjoin with next, if not nil.
			next = uintptr(unsafe.Pointer(after))
		}
		prev.next = next
		printFreeList()
		println("returning", v)
		println()
		return v
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
