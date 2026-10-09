//go:build windows

package main

// Ilovaning rasmiy o'chiruvchisini ishga tushirish (iloji bo'lsa jim rejimda)
// va u (hamda u ochgan barcha jarayonlar) tugashini kutish.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type uninstallResult int

const (
	unOK uninstallResult = iota
	unReboot
	unAlreadyGone
	unCancelled
	unBusy
	unFailed
	unNoUninstaller
)

// silentBySignature o'rnatuvchi turini fayl ichidagi belgilardan aniqlaydi.
func silentBySignature(exe string) (args, kind string) {
	f, err := os.Open(exe)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	buf := make([]byte, 4*1024*1024)
	n, _ := f.Read(buf)
	buf = buf[:n]
	switch {
	case bytes.Contains(buf, []byte("Inno Setup")):
		return "/VERYSILENT /SUPPRESSMSGBOXES /NORESTART", "jim (Inno Setup)"
	case bytes.Contains(buf, []byte("Nullsoft")) || bytes.Contains(buf, []byte("NSIS")):
		return "/S", "jim (NSIS)"
	}
	return "", ""
}

// buildUninstallCommand o'chirish buyrug'ini tuzadi va qaysi rejimda ishlashini aytadi.
func buildUninstallCommand(a *App) (cmd, mode string) {
	if a.MSICode != "" {
		return fmt.Sprintf(`msiexec.exe /x %s /qn /norestart`, a.MSICode), "jim (Windows Installer)"
	}
	if a.QuietUninstallString != "" {
		return a.QuietUninstallString, "jim (QuietUninstallString)"
	}
	cmd = a.UninstallString
	exe := exePathFromCommand(cmd)
	lower := strings.ToLower(cmd)
	if strings.HasSuffix(strings.ToLower(a.KeyName), "_is1") || strings.Contains(strings.ToLower(exe), `\unins0`) {
		if !strings.Contains(lower, "/verysilent") {
			cmd += " /VERYSILENT /SUPPRESSMSGBOXES /NORESTART"
		}
		return cmd, "jim (Inno Setup)"
	}
	if strings.Contains(lower, " /s") || strings.Contains(lower, "/silent") || strings.Contains(lower, "--silent") {
		return cmd, "jim"
	}
	if args, kind := silentBySignature(exe); args != "" {
		return cmd + " " + args, kind
	}
	return cmd, "oddiy (o'chiruvchi oynasi ochiladi)"
}

// quoteCommand yo'lida bo'sh joy bo'lgan, qo'shtirnoqsiz buyruqni tuzatadi.
func quoteCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || cmd[0] == '"' {
		return cmd
	}
	exe := exePathFromCommand(cmd)
	if strings.Contains(exe, " ") && exists(exe) {
		return `"` + exe + `"` + cmd[len(exe):]
	}
	return cmd
}

type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

// runAndWaitTree buyruqni ishga tushiradi va u hamda u yaratgan barcha
// jarayonlar tugashini kutadi (NSIS/Inno o'chiruvchilari o'zini nusxalab qayta ishga tushiradi).
func runAndWaitTree(cmdline string, timeout time.Duration) (int, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err == nil {
		defer windows.CloseHandle(job)
	}
	exe := exePathFromCommand(cmdline)
	c := exec.Command(exe)
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdline}
	if err := c.Start(); err != nil {
		return -1, err
	}
	if job != 0 {
		if h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid)); err == nil {
			if windows.AssignProcessToJobObject(job, h) != nil {
				windows.CloseHandle(job)
				job = 0
			}
			windows.CloseHandle(h)
		}
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	deadline := time.After(timeout)
	var waitErr error
	select {
	case waitErr = <-done:
	case <-deadline:
		return -1, fmt.Errorf("vaqt tugadi (%v)", timeout)
	}
	code := 0
	if c.ProcessState != nil {
		code = c.ProcessState.ExitCode()
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return code, waitErr
	}
	if job == 0 {
		return code, nil
	}
	// Ichki (bola) jarayonlar tugashini kutamiz.
	for {
		var info jobAccounting
		if windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil) != nil || info.ActiveProcesses == 0 {
			return code, nil
		}
		select {
		case <-deadline:
			return code, fmt.Errorf("vaqt tugadi (%v)", timeout)
		case <-time.After(700 * time.Millisecond):
		}
	}
}

