//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type ui struct {
	app   *tview.Application
	pages *tview.Pages

	header *tview.TextView
	search *tview.InputField
	table  *tview.Table
	footer *tview.TextView

	logView *tview.TextView
	logDone chan struct{} // jarayon tugagach Enter/Esc bosilishini kutish

	leftTable  *tview.Table
	leftHeader *tview.TextView
	leftFooter *tview.TextView
	leftItems  []*Leftover
	leftRows   []leftRow
	leftDone   func(ok bool)

	mu      sync.Mutex
	all     []*App
	visible []*App
	checked map[string]bool
	busy    bool
	filter  string
	admin   bool
	loading bool
}

type leftRow struct {
	group string
	item  *Leftover
}

const (
	colCheck  = "[x]"
	colEmpty  = "[ ]"
	pageMain  = "main"
	pageLog   = "log"
	pageLeft  = "left"
	pageModal = "modal"
)

func newUI() *ui {
	u := &ui{
		app:     tview.NewApplication(),
		pages:   tview.NewPages(),
		checked: map[string]bool{},
		admin:   isAdmin(),
	}
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault
	tview.Styles.ContrastBackgroundColor = tcell.ColorDarkBlue

	// ----- Asosiy sahifa: dasturlar ro'yxati -----
	u.header = tview.NewTextView().SetDynamicColors(true)
	u.search = tview.NewInputField().SetLabel(" Qidirish: ").SetFieldWidth(0).
		SetPlaceholder("dastur nomi yoki ishlab chiqaruvchi...")
	u.search.SetChangedFunc(func(text string) {
		u.filter = text
		u.refresh()
	})
	u.search.SetDoneFunc(func(key tcell.Key) { u.app.SetFocus(u.table) })

	u.table = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	u.table.SetBorder(true).SetTitle(" O'rnatilgan dasturlar ")
	u.table.SetSelectedFunc(func(row, col int) { u.toggle(row) })
	u.table.SetInputCapture(u.mainKeys)

	u.footer = tview.NewTextView().SetDynamicColors(true).SetText(
		" [yellow]↑/↓[-] tanlash  [yellow]Space/Enter[-] belgilash  [yellow]/[-] qidirish  " +
			"[yellow]Del[-] o'chirish  [yellow]Ctrl+A[-] hammasi  [yellow]Ctrl+D[-] bekor  " +
			"[yellow]F2[-] o'chirilgan dasturlar qoldiqlari  [yellow]F3[-] Savat  [yellow]F5[-] yangilash  [yellow]Esc[-] chiqish")

	main := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(u.header, 2, 0, false).
		AddItem(u.search, 1, 0, false).
		AddItem(u.table, 0, 1, true).
		AddItem(u.footer, 2, 0, false)
	u.pages.AddPage(pageMain, main, true, true)

	// ----- Jarayon jurnali -----
	u.logView = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true)
	u.logView.SetBorder(true).SetTitle(" Jarayon ")
	u.logView.SetChangedFunc(func() { u.logView.ScrollToEnd() })
	u.logView.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if (ev.Key() == tcell.KeyEnter || ev.Key() == tcell.KeyEscape) && u.logDone != nil {
			ch := u.logDone
			u.logDone = nil
			close(ch)
			return nil
		}
		return ev
	})
	u.pages.AddPage(pageLog, u.logView, true, false)

	// ----- Qoldiqlar ro'yxati (ko'rib chiqish) -----
	u.leftHeader = tview.NewTextView().SetDynamicColors(true)
	u.leftTable = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	u.leftTable.SetBorder(true)
	u.leftTable.SetSelectedFunc(func(row, col int) { u.toggleLeft(row) })
	u.leftTable.SetInputCapture(u.leftKeys)
	u.leftFooter = tview.NewTextView().SetDynamicColors(true).SetText(
		" [yellow]Space/Enter[-] belgilash (guruh qatorida — butun guruh)  [yellow]Ctrl+A[-] hammasi  " +
			"[yellow]Ctrl+D[-] bekor  [yellow]Del/F10[-] belgilanganlarni o'chirish  [yellow]Esc[-] o'tkazib yuborish\n" +
			" [green]Yashil[-] — xavfsiz, avtomatik belgilangan.  [orange]To'q sariq[-] — ehtimoliy, o'zingiz tekshirib belgilang.")
	left := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(u.leftHeader, 2, 0, false).
		AddItem(u.leftTable, 0, 1, true).
		AddItem(u.leftFooter, 2, 0, false)
	u.pages.AddPage(pageLeft, left, true, false)

	u.app.SetRoot(u.pages, true).EnableMouse(true)
	return u
}

