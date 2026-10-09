//go:build windows

package main

// Bitta (yoki bir nechta) ilova uchun qoldiqlarni qidirish: papkalar, registr
// kalitlari, avtoyuklash, xizmatlar, rejalashtirilgan vazifalar, yorliqlar,
// firewall qoidalari, PATH yozuvlari va MSI yozuvlari.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

type LeftKind int

const (
	LDir LeftKind = iota
	LFile
	LRegKey
	LRegValue
	LService
	LTask
	LPathEntry
)

func (k LeftKind) Label() string {
	switch k {
	case LDir:
		return "Papka"
	case LFile:
		return "Fayl"
	case LRegKey:
		return "Registr"
	case LRegValue:
		return "Qiymat"
	case LService:
		return "Xizmat"
	case LTask:
		return "Vazifa"
	case LPathEntry:
		return "PATH"
	}
	return "?"
}

type Leftover struct {
	Kind    LeftKind
	Path    string // fayl/papka yo'li, xizmat nomi, vazifa nomi yoki PATH elementi
	Reg     regRef // registr kaliti (LRegKey, LRegValue, LService, LPathEntry uchun)
	Value   string // qiymat nomi (LRegValue)
	Size    int64
	Reason  string
	Group   string // ekranda guruhlash uchun (ilova nomi yoki toifa)
	Checked bool
	Risky   bool // ehtimoliy, tasdiqlash kerak
	RootApp bool // disk ildizidagi ilova papkasi (C:\xampp) — faqat o'rnatish papkasi sifatida
}

func (l *Leftover) Display() string {
	switch l.Kind {
	case LRegKey:
		return l.Reg.String()
	case LRegValue:
		return l.Reg.String() + ` → "` + l.Value + `"`
	case LService:
		return l.Path + "  (" + l.Reg.String() + ")"
	}
	return l.Path
}

func (l *Leftover) id() string {
	return l.Kind.Label() + "|" + strings.ToLower(l.Display())
}

type scanner struct {
	apps     []*App   // o'chirilayotgan ilovalar
	others   []*App   // tizimda qoladigan boshqa ilovalar
	names    []string // qidiriladigan nomlar
	pubs     []string // ishlab chiqaruvchi nomlari
	dirs     []string // o'rnatish papkalari
	results  []*Leftover
	seen     map[string]bool
	progress func(string)
}

func newScanner(apps, all []*App, progress func(string)) *scanner {
	s := &scanner{apps: apps, seen: map[string]bool{}, progress: progress}
	ids := map[string]bool{}
	for _, a := range apps {
		ids[a.ID()] = true
		for _, n := range a.nameSet() {
			s.names = addUnique(s.names, n)
		}
		if p := a.publisherName(); p != "" {
			s.pubs = addUnique(s.pubs, p)
		}
		for _, d := range a.installDirs() {
			s.dirs = addUniquePath(s.dirs, d)
		}
	}
	for _, a := range all {
		if !ids[a.ID()] {
			s.others = append(s.others, a)
		}
	}
	if s.progress == nil {
		s.progress = func(string) {}
	}
	return s
}

func (s *scanner) add(l *Leftover) {
	if s.seen[l.id()] {
		return
	}
	s.seen[l.id()] = true
	l.Checked = !l.Risky
	if l.Group == "" && len(s.apps) == 1 {
		l.Group = s.apps[0].Name
	}
	s.results = append(s.results, l)
}

// protectedByOthers boshqa o'rnatilgan dastur ham shu papka/nomdan foydalanadimi.
func (s *scanner) protectedByOthers(path, name string) bool {
	for _, o := range s.others {
		for _, d := range o.installDirs() {
			if path != "" && (isUnderOrSame(d, path) || isUnderOrSame(path, d)) {
				return true
			}
		}
		if name != "" && matchAny(name, o.nameSet()) {
			return true
		}
	}
	return false
}

func (s *scanner) addDir(p, reason string, risky bool) {
	s.addDirEx(p, reason, risky, false)
}

func (s *scanner) addDirEx(p, reason string, risky, rootApp bool) {
	if !(isSafeDir(p) || (rootApp && isRootLevelDir(p))) || !isDir(p) {
		return
	}
	for _, r := range s.results { // ichki papkalarni alohida qo'shmaymiz
		if r.Kind == LDir && isUnderOrSame(p, r.Path) {
			return
		}
	}
	if s.protectedByOthers(p, filepath.Base(p)) {
		return
	}
	// Tashqi papka qo'shilsa, oldin qo'shilgan ichkilarini olib tashlaymiz.
	kept := s.results[:0]
	for _, r := range s.results {
		if !(r.Kind == LDir && isUnder(r.Path, p)) {
			kept = append(kept, r)
		}
	}
	s.results = kept
	size, _, _ := dirStats(p, 5*time.Second)
	s.add(&Leftover{Kind: LDir, Path: p, Size: size, Reason: reason, Risky: risky, RootApp: rootApp})
}

