//go:build windows

package main

// O'rnatilgan dasturlar ro'yxatini registrdan (Win32) va PowerShell orqali
// (Microsoft Store paketlari) yig'ish.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

type AppKind int

const (
	KindWin32 AppKind = iota
	KindStore
)

type App struct {
	Kind        AppKind
	Name        string
	Version     string
	Publisher   string
	InstallDate string
	SizeBytes   int64

	// Win32
	Key                  regRef // Uninstall kaliti
	KeyName              string
	InstallLocation      string
	UninstallString      string
	QuietUninstallString string
	DisplayIcon          string
	MSICode              string
	SystemComponent      bool
	PerUser              bool

	// Store
	PackageFullName   string
	PackageFamilyName string
	PackageName       string
}

func (a *App) ID() string {
	if a.Kind == KindStore {
		return "store:" + a.PackageFullName
	}
	return "win32:" + a.Key.String()
}

func (a *App) KindLabel() string {
	if a.Kind == KindStore {
		return "Store"
	}
	if a.MSICode != "" {
		return "MSI"
	}
	if a.PerUser {
		return "Foydal."
	}
	return "Win32"
}

// nameSet ilova bilan bog'liq papka va kalitlarni qidirish uchun nomlar.
func (a *App) nameSet() []string {
	names := nameSet(a.Name, a.Publisher, a.KeyName)
	if a.Kind == KindStore && a.PackageName != "" {
		names = addUnique(names, normName(a.PackageName))
		if i := strings.LastIndexByte(a.PackageName, '.'); i >= 0 {
			if n := normName(a.PackageName[i+1:]); usableName(n) {
				names = addUnique(names, n)
			}
		}
	}
	for _, d := range a.installDirs() {
		// Qisqa papka nomlari ("VLC") faqat ilova nomining boshlanishi bo'lsa.
		if n := normName(filepath.Base(d)); !genericNames[n] && (len(n) >= 4 || (len(n) == 3 && strings.HasPrefix(normName(a.Name), n))) {
			names = addUnique(names, n)
		}
	}
	return names
}

// publisherName ishlab chiqaruvchi papkasi nomi (masalan "Mozilla", "JetBrains").
func (a *App) publisherName() string {
	n := normName(cleanPublisher(a.Publisher))
	if usableName(n) {
		return n
	}
	return ""
}

// installDirs ilova o'rnatilgan papkalar (mavjud va o'chirish xavfsiz bo'lganlari).
func (a *App) installDirs() []string {
	// Store paketlari papkasini (WindowsApps) faqat Windows o'zi o'chiradi.
	if a.Kind == KindStore {
		return nil
	}
	var dirs []string
	add := func(d string, explicit bool) {
		d = strings.Trim(strings.TrimSpace(d), `"`)
		if d == "" || !filepath.IsAbs(d) {
			return
		}
		d = filepath.Clean(d)
		if !isSafeDir(d) && !(explicit && isRootAppDir(d, a)) {
			return
		}
		dirs = addUniquePath(dirs, d)
	}
	add(a.InstallLocation, true)
	own := nameSet(a.Name, a.Publisher, a.KeyName)
	// Taxminiy papkalar (o'chiruvchi yoki belgi fayli joylashgan joy) faqat umumiy
	// joy bo'lmasa va ishlab chiqaruvchining umumiy papkasi bo'lmasa qabul qilinadi.
	guess := func(d string) {
		d = filepath.Clean(d)
		// O'chiruvchi ko'pincha "uninstall" kabi ichki papkada turadi.
		switch strings.ToLower(filepath.Base(d)) {
		case "uninstall", "uninst", "_uninst", "uninstaller", "bin":
			d = filepath.Dir(d)
		}
		if isGenericToolDir(d) {
			return
		}
		if pub := a.publisherName(); pub != "" && normName(filepath.Base(d)) == pub && !matchAny(filepath.Base(d), own) {
			return
		}
		add(d, false)
	}
	if a.MSICode == "" {
		if exe := exePathFromCommand(a.UninstallString); exe != "" {
			guess(filepath.Dir(exe))
		}
	}
	if icon := exePathFromIcon(a.DisplayIcon); icon != "" {
		dir := filepath.Dir(icon)
		// Belgi faylining papkasi faqat ilova nomiga mos kelsa yoki boshqa ma'lumot yo'q bo'lsa.
		if len(dirs) == 0 || matchAny(filepath.Base(dir), own) {
			guess(dir)
		}
	}
	// Bir papka boshqasining ichida bo'lsa, faqat tashqisini qoldiramiz.
	var out []string
	for _, d := range dirs {
		inner := false
		for _, o := range dirs {
			if isUnder(d, o) {
				inner = true
				break
			}
		}
		if !inner {
			out = append(out, d)
		}
	}
	return out
}