func (u *ui) run() error {
	u.updateHeader()
	u.reload()
	return u.app.Run()
}

// ---------- Asosiy ro'yxat ----------

func (u *ui) updateHeader() {
	mode := "[green]Administrator[-]"
	if !u.admin {
		mode = "[red]Administrator emas — ba'zi qoldiqlarni o'chirib bo'lmaydi[-]"
	}
	recycle := "[green]Savatga (Recycle Bin)[-]"
	if !recycleMode {
		recycle = "[orange]to'g'ridan-to'g'ri (Savatsiz)[-]"
	}
	u.mu.Lock()
	total, sel := len(u.all), len(u.checked)
	u.mu.Unlock()
	status := fmt.Sprintf("Dasturlar: %d   Belgilangan: [yellow]%d[-]", total, sel)
	if u.loading {
		status = "[yellow]Ro'yxat yuklanmoqda...[-]"
	}
	u.header.SetText(fmt.Sprintf(" [::b]STORAGE TRASH CLEANER[::-] — dasturlarni to'liq o'chirish va qoldiqlarni tozalash\n %s   Rejim: %s   Fayllar: %s",
		status, mode, recycle))
}

func (u *ui) reload() {
	u.loading = true
	u.updateHeader()
	go func() {
		apps := loadApps(true)
		u.app.QueueUpdateDraw(func() {
			u.mu.Lock()
			u.all = apps
			ids := map[string]bool{}
			for _, a := range apps {
				ids[a.ID()] = true
			}
			for id := range u.checked {
				if !ids[id] {
					delete(u.checked, id)
				}
			}
			u.mu.Unlock()
			u.loading = false
			u.refresh()
		})
	}()
}

func (u *ui) refresh() {
	u.mu.Lock()
	f := strings.ToLower(strings.TrimSpace(u.filter))
	u.visible = u.visible[:0]
	for _, a := range u.all {
		if f == "" || strings.Contains(strings.ToLower(a.Name), f) || strings.Contains(strings.ToLower(a.Publisher), f) {
			u.visible = append(u.visible, a)
		}
	}
	u.mu.Unlock()

	row, _ := u.table.GetSelection()
	u.table.Clear()
	headers := []string{"   ", "Nomi", "Versiya", "Ishlab chiqaruvchi", "Hajmi", "Sana", "Turi"}
	for i, h := range headers {
		u.table.SetCell(0, i, tview.NewTableCell(h).SetTextColor(tcell.ColorYellow).SetSelectable(false).
			SetAttributes(tcell.AttrBold))
	}
	for i, a := range u.visible {
		mark := colEmpty
		color := tcell.ColorWhite
		if u.checked[a.ID()] {
			mark, color = colCheck, tcell.ColorLightGreen
		}
		size := ""
		if a.SizeBytes > 0 {
			size = humanSize(a.SizeBytes)
		}
		date := a.InstallDate
		if len(date) == 8 {
			date = date[:4] + "-" + date[4:6] + "-" + date[6:]
		}
		cells := []string{mark, a.Name, a.Version, a.Publisher, size, date, a.KindLabel()}
		for j, c := range cells {
			cell := tview.NewTableCell(tview.Escape(c)).SetTextColor(color)
			switch j {
			case 1:
				cell.SetExpansion(3).SetMaxWidth(50)
			case 3:
				cell.SetExpansion(2).SetMaxWidth(30)
			case 2:
				cell.SetMaxWidth(18)
			case 4:
				cell.SetAlign(tview.AlignRight)
			}
			u.table.SetCell(i+1, j, cell)
		}
	}
	if row < 1 {
		row = 1
	}
	if row > len(u.visible) {
		row = len(u.visible)
	}
	u.table.Select(row, 0)
	u.table.SetTitle(fmt.Sprintf(" O'rnatilgan dasturlar (%d) ", len(u.visible)))
	u.updateHeader()
}

