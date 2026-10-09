package main

// Platformaga bog'liq bo'lmagan yordamchi funksiyalar: nomlarni solishtirish,
// yo'llarni tekshirish va buyruq satrlarini tahlil qilish. Ular Linuxda ham
// test qilinadi.

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// normName nomni faqat kichik harf va raqamlardan iborat ko'rinishga keltiradi:
// "Mozilla Firefox (x64)" -> "mozillafirefoxx64".
func normName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var (
	reVersion  = regexp.MustCompile(`(?i)\s*[\(\[]?\b(v(er(sion)?)?\.?\s*)?\d+(\.\d+)+[a-z0-9.\-]*[\)\]]?`)
	reBrackets = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]`)
	reArch     = regexp.MustCompile(`(?i)\b(x64|x86|x86_64|amd64|arm64|64-bit|32-bit|64 bit|32 bit|win64|win32)\b`)
	reLangCode = regexp.MustCompile(`(?i)\s+-\s+[a-z]{2}(-[a-z]{2})?$`)
	reSpaces   = regexp.MustCompile(`\s+`)
)

// cleanAppName ko'rsatiladigan nomdan versiya, arxitektura va qavs ichidagi
// qo'shimchalarni olib tashlaydi: "7-Zip 23.01 (x64)" -> "7-Zip".
func cleanAppName(name string) string {
	s := reVersion.ReplaceAllString(name, "")
	s = reBrackets.ReplaceAllString(s, "")
	s = reArch.ReplaceAllString(s, "")
	s = reLangCode.ReplaceAllString(s, "")
	s = reSpaces.ReplaceAllString(s, " ")
	return strings.Trim(s, " -_.,")
}

var publisherSuffixes = []string{
	"corporation", "corp", "incorporated", "inc", "limited", "ltd", "llc", "gmbh",
	"co", "company", "s.r.o", "sro", "srl", "s.a", "sa", "ag", "bv", "b.v", "oy",
	"ab", "as", "pty", "plc", "kg", "foundation", "software", "technologies", "technology", "group",
}

// cleanPublisher "Google LLC" -> "Google", "Mozilla Corporation" -> "Mozilla".
func cleanPublisher(p string) string {
	words := strings.Fields(strings.NewReplacer(",", " ", "®", "", "™", "").Replace(p))
	for len(words) > 1 {
		last := strings.Trim(strings.ToLower(words[len(words)-1]), ".")
		found := false
		for _, s := range publisherSuffixes {
			if last == strings.Trim(s, ".") {
				found = true
				break
			}
		}
		if !found {
			break
		}
		words = words[:len(words)-1]
	}
	return strings.Join(words, " ")
}

// genericNames hech qachon "dastur nomi" sifatida qabul qilinmaydigan umumiy
// so'zlar. Ular bo'yicha moslik topilsa, noto'g'ri narsani o'chirib yuborish xavfi bor.
var genericNames = map[string]bool{
	"microsoft": true, "windows": true, "common": true, "commonfiles": true, "shared": true,
	"system": true, "system32": true, "program": true, "programs": true, "programfiles": true,
	"data": true, "temp": true, "tmp": true, "cache": true, "caches": true, "config": true,
	"settings": true, "update": true, "updater": true, "updates": true, "setup": true,
	"installer": true, "install": true, "uninstall": true, "app": true, "apps": true,
	"application": true, "applications": true, "tools": true, "tool": true, "bin": true,
	"lib": true, "logs": true, "log": true, "user": true, "users": true, "public": true,
	"default": true, "desktop": true, "documents": true, "downloads": true, "music": true,
	"videos": true, "pictures": true, "packages": true, "package": true, "software": true,
	"classes": true, "policies": true, "clients": true, "help": true, "helper": true,
	"service": true, "services": true, "driver": true, "drivers": true, "plugins": true,
	"plugin": true, "runtime": true, "runtimes": true, "framework": true, "client": true,
	"server": true, "launcher": true, "game": true, "games": true, "office": true,
	"python": true, "java": true, "dotnet": true, "net": true, "nvidia": true, "intel": true,
	"amd": true, "google": true, "apple": true, "adobe": true, "store": true, "edge": true,
	"browser": true, "player": true, "media": true, "studio": true, "free": true, "pro": true,
	"the": true, "and": true, "for": true, "new": true, "inc": true, "llc": true, "ltd": true,
}

// usableName nom moslik qidirish uchun yaroqlimi (yetarlicha uzun va umumiy emas).
func usableName(n string) bool {
	if len([]rune(n)) < 4 {
		return false
	}
	return !genericNames[n]
}

// nameSet ilova uchun qidiriladigan normallashtirilgan nomlar to'plami.
func nameSet(displayName, publisher, keyName string) []string {
	var out []string
	add := func(s string) {
		n := normName(s)
		if usableName(n) {
			out = addUnique(out, n)
		}
	}
	clean := cleanAppName(displayName)
	add(clean)
	add(displayName)
	// "Mozilla Firefox" -> "Firefox": ishlab chiqaruvchi papkasi ichidagi
	// ilova papkasini (AppData\Roaming\Mozilla\Firefox) topish uchun.
	if pub := normName(cleanPublisher(publisher)); len(pub) >= 3 {
		if n := normName(clean); strings.HasPrefix(n, pub) {
			if rest := n[len(pub):]; usableName(rest) {
				out = addUnique(out, rest)
			}
		}
	}
	// Uninstall kalitining nomi ko'pincha papka nomi bilan bir xil bo'ladi
	// (masalan "Notepad++", "VLC media player"). GUID bo'lsa foydasiz.
	if keyName != "" && !strings.HasPrefix(keyName, "{") {
		add(cleanAppName(strings.TrimSuffix(keyName, "_is1")))
	}
	return out
}

// matchAny papka/kalit nomi ilova nomlaridan biriga mos keladimi.
// To'liq tenglik yoki papka nomi ilova nomi bilan boshlanib, biroz uzunroq
// bo'lishi ("Mozilla Firefox" -> "Mozilla Firefox ESR") qabul qilinadi.
// Papka nomi ilova nomidan QISQA bo'lsa ("Mozilla" < "Mozilla Firefox") mos
// kelmaydi — bu ishlab chiqaruvchining umumiy papkasi bo'lishi mumkin.
func matchAny(entry string, names []string) bool {
	e := normName(entry)
	if e == "" || genericNames[e] {
		return false
	}
	for _, n := range names {
		if e == n {
			return true
		}
		if len(n) >= 6 && len(e) > len(n) && len(e)-len(n) <= 8 && strings.HasPrefix(e, n) {
			return true
		}
	}
	return false
}

// pathKey yo'llarni solishtirish uchun kalit (registr-va-harf farqisiz, oxirgi
// slash'siz, "/" o'rniga "\").
func pathKey(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "/", `\`)
	p = strings.TrimRight(p, `\`)
	return strings.ToLower(p)
}

func samePath(a, b string) bool {
	return a != "" && b != "" && pathKey(a) == pathKey(b)
}

// isUnder p yo'li dir ichidami (dir ning o'zi emas).
func isUnder(p, dir string) bool {
	pk, dk := pathKey(p), pathKey(dir)
	if pk == "" || dk == "" {
		return false
	}
	return strings.HasPrefix(pk, dk+`\`)
}

func isUnderOrSame(p, dir string) bool {
	return samePath(p, dir) || isUnder(p, dir)
}

func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if isUnderOrSame(p, d) {
			return true
		}
	}
	return false
}

// textRefersTo matn (buyruq satri, registr qiymati) dir ichidagi faylga ishora qiladimi.
func textRefersTo(text, dir string) bool {
	t, d := pathKey(text), pathKey(dir)
	if d == "" || len(d) < 4 {
		return false
	}
	i := strings.Index(t, d)
	if i < 0 {
		return false
	}
	// "C:\Apps\Foo" "C:\Apps\FooBar\x.exe" ga mos kelmasligi kerak.
	rest := t[i+len(d):]
	return rest == "" || rest[0] == '\\' || rest[0] == '"' || rest[0] == ' ' || rest[0] == ',' || rest[0] == ';'
}

// exePathFromCommand buyruq satridan bajariladigan fayl yo'lini ajratadi:
// `"C:\P F\a.exe" /S` -> `C:\P F\a.exe`, `C:\P F\a.exe /S` -> `C:\P F\a.exe`.
func exePathFromCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if cmd[0] == '"' {
		if end := strings.IndexByte(cmd[1:], '"'); end >= 0 {
			return cmd[1 : end+1]
		}
		return strings.Trim(cmd, `"`)
	}
	lower := strings.ToLower(cmd)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com", ".msc", ".dll"} {
		if i := strings.Index(lower, ext); i >= 0 {
			end := i + len(ext)
			if end == len(cmd) || cmd[end] == ' ' || cmd[end] == ',' || cmd[end] == '"' {
				return cmd[:end]
			}
		}
	}
	if i := strings.IndexByte(cmd, ' '); i > 0 {
		return cmd[:i]
	}
	return cmd
}

