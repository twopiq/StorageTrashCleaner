//go:build windows

package main

// Tizim papkalari va xavfsizlik tekshiruvlari: dastur hech qachon Windows
// papkalarini, foydalanuvchi profilini yoki umumiy papkalarni o'chirmasligi kerak.

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

type sysPaths struct {
	Windows, System32, ProgramFiles, ProgramFilesX86, ProgramData, Users, Profile string
	Roaming, Local, LocalLow, LocalPrograms, Temp, WinTemp                        string
	StartMenu, CommonStartMenu, Desktop, PublicDesktop, QuickLaunch, Tasks        string

	// protected — bu papkalarning o'zi ham, ularning ota-papkalari ham o'chirilmaydi.
	protected []string
	// leftoverRoots — qoldiq papkalar qidiriladigan joylar (faqat ularning ichidagilar).
	leftoverRoots []string
}

var sys sysPaths

func loadSysDirs() {
	s := &sys
	s.Windows = knownFolder(windows.FOLDERID_Windows)
	if s.Windows == "" {
		s.Windows = os.Getenv("SystemRoot")
	}
	s.System32 = filepath.Join(s.Windows, "System32")
	s.ProgramFiles = firstNonEmpty(knownFolder(windows.FOLDERID_ProgramFilesX64), os.Getenv("ProgramW6432"), os.Getenv("ProgramFiles"))
	s.ProgramFilesX86 = firstNonEmpty(knownFolder(windows.FOLDERID_ProgramFilesX86), os.Getenv("ProgramFiles(x86)"))
	s.ProgramData = firstNonEmpty(knownFolder(windows.FOLDERID_ProgramData), os.Getenv("ProgramData"))
	s.Users = knownFolder(windows.FOLDERID_UserProfiles)
	s.Profile = firstNonEmpty(knownFolder(windows.FOLDERID_Profile), os.Getenv("USERPROFILE"))
	s.Roaming = firstNonEmpty(knownFolder(windows.FOLDERID_RoamingAppData), os.Getenv("APPDATA"))
	s.Local = firstNonEmpty(knownFolder(windows.FOLDERID_LocalAppData), os.Getenv("LOCALAPPDATA"))
	s.LocalLow = knownFolder(windows.FOLDERID_LocalAppDataLow)
	s.LocalPrograms = knownFolder(windows.FOLDERID_UserProgramFiles)
	if s.LocalPrograms == "" && s.Local != "" {
		s.LocalPrograms = filepath.Join(s.Local, "Programs")
	}
	s.Temp = os.TempDir()
	s.WinTemp = filepath.Join(s.Windows, "Temp")
	s.StartMenu = knownFolder(windows.FOLDERID_Programs)
	s.CommonStartMenu = knownFolder(windows.FOLDERID_CommonPrograms)
	s.Desktop = knownFolder(windows.FOLDERID_Desktop)
	s.PublicDesktop = knownFolder(windows.FOLDERID_PublicDesktop)
	if s.Roaming != "" {
		s.QuickLaunch = filepath.Join(s.Roaming, `Microsoft\Internet Explorer\Quick Launch`)
	}
	s.Tasks = filepath.Join(s.System32, "Tasks")

	drive := filepath.VolumeName(s.Windows) + `\`
	s.protected = nonEmpty(drive, s.Windows, s.ProgramFiles, s.ProgramFilesX86, s.ProgramData, s.Users,
		s.Profile, s.Roaming, s.Local, s.LocalLow, s.LocalPrograms, s.Temp, s.WinTemp, s.StartMenu,
		s.CommonStartMenu, s.Desktop, s.PublicDesktop, s.QuickLaunch,
		filepath.Join(s.ProgramFiles, "Common Files"), filepath.Join(s.ProgramFilesX86, "Common Files"),
		filepath.Join(s.ProgramFiles, "WindowsApps"), filepath.Join(s.ProgramData, "Microsoft"),
		filepath.Join(s.Roaming, "Microsoft"), filepath.Join(s.Local, "Microsoft"),
		filepath.Join(s.Local, "Packages"), filepath.Join(s.ProgramData, "Package Cache"),
		knownFolder(windows.FOLDERID_Documents), knownFolder(windows.FOLDERID_Downloads),
		knownFolder(windows.FOLDERID_Pictures), knownFolder(windows.FOLDERID_Music),
		knownFolder(windows.FOLDERID_Videos), knownFolder(windows.FOLDERID_Public),
		knownFolder(windows.FOLDERID_PublicDocuments), knownFolder(windows.FOLDERID_SavedGames),
		knownFolder(windows.FOLDERID_Startup), knownFolder(windows.FOLDERID_CommonStartup),
		filepath.Join(s.Profile, "OneDrive"))
	s.leftoverRoots = nonEmpty(s.ProgramFiles, s.ProgramFilesX86, s.ProgramData, s.Roaming, s.Local,
		s.LocalLow, s.LocalPrograms, filepath.Join(s.ProgramFiles, "Common Files"),
		filepath.Join(s.ProgramFilesX86, "Common Files"))
	if s.ProgramFiles == s.ProgramFilesX86 { // 32-bit Windows
		s.leftoverRoots = nonEmpty(s.ProgramFiles, s.ProgramData, s.Roaming, s.Local, s.LocalLow,
			s.LocalPrograms, filepath.Join(s.ProgramFiles, "Common Files"))
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if v != "" {
			out = addUniquePath(out, filepath.Clean(v))
		}
	}
	return out
}

// isSafeDir papkani o'chirish xavfsizmi: u tizim papkasi, foydalanuvchi profili
// yoki ularning ota-papkasi bo'lmasligi, Windows papkasi ichida bo'lmasligi kerak.
func isSafeDir(p string) bool {
	if p == "" || !filepath.IsAbs(p) || strings.HasPrefix(p, `\\`) {
		return false
	}
	p = filepath.Clean(p)
	vol := filepath.VolumeName(p)
	rest := strings.Trim(p[len(vol):], `\`)
	if rest == "" { // disk ildizi
		return false
	}
	if isUnderOrSame(p, sys.Windows) || isUnderOrSame(p, filepath.Join(sys.ProgramFiles, "WindowsApps")) {
		return false
	}
	for _, prot := range sys.protected {
		// Himoyalangan papkaning o'zi yoki uning ota-papkasi bo'lsa — xavfli.
		if isUnderOrSame(prot, p) {
			return false
		}
	}
	// Foydalanuvchi profillarining o'zi (C:\Users\X) va undan yuqori.
	if sys.Users != "" && samePath(filepath.Dir(p), sys.Users) {
		return false
	}
	// Disk ildizidagi bir darajali papka (D:\Games) faqat Windows o'rnatilmagan
	// disklarda ruxsat etiladi; C:\ ildizidagi papkalar (C:\Intel, C:\PerfLogs) emas.
	if !strings.Contains(rest, `\`) && strings.EqualFold(vol, filepath.VolumeName(sys.Windows)) {
		return false
	}
	return true
}

// protectedRegPath registr kaliti umumiy (tizim) kalitimi — bularni hech qachon o'chirmaymiz.
func protectedRegPath(path string) bool {
	p := strings.ToLower(strings.Trim(path, `\`))
	switch p {
	case "", "software", `software\wow6432node`, `software\classes`, `software\microsoft`,
		`software\policies`, `software\clients`, `software\registeredapplications`,
		`software\wow6432node\microsoft`, `software\wow6432node\classes`, `software\wow6432node\policies`,
		`software\wow6432node\clients`, `software\wow6432node\registeredapplications`,
		`software\microsoft\windows\currentversion\uninstall`,
		`software\wow6432node\microsoft\windows\currentversion\uninstall`,
		`software\microsoft\windows\currentversion\run`, `system`, `system\currentcontrolset`,
		`system\currentcontrolset\services`, `software\classes\installer\products`,
		`software\classes\installer\features`:
		return true
	}
	// Software\Microsoft\... va Software\Classes\... ichidagi kalitlarni faqat aniq
	// yo'l bilan (Uninstall yozuvi, MSI yozuvi) o'chiramiz, nom bo'yicha emas.
	return strings.Count(p, `\`) < 1
}

// isRootLevelDir C:\xampp kabi disk ildizidagi bir darajali papka, tizim
// papkasi bo'lmasa. Faqat ilovaning o'z InstallLocation'i uchun ishlatiladi.
func isRootLevelDir(p string) bool {
	if p == "" || !filepath.IsAbs(p) || strings.HasPrefix(p, `\\`) {
		return false
	}
	p = filepath.Clean(p)
	vol := filepath.VolumeName(p)
	rest := strings.Trim(p[len(vol):], `\`)
	if rest == "" || strings.Contains(rest, `\`) || isUnderOrSame(p, sys.Windows) {
		return false
	}
	for _, prot := range sys.protected {
		if isUnderOrSame(prot, p) {
			return false
		}
	}
	switch strings.ToLower(rest) {
	case "users", "windows", "program files", "program files (x86)", "programdata", "perflogs",
		"recovery", "boot", "efi", "intel", "amd", "nvidia", "drivers", "temp", "tmp", "$recycle.bin",
		"system volume information", "documents and settings", "onedrivetemp", "msocache":
		return false
	}
	return true
}