func (u *ui) toggle(row int) {
	if row < 1 || row > len(u.visible) {
		return
	}
	id := u.visible[row-1].ID()
	u.mu.Lock()
	if u.checked[id] {
		delete(u.checked, id)
	} else {
		u.checked[id] = true
	}
	u.mu.Unlock()
	u.refresh()
	if row < len(u.visible) {
		u.table.Select(row+1, 0)
	}
}

func (u *ui) checkAllVisible() {
	u.mu.Lock()
	for _, a := range u.visible {
		u.checked[a.ID()] = true
	}
	u.mu.Unlock()
	u.refresh()
}

func (u *ui) uncheckAll() {
	u.mu.Lock()
	u.checked = map[string]bool{}
	u.mu.Unlock()
	u.refresh()
}

func (u *ui) selectedApps() []*App {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []*App
	for _, a := range u.all {
		if u.checked[a.ID()] {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		if row, _ := u.table.GetSelection(); row >= 1 && row <= len(u.visible) {
			out = append(out, u.visible[row-1])
		}
	}
	return out
}

func (u *ui) mainKeys(ev *tcell.EventKey) *tcell.EventKey {
	if u.busy {
		return nil
	}
	switch ev.Key() {
	case tcell.KeyCtrlA:
		u.checkAllVisible()
		return nil
	case tcell.KeyCtrlD:
		u.uncheckAll()
		return nil
	case tcell.KeyDelete:
		u.confirmRemove()
		return nil
	case tcell.KeyF5:
		u.reload()
		return nil
	case tcell.KeyF2:
		u.runOrphanScan()
		return nil
	case tcell.KeyF3:
		recycleMode = !recycleMode
		u.updateHeader()
		return nil
	case tcell.KeyEscape, tcell.KeyF10:
		u.confirm("Dasturdan chiqilsinmi?", []string{"Chiqish", "Bekor qilish"}, func(i int) {
			if i == 0 {
				u.app.Stop()
			}
		})
		return nil
	case tcell.KeyTab:
		u.app.SetFocus(u.search)
		return nil
	case tcell.KeyRune:
		switch ev.Rune() {
		case ' ':
			row, _ := u.table.GetSelection()
			u.toggle(row)
			return nil
		case '/':
			u.app.SetFocus(u.search)
			return nil
		}
		// Harf yozilsa — qidiruvga o'tamiz.
		u.app.SetFocus(u.search)
		u.search.SetText(u.search.GetText() + string(ev.Rune()))
		return nil
	}
	return ev
}

// confirm modal oyna ko'rsatadi (UI oqimida chaqiriladi).
func (u *ui) confirm(text string, buttons []string, done func(int)) {
	prev := u.app.GetFocus()
	m := tview.NewModal().SetText(text).AddButtons(buttons).SetDoneFunc(func(i int, _ string) {
		u.pages.RemovePage(pageModal)
		u.app.SetFocus(prev)
		done(i)
	})
	u.pages.AddPage(pageModal, m, true, true)
	u.app.SetFocus(m)
}

// ask fon oqimidan foydalanuvchiga savol beradi va javobni kutadi.
func (u *ui) ask(text string, buttons []string) int {
	ch := make(chan int, 1)
	u.app.QueueUpdateDraw(func() { u.confirm(text, buttons, func(i int) { ch <- i }) })
	return <-ch
}

func (u *ui) confirmRemove() {
	apps := u.selectedApps()
	if len(apps) == 0 {
		return
	}
	var names []string
	for i, a := range apps {
		if i == 10 {
			names = append(names, fmt.Sprintf("... va yana %d ta", len(apps)-10))
			break
		}
		names = append(names, "• "+a.Name)
	}
	text := fmt.Sprintf("Quyidagi %d ta ilova TO'LIQ o'chiriladi:\n\n%s\n\n"+
		"Avval ilovaning rasmiy o'chiruvchisi ishga tushadi, so'ng qolgan fayllar, "+
		"sozlamalar va registr yozuvlari qidiriladi. Topilganlarni o'chirishdan oldin ko'rib chiqasiz.",
		len(apps), strings.Join(names, "\n"))
	u.confirm(text, []string{"Ha, o'chirish", "Bekor qilish"}, func(i int) {
		if i == 0 {
			u.runRemoval(apps)
		}
	})
}

// ---------- Jurnal ----------

func (u *ui) showLog(title string) {
	u.logView.Clear()
	u.logView.SetTitle(" " + title + " ")
	u.pages.SwitchToPage(pageLog)
	u.app.SetFocus(u.logView)
}

func (u *ui) logf(s string) {
	color := ""
	switch {
	case strings.HasPrefix(s, "[OK]"):
		color = "[green]"
	case strings.HasPrefix(s, "[XATO]"):
		color = "[red]"
	case strings.HasPrefix(s, "[!]"):
		color = "[orange]"
	case strings.HasPrefix(s, "==="):
		color = "[yellow::b]"
	}
	line := tview.Escape(s)
	if color != "" {
		line = color + line + "[-:-:-]"
	}
	u.app.QueueUpdateDraw(func() { fmt.Fprintln(u.logView, line) })
}

// waitLogClose jurnal oynasida Enter/Esc bosilishini kutadi.
func (u *ui) waitLogClose(msg string) {
	ch := make(chan struct{})
	u.app.QueueUpdateDraw(func() {
		fmt.Fprintf(u.logView, "\n[yellow]%s[-]\n", tview.Escape(msg))
		u.logDone = ch
	})
	<-ch
}

func (u *ui) backToMain() {
	u.app.QueueUpdateDraw(func() {
		u.pages.SwitchToPage(pageMain)
		u.app.SetFocus(u.table)
		u.busy = false
	})
}

// ---------- Ilovalarni o'chirish ----------

func (u *ui) runRemoval(apps []*App) {
	u.busy = true
	u.showLog("Ilovalarni o'chirish")
	u.mu.Lock()
	all := append([]*App(nil), u.all...)
	u.mu.Unlock()
	go func() {
		defer u.backToMain()
		var total removeResult
		removed := 0
		for i, a := range apps {
			u.logf(fmt.Sprintf("=== [%d/%d] %s ===", i+1, len(apps), a.Name))
			res := runOfficialUninstaller(a, u.logf)
			force := false
			if res != unOK && res != unReboot && res != unAlreadyGone && stillInstalled(a) {
				why := map[uninstallResult]string{
					unCancelled:     "o'chiruvchi bekor qilindi",
					unBusy:          "boshqa o'rnatish jarayoni ketmoqda",
					unFailed:        "o'chiruvchi xato berdi",
					unNoUninstaller: "o'chiruvchi topilmadi",
				}[res]
				choice := u.ask(fmt.Sprintf("%q hali ham o'rnatilgan deb turibdi (%s).\n\n"+
					"Majburiy o'chirish (fayllar va registrni to'g'ridan-to'g'ri tozalash) davom ettirilsinmi?", a.Name, why),
					[]string{"Ha, majburiy", "O'tkazib yuborish"})
				if choice != 0 {
					u.logf("[!] o'tkazib yuborildi")
					continue
				}
				force = true
				forceUninstallPrep(a, u.logf)
			}
			u.logf("Qoldiqlar qidirilmoqda (fayllar, sozlamalar, registr, yorliqlar, xizmatlar)...")
			sc := newScanner([]*App{a}, all, func(s string) { u.logf("  " + s) })
			items := sc.run()
			if force {
				items = append(items, forcedEntries(a, sc)...)
			}
			if len(items) == 0 {
				u.logf("[OK]   hech qanday qoldiq topilmadi — ilova to'liq o'chirildi")
				removed++
				continue
			}
			u.logf(fmt.Sprintf("     %d ta qoldiq topildi", len(items)))
			if !u.review(fmt.Sprintf("%s — %d ta qoldiq topildi", a.Name, len(items)), items) {
				u.logf("[!] qoldiqlar o'chirilmadi (foydalanuvchi o'tkazib yubordi)")
				continue
			}
			r := deleteLeftovers(items, recycleMode, u.logf)
			total.OK += r.OK
			total.Failed += r.Failed
			total.Reboot += r.Reboot
			total.Freed += r.Freed
			removed++
		}
		u.logf("")
		u.logf(fmt.Sprintf("=== Tugadi: %d ta ilova qayta ishlandi, %d ta qoldiq o'chirildi, %d ta o'chmadi, %s bo'shatildi ===",
			removed, total.OK, total.Failed, humanSize(total.Freed)))
		if total.Reboot > 0 {
			u.logf("[!] Ba'zi fayllar band edi - ular kompyuter qayta yuklanganda avtomatik o'chiriladi.")
		}
		if bk.dir != "" {
			u.logf("Registr zaxirasi va jurnal: " + bk.dir)
		}
		u.waitLogClose("Ro'yxatga qaytish uchun Enter bosing...")
		u.app.QueueUpdateDraw(func() {
			u.uncheckAll()
			u.reload()
		})
	}()
}

// forcedEntries majburiy o'chirishda ilovaning o'chirish yozuvini ham qo'shadi.
func forcedEntries(a *App, sc *scanner) []*Leftover {
	if a.Kind != KindWin32 || !keyExists(a.Key) || sc.seen[(&Leftover{Kind: LRegKey, Reg: a.Key}).id()] {
		return nil
	}
	return []*Leftover{{Kind: LRegKey, Reg: a.Key, Group: a.Name, Checked: true,
		Reason: "o'chirish yozuvi (majburiy o'chirish)"}}
}

// ---------- Qoldiqlarni ko'rib chiqish ----------

// review qoldiqlar ro'yxatini ko'rsatadi va foydalanuvchi qaror qilishini kutadi.
func (u *ui) review(title string, items []*Leftover) bool {
	ch := make(chan bool, 1)
	u.app.QueueUpdateDraw(func() {
		u.showLeftovers(title, items, func(ok bool) { ch <- ok })
	})
	ok := <-ch
	u.app.QueueUpdateDraw(func() {
		u.pages.SwitchToPage(pageLog)
		u.app.SetFocus(u.logView)
	})
	return ok
}

func (u *ui) showLeftovers(title string, items []*Leftover, done func(bool)) {
	u.leftItems = items
	u.leftDone = done
	u.leftTable.SetTitle(" " + tview.Escape(title) + " ")
	u.renderLeftovers()
	u.leftTable.Select(1, 0)
	u.pages.SwitchToPage(pageLeft)
	u.app.SetFocus(u.leftTable)
}

func (u *ui) renderLeftovers() {
	row, _ := u.leftTable.GetSelection()
	u.leftTable.Clear()
	u.leftRows = []leftRow{{}}
	for i, h := range []string{"   ", "Turi", "Hajmi", "Joylashuv", "Sabab"} {
		u.leftTable.SetCell(0, i, tview.NewTableCell(h).SetTextColor(tcell.ColorYellow).SetSelectable(false).SetAttributes(tcell.AttrBold))
	}
	var groups []string
	byGroup := map[string][]*Leftover{}
	for _, l := range u.leftItems {
		if _, ok := byGroup[l.Group]; !ok {
			groups = append(groups, l.Group)
		}
		byGroup[l.Group] = append(byGroup[l.Group], l)
	}
	var selCount int
	var selSize int64
	r := 1
	for _, g := range groups {
		list := byGroup[g]
		if len(groups) > 1 || g != "" {
			all := true
			var size int64
			for _, l := range list {
				all = all && l.Checked
				size += l.Size
			}
			mark := colEmpty
			if all {
				mark = colCheck
			}
			sizeText := ""
			if size > 0 {
				sizeText = humanSize(size)
			}
			u.leftTable.SetCell(r, 0, tview.NewTableCell(mark).SetTextColor(tcell.ColorAqua))
			u.leftTable.SetCell(r, 1, tview.NewTableCell(fmt.Sprintf("%d ta", len(list))).SetTextColor(tcell.ColorAqua))
			u.leftTable.SetCell(r, 2, tview.NewTableCell(sizeText).SetTextColor(tcell.ColorAqua).SetAlign(tview.AlignRight))
			u.leftTable.SetCell(r, 3, tview.NewTableCell("── "+tview.Escape(g)+" ──").SetTextColor(tcell.ColorAqua).SetAttributes(tcell.AttrBold))
			u.leftTable.SetCell(r, 4, tview.NewTableCell(""))
			u.leftRows = append(u.leftRows, leftRow{group: g})
			r++
		}
		for _, l := range list {
			mark := colEmpty
			color := tcell.ColorGray
			if l.Checked {
				mark = colCheck
				color = tcell.ColorLightGreen
				if l.Risky {
					color = tcell.ColorOrange
				}
				selCount++
				selSize += l.Size
			} else if l.Risky {
				color = tcell.ColorDarkOrange
			}
			size := ""
			if l.Size > 0 {
				size = humanSize(l.Size)
			}
			u.leftTable.SetCell(r, 0, tview.NewTableCell(mark).SetTextColor(color))
			u.leftTable.SetCell(r, 1, tview.NewTableCell(l.Kind.Label()).SetTextColor(color))
			u.leftTable.SetCell(r, 2, tview.NewTableCell(size).SetTextColor(color).SetAlign(tview.AlignRight))
			u.leftTable.SetCell(r, 3, tview.NewTableCell(tview.Escape(l.Display())).SetTextColor(color).SetExpansion(3).SetMaxWidth(90))
			u.leftTable.SetCell(r, 4, tview.NewTableCell(tview.Escape(l.Reason)).SetTextColor(tcell.ColorSilver).SetExpansion(2).SetMaxWidth(70))
			u.leftRows = append(u.leftRows, leftRow{item: l})
			r++
		}
	}
	u.leftHeader.SetText(fmt.Sprintf(" [::b]Topilgan qoldiqlar:[::-] %d ta   Belgilangan: [yellow]%d ta (%s)[-]   Fayllar: %s",
		len(u.leftItems), selCount, humanSize(selSize), map[bool]string{true: "[green]Savatga[-]", false: "[orange]to'g'ridan-to'g'ri[-]"}[recycleMode]))
	if row < 1 {
		row = 1
	}
	if row >= r {
		row = r - 1
	}
	u.leftTable.Select(row, 0)
}

func (u *ui) toggleLeft(row int) {
	if row < 1 || row >= len(u.leftRows) {
		return
	}
	lr := u.leftRows[row]
	if lr.item != nil {
		lr.item.Checked = !lr.item.Checked
	} else {
		all := true
		for _, l := range u.leftItems {
			if l.Group == lr.group {
				all = all && l.Checked
			}
		}
		for _, l := range u.leftItems {
			if l.Group == lr.group {
				l.Checked = !all
			}
		}
	}
	u.renderLeftovers()
	if row+1 < len(u.leftRows) {
		u.leftTable.Select(row+1, 0)
	}
}

func (u *ui) leftKeys(ev *tcell.EventKey) *tcell.EventKey {
	finish := func(ok bool) {
		if d := u.leftDone; d != nil {
			u.leftDone = nil
			d(ok)
		}
	}
	switch ev.Key() {
	case tcell.KeyCtrlA:
		for _, l := range u.leftItems {
			l.Checked = true
		}
		u.renderLeftovers()
		return nil
	case tcell.KeyCtrlD:
		for _, l := range u.leftItems {
			l.Checked = false
		}
		u.renderLeftovers()
		return nil
	case tcell.KeyF3:
		recycleMode = !recycleMode
		u.renderLeftovers()
		return nil
	case tcell.KeyDelete, tcell.KeyF10:
		n, risky := 0, 0
		var size int64
		for _, l := range u.leftItems {
			if l.Checked {
				n++
				size += l.Size
				if l.Risky {
					risky++
				}
			}
		}
		if n == 0 {
			return nil
		}
		text := fmt.Sprintf("%d ta qoldiq o'chirilsinmi? (%s)", n, humanSize(size))
		if risky > 0 {
			text += fmt.Sprintf("\n\nDiqqat: ulardan %d tasi ehtimoliy (to'q sariq) — ichida kerakli ma'lumot yo'qligiga ishonch hosil qiling.", risky)
		}
		text += "\n\nRegistr yozuvlarining zaxira nusxasi saqlanadi."
		u.confirm(text, []string{"Ha, o'chirish", "Bekor qilish"}, func(i int) {
			if i == 0 {
				finish(true)
			}
		})
		return nil
	case tcell.KeyEscape:
		finish(false)
		return nil
	case tcell.KeyRune:
		if ev.Rune() == ' ' {
			row, _ := u.leftTable.GetSelection()
			u.toggleLeft(row)
			return nil
		}
	}
	return ev
}

// ---------- O'chirilgan dasturlar qoldiqlari (yetim qoldiqlar) ----------

func (u *ui) runOrphanScan() {
	u.busy = true
	u.showLog("O'chirilgan dasturlar qoldiqlarini qidirish")
	go func() {
		defer u.backToMain()
		u.logf("=== C diskdagi o'chirilgan dasturlardan qolgan izlar qidirilmoqda ===")
		items := scanOrphans(true, func(s string) { u.logf("  " + s) })
		if len(items) == 0 {
			u.logf("[OK]   hech qanday qoldiq topilmadi")
			u.waitLogClose("Ro'yxatga qaytish uchun Enter bosing...")
			return
		}
		u.logf(fmt.Sprintf("     %d ta qoldiq topildi", len(items)))
		if !u.review(fmt.Sprintf("O'chirilgan dasturlar qoldiqlari — %d ta", len(items)), items) {
			return
		}
		u.logf("=== O'chirilmoqda ===")
		r := deleteLeftovers(items, recycleMode, u.logf)
		u.logf("")
		u.logf(fmt.Sprintf("=== Tugadi: %d ta o'chirildi, %d ta o'chirib bo'lmadi, %s bo'shatildi ===", r.OK, r.Failed, humanSize(r.Freed)))
		if r.Reboot > 0 {
			u.logf("[!] Ba'zi fayllar band edi - ular kompyuter qayta yuklanganda avtomatik o'chiriladi.")
		}
		if recycleMode && r.Freed > 0 {
			u.logf("[!] Fayllar Savatga yuborildi — joy bo'shashi uchun Savatni tozalang.")
		}
		if bk.dir != "" {
			u.logf("Registr zaxirasi va jurnal: " + bk.dir)
		}
		u.waitLogClose("Ro'yxatga qaytish uchun Enter bosing...")
	}()
}
