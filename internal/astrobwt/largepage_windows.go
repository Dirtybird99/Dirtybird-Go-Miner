//go:build windows

package astrobwt

import (
	"sync"
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
	largePageKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	procLargePageMin  = largePageKernel32.NewProc("GetLargePageMinimum")

	largePageOnce   sync.Once
	largePageSize   uintptr
	largePageActive bool
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

// largePageAlloc returns a zeroed large-page-backed region of at least size
// bytes, or nil when large pages are unavailable. The region is deliberately
// never freed: callers are per-worker scratches that live for the process.
func largePageAlloc(size uintptr) []byte {
	largePageOnce.Do(func() {
		enableLockMemoryPrivilege()
		minSize, _, _ := procLargePageMin.Call()
		largePageSize = minSize
	})
	if largePageSize == 0 {
		return nil
	}
	n := (size + largePageSize - 1) &^ (largePageSize - 1)
	addr, err := windows.VirtualAlloc(0, n,
		windows.MEM_RESERVE|windows.MEM_COMMIT|memLargePages, windows.PAGE_READWRITE)
	if err != nil || addr == 0 {
		return nil
	}
	largePageActive = true
	return unsafe.Slice((*byte)(unsafe.Pointer(addr)), n)
}

// LargePagesActive reports whether at least one scratch allocation landed on
// large pages this process.
func LargePagesActive() bool { return largePageActive }