// exePathFromIcon DisplayIcon qiymatidan fayl yo'lini ajratadi: `"C:\a.exe",0` -> `C:\a.exe`.
func exePathFromIcon(icon string) string {
	icon = strings.TrimSpace(icon)
	if icon == "" {
		return ""
	}
	if icon[0] == '"' {
		return exePathFromCommand(icon)
	}
	if i := strings.LastIndexByte(icon, ','); i > 0 {
		rest := strings.TrimSpace(icon[i+1:])
		if _, ok := atoiSigned(rest); ok {
			icon = icon[:i]
		}
	}
	return strings.TrimSpace(icon)
}

func atoiSigned(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	neg := false
	if s[0] == '-' {
		neg, s = true, s[1:]
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, s != ""
}

var reGUID = regexp.MustCompile(`\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}`)

// msiProductCode MSI o'chirish buyrug'i yoki kalit nomidan mahsulot GUID'ini topadi.
func msiProductCode(keyName, uninstallString string) string {
	if reGUID.MatchString(keyName) && len(keyName) == 38 {
		if strings.Contains(strings.ToLower(uninstallString), "msiexec") {
			return strings.ToUpper(keyName)
		}
	}
	lower := strings.ToLower(uninstallString)
	if strings.Contains(lower, "msiexec") {
		if g := reGUID.FindString(uninstallString); g != "" {
			return strings.ToUpper(g)
		}
	}
	return ""
}

// packGUID MSI registr kalitlarida ishlatiladigan "packed" GUID ko'rinishi:
// {12345678-ABCD-EF01-2345-6789ABCDEF01} -> 87654321DCBA10FE5432987654321EFCDBA
func packGUID(g string) string {
	g = strings.Trim(strings.ToUpper(g), "{}")
	parts := strings.Split(g, "-")
	if len(parts) != 5 {
		return ""
	}
	rev := func(s string) string {
		r := []byte(s)
		for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
			r[i], r[j] = r[j], r[i]
		}
		return string(r)
	}
	swapPairs := func(s string) string {
		r := []byte(s)
		for i := 0; i+1 < len(r); i += 2 {
			r[i], r[i+1] = r[i+1], r[i]
		}
		return string(r)
	}
	return rev(parts[0]) + rev(parts[1]) + rev(parts[2]) + swapPairs(parts[3]) + swapPairs(parts[4])
}

func addUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func addUniquePath(list []string, p string) []string {
	if p == "" {
		return list
	}
	for _, x := range list {
		if samePath(x, p) {
			return list
		}
	}
	return append(list, p)
}

// humanSize baytlarni o'qishga qulay ko'rinishga keltiradi.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	f := float64(n) / unit
	i := 0
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// orphanSkip — bu nomlar bilan boshlanadigan papka/kalitlar tizimga yoki
// umumiy vositalarga tegishli, ularni "yetim" deb hisoblamaymiz.
var orphanSkip = []string{
	"microsoft", "windows", "msbuild", "referenceassemblies", "commonfiles", "internetexplorer",
	"uninstallinformation", "packagecache", "packages", "package", "regid", "ssh", "uso",
	"softwaredistribution", "comms", "connecteddevicesplatform", "crashdumps", "d3dscache", "temp",
	"tmp", "programs", "publishers", "history", "peerdistrepub", "applicationdata",
	"temporaryinternetfiles", "nvidia", "intel", "amd", "ati", "realtek", "dell", "hewlett", "hp",
	"lenovo", "asus", "acer", "oem", "drivers", "virtualstore", "isolatedstorage", "fontconfig",
	"pip", "pypa", "npm", "nuget", "yarn", "nodegyp", "gobuild", "pnpm", "cmake", "conda",
	"jupyter", "ipython", "dotnet", "assembly", "wsl", "containers", "crashpad", "cef",
	"squirreltemp", "elevateddiagnostics", "diagnostics", "deliveryoptimization", "identities",
	"classes", "clients", "policies", "registeredapplications", "wow6432node", "odbc", "khronos",
	"partner", "defaultuserenvironment", "setup", "dolby", "synaptics", "elantech", "waves",
	"conexant", "appdatalow", "system", "volatileenvironment", "python", "java", "javasoft",
	"oracle", "sun", "chromium", "google", "mozilla", "apple", "adobe", "desktopini", "default",
	"public", "all users", "allusers", "ssl", "certs", "fonts", "gtk", "qt", "vulkan", "openal",
	"directx", "physx", "vcredist", "visualstudio", "vs", "vscode", "jetbrains", "unity", "epic",
	"steam", "battle", "riot", "gpu", "shader", "wer", "speech", "voice", "openssh", "git",
	"github", "docker", "hyperv", "vmware", "virtualbox", "oracle", "kaspersky", "eset", "avast",
	"avg", "mcafee", "norton", "symantec", "bitdefender", "defender", "onedrive", "teams", "office",
	"edge", "skype", "zoom", "whatsapp", "telegram", "discord", "spotify",
}

func orphanSkipped(name string) bool {
	n := normName(name)
	if len(n) < 3 || reGUID.MatchString(name) || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "{") {
		return true
	}
	for _, p := range orphanSkip {
		if strings.HasPrefix(n, normName(p)) {
			return true
		}
	}
	return false
}

// looseMatch — "yetim"ni aniqlashda ishlatiladi: o'rnatilgan dasturga
// ozgina o'xshasa ham papka himoyalanadi (xato o'chirishdan ko'ra qoldirgan yaxshi).
func looseMatch(entry string, names []string) bool {
	e := normName(entry)
	if len(e) < 3 {
		return true
	}
	for _, n := range names {
		if len(n) < 3 {
			continue
		}
		if e == n || strings.HasPrefix(n, e) || (len(e) >= 4 && strings.Contains(n, e)) || (len(n) >= 4 && strings.Contains(e, n)) {
			return true
		}
	}
	return false
}
