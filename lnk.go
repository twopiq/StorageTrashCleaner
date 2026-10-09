package main

// Windows .lnk (yorliq) fayllaridan maqsad yo'lini o'qiydigan minimal parser
// (MS-SHLLINK spetsifikatsiyasi). COM ishlatilmaydi, shuning uchun tez va xavfsiz.

import (
	"encoding/binary"
	"os"
	"strings"
	"unicode/utf16"
)

const (
	lnkHasIDList     = 0x00000001
	lnkHasLinkInfo   = 0x00000002
	lnkHasName       = 0x00000004
	lnkHasRelPath    = 0x00000008
	lnkHasWorkingDir = 0x00000010
	lnkHasArguments  = 0x00000020
	lnkHasIconLoc    = 0x00000040
	lnkIsUnicode     = 0x00000080
	lnkHasExpString  = 0x00000200
	lnkHasDarwinID   = 0x00001000

	lnkEnvBlockSig = 0xA0000001
)

func cstrA(b []byte, off int) string {
	if off < 0 || off >= len(b) {
		return ""
	}
	end := off
	for end < len(b) && b[end] != 0 {
		end++
	}
	// ANSI qismida odatda faqat ASCII yo'llar bo'ladi; qolganini Latin-1 deb o'qiymiz.
	r := make([]rune, 0, end-off)
	for _, c := range b[off:end] {
		r = append(r, rune(c))
	}
	return string(r)
}

func cstrW(b []byte, off int) string {
	if off < 0 || off >= len(b) {
		return ""
	}
	var u []uint16
	for i := off; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// parseLnkTarget yorliq ko'rsatadigan fayl yo'lini qaytaradi; aniqlab bo'lmasa "".
func parseLnkTarget(b []byte) (target string) {
	defer func() {
		if recover() != nil {
			target = ""
		}
	}()
	if len(b) < 76 || binary.LittleEndian.Uint32(b) != 0x4C {
		return ""
	}
	flags := binary.LittleEndian.Uint32(b[20:])
	// MSI "advertised" yorliqlar (Office va h.k.) aniq faylga ishora qilmaydi.
	if flags&lnkHasDarwinID != 0 {
		return ""
	}
	pos := 76
	if flags&lnkHasIDList != 0 {
		pos += 2 + int(binary.LittleEndian.Uint16(b[pos:]))
	}
	if flags&lnkHasLinkInfo != 0 {
		start := pos
		size := int(binary.LittleEndian.Uint32(b[start:]))
		hdr := binary.LittleEndian.Uint32(b[start+4:])
		infoFlags := binary.LittleEndian.Uint32(b[start+8:])
		if infoFlags&1 != 0 { // VolumeIDAndLocalBasePath
			var base, suffix string
			if hdr >= 0x24 {
				base = cstrW(b, start+int(binary.LittleEndian.Uint32(b[start+28:])))
				suffix = cstrW(b, start+int(binary.LittleEndian.Uint32(b[start+32:])))
			} else {
				base = cstrA(b, start+int(binary.LittleEndian.Uint32(b[start+16:])))
				suffix = cstrA(b, start+int(binary.LittleEndian.Uint32(b[start+24:])))
			}
			if base != "" {
				if suffix == "" || strings.HasSuffix(base, `\`) {
					return base + suffix
				}
				return base + `\` + suffix
			}
		}
		pos = start + size
	}
	if flags&lnkHasExpString == 0 {
		return ""
	}
	charSize := 1
	if flags&lnkIsUnicode != 0 {
		charSize = 2
	}
	for _, f := range []uint32{lnkHasName, lnkHasRelPath, lnkHasWorkingDir, lnkHasArguments, lnkHasIconLoc} {
		if flags&f != 0 {
			n := int(binary.LittleEndian.Uint16(b[pos:]))
			pos += 2 + n*charSize
		}
	}
	for pos+8 <= len(b) {
		size := int(binary.LittleEndian.Uint32(b[pos:]))
		sig := binary.LittleEndian.Uint32(b[pos+4:])
		if size < 8 {
			break
		}
		if sig == lnkEnvBlockSig && pos+8+260+520 <= len(b) {
			t := cstrW(b, pos+8+260)
			if t == "" {
				t = cstrA(b, pos+8)
			}
			return expandEnv(t)
		}
		pos += size
	}
	return ""
}

func lnkTarget(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	n, _ := f.Read(buf)
	return parseLnkTarget(buf[:n])
}

// expandEnv Windows uslubidagi %VAR% o'zgaruvchilarini ochadi.
func expandEnv(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			break
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			b.WriteString(s)
			break
		}
		name := s[i+1 : i+1+j]
		b.WriteString(s[:i])
		if v, ok := lookupEnvFold(name); ok && name != "" {
			b.WriteString(v)
		} else {
			b.WriteString(s[i : i+j+2])
		}
		s = s[i+j+2:]
	}
	return b.String()
}

func lookupEnvFold(name string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}
