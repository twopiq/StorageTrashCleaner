//go:build windows

package main

// Topilgan qoldiqlarni o'chirish. Har bir o'chirishdan oldin xavfsizlik qayta
// tekshiriladi, registr kalitlari va vazifalar zaxiralanadi.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// backup — o'chirilgan registr kalitlari va vazifalar nusxasi saqlanadigan joy.
type backup struct {
	once     sync.Once
	dir      string
	n        int
	logFile  *os.File
	exported map[string]bool
}

var bk backup

func backupRoot() string {
	base := sys.ProgramData
	if !isAdmin() || base == "" {
		base = sys.Local
	}
	return filepath.Join(base, "StorageTrashCleaner", "Backup")
}

func (b *backup) ensure() string {
	b.once.Do(func() {
		b.dir = filepath.Join(backupRoot(), time.Now().Format("2006-01-02_15-04-05"))
		os.MkdirAll(b.dir, 0o755)
		b.logFile, _ = os.OpenFile(filepath.Join(b.dir, "log.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		b.exported = map[string]bool{}
		os.WriteFile(filepath.Join(b.dir, "O'QING.txt"), []byte(
			"Bu papkada StorageTrashCleaner o'chirgan registr kalitlarining zaxira nusxasi saqlanadi.\r\n"+
				"Biror narsani qaytarish kerak bo'lsa, tegishli .reg faylni ikki marta bosing.\r\n"+
				"Rejalashtirilgan vazifalar nusxasi tasks\\ papkasida (schtasks /Create /XML bilan tiklanadi).\r\n"+
				"O'chirilgan papka va fayllar Savatda (Recycle Bin) bo'lishi mumkin.\r\n"), 0o644)
	})
	return b.dir
}

func (b *backup) log(format string, args ...any) {
	b.ensure()
	if b.logFile != nil {
		fmt.Fprintf(b.logFile, "%s  %s\r\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	}
}

func (b *backup) exportKey(r regRef) error {
	dir := b.ensure()
	id := strings.ToLower(r.String())
	if b.exported[id] {
		return nil
	}
	b.n++
	_, name := parentKey(r.Path)
	file := filepath.Join(dir, fmt.Sprintf("%03d_%s_%s.reg", b.n, rootName(r.Root), sanitizeFileName(name)))
	if err := exportKey(r, file); err != nil {
		if !keyExists(r) {
			return nil
		}
		return fmt.Errorf("zaxira nusxa olib bo'lmadi: %w", err)
	}
	b.exported[id] = true
	return nil
}

func sanitizeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, s)
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// ---------- Xavfsizlik ----------

func isTempChild(p string) bool {
	parent := filepath.Dir(filepath.Clean(p))
	return samePath(parent, sys.Temp) || samePath(parent, sys.WinTemp)
}

func underShortcutDirs(p string) bool {
	for _, d := range shortcutDirs() {
		if isUnder(p, d) {
			return true
		}
	}
	return false
}

func safeToDelete(l *Leftover) error {
	switch l.Kind {
	case LDir:
		if isSafeDir(l.Path) || (l.RootApp && isRootLevelDir(l.Path)) || (isTempChild(l.Path) && !samePath(l.Path, sys.Temp)) {
			return nil
		}
		return errors.New("himoyalangan papka — o'tkazib yuborildi")
	case LFile:
		if isTempChild(l.Path) || underShortcutDirs(l.Path) || isSafeDir(filepath.Dir(l.Path)) {
			return nil
		}
		return errors.New("himoyalangan joydagi fayl — o'tkazib yuborildi")
	case LRegKey, LService:
		if protectedRegPath(l.Reg.Path) {
			return errors.New("himoyalangan registr kaliti — o'tkazib yuborildi")
		}
	}
	return nil
}

// ---------- O'chirish ----------

type removeResult struct {
	OK, Failed, Reboot int
	Freed              int64
}

// deleteLeftovers belgilangan qoldiqlarni o'chiradi. recycle=true bo'lsa,
// papka va fayllar avval Savatga yuborishga urinib ko'riladi.
func deleteLeftovers(items []*Leftover, recycle bool, logf func(string)) removeResult {
	recycleMode = recycle
	var res removeResult
	pathChanged := false
	var deletedDirs []string
	var deletedKeys []regRef

	// Avval xizmatlar va vazifalar (ular fayllarni band qilib turishi mumkin),
	// keyin fayllar, oxirida registr.
	order := map[LeftKind]int{LService: 0, LTask: 1, LDir: 2, LFile: 3, LRegValue: 4, LPathEntry: 5, LRegKey: 6}
	sorted := append([]*Leftover(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return order[sorted[i].Kind] < order[sorted[j].Kind] })

	for _, l := range sorted {
		if !l.Checked {
			continue
		}
		if err := safeToDelete(l); err != nil {
			logf(fmt.Sprintf("[!] %s: %v", l.Display(), err))
			bk.log("SKIP %s: %v", l.Display(), err)
			res.Failed++
			continue
		}
		reboot, err := removeOne(l)
		switch {
		case err != nil:
			res.Failed++
			logf(fmt.Sprintf("[XATO] %s: %v", l.Display(), err))
			bk.log("FAIL %s: %v", l.Display(), err)
		case reboot:
			res.Reboot++
			res.OK++
			res.Freed += l.Size
			logf(fmt.Sprintf("[OK]   %s (bir qismi qayta yuklashdan keyin o'chadi)", l.Display()))
			bk.log("REBOOT %s", l.Display())
		default:
			res.OK++
			res.Freed += l.Size
			logf(fmt.Sprintf("[OK]   %s", l.Display()))
			bk.log("OK %s", l.Display())
		}
		if err == nil {
			switch l.Kind {
			case LDir, LFile:
				deletedDirs = append(deletedDirs, filepath.Dir(l.Path))
			case LRegKey:
				deletedKeys = append(deletedKeys, l.Reg)
			case LPathEntry:
				pathChanged = true
			}
		}
	}
	pruneEmptyDirs(deletedDirs)
	for _, r := range deletedKeys {
		parent, _ := parentKey(r.Path)
		if strings.Count(parent, `\`) >= 1 {
			pruneEmptyKey(regRef{r.Root, parent, r.Access})
		}
	}
	if pathChanged {
		broadcastEnvChange()
	}
	return res
}

var recycleMode = true

func removeOne(l *Leftover) (reboot bool, err error) {
	switch l.Kind {
	case LDir:
		return removeDir(l.Path, recycleMode)
	case LFile:
		return removeFile(l.Path, recycleMode)
	case LRegKey:
		if err := bk.exportKey(l.Reg); err != nil {
			return false, err
		}
		return false, deleteKeyTree(l.Reg)
	case LRegValue:
		return false, deleteValue(l.Reg, l.Value)
	case LService:
		return false, deleteService(l.Path, l.Reg)
	case LTask:
		return false, deleteTask(l.Path)
	case LPathEntry:
		return false, removePathEntry(l.Reg, l.Path)
	}
	return false, errors.New("noma'lum tur")
}

func clearReadOnly(root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if fi, err := d.Info(); err == nil && isReparse(fi) && p != root {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if pp, err := windows.UTF16PtrFromString(p); err == nil {
			if attrs, err := windows.GetFileAttributes(pp); err == nil &&
				attrs&(windows.FILE_ATTRIBUTE_READONLY|windows.FILE_ATTRIBUTE_SYSTEM|windows.FILE_ATTRIBUTE_HIDDEN) != 0 {
				windows.SetFileAttributes(pp, attrs&^(windows.FILE_ATTRIBUTE_READONLY|windows.FILE_ATTRIBUTE_SYSTEM|windows.FILE_ATTRIBUTE_HIDDEN))
			}
		}
		return nil
	})
}

// removeDir papkani o'chiradi; band fayllarni qayta yuklashda o'chirishga belgilaydi.
func removeDir(p string, recycle bool) (bool, error) {
	if !exists(p) {
		return false, nil
	}
	killProcessesUnder([]string{p})
	if recycle && moveToRecycleBin(p) == nil {
		return false, nil
	}
	clearReadOnly(p)
	for i := 0; i < 3; i++ {
		if err := os.RemoveAll(p); err == nil || !exists(p) {
			return false, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Qolgan (band) fayllar: qayta yuklashda o'chirish.
	var leftovers []string
	filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
		if err == nil {
			leftovers = append(leftovers, q)
		}
		return nil
	})
	if len(leftovers) == 0 {
		return false, nil
	}
	sort.Slice(leftovers, func(i, j int) bool { return len(leftovers[i]) > len(leftovers[j]) })
	for _, q := range leftovers {
		if err := scheduleDeleteOnReboot(q); err != nil {
			return false, fmt.Errorf("fayllar band, qayta yuklashga ham belgilab bo'lmadi: %w", err)
		}
	}
	return true, nil
}

func removeFile(p string, recycle bool) (bool, error) {
	if !exists(p) {
		return false, nil
	}
	if recycle && moveToRecycleBin(p) == nil {
		return false, nil
	}
	if pp, err := windows.UTF16PtrFromString(p); err == nil {
		windows.SetFileAttributes(pp, windows.FILE_ATTRIBUTE_NORMAL)
	}
	if err := os.Remove(p); err != nil {
		if exists(p) {
			if scheduleDeleteOnReboot(p) == nil {
				return true, nil
			}
			return false, err
		}
	}
	return false, nil
}

// pruneEmptyDirs o'chirishdan keyin bo'sh qolgan ota-papkalarni (masalan
// ishlab chiqaruvchi papkasini) o'chiradi.
func pruneEmptyDirs(dirs []string) {
	for _, d := range dirs {
		for d != "" && isSafeDir(d) {
			entries, err := os.ReadDir(d)
			if err != nil || len(entries) > 0 {
				break
			}
			if os.Remove(d) != nil {
				break
			}
			bk.log("EMPTY-DIR %s", d)
			d = filepath.Dir(d)
		}
	}
}

func deleteValue(r regRef, name string) error {
	if err := bk.exportKey(r); err != nil {
		return err
	}
	k, err := r.open(registry.SET_VALUE)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func stopService(s *mgr.Service) {
	st, err := s.Query()
	if err != nil || st.State == svc.Stopped {
		return
	}
	s.Control(svc.Stop)
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		if st, err := s.Query(); err != nil || st.State == svc.Stopped {
			return
		}
	}
}

func deleteService(name string, key regRef) error {
	if err := bk.exportKey(key); err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err == nil {
		stopService(s)
		err = s.Delete()
		s.Close()
		if err != nil && !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
			return err
		}
	}
	// Xizmat menejeri kalitni darhol o'chirmasligi mumkin.
	time.Sleep(300 * time.Millisecond)
	if keyExists(key) {
		deleteKeyTree(key)
	}
	return nil
}

func deleteTask(name string) error {
	src := filepath.Join(sys.Tasks, strings.TrimPrefix(name, `\`))
	if b, err := os.ReadFile(src); err == nil {
		dst := filepath.Join(bk.ensure(), "tasks", strings.TrimPrefix(name, `\`)+".xml")
		os.MkdirAll(filepath.Dir(dst), 0o755)
		os.WriteFile(dst, b, 0o644)
	}
	out, err := runHidden("schtasks.exe", "/Delete", "/TN", name, "/F")
	if err != nil {
		if !exists(src) {
			return nil
		}
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func removePathEntry(r regRef, entry string) error {
	if err := bk.exportKey(r); err != nil {
		return err
	}
	k, err := r.open(registry.QUERY_VALUE | registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	raw, typ, err := k.GetStringValue("Path")
	if err != nil {
		return err
	}
	var kept []string
	for _, e := range strings.Split(raw, ";") {
		if strings.TrimSpace(e) == "" || strings.EqualFold(strings.TrimSpace(e), entry) {
			continue
		}
		kept = append(kept, e)
	}
	val := strings.Join(kept, ";")
	if typ == registry.EXPAND_SZ {
		return k.SetExpandStringValue("Path", val)
	}
	return k.SetStringValue("Path", val)
}
