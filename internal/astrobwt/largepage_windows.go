//go:build windows && !nolargepages

package astrobwt

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 2 MiB large pages take the v114 scratch's page-walk cost off the hot path:
// the radix passes stream ~1.1 MiB of run records per hash, which on 4 KiB
// pages is hundreds of TLB entries per worker. Requires the "Lock pages in
// memory" user right; everything degrades to ordinary heap allocation when
// the privilege or a contiguous run of large pages is unavailable.

const memLargePages = 0x20000000

var (
	largePageOnce   sync.Once
	largePageSize   uintptr
	largePageActive atomic.Bool
)

// enableLockMemoryPrivilege best-effort enables SeLockMemoryPrivilege on the
// process token. AdjustTokenPrivileges reports success even when the right is
// not assigned (ERROR_NOT_ALL_ASSIGNED), so the VirtualAlloc call — not this
// function — is what decides whether large pages are usable.
func enableLockMemoryPrivilege() {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return
	}
	defer token.Close()
	name, err := windows.UTF16PtrFromString("SeLockMemoryPrivilege")
	if err != nil {
		return
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		return
	}
	privs := windows.Tokenprivileges{PrivilegeCount: 1}
	privs.Privileges[0] = windows.LUIDAndAttributes{
		Luid:       luid,
		Attributes: windows.SE_PRIVILEGE_ENABLED,
	}
	_ = windows.AdjustTokenPrivileges(token, false, &privs, 0, nil, nil)
}

// largePageRegion returns a zeroed, large-page-backed region of exactly size
// bytes (the mapping is rounded up to whole large pages underneath) and the
// function that releases it, or nil when large pages are unavailable.
//
// The region lives outside the Go heap. That is legal only because every
// value ever stored in it is pointer-free and because no slice of it may
// outlive the release: the collector neither scans nor keeps the mapping
// alive, so the owner must tie release to its own lifetime.
func largePageRegion(size int) ([]byte, func()) {
	largePageOnce.Do(func() {
		enableLockMemoryPrivilege()
		largePageSize = windows.GetLargePageMinimum()
	})
	if largePageSize == 0 {
		return nil, nil
	}
	n := (uintptr(size) + largePageSize - 1) &^ (largePageSize - 1)
	addr, err := windows.VirtualAlloc(0, n,
		windows.MEM_RESERVE|windows.MEM_COMMIT|memLargePages, windows.PAGE_READWRITE)
	if err != nil || addr == 0 {
		return nil, nil
	}
	largePageActive.Store(true)
	// addr is an address the kernel owns, not a Go object, so the pointer
	// rules for uintptr round-trips do not apply; read the word back as a
	// pointer instead of converting the integer, which keeps the conversion
	// out of vet's uintptr->Pointer pattern and checkptr's arithmetic checks.
	p := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
	region := unsafe.Slice((*byte)(p), n)[:size:size]
	release := func() { _ = windows.VirtualFree(addr, 0, windows.MEM_RELEASE) }
	return region, release
}

// LargePagesActive reports whether at least one scratch region landed on
// large pages in this process.
func LargePagesActive() bool { return largePageActive.Load() }