// isRootAppDir C:\xampp kabi disk ildizidagi papka — faqat nomi ilovaga mos kelsa.
func isRootAppDir(d string, a *App) bool {
	return isRootLevelDir(d) && matchAny(filepath.Base(d), nameSet(a.Name, a.Publisher, a.KeyName))
}

// isGenericToolDir o'chiruvchi umumiy joyda turadimi (msiexec, rundll32, Package Cache...).
func isGenericToolDir(p string) bool {
	if isUnderOrSame(p, sys.Windows) || isUnder(p, filepath.Join(sys.ProgramData, "Package Cache")) ||
		isUnderOrSame(p, filepath.Join(sys.ProgramFiles, "Common Files")) ||
		isUnderOrSame(p, filepath.Join(sys.ProgramFilesX86, "Common Files")) {
		return true
	}
	l := strings.ToLower(p)
	return strings.Contains(l, `\installshield installation information\`) ||
		strings.Contains(l, `\package cache\`) || strings.HasPrefix(pathKey(l), pathKey(sys.Temp))
}

var uninstallRoots = []regRef{
	{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, registry.WOW64_64KEY},
	{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, registry.WOW64_32KEY},
	{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, 0},
}

// readAllUninstallEntries Uninstall kalitlaridagi barcha yozuvlarni o'qiydi
// (tizim komponentlari va yangilanishlar ham).
func readAllUninstallEntries() []*App {
	var apps []*App
	seen := map[string]bool{}
	for _, root := range uninstallRoots {
		for _, name := range subKeys(root) {
			ref := regRef{root.Root, root.Path + `\` + name, root.Access}
			k, err := ref.open(registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			a := &App{
				Kind:                 KindWin32,
				Key:                  ref,
				KeyName:              name,
				Name:                 regString(k, "DisplayName"),
				Version:              regString(k, "DisplayVersion"),
				Publisher:            regString(k, "Publisher"),
				InstallDate:          regString(k, "InstallDate"),
				InstallLocation:      regString(k, "InstallLocation"),
				UninstallString:      regString(k, "UninstallString"),
				QuietUninstallString: regString(k, "QuietUninstallString"),
				DisplayIcon:          regString(k, "DisplayIcon"),
				SizeBytes:            int64(regInt(k, "EstimatedSize")) * 1024,
				SystemComponent:      regInt(k, "SystemComponent") == 1,
				PerUser:              root.Root == registry.CURRENT_USER,
			}
			parent := regString(k, "ParentKeyName")
			release := strings.ToLower(regString(k, "ReleaseType"))
			k.Close()
			a.MSICode = msiProductCode(name, a.UninstallString)
			if a.Name == "" || parent != "" || strings.Contains(release, "update") || release == "hotfix" {
				a.SystemComponent = true
			}
			// 64-bit tizimda HKCU ikki marta ko'rinmasligi uchun.
			id := strings.ToLower(rootName(ref.Root) + name + a.Name)
			if seen[id] {
				continue
			}
			seen[id] = true
			apps = append(apps, a)
		}
	}
	return apps
}

// loadWin32Apps foydalanuvchi o'chira oladigan klassik dasturlar ro'yxati.
func loadWin32Apps() []*App {
	var out []*App
	for _, a := range readAllUninstallEntries() {
		if a.SystemComponent || (a.UninstallString == "" && a.QuietUninstallString == "") {
			continue
		}
		out = append(out, a)
	}
	return out
}

type appxPkg struct {
	Name              string
	PackageFullName   string
	PackageFamilyName string
	Version           string
	Publisher         string
	InstallLocation   string
	DisplayName       string
	IsFramework       bool
	SignatureKind     int // 0 None, 1 Developer, 2 Enterprise, 3 Store, 4 System
	NonRemovable      bool
}

// loadStoreApps Microsoft Store orqali o'rnatilgan, o'chirsa bo'ladigan paketlar.
func loadStoreApps() []*App {
	script := `$ErrorActionPreference='SilentlyContinue';` +
		`[Console]::OutputEncoding=[Text.Encoding]::UTF8;` +
		`Get-AppxPackage -PackageTypeFilter Main | ForEach-Object {` +
		`$d=$_.Name; try { $m=(Get-AppxPackageManifest $_).Package.Properties.DisplayName; if($m -and $m -notlike 'ms-resource:*'){$d=$m} } catch {};` +
		`[pscustomobject]@{Name=$_.Name;PackageFullName=$_.PackageFullName;PackageFamilyName=$_.PackageFamilyName;` +
		`Version=[string]$_.Version;Publisher=$_.Publisher;InstallLocation=$_.InstallLocation;DisplayName=$d;` +
		`IsFramework=$_.IsFramework;SignatureKind=[int]$_.SignatureKind;NonRemovable=[bool]$_.NonRemovable} } | ConvertTo-Json -Compress`
	out, err := runHidden("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	out = strings.TrimSpace(out)
	if strings.HasPrefix(out, "{") {
		out = "[" + out + "]"
	}
	var pkgs []appxPkg
	if json.Unmarshal([]byte(out), &pkgs) != nil {
		return nil
	}
	var apps []*App
	for _, p := range pkgs {
		if p.IsFramework || p.NonRemovable || p.SignatureKind == 4 {
			continue
		}
		name := p.DisplayName
		if name == "" {
			name = p.Name
		}
		apps = append(apps, &App{
			Kind:              KindStore,
			Name:              name,
			Version:           p.Version,
			Publisher:         publisherCN(p.Publisher),
			InstallLocation:   p.InstallLocation,
			PackageFullName:   p.PackageFullName,
			PackageFamilyName: p.PackageFamilyName,
			PackageName:       p.Name,
		})
	}
	return apps
}

// publisherCN "CN=Mozilla Corporation, O=..." -> "Mozilla Corporation".
func publisherCN(p string) string {
	for _, part := range strings.Split(p, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToUpper(part), "CN=") {
			return strings.Trim(part[3:], `"`)
		}
	}
	return p
}

