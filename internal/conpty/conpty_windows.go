//go:build windows

// Adapted from github.com/charmbracelet/x/conpty v0.1.1 (MIT License,
// Copyright (c) Charmbracelet, Inc). The ConPTY syscalls go through the
// backend resolved in dll_windows.go instead of x/sys's kernel32-only
// wrappers; see the package doc for why.

package conpty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Default size.
const (
	DefaultWidth  = 80
	DefaultHeight = 25
)

// ConPty represents a Windows Console Pseudo-terminal.
// https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session#preparing-the-communication-channels
type ConPty struct {
	hpc                 *windows.Handle
	inPipeFd, outPipeFd windows.Handle
	inPipe, outPipe     *os.File
	attrList            *windows.ProcThreadAttributeListContainer
	size                windows.Coord
	closeOnce           sync.Once
}

var (
	_ io.Writer = &ConPty{}
	_ io.Reader = &ConPty{}
)

// packCoord packs a COORD into the single machine word that the
// (Conpty)Create/ResizePseudoConsole calling convention expects (x/sys
// zsyscall passes COORDs the same way).
func packCoord(c windows.Coord) uintptr {
	return uintptr(uint32(uint16(c.X)) | uint32(uint16(c.Y))<<16)
}

// New creates a new ConPty device with the given size. Unlike x/conpty's New
// there is no flags parameter: we always pass 0, and which ConPTY backs the
// console is chosen by the package (see dll_windows.go), not per call.
func New(w int, h int) (c *ConPty, err error) {
	b := getBackend()
	if b.err != nil {
		return nil, b.err
	}

	if w <= 0 {
		w = DefaultWidth
	}
	if h <= 0 {
		h = DefaultHeight
	}

	c = &ConPty{
		hpc: new(windows.Handle),
		size: windows.Coord{
			X: int16(w), Y: int16(h),
		},
	}

	// Release everything created so far on any failure path. x/conpty's New
	// leaks pipe handles / the HPCON / the attribute list when a later step
	// fails; cleaning up here keeps a failed New from leaking kernel handles.
	// ptyIn/ptyOut are zeroed once handed to the pseudo-console so the deferred
	// CloseHandle on them is a no-op after that point.
	var ptyIn, ptyOut windows.Handle
	ok := false
	defer func() {
		if ok {
			return
		}
		windows.CloseHandle(ptyIn)
		windows.CloseHandle(ptyOut)
		windows.CloseHandle(c.inPipeFd)
		windows.CloseHandle(c.outPipeFd)
		if *c.hpc != 0 {
			_, _, _ = b.close.Call(uintptr(*c.hpc))
		}
		if c.attrList != nil {
			c.attrList.Delete()
		}
	}()

	if err := windows.CreatePipe(&ptyIn, &c.inPipeFd, nil, 0); err != nil {
		return nil, fmt.Errorf("failed to create pipes for pseudo console: %w", err)
	}

	if err := windows.CreatePipe(&c.outPipeFd, &ptyOut, nil, 0); err != nil {
		return nil, fmt.Errorf("failed to create pipes for pseudo console: %w", err)
	}

	r0, _, _ := b.create.Call(packCoord(c.size), uintptr(ptyIn), uintptr(ptyOut), 0, uintptr(unsafe.Pointer(c.hpc)))
	if err := hresult(r0); err != nil {
		return nil, fmt.Errorf("failed to create pseudo console: %w", err)
	}

	// We don't need the pty pipes anymore, these will get dup'd when the
	// new process starts. Zero the handles so the deferred cleanup does not
	// double-close them.
	if err := windows.CloseHandle(ptyOut); err != nil {
		return nil, fmt.Errorf("failed to close pseudo console handle: %w", err)
	}
	ptyOut = 0
	if err := windows.CloseHandle(ptyIn); err != nil {
		return nil, fmt.Errorf("failed to close pseudo console handle: %w", err)
	}
	ptyIn = 0

	c.inPipe = os.NewFile(uintptr(c.inPipeFd), "|0")
	c.outPipe = os.NewFile(uintptr(c.outPipeFd), "|1")

	// Allocate an attribute list that's large enough to do the operations we care about
	// 1. Pseudo console setup
	c.attrList, err = windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}

	// The dll's HPCON shares its ABI with the OS one, so the same attribute
	// works regardless of which backend created the handle. The reinterpret
	// (rather than unsafe.Pointer(*c.hpc)) passes the handle value as the
	// attribute without a uintptr->Pointer conversion vet would flag.
	if err := c.attrList.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		*(*unsafe.Pointer)(unsafe.Pointer(c.hpc)),
		unsafe.Sizeof(*c.hpc),
	); err != nil {
		return nil, fmt.Errorf("failed to update proc thread attributes for pseudo console: %w", err)
	}

	ok = true // success: the deferred cleanup must not run
	return
}

// Fd returns the ConPty handle.
func (p *ConPty) Fd() uintptr {
	return uintptr(*p.hpc)
}

// Close closes the ConPty device. Note: with the dll backend this does NOT
// terminate an attached child process (the dll's close only releases handles);
// callers that want kill semantics must terminate the child themselves, which
// the agent manager already does via cmd.Process.Kill.
func (p *ConPty) Close() error {
	var err error
	p.closeOnce.Do(func() {
		if p.attrList != nil {
			p.attrList.Delete()
		}
		// kernel32's ClosePseudoConsole returns void; the dll's Conpty
		// variant returns an HRESULT we have no recovery for. Ignore both.
		_, _, _ = getBackend().close.Call(uintptr(*p.hpc))
		err = errors.Join(p.inPipe.Close(), p.outPipe.Close())
	})
	return err
}

