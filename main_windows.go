//go:build windows

package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func main() {
	list := flag.Bool("list", false, "o'rnatilgan dasturlar ro'yxatini chiqarish")
	orphans := flag.Bool("orphans", false, "o'chirilgan dasturlar qoldiqlarini topib, ro'yxatini chiqarish (hech narsa o'chirilmaydi)")
	noAdmin := flag.Bool("no-admin", false, "administrator huquqini so'ramaslik")
	flag.Parse()

	enableVT()
	loadSysDirs()
	setConsoleTitle("StorageTrashCleaner - dasturlarni to'liq o'chirish")

	if !isAdmin() && !*noAdmin && !*list && !*orphans {
		fmt.Println("StorageTrashCleaner administrator huquqi bilan ishlashi kerak.")
		fmt.Println("Administrator ruxsati so'ralmoqda (UAC)...")
		if err := relaunchAsAdmin(); err == nil {
			return
		}
		fmt.Println("Ruxsat berilmadi. Dastur cheklangan rejimda ishlaydi: HKLM va Program Files dagi")
		fmt.Println("qoldiqlarni o'chirib bo'lmasligi mumkin. To'liq ishlashi uchun dastur ustida o'ng tugma ->")
		fmt.Println(`"Run as administrator" (Administrator sifatida ishga tushirish).`)
		fmt.Print("\nDavom etish uchun Enter bosing...")
		readLine()
	}

	switch {
	case *list:
		for _, a := range loadApps(true) {
			fmt.Printf("%-50s  %-16s  %-30s  %s\n", trunc(a.Name, 50), trunc(a.Version, 16), trunc(a.Publisher, 30), a.KindLabel())
		}
		return
	case *orphans:
		items := scanOrphans(true, func(s string) { fmt.Fprintln(os.Stderr, s) })
		group := ""
		var total int64
		for _, l := range items {
			if l.Group != group {
				group = l.Group
				fmt.Printf("\n== %s ==\n", group)
			}
			mark := "[x]"
			if !l.Checked {
				mark = "[?]"
			}
			size := ""
			if l.Size > 0 {
				size = " (" + humanSize(l.Size) + ")"
				total += l.Size
			}
			fmt.Printf("%s %-7s %s%s\n      %s\n", mark, l.Kind.Label(), l.Display(), size, l.Reason)
		}
		fmt.Printf("\n%d ta qoldiq topildi, jami %s. [x] — xavfsiz, [?] — tekshirish kerak.\n", len(items), humanSize(total))
		fmt.Println("O'chirish uchun dasturni parametrsiz ishga tushiring va F2 tugmasini bosing.")
		return
	}

	if err := newUI().run(); err != nil {
		fail(err)
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func readLine() string {
	s, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(s)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "[XATO]", err)
	fmt.Print("Chiqish uchun Enter bosing...")
	readLine()
	os.Exit(1)
}

// enableVT konsolda ANSI ranglarni yoqadi.
func enableVT() {
	h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return
	}
	var mode uint32
	if windows.GetConsoleMode(h, &mode) == nil {
		windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
	windows.SetConsoleOutputCP(65001)
}