func loadApps(withStore bool) []*App {
	apps := loadWin32Apps()
	if withStore {
		apps = append(apps, loadStoreApps()...)
	}
	sortApps(apps)
	return apps
}

func sortApps(apps []*App) {
	sort.SliceStable(apps, func(i, j int) bool {
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
}

// stillInstalled ilova hali ham tizimda ro'yxatdan o'tganmi.
func stillInstalled(a *App) bool {
	if a.Kind == KindStore {
		out, _ := runHidden("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"if (Get-AppxPackage -PackageTypeFilter Main | Where-Object PackageFullName -eq '"+
				strings.ReplaceAll(a.PackageFullName, "'", "''")+"') { 'yes' }")
		return strings.Contains(out, "yes")
	}
	if a.MSICode != "" && msiInstalled(a.MSICode) {
		return true
	}
	if !keyExists(a.Key) {
		return false
	}
	// Yozuv qolgan, lekin o'chiruvchi fayli yo'q va MSI ham o'rnatilmagan — aslida o'chirilgan.
	if a.MSICode != "" {
		return false
	}
	exe := exePathFromCommand(a.UninstallString)
	if exe != "" && filepath.IsAbs(exe) && !exists(exe) && (a.InstallLocation == "" || !exists(a.InstallLocation)) {
		return false
	}
	return true
}

func appDataBases() []string {
	return nonEmpty(sys.Roaming, sys.Local, sys.LocalLow, filepath.Join(os.Getenv("USERPROFILE"), ".config"))
}