// Write safely writes bytes to the ConPty.
func (c *ConPty) Write(p []byte) (n int, err error) {
	var l uint32
	err = windows.WriteFile(c.inPipeFd, p, &l, nil)
	return int(l), err
}

// Read safely reads bytes from the ConPty.
func (c *ConPty) Read(p []byte) (n int, err error) {
	var l uint32
	err = windows.ReadFile(c.outPipeFd, p, &l, nil)
	return int(l), err
}

// Resize resizes the pseudo-console.
func (c *ConPty) Resize(w int, h int) error {
	size := windows.Coord{X: int16(w), Y: int16(h)}
	r0, _, _ := getBackend().resize.Call(uintptr(*c.hpc), packCoord(size))
	if err := hresult(r0); err != nil {
		return fmt.Errorf("failed to resize pseudo console: %w", err)
	}
	c.size = size
	return nil
}

// Size returns the current pseudo-console size.
func (c *ConPty) Size() (w int, h int, err error) {
	w = int(c.size.X)
	h = int(c.size.Y)
	return
}

var zeroAttr syscall.ProcAttr

// Spawn spawns a new process attached to the pseudo-console.
func (c *ConPty) Spawn(name string, args []string, attr *syscall.ProcAttr) (pid int, handle uintptr, err error) {
	if attr == nil {
		attr = &zeroAttr
	}

	argv0, err := lookExtensions(name, attr.Dir)
	if err != nil {
		return 0, 0, err
	}
	if len(attr.Dir) != 0 {
		// Windows CreateProcess looks for argv0 relative to the current
		// directory, and, only once the new process is started, it does
		// Chdir(attr.Dir). We are adjusting for that difference here by
		// making argv0 absolute.
		var err error
		argv0, err = joinExeDirAndFName(attr.Dir, argv0)
		if err != nil {
			return 0, 0, err
		}
	}

	argv0p, err := windows.UTF16PtrFromString(argv0)
	if err != nil {
		return 0, 0, err
	}

	var cmdline string
	if attr.Sys != nil && attr.Sys.CmdLine != "" {
		cmdline = attr.Sys.CmdLine
	} else {
		cmdline = windows.ComposeCommandLine(args)
	}
	argvp, err := windows.UTF16PtrFromString(cmdline)
	if err != nil {
		return 0, 0, err
	}

	var dirp *uint16
	if len(attr.Dir) != 0 {
		dirp, err = windows.UTF16PtrFromString(attr.Dir)
		if err != nil {
			return 0, 0, err
		}
	}

	if attr.Env == nil {
		attr.Env, err = execEnvDefault(attr.Sys)
		if err != nil {
			return 0, 0, err
		}
	}

	siEx := new(windows.StartupInfoEx)
	siEx.Flags = windows.STARTF_USESTDHANDLES

	pi := new(windows.ProcessInformation)

	// Need EXTENDED_STARTUPINFO_PRESENT as we're making use of the attribute list field.
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT) | windows.EXTENDED_STARTUPINFO_PRESENT
	if attr.Sys != nil && attr.Sys.CreationFlags != 0 {
		flags |= attr.Sys.CreationFlags
	}

	var zeroSec windows.SecurityAttributes
	pSec := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(zeroSec)), InheritHandle: 1}
	if attr.Sys != nil && attr.Sys.ProcessAttributes != nil {
		pSec = &windows.SecurityAttributes{
			Length:        attr.Sys.ProcessAttributes.Length,
			InheritHandle: attr.Sys.ProcessAttributes.InheritHandle,
		}
	}
	tSec := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(zeroSec)), InheritHandle: 1}
	if attr.Sys != nil && attr.Sys.ThreadAttributes != nil {
		tSec = &windows.SecurityAttributes{
			Length:        attr.Sys.ThreadAttributes.Length,
			InheritHandle: attr.Sys.ThreadAttributes.InheritHandle,
		}
	}

	siEx.ProcThreadAttributeList = c.attrList.List() //nolint:govet // unusedwrite: ProcThreadAttributeList will be read in syscall
	siEx.Cb = uint32(unsafe.Sizeof(*siEx))
	if attr.Sys != nil && attr.Sys.Token != 0 {
		err = windows.CreateProcessAsUser(
			windows.Token(attr.Sys.Token),
			argv0p,
			argvp,
			pSec,
			tSec,
			false,
			flags,
			createEnvBlock(addCriticalEnv(dedupEnvCase(true, attr.Env))),
			dirp,
			&siEx.StartupInfo,
			pi,
		)
	} else {
		err = windows.CreateProcess(
			argv0p,
			argvp,
			pSec,
			tSec,
			false,
			flags,
			createEnvBlock(addCriticalEnv(dedupEnvCase(true, attr.Env))),
			dirp,
			&siEx.StartupInfo,
			pi,
		)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create process: %w", err)
	}

	defer windows.CloseHandle(pi.Thread)

	// node-pty useConptyDll semantics: release the \Reference handle right
	// after the (single) child is attached, so OpenConsole exits naturally
	// once the child does and our read on the output pipe sees EOF
	// (ERROR_BROKEN_PIPE) with all output drained. After this no further
	// clients can attach to the HPCON, which is fine — we spawn exactly one
	// child per pseudo-console. kernel32 has no such export (release is nil).
	if b := getBackend(); b.release != nil {
		_, _, _ = b.release.Call(uintptr(*c.hpc))
	}

	return int(pi.ProcessId), uintptr(pi.Process), nil
}