func (s *scanner) addFile(p, reason string) {
	fi, err := os.Lstat(p)
	if err != nil || fi.IsDir() {
		return
	}
	for _, r := range s.results {
		if r.Kind == LDir && isUnder(p, r.Path) {
			return
		}
	}
	s.add(&Leftover{Kind: LFile, Path: p, Size: fi.Size(), Reason: reason})
}

func (s *scanner) addKey(r regRef, reason string, risky bool) {
	if protectedRegPath(r.Path) || !keyExists(r) {
		return
	}
	_, name := parentKey(r.Path)
	if s.protectedByOthers("", name) {
		return
	}
	s.add(&Leftover{Kind: LRegKey, Reg: r, Reason: reason, Risky: risky})
}

func (s *scanner) addValue(r regRef, value, reason string) {
	s.add(&Leftover{Kind: LRegValue, Reg: r, Value: value, Reason: reason})
}

func (s *scanner) refersToApp(text string) bool {
	for _, d := range s.dirs {
		if textRefersTo(text, d) {
			return true
		}
	}
	return false
}

// run barcha qidiruvlarni bajaradi.
func (s *scanner) run() []*Leftover {
	s.progress("Papkalar tekshirilmoqda...")
	s.scanFiles()
	s.progress("Registr tekshirilmoqda...")
	s.scanUninstallEntries()
	s.scanRegistryNames()
	s.scanMSI()
	s.progress("Avtoyuklash va xizmatlar tekshirilmoqda...")
	s.scanRun()
	s.scanServices()
	s.progress("Rejalashtirilgan vazifalar tekshirilmoqda...")
	s.scanTasks()
	s.progress("Yorliqlar tekshirilmoqda...")
	s.scanShortcuts()
	s.progress("Firewall va PATH tekshirilmoqda...")
	s.scanFirewall()
	s.scanPath()
	sort.SliceStable(s.results, func(i, j int) bool { return s.results[i].Kind < s.results[j].Kind })
	return s.results
}

func (s *scanner) scanFiles() {
	for _, d := range s.dirs {
		s.addDirEx(d, "o'rnatish papkasi", false, !isSafeDir(d))
	}
	// Store ilovalarining ma'lumotlari: AppData\Local\Packages\<PackageFamilyName>
	for _, a := range s.apps {
		if a.Kind == KindStore && a.PackageFamilyName != "" {
			p := filepath.Join(sys.Local, "Packages", a.PackageFamilyName)
			if isDir(p) && isSafeDir(p) {
				size, _, _ := dirStats(p, 5*time.Second)
				s.add(&Leftover{Kind: LDir, Path: p, Size: size, Reason: "Store ilova ma'lumotlari"})
			}
		}
	}
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
			if fi, err := os.Lstat(full); err != nil || isReparse(fi) {
				continue
			}
			if matchAny(e.Name(), s.names) {
				s.addDir(full, "ilova nomidagi papka", false)
				continue
			}
			// Ishlab chiqaruvchi papkasi ichidagi ilova papkasi: AppData\Roaming\Mozilla\Firefox
			if matchAny(e.Name(), s.pubs) {
				subs, _ := os.ReadDir(full)
				for _, sub := range subs {
					if sub.IsDir() && matchAny(sub.Name(), s.names) {
						s.addDir(filepath.Join(full, sub.Name()), "ishlab chiqaruvchi papkasidagi ilova papkasi", false)
					}
				}
			}
		}
	}
}

func softwareRoots() []regRef {
	return []regRef{
		{registry.CURRENT_USER, `Software`, 0},
		{registry.LOCAL_MACHINE, `SOFTWARE`, registry.WOW64_64KEY},
		{registry.LOCAL_MACHINE, `SOFTWARE`, registry.WOW64_32KEY},
	}
}

func (s *scanner) scanUninstallEntries() {
	for _, a := range s.apps {
		if a.Kind == KindWin32 && keyExists(a.Key) && !stillInstalled(a) {
			s.add(&Leftover{Kind: LRegKey, Reg: a.Key, Reason: "o'chirish yozuvi (Programs and Features)"})
		}
	}
}

