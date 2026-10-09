//go:build windows

package main

// "Yetim" qoldiqlarni qidirish: kompyuterdan allaqachon o'chirilgan dasturlardan
// qolib ketgan papkalar, registr kalitlari, buzilgan yorliqlar, eskirgan o'chirish
// yozuvlari, ishlamaydigan avtoyuklash/vazifa/xizmat/firewall yozuvlari va
// vaqtinchalik fayllar.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	GroupUninstall = "Eskirgan o'chirish yozuvlari"
	GroupDirs      = "Qoldiq papkalar"
	GroupRegistry  = "Qoldiq registr kalitlari"
	GroupShortcuts = "Buzilgan yorliqlar"
	GroupAutorun   = "Ishlamaydigan avtoyuklash / vazifalar / xizmatlar"
	GroupFirewall  = "Eskirgan firewall qoidalari"
	GroupPath      = "PATH dagi mavjud bo'lmagan papkalar"
	GroupTemp      = "Vaqtinchalik fayllar"
)

type orphanScanner struct {
	installed []*App   // haqiqatan o'rnatilgan dasturlar (tizim komponentlari bilan)
	names     []string // ularning barcha nomlari va ishlab chiqaruvchilari
	dirs      []string // ularning o'rnatish papkalari
	refs      []string // ishlatilayotgan fayllar (jarayonlar, xizmatlar, vazifalar...)
	results   []*Leftover
	seen      map[string]bool
	progress  func(string)
}

func (o *orphanScanner) add(l *Leftover) {
	if o.seen[l.id()] {
		return
	}
	o.seen[l.id()] = true
	l.Checked = !l.Risky
	o.results = append(o.results, l)
}

// localMissing yo'l mahalliy diskda bo'lib, mavjud emasligini tekshiradi.
// Tarmoq, olinadigan disk yoki aniqlab bo'lmaydigan yo'llar uchun false.
func localMissing(p string) bool {
	p = strings.Trim(strings.TrimSpace(p), `"`)
	if len(p) < 3 || p[1] != ':' || p[2] != '\\' || strings.Contains(p, "%") {
		return false
	}
	root, _ := windows.UTF16PtrFromString(p[:3])
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return false
	}
	return !exists(p)
}

func scanOrphans(includeTemp bool, progress func(string)) []*Leftover {
	if progress == nil {
		progress = func(string) {}
	}
	o := &orphanScanner{seen: map[string]bool{}, progress: progress}

	progress("O'rnatilgan dasturlar ro'yxati o'qilmoqda...")
	entries := readAllUninstallEntries()
	for _, a := range entries {
		if a.Name == "" && a.UninstallString == "" {
			continue
		}
		if a.MSICode != "" && procMsiQueryProduct.Find() == nil && !msiInstalled(a.MSICode) {
			o.add(&Leftover{Kind: LRegKey, Reg: a.Key, Group: GroupUninstall,
				Reason: fmt.Sprintf("%q — MSI mahsuloti o'rnatilmagan, yozuv eskirgan", displayOr(a.Name, a.KeyName))})
			continue
		}
		if a.MSICode == "" && !a.SystemComponent && !stillInstalled(a) {
			o.add(&Leftover{Kind: LRegKey, Reg: a.Key, Group: GroupUninstall,
				Reason: fmt.Sprintf("%q — o'chiruvchi fayli va papkasi yo'q", displayOr(a.Name, a.KeyName))})
			continue
		}
		o.installed = append(o.installed, a)
	}
	o.installed = append(o.installed, loadStoreApps()...)
	for _, a := range o.installed {
		for _, n := range a.nameSet() {
			o.names = addUnique(o.names, n)
		}
		for _, n := range []string{a.Name, cleanAppName(a.Name), a.Publisher, cleanPublisher(a.Publisher), a.KeyName, a.PackageName} {
			if nn := normName(n); len(nn) >= 3 && !reGUID.MatchString(n) {
				o.names = addUnique(o.names, nn)
			}
		}
		for _, d := range a.installDirs() {
			o.dirs = addUniquePath(o.dirs, d)
		}
		if a.InstallLocation != "" {
			o.dirs = addUniquePath(o.dirs, filepath.Clean(a.InstallLocation))
		}
	}

	progress("Ishlatilayotgan fayllar aniqlanmoqda...")
	o.buildRefIndex()

	progress("Buzilgan yorliqlar qidirilmoqda...")
	o.scanShortcuts()
	progress("Avtoyuklash, vazifalar va xizmatlar tekshirilmoqda...")
	o.scanAutorun()
	progress("Firewall va PATH tekshirilmoqda...")
	o.scanFirewall()
	o.scanPath()
	progress("Qoldiq papkalar qidirilmoqda...")
	o.scanDirs()
	progress("Qoldiq registr kalitlari qidirilmoqda...")
	o.scanRegistry()
	if includeTemp {
		progress("Vaqtinchalik fayllar hisoblanmoqda...")
		o.scanTemp()
	}
	return o.results
}

func displayOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// buildRefIndex hozir ishlatilayotgan fayllar ro'yxati: ular joylashgan papkalar
// hech qachon "yetim" deb topilmaydi.
func (o *orphanScanner) buildRefIndex() {
	for _, p := range listProcesses() {
		o.refs = addUniquePath(o.refs, p.Path)
	}
	root := servicesRoot()
	for _, name := range subKeys(root) {
		if k, err := (regRef{root.Root, root.Path + `\` + name, 0}).open(registry.QUERY_VALUE); err == nil {
			o.refs = addUniquePath(o.refs, exePathFromCommand(serviceImage(k)))
			k.Close()
		}
	}
	for _, t := range listTasks() {
		o.refs = addUniquePath(o.refs, t.Command)
	}
	for _, r := range runKeys() {
		if k, err := r.open(registry.QUERY_VALUE); err == nil {
			names, _ := k.ReadValueNames(-1)
			for _, n := range names {
				o.refs = addUniquePath(o.refs, exePathFromCommand(regString(k, n)))
			}
			k.Close()
		}
	}
	for _, dir := range shortcutDirs() {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".lnk") {
				if t := lnkTarget(p); t != "" && exists(t) {
					o.refs = addUniquePath(o.refs, t)
				}
			}
			return nil
		})
	}
}

func (o *orphanScanner) referenced(dir string) bool {
	for _, r := range o.refs {
		if r != "" && isUnderOrSame(r, dir) {
			return true
		}
	}
	for _, d := range o.dirs {
		if isUnderOrSame(d, dir) || isUnderOrSame(dir, d) {
			return true
		}
	}
	return false
}

func (o *orphanScanner) scanDirs() {
	for _, root := range sys.leftoverRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			full := filepath.Join(root, e.Name())
			fi, err := os.Lstat(full)
			if err != nil || isReparse(fi) || !isSafeDir(full) || orphanSkipped(e.Name()) {
				continue
			}
			if looseMatch(e.Name(), o.names) || o.referenced(full) {
				continue
			}
			size, files, newest := dirStats(full, 3*time.Second)
			if files == 0 {
				o.add(&Leftover{Kind: LDir, Path: full, Group: GroupDirs, Reason: "bo'sh papka"})
				continue
			}
			when := ""
			if !newest.IsZero() {
				when = ", oxirgi o'zgarish: " + newest.Format("2006-01-02")
			}
			o.add(&Leftover{Kind: LDir, Path: full, Size: size, Group: GroupDirs, Risky: true,
				Reason: fmt.Sprintf("hech bir o'rnatilgan dasturga tegishli emas (%d fayl%s)", files, when)})
		}
	}
}

func (o *orphanScanner) scanRegistry() {
	for _, root := range softwareRoots() {
		for _, name := range subKeys(root) {
			ref := regRef{root.Root, root.Path + `\` + name, root.Access}
			if protectedRegPath(ref.Path) || orphanSkipped(name) || looseMatch(name, o.names) {
				continue
			}
			k, err := ref.open(registry.QUERY_VALUE | registry.ENUMERATE_SUB_KEYS)
			if err != nil {
				continue
			}
			info, err := k.Stat()
			k.Close()
			if err != nil {
				continue
			}
			// Kalit ichidagi ilova kalitlari ham o'rnatilgan dasturga mos kelmasligi kerak.
			protected := false
			for _, sub := range subKeys(ref) {
				if looseMatch(sub, o.names) {
					protected = true
					break
				}
			}
			if protected {
				continue
			}
			if info.SubKeyCount == 0 && info.ValueCount == 0 {
				o.add(&Leftover{Kind: LRegKey, Reg: ref, Group: GroupRegistry, Reason: "bo'sh kalit"})
			} else {
				o.add(&Leftover{Kind: LRegKey, Reg: ref, Group: GroupRegistry, Risky: true,
					Reason: "hech bir o'rnatilgan dasturga tegishli emas"})
			}
		}
	}
}

func (o *orphanScanner) scanShortcuts() {
	for _, root := range shortcutDirs() {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".lnk") {
				return nil
			}
			if t := lnkTarget(p); t != "" && localMissing(t) {
				o.add(&Leftover{Kind: LFile, Path: p, Group: GroupShortcuts, Reason: "yorliq mavjud bo'lmagan faylga ishora qiladi: " + t})
			}
			return nil
		})
	}
	// Start menyudagi bo'sh papkalar (yoki faqat buzilgan yorliqlari qolganlar).
	for _, root := range nonEmpty(sys.StartMenu, sys.CommonStartMenu) {
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			full := filepath.Join(root, e.Name())
			if !e.IsDir() || !isSafeDir(full) {
				continue
			}
			alive := false
			filepath.WalkDir(full, func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || alive {
					return nil
				}
				if strings.EqualFold(d.Name(), "desktop.ini") {
					return nil
				}
				if !o.seen[(&Leftover{Kind: LFile, Path: p}).id()] {
					alive = true
				}
				return nil
			})
			if !alive {
				o.add(&Leftover{Kind: LDir, Path: full, Group: GroupShortcuts, Reason: "Start menyuda ishlamaydigan yorliqlar papkasi"})
			}
		}
	}
}

func (o *orphanScanner) scanAutorun() {
	for _, r := range runKeys() {
		k, err := r.open(registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, _ := k.ReadValueNames(-1)
		for _, n := range names {
			v := regString(k, n)
			if exe := exePathFromCommand(v); localMissing(exe) {
				o.add(&Leftover{Kind: LRegValue, Reg: r, Value: n, Group: GroupAutorun, Reason: "avtoyuklash: fayl topilmadi — " + exe})
			}
		}
		k.Close()
	}
	for _, t := range listTasks() {
		if strings.HasPrefix(strings.ToLower(t.Name), `\microsoft\`) || isUnderOrSame(t.Command, sys.Windows) {
			continue
		}
		if localMissing(t.Command) {
			o.add(&Leftover{Kind: LTask, Path: t.Name, Group: GroupAutorun, Reason: "vazifa: fayl topilmadi — " + t.Command})
		}
	}
	root := servicesRoot()
	for _, name := range subKeys(root) {
		ref := regRef{root.Root, root.Path + `\` + name, 0}
		k, err := ref.open(registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		typ := regInt(k, "Type")
		img := exePathFromCommand(serviceImage(k))
		k.Close()
		// Faqat oddiy (drayver bo'lmagan) xizmatlar, Windows papkasidan tashqarida.
		if typ&0x30 == 0 || isUnderOrSame(img, sys.Windows) {
			continue
		}
		if localMissing(img) {
			o.add(&Leftover{Kind: LService, Path: name, Reg: ref, Group: GroupAutorun, Risky: true,
				Reason: "xizmat fayli topilmadi — " + img})
		}
	}
}

func (o *orphanScanner) scanFirewall() {
	r := firewallRoot()
	k, err := r.open(registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	names, _ := k.ReadValueNames(-1)
	for _, n := range names {
		rule, _, err := k.GetStringValue(n)
		if err != nil {
			continue
		}
		if app := firewallApp(rule); app != "" && !isUnderOrSame(app, sys.Windows) && localMissing(app) {
			o.add(&Leftover{Kind: LRegValue, Reg: r, Value: n, Group: GroupFirewall, Reason: "dastur topilmadi — " + app})
		}
	}
}

func (o *orphanScanner) scanPath() {
	for _, r := range pathKeys() {
		k, err := r.open(registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		raw, _, err := k.GetStringValue("Path")
		k.Close()
		if err != nil {
			continue
		}
		for _, e := range strings.Split(raw, ";") {
			e = strings.TrimSpace(e)
			if e != "" && localMissing(expandEnv(e)) {
				o.add(&Leftover{Kind: LPathEntry, Path: e, Reg: r, Group: GroupPath, Reason: "papka mavjud emas"})
			}
		}
	}
}

func (o *orphanScanner) scanTemp() {
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, root := range nonEmpty(sys.Temp, sys.WinTemp) {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil || fi.ModTime().After(cutoff) {
				continue
			}
			full := filepath.Join(root, e.Name())
			if fi.IsDir() && !isReparse(fi) {
				size, _, newest := dirStats(full, 2*time.Second)
				if newest.After(cutoff) {
					continue
				}
				o.add(&Leftover{Kind: LDir, Path: full, Size: size, Group: GroupTemp, Reason: "vaqtinchalik papka (1 kundan eski)"})
			} else if !fi.IsDir() {
				o.add(&Leftover{Kind: LFile, Path: full, Size: fi.Size(), Group: GroupTemp, Reason: "vaqtinchalik fayl (1 kundan eski)"})
			}
		}
	}
}
