//go:build windows

package main

// Windows API bilan ishlash uchun past darajali yordamchilar.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	advapi32             = windows.NewLazySystemDLL("advapi32.dll")
	procRegDeleteTreeW   = advapi32.NewProc("RegDeleteTreeW")
	user32               = windows.NewLazySystemDLL("user32.dll")
	procSendMessageTimeW = user32.NewProc("SendMessageTimeoutW")
	shell32              = windows.NewLazySystemDLL("shell32.dll")
	procSHFileOperationW = shell32.NewProc("SHFileOperationW")
	msi                  = windows.NewLazySystemDLL("msi.dll")
	procMsiQueryProduct  = msi.NewProc("MsiQueryProductStateW")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procSetConsoleTitleW = kernel32.NewProc("SetConsoleTitleW")
)

// ---------- Administrator huquqi ----------

func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID, windows.DOMAIN_ALIAS_RID_ADMINS, 0, 0, 0, 0, 0, 0, &sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)
	member, err := windows.Token(0).IsMember(sid)
	return err == nil && member
}

// relaunchAsAdmin dasturni UAC orqali administrator sifatida qayta ishga tushiradi.
func relaunchAsAdmin() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	args := make([]string, 0, len(os.Args)-1)
	for _, a := range os.Args[1:] {
		args = append(args, syscall.EscapeArg(a))
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(args, " "))
	dir, _ := windows.UTF16PtrFromString(cwd)
	return windows.ShellExecute(0, verb, file, params, dir, windows.SW_NORMAL)
}

func setConsoleTitle(title string) {
	p, _ := windows.UTF16PtrFromString(title)
	procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(p)))
}

// ---------- Registr ----------

// regRef registrdagi kalitni to'liq ko'rsatadi (ildiz, yo'l va 32/64-bit ko'rinish).
type regRef struct {
	Root   registry.Key
	Path   string
	Access uint32 // registry.WOW64_64KEY yoki registry.WOW64_32KEY yoki 0
}

func rootName(k registry.Key) string {
	switch k {
	case registry.LOCAL_MACHINE:
		return "HKLM"
	case registry.CURRENT_USER:
		return "HKCU"
	case registry.CLASSES_ROOT:
		return "HKCR"
	case registry.USERS:
		return "HKU"
	}
	return "HK?"
}