func (s *scanner) scanRegistryNames() {
	for _, root := range softwareRoots() {
		for _, name := range subKeys(root) {
			ref := regRef{root.Root, root.Path + `\` + name, root.Access}
			if matchAny(name, s.names) {
				s.addKey(ref, "ilova sozlamalari", false)
				continue
			}
			if matchAny(name, s.pubs) {
				for _, sub := range subKeys(ref) {
					if matchAny(sub, s.names) {
						s.addKey(regRef{ref.Root, ref.Path + `\` + sub, ref.Access}, "ilova sozlamalari", false)
					}
				}
			}
		}
	}
	// App Paths: "foo.exe" -> ilova papkasidagi fayl.
	for _, root := range []regRef{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths`, registry.WOW64_64KEY},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths`, registry.WOW64_32KEY},
		{registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\App Paths`, 0},
	} {
		for _, name := range subKeys(root) {
			ref := regRef{root.Root, root.Path + `\` + name, root.Access}
			k, err := ref.open(registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			target := regString(k, "")
			k.Close()
			if target != "" && s.refersToApp(target) {
				s.add(&Leftover{Kind: LRegKey, Reg: ref, Reason: "App Paths yozuvi"})
			}
		}
	}
}

func (s *scanner) scanMSI() {
	for _, a := range s.apps {
		if a.MSICode == "" || msiInstalled(a.MSICode) {
			continue
		}
		packed := packGUID(a.MSICode)
		if packed == "" {
			continue
		}
		for _, p := range []string{
			`SOFTWARE\Classes\Installer\Products\` + packed,
			`SOFTWARE\Classes\Installer\Features\` + packed,
			`SOFTWARE\Microsoft\Windows\CurrentVersion\Installer\UserData\S-1-5-18\Products\` + packed,
		} {
			r := regRef{registry.LOCAL_MACHINE, p, registry.WOW64_64KEY}
			if keyExists(r) {
				s.add(&Leftover{Kind: LRegKey, Reg: r, Reason: "MSI: bu mahsulot aslida o'rnatilmagan (yozuv eskirgan)"})
			}
		}
		r := regRef{registry.CURRENT_USER, `Software\Microsoft\Installer\Products\` + packed, 0}
		if keyExists(r) {
			s.add(&Leftover{Kind: LRegKey, Reg: r, Reason: "MSI: foydalanuvchi yozuvi"})
		}
	}
}

func runKeys() []regRef {
	var out []regRef
	for _, p := range []string{`Software\Microsoft\Windows\CurrentVersion\Run`, `Software\Microsoft\Windows\CurrentVersion\RunOnce`} {
		out = append(out,
			regRef{registry.CURRENT_USER, p, 0},
			regRef{registry.LOCAL_MACHINE, p, registry.WOW64_64KEY},
			regRef{registry.LOCAL_MACHINE, p, registry.WOW64_32KEY})
	}
	return out
}

func (s *scanner) scanRun() {
	for _, r := range runKeys() {
		k, err := r.open(registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, _ := k.ReadValueNames(-1)
		for _, n := range names {
			v := regString(k, n)
			if s.refersToApp(v) || (matchAny(n, s.names) && !exists(exePathFromCommand(v))) {
				s.addValue(r, n, "avtoyuklash (Run): "+v)
			}
		}
		k.Close()
	}
	// Startup papkasidagi yorliqlar scanShortcuts ichida tekshiriladi.
}

func servicesRoot() regRef {
	return regRef{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, 0}
}

// serviceImage xizmatning bajariladigan fayli yo'li (\SystemRoot\, \??\ kabi prefikslar ochiladi).
func serviceImage(k registry.Key) string {
	img := regString(k, "ImagePath")
	img = strings.TrimPrefix(img, `\??\`)
	if strings.HasPrefix(strings.ToLower(img), `\systemroot\`) {
		img = sys.Windows + img[len(`\systemroot`):]
	} else if strings.HasPrefix(strings.ToLower(img), `system32\`) {
		img = filepath.Join(sys.Windows, img)
	}
	return img
}

func (s *scanner) scanServices() {
	root := servicesRoot()
	for _, name := range subKeys(root) {
		ref := regRef{root.Root, root.Path + `\` + name, 0}
		k, err := ref.open(registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		img := serviceImage(k)
		k.Close()
		if img != "" && s.refersToApp(img) {
			s.add(&Leftover{Kind: LService, Path: name, Reg: ref, Reason: "xizmat: " + img})
		}
	}
}

var (
	reTaskCommand = regexp.MustCompile(`(?is)<Command>(.*?)</Command>`)
	reTaskArgs    = regexp.MustCompile(`(?is)<Arguments>(.*?)</Arguments>`)
)

type taskInfo struct {
	Name    string // \Folder\TaskName
	File    string
	Command string
	Args    string
}

func listTasks() []taskInfo {
	var out []taskInfo
	filepath.WalkDir(sys.Tasks, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		text := decodeText(b)
		t := taskInfo{Name: `\` + strings.TrimPrefix(p[len(sys.Tasks):], `\`), File: p}
		if m := reTaskCommand.FindStringSubmatch(text); m != nil {
			t.Command = expandEnv(strings.Trim(strings.TrimSpace(xmlUnescape(m[1])), `"`))
		}
		if m := reTaskArgs.FindStringSubmatch(text); m != nil {
			t.Args = xmlUnescape(m[1])
		}
		out = append(out, t)
		return nil
	})
	return out
}

