package main

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

func TestCleanAppName(t *testing.T) {
	cases := map[string]string{
		"7-Zip 23.01 (x64)":           "7-Zip",
		"Mozilla Firefox (x64 en-US)": "Mozilla Firefox",
		"Notepad++ (64-bit x64)":      "Notepad++",
		"VLC media player":            "VLC media player",
		"Python 3.12.1 (64-bit)":      "Python",
		"Microsoft Visual C++ 2015-2022 Redistributable (x64) - 14.38.33130": "Microsoft Visual C++ 2015-2022 Redistributable",
		"Telegram Desktop version 4.14.9":                                    "Telegram Desktop",
	}
	for in, want := range cases {
		if got := cleanAppName(in); got != want {
			t.Errorf("cleanAppName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanPublisher(t *testing.T) {
	cases := map[string]string{
		"Mozilla Corporation":        "Mozilla",
		"Google LLC":                 "Google",
		"JetBrains s.r.o.":           "JetBrains",
		"Igor Pavlov":                "Igor Pavlov",
		"Python Software Foundation": "Python",
		"Valve Corporation":          "Valve",
	}
	for in, want := range cases {
		if got := cleanPublisher(in); got != want {
			t.Errorf("cleanPublisher(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchAny(t *testing.T) {
	firefox := nameSet("Mozilla Firefox (x64 en-US)", "Mozilla", "Mozilla Firefox 120.0 (x64 en-US)")
	teams := nameSet("Microsoft Teams", "Microsoft Corporation", "Teams")
	cases := []struct {
		entry string
		names []string
		want  bool
	}{
		{"Mozilla Firefox", firefox, true},
		{"Firefox", firefox, true},
		// Ishlab chiqaruvchining umumiy papkasi (Thunderbird ham shu yerda) — mos kelmasligi kerak.
		{"Mozilla", firefox, false},
		{"Microsoft", teams, false},
		{"Teams", teams, true},
		{"Microsoft Teams", teams, true},
		{"Common Files", firefox, false},
		{"Thunderbird", firefox, false},
		{"", firefox, false},
	}
	for _, c := range cases {
		if got := matchAny(c.entry, c.names); got != c.want {
			t.Errorf("matchAny(%q, %v) = %v, want %v", c.entry, c.names, got, c.want)
		}
	}
}

func TestShortNamesAreIgnored(t *testing.T) {
	// "Go", "R" kabi juda qisqa nomlar hamma narsaga mos kelib qolmasligi kerak.
	names := nameSet("Go", "Google", "Go")
	if len(names) != 0 {
		t.Fatalf("expected no usable names, got %v", names)
	}
	if matchAny("Go", names) {
		t.Fatal("short name matched")
	}
}

func TestPaths(t *testing.T) {
	if !isUnder(`C:\Program Files\Foo\bin\a.exe`, `c:\program files\foo`) {
		t.Error("isUnder failed")
	}
	if isUnder(`C:\Program Files\FooBar`, `C:\Program Files\Foo`) {
		t.Error("isUnder prefix bug")
	}
	if !samePath(`C:\A\B\`, `c:/a/b`) {
		t.Error("samePath failed")
	}
	if !textRefersTo(`"C:\Program Files\Foo\foo.exe" --tray`, `C:\Program Files\Foo`) {
		t.Error("textRefersTo failed")
	}
	if textRefersTo(`"C:\Program Files\FooBar\foo.exe"`, `C:\Program Files\Foo`) {
		t.Error("textRefersTo prefix bug")
	}
}

func TestExePathFromCommand(t *testing.T) {
	cases := map[string]string{
		`"C:\Program Files\Foo\unins000.exe"`:                  `C:\Program Files\Foo\unins000.exe`,
		`"C:\Program Files\Foo\uninstall.exe" /S`:              `C:\Program Files\Foo\uninstall.exe`,
		`C:\Program Files\Foo\uninstall.exe /S`:                `C:\Program Files\Foo\uninstall.exe`,
		`MsiExec.exe /X{11111111-2222-3333-4444-555555555555}`: `MsiExec.exe`,
		`rundll32.exe C:\x.dll,Entry`:                          `rundll32.exe`,
	}
	for in, want := range cases {
		if got := exePathFromCommand(in); got != want {
			t.Errorf("exePathFromCommand(%q) = %q, want %q", in, got, want)
		}
	}
	if got := exePathFromIcon(`C:\Foo\foo.exe,0`); got != `C:\Foo\foo.exe` {
		t.Errorf("exePathFromIcon = %q", got)
	}
	if got := exePathFromIcon(`"C:\Foo Bar\foo.exe",-101`); got != `C:\Foo Bar\foo.exe` {
		t.Errorf("exePathFromIcon quoted = %q", got)
	}
}

func TestMSI(t *testing.T) {
	g := "{90120000-0030-0000-0000-0000000FF1CE}"
	if got := msiProductCode(g, "MsiExec.exe /X"+g); got != g {
		t.Errorf("msiProductCode = %q", got)
	}
	if got := msiProductCode("Foo", `"C:\foo\uninstall.exe"`); got != "" {
		t.Errorf("non-MSI product code = %q", got)
	}
	if got := packGUID(g); got != "00002109030000000000000000F01FEC" {
		t.Errorf("packGUID = %q", got)
	}
}

func TestLooseMatchAndSkip(t *testing.T) {
	names := []string{"mozillafirefox", "googlechrome", "googlellc"}
	for _, e := range []string{"Mozilla", "Google", "Chrome", "Firefox"} {
		if !looseMatch(e, names) {
			t.Errorf("looseMatch(%q) should protect", e)
		}
	}
	if looseMatch("WinRAR", names) {
		t.Error("WinRAR should not match")
	}
	for _, e := range []string{"Microsoft", "Packages", "{1234ABCD-0000-0000-0000-000000000000}", ".vscode", "NVIDIA Corporation", "ModifiableWindowsApps", "dbg"} {
		if !orphanSkipped(e) {
			t.Errorf("orphanSkipped(%q) = false", e)
		}
	}
	if orphanSkipped("WinRAR") {
		t.Error("WinRAR skipped")
	}
}

func utf16z(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, 2*len(u)+2)
	for _, c := range u {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	return append(b, 0, 0)
}

func TestParseLnk(t *testing.T) {
	// Sarlavha + LinkInfo (ANSI yo'l bilan).
	hdr := make([]byte, 76)
	binary.LittleEndian.PutUint32(hdr, 0x4C)
	binary.LittleEndian.PutUint32(hdr[20:], lnkHasLinkInfo)
	base := []byte(`C:\Program Files\Foo\foo.exe` + "\x00")
	info := make([]byte, 28)
	binary.LittleEndian.PutUint32(info[4:], 0x1C) // header size
	binary.LittleEndian.PutUint32(info[8:], 1)    // VolumeIDAndLocalBasePath
	binary.LittleEndian.PutUint32(info[16:], 28)  // LocalBasePathOffset
	suffixOff := 28 + len(base)
	binary.LittleEndian.PutUint32(info[24:], uint32(suffixOff))
	info = append(info, base...)
	info = append(info, 0)
	binary.LittleEndian.PutUint32(info, uint32(len(info)))
	data := append(append([]byte{}, hdr...), info...)
	if got := parseLnkTarget(data); got != `C:\Program Files\Foo\foo.exe` {
		t.Errorf("parseLnkTarget = %q", got)
	}

	// Muhit o'zgaruvchili maqsad (EnvironmentVariableDataBlock).
	t.Setenv("STC_TEST_DIR", `D:\Apps`)
	hdr2 := make([]byte, 76)
	binary.LittleEndian.PutUint32(hdr2, 0x4C)
	binary.LittleEndian.PutUint32(hdr2[20:], lnkHasExpString|lnkIsUnicode)
	block := make([]byte, 8+260+520)
	binary.LittleEndian.PutUint32(block, uint32(len(block)))
	binary.LittleEndian.PutUint32(block[4:], lnkEnvBlockSig)
	copy(block[8+260:], utf16z(`%STC_TEST_DIR%\bar.exe`))
	data2 := append(append(hdr2, block...), 0, 0, 0, 0)
	if got := parseLnkTarget(data2); got != `D:\Apps\bar.exe` {
		t.Errorf("parseLnkTarget env = %q", got)
	}

	// MSI "advertised" yorliq — maqsad aniqlanmaydi.
	binary.LittleEndian.PutUint32(hdr[20:], lnkHasLinkInfo|lnkHasDarwinID)
	if got := parseLnkTarget(append(hdr, info...)); got != "" {
		t.Errorf("darwin lnk = %q", got)
	}
	// Buzilgan fayl panic qilmasligi kerak.
	if got := parseLnkTarget(data[:90]); got != "" {
		t.Errorf("truncated = %q", got)
	}
}

func TestHumanSize(t *testing.T) {
	if got := humanSize(1536); got != "1.5 KB" {
		t.Errorf("humanSize = %q", got)
	}
	if got := humanSize(10); got != "10 B" {
		t.Errorf("humanSize = %q", got)
	}
}