func (r regRef) String() string {
	s := rootName(r.Root) + `\` + r.Path
	if r.Access == registry.WOW64_32KEY {
		s += " (32-bit)"
	}
	return s
}

func (r regRef) open(access uint32) (registry.Key, error) {
	return registry.OpenKey(r.Root, r.Path, access|r.Access)
}

func keyExists(r regRef) bool {
	k, err := r.open(registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

func subKeys(r regRef) []string {
	k, err := r.open(registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	return names
}

func regString(k registry.Key, name string) string {
	s, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	if strings.Contains(s, "%") {
		s = expandEnv(s)
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "\x00"))
}

func regInt(k registry.Key, name string) uint64 {
	v, _, err := k.GetIntegerValue(name)
	if err != nil {
		return 0
	}
	return v
}

func parentKey(path string) (string, string) {
	i := strings.LastIndexByte(path, '\\')
	if i < 0 {
		return "", path
	}
	return path[:i], path[i+1:]
}

// deleteKeyTree kalitni barcha ichki kalitlari bilan o'chiradi.
func deleteKeyTree(r regRef) error {
	parent, name := parentKey(r.Path)
	pk, err := registry.OpenKey(r.Root, parent, registry.ALL_ACCESS|r.Access)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	defer pk.Close()
	n, _ := windows.UTF16PtrFromString(name)
	ret, _, _ := procRegDeleteTreeW.Call(uintptr(pk), uintptr(unsafe.Pointer(n)))
	if ret != 0 && syscall.Errno(ret) != windows.ERROR_FILE_NOT_FOUND {
		return syscall.Errno(ret)
	}
	err = registry.DeleteKey(pk, name)
	if err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// pruneEmptyKey kalit bo'sh qolgan bo'lsa (qiymat va ichki kalitsiz) uni o'chiradi.
func pruneEmptyKey(r regRef) {
	k, err := r.open(registry.QUERY_VALUE | registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return
	}
	info, err := k.Stat()
	k.Close()
	if err == nil && info.SubKeyCount == 0 && info.ValueCount == 0 && !protectedRegPath(r.Path) {
		deleteKeyTree(r)
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) ||
		errors.Is(err, os.ErrNotExist)
}

// exportKey kalitni .reg faylga zaxiralaydi (reg.exe orqali).
func exportKey(r regRef, file string) error {
	args := []string{"export", rootName(r.Root) + `\` + r.Path, file, "/y"}
	switch r.Access {
	case registry.WOW64_32KEY:
		args = append(args, "/reg:32")
	case registry.WOW64_64KEY:
		args = append(args, "/reg:64")
	}
	_, err := runHidden("reg.exe", args...)
	return err
}

// ---------- Fayllar ----------

func exists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Lstat(p)
	return err == nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// isReparse papka simlink yoki junction ekanini tekshiradi (ularning ichiga kirmaymiz).
func isReparse(fi os.FileInfo) bool {
	if fi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return d.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
	}
	return false
}

// dirStats papka hajmi, fayllar soni va eng oxirgi o'zgarish vaqtini hisoblaydi.
func dirStats(root string, limit time.Duration) (size int64, files int, newest time.Time) {
	deadline := time.Now().Add(limit)
	var walk func(string)
	walk = func(dir string) {
		if time.Now().After(deadline) {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil {
				continue
			}
			if fi.ModTime().After(newest) {
				newest = fi.ModTime()
			}
			if isReparse(fi) {
				continue
			}
			if fi.IsDir() {
				walk(filepath.Join(dir, e.Name()))
			} else {
				size += fi.Size()
				files++
			}
		}
	}
	walk(root)
	return
}

// scheduleDeleteOnReboot band faylni kompyuter qayta yuklanganda o'chirishga belgilaydi.
func scheduleDeleteOnReboot(p string) error {
	from, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}

type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

// moveToRecycleBin faylni yoki papkani Savatga (Recycle Bin) yuboradi.
func moveToRecycleBin(p string) error {
	const (
		foDelete          = 0x3
		fofSilent         = 0x4
		fofNoConfirmation = 0x10
		fofAllowUndo      = 0x40
		fofNoErrorUI      = 0x400
	)
	u := utf16.Encode([]rune(p))
	u = append(u, 0, 0) // ikki marta NUL bilan tugaydigan ro'yxat
	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &u[0],
		fFlags: fofSilent | fofNoConfirmation | fofAllowUndo | fofNoErrorUI,
	}
	ret, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if ret != 0 {
		return syscall.Errno(ret)
	}
	if op.fAnyOperationsAborted != 0 {
		return errors.New("Savatga yuborish to'xtatildi")
	}
	if exists(p) {
		return errors.New("Savatga yuborib bo'lmadi")
	}
	return nil
}

// ---------- Jarayonlar ----------

type procInfo struct {
	PID  uint32
	Name string
	Path string
}

func listProcesses() []procInfo {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var out []procInfo
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		p := procInfo{PID: pe.ProcessID, Name: windows.UTF16ToString(pe.ExeFile[:])}
		if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pe.ProcessID); err == nil {
			buf := make([]uint16, windows.MAX_LONG_PATH)
			n := uint32(len(buf))
			if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
				p.Path = windows.UTF16ToString(buf[:n])
			}
			windows.CloseHandle(h)
		}
		out = append(out, p)
	}
	return out
}

// killProcessesUnder dirs ichidan ishga tushgan jarayonlarni to'xtatadi.
func killProcessesUnder(dirs []string) []string {
	self := uint32(os.Getpid())
	var killed []string
	for _, p := range listProcesses() {
		if p.PID == self || p.Path == "" || !underAny(p.Path, dirs) {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, p.PID)
		if err != nil {
			continue
		}
		if windows.TerminateProcess(h, 1) == nil {
			windows.WaitForSingleObject(h, 5000)
			killed = append(killed, p.Name)
		}
		windows.CloseHandle(h)
	}
	return killed
}

// ---------- Buyruqlar ----------

// runHidden buyruqni konsol oynasisiz ishga tushiradi va natijasini qaytaradi.
func runHidden(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	return decodeText(out), err
}

// runHiddenRaw buyruq satrini o'zgartirmasdan (CmdLine orqali) ishga tushiradi.
func runHiddenRaw(cmdline string) (string, error) {
	cmd := exec.Command(exePathFromCommand(cmdline))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CmdLine: cmdline, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	return decodeText(out), err
}

// decodeText UTF-16 (BOM bilan) yoki UTF-8 matnni o'qiydi.
func decodeText(b []byte) string {
	if len(b) >= 2 && ((b[0] == 0xFF && b[1] == 0xFE) || (len(b) >= 4 && b[1] == 0 && b[3] == 0)) {
		if b[0] == 0xFF && b[1] == 0xFE {
			b = b[2:]
		}
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		return string(utf16.Decode(u))
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	return string(b)
}

// broadcastEnvChange boshqa dasturlarga PATH o'zgarganini xabar qiladi.
func broadcastEnvChange() {
	const hwndBroadcast = 0xFFFF
	const wmSettingChange = 0x001A
	const smtoAbortIfHung = 0x0002
	env, _ := windows.UTF16PtrFromString("Environment")
	var res uintptr
	procSendMessageTimeW.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)),
		smtoAbortIfHung, 3000, uintptr(unsafe.Pointer(&res)))
}

// msiInstalled MSI mahsuloti haqiqatan o'rnatilganligini Windows Installer'dan so'raydi.
func msiInstalled(code string) bool {
	if code == "" || procMsiQueryProduct.Find() != nil {
		return false
	}
	p, _ := windows.UTF16PtrFromString(code)
	ret, _, _ := procMsiQueryProduct.Call(uintptr(unsafe.Pointer(p)))
	state := int32(ret)
	// INSTALLSTATE_DEFAULT(5), LOCAL(3), SOURCE(4), ADVERTISED(1)
	return state == 5 || state == 3 || state == 4 || state == 1
}

// ---------- Ma'lum papkalar ----------

func knownFolder(id *windows.KNOWNFOLDERID) string {
	p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return ""
	}
	return p
}