func xmlUnescape(s string) string {
	return strings.NewReplacer("&quot;", `"`, "&apos;", "'", "&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(s)
}

func (s *scanner) scanTasks() {
	for _, t := range listTasks() {
		if strings.HasPrefix(strings.ToLower(t.Name), `\microsoft\`) {
			continue
		}
		base := filepath.Base(t.Name)
		if (t.Command != "" && s.refersToApp(t.Command)) ||
			(matchAny(base, s.names) && (t.Command == "" || !exists(t.Command))) ||
			nameStartsWithAny(base, s.names) && (t.Command == "" || !exists(t.Command)) {
			s.add(&Leftover{Kind: LTask, Path: t.Name, Reason: "rejalashtirilgan vazifa: " + t.Command})
		}
	}
}

// nameStartsWithAny "GoogleUpdateTaskMachineUA" kabi vazifa nomlari uchun.
func nameStartsWithAny(entry string, names []string) bool {
	e := normName(entry)
	for _, n := range names {
		if len(n) >= 5 && strings.HasPrefix(e, n) {
			return true
		}
	}
	return false
}

func shortcutDirs() []string {
	return nonEmpty(sys.StartMenu, sys.CommonStartMenu, sys.Desktop, sys.PublicDesktop, sys.QuickLaunch)
}

func (s *scanner) scanShortcuts() {
	for _, root := range shortcutDirs() {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				inStart := isUnder(p, sys.StartMenu) || isUnder(p, sys.CommonStartMenu)
				if p != root && inStart && matchAny(d.Name(), s.names) {
					s.addStartMenuDir(p)
				}
				return nil
			}
			if !strings.EqualFold(filepath.Ext(p), ".lnk") && !strings.EqualFold(filepath.Ext(p), ".url") {
				return nil
			}
			target := ""
			if strings.EqualFold(filepath.Ext(p), ".lnk") {
				target = lnkTarget(p)
			}
			name := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
			if (target != "" && s.refersToApp(target)) || (matchAny(name, s.names) && (target == "" || !exists(target))) {
				s.addFile(p, "yorliq")
			}
			return nil
		})
	}
}

// addStartMenuDir Start menyudagi ilova papkasini (yorliqlari bilan) qo'shadi.
func (s *scanner) addStartMenuDir(p string) {
	if !isSafeDir(p) || s.protectedByOthers(p, filepath.Base(p)) {
		return
	}
	for _, r := range s.results {
		if r.Kind == LDir && isUnderOrSame(p, r.Path) {
			return
		}
	}
	size, _, _ := dirStats(p, time.Second)
	s.add(&Leftover{Kind: LDir, Path: p, Size: size, Reason: "Start menyu papkasi"})
}

func firewallRoot() regRef {
	return regRef{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy\FirewallRules`, 0}
}

// firewallApp qoida matnidan "App=" maydonini ajratadi.
func firewallApp(rule string) string {
	for _, part := range strings.Split(rule, "|") {
		if len(part) > 4 && strings.EqualFold(part[:4], "app=") {
			return expandEnv(part[4:])
		}
	}
	return ""
}

func (s *scanner) scanFirewall() {
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
		if app := firewallApp(rule); app != "" && s.refersToApp(app) {
			s.addValue(r, n, "firewall qoidasi: "+app)
		}
	}
}

func pathKeys() []regRef {
	return []regRef{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, 0},
		{registry.CURRENT_USER, `Environment`, 0},
	}
}

func (s *scanner) scanPath() {
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
			if e = strings.TrimSpace(e); e != "" && s.refersToApp(expandEnv(e)) {
				s.add(&Leftover{Kind: LPathEntry, Path: e, Reg: r, Reason: "PATH o'zgaruvchisidagi yozuv"})
			}
		}
	}
}

// scanLeftovers tanlangan ilovalar uchun qoldiqlarni qidiradi.
func scanLeftovers(apps, all []*App, progress func(string)) []*Leftover {
	return newScanner(apps, all, progress).run()
}
