// Package conpty implements Windows pseudo-console (ConPTY) support with a
// dynamically selected backend.
//
// Adapted from github.com/charmbracelet/x/conpty v0.1.1 (MIT License,
// Copyright (c) Charmbracelet, Inc). The fork exists because x/conpty reaches
// ConPTY through golang.org/x/sys/windows, which hardwires kernel32.dll — so
// users are stuck with whatever ConPTY is frozen into their OS build. Old
// builds (notably Windows 10) have wide-char (DBCS/CJK) rendering bugs that
// leave ghost characters in our vt emulator, and repaint in place instead of
// scrolling, which leaves scrollback empty.
//
// This package can instead LoadLibrary a newer redistributable conpty.dll
// (paired with OpenConsole.exe) placed next to myagents.exe — the VS Code /
// node-pty "useConptyDll" approach — and falls back to kernel32 when absent.
// The binaries ship in the MIT-licensed "Microsoft.Windows.Console.ConPTY"
// NuGet package. See dll_windows.go for the resolution order and the
// MYAGENTS_CONPTY / MYAGENTS_CONPTY_DLL overrides; Backend reports which
// implementation ended up active.
package conpty