// runOfficialUninstaller ilovaning o'z o'chiruvchisini ishga tushiradi.
func runOfficialUninstaller(a *App, logf func(string)) uninstallResult {
	if a.Kind == KindStore {
		logf("Store paketi o'chirilmoqda: " + a.PackageFullName)
		out, err := runHidden("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
			"$ErrorActionPreference='Stop'; Remove-AppxPackage -Package '"+strings.ReplaceAll(a.PackageFullName, "'", "''")+"'")
		if err != nil {
			if !stillInstalled(a) {
				logf("Store paketi allaqachon o'chirilgan")
				return unAlreadyGone
			}
			logf("Store paketini o'chirib bo'lmadi: " + psErrorText(out))
			return unFailed
		}
		logf("Store paketi o'chirildi")
		return unOK
	}
	if a.UninstallString == "" && a.QuietUninstallString == "" && a.MSICode == "" {
		logf("Rasmiy o'chiruvchi ko'rsatilmagan.")
		return unNoUninstaller
	}
	if !stillInstalled(a) {
		logf("allaqachon o'chirilgan (oldingi o'chiruvchi olib tashlagan) - faqat qoldiqlar tekshiriladi")
		return unAlreadyGone
	}
	cmd, mode := buildUninstallCommand(a)
	cmd = quoteCommand(cmd)
	exe := exePathFromCommand(cmd)
	if a.MSICode == "" && strings.Contains(exe, `\`) && !exists(exe) {
		logf("O'chiruvchi fayli topilmadi: " + exe)
		return unNoUninstaller
	}
	logf("Rejim: " + mode)
	logf("Buyruq: " + cmd)
	if strings.HasPrefix(mode, "oddiy") {
		logf("O'chiruvchi oynasi ochiladi — undagi ko'rsatmalarga amal qiling.")
	}
	logf("Rasmiy o'chiruvchi ishga tushirilmoqda...")
	code, err := runAndWaitTree(cmd, 30*time.Minute)
	if err != nil {
		logf("o'chiruvchini ishga tushirib bo'lmadi: " + err.Error())
		return unFailed
	}
	switch code {
	case 0:
		logf("o'chiruvchi muvaffaqiyatli tugadi")
	case 3010, 1641:
		logf("o'chiruvchi tugadi (kompyuterni qayta yuklash tavsiya etiladi)")
		return unReboot
	case 1605:
		logf("Windows Installer: mahsulot allaqachon o'chirilgan")
		return unAlreadyGone
	case 1602, 1223:
		logf("o'chiruvchi bekor qilindi")
		return unCancelled
	case 1618:
		logf("boshqa o'rnatish/o'chirish jarayoni ketmoqda (MSI band, kod 1618)")
		return unBusy
	default:
		logf(fmt.Sprintf("o'chiruvchi %d kodi bilan tugadi", code))
	}
	// Ba'zi o'chiruvchilar registrni biroz kechikib tozalaydi.
	for i := 0; i < 10 && stillInstalled(a); i++ {
		time.Sleep(time.Second)
	}
	if stillInstalled(a) {
		if code == 0 {
			return unCancelled
		}
		return unFailed
	}
	return unOK
}

func psErrorText(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "At line") && !strings.HasPrefix(line, "+") {
			return line
		}
	}
	return strings.TrimSpace(out)
}

// forceUninstall rasmiy o'chiruvchi ishlamasa: jarayonlarni to'xtatadi va
// o'chirish yozuvini qoldiqlar ro'yxatiga qo'shish uchun tayyorlaydi.
func forceUninstallPrep(a *App, logf func(string)) {
	if dirs := a.installDirs(); len(dirs) > 0 {
		if killed := killProcessesUnder(dirs); len(killed) > 0 {
			logf("To'xtatilgan jarayonlar: " + strings.Join(killed, ", "))
		}
	}
}
