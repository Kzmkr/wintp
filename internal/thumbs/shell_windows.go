package thumbs

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32   = windows.NewLazySystemDLL("ole32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")
	user32  = windows.NewLazySystemDLL("user32.dll")

	procCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	procCoUninitialize              = ole32.NewProc("CoUninitialize")
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	procDeleteObject                = gdi32.NewProc("DeleteObject")
	procGetDpiForSystem             = user32.NewProc("GetDpiForSystem")

	iidShellItemImageFactory = windows.GUID{Data1: 0xbcc18b79, Data2: 0xba16, Data3: 0x442f,
		Data4: [8]byte{0x80, 0xc4, 0x8a, 0x59, 0xc3, 0x0c, 0x46, 0x3b}}
)

const (
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4

	siigbfThumbnailOnly = 0x08 // never fall back to the file-type icon
	siigbfInCacheOnly   = 0x10 // only report what the thumbnail cache has
)

// imageFactory is the COM IShellItemImageFactory interface.
type imageFactory struct{ vtbl *imageFactoryVtbl }

type imageFactoryVtbl struct {
	QueryInterface, AddRef, Release, GetImage uintptr
}

// withCOM runs fn on a locked OS thread with COM initialised, as the shell
// thumbnail APIs require.
func withCOM(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	if int32(hr) >= 0 { // S_OK or S_FALSE
		defer procCoUninitialize.Call()
	}
	fn()
}

// getImage asks the Windows shell for path's thumbnail at size x size, the
// same way File Explorer does, so the result lands in (or comes from) the
// system thumbnail cache. Must run inside withCOM.
func getImage(path string, size int, flags uintptr) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var f *imageFactory
	hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&iidShellItemImageFactory)), uintptr(unsafe.Pointer(&f)))
	if int32(hr) < 0 || f == nil {
		return fmt.Errorf("open %s: HRESULT 0x%08x", path, uint32(hr))
	}
	defer syscall.SyscallN(f.vtbl.Release, uintptr(unsafe.Pointer(f)))

	// SIZE{cx, cy} is passed by value; on amd64/arm64 an 8-byte struct
	// travels in a single integer register.
	sz := uintptr(uint32(size)) | uintptr(uint32(size))<<32
	var hbm uintptr
	hr, _, _ = syscall.SyscallN(f.vtbl.GetImage, uintptr(unsafe.Pointer(f)), sz, flags,
		uintptr(unsafe.Pointer(&hbm)))
	if hbm != 0 {
		procDeleteObject.Call(hbm)
	}
	if int32(hr) < 0 {
		return fmt.Errorf("thumbnail %s: HRESULT 0x%08x", path, uint32(hr))
	}
	return nil
}

func shellHas(path string, size int) bool {
	return getImage(path, size, siigbfThumbnailOnly|siigbfInCacheOnly) == nil
}

func shellGenerate(path string, size int) error {
	return getImage(path, size, siigbfThumbnailOnly)
}

// DefaultSize is the thumbnail size Explorer's "Large icons" view asks for
// at the system DPI. A cached thumbnail also serves every smaller view.
func DefaultSize() int {
	dpi := 96
	if procGetDpiForSystem.Find() == nil {
		if d, _, _ := procGetDpiForSystem.Call(); d > 0 {
			dpi = int(d)
		}
	}
	size := 256 * dpi / 96
	return min(max(size, 256), 1024)
}
