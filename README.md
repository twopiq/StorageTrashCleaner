# StorageTrashCleaner

Windows uchun dasturlarni **to'liq** o'chirish va kompyuterdan allaqachon o'chirilgan
dasturlardan qolib ketgan **barcha izlarni** (papkalar, fayllar, registr yozuvlari,
yorliqlar, avtoyuklash, xizmatlar, vazifalar...) topib tozalaydigan dastur.

Bitta `.exe` fayl — o'rnatish shart emas, Python yoki boshqa narsa kerak emas.

![icon](winres/icon.png)

## Imkoniyatlar

### 1. Dasturni to'liq o'chirish (Revo Uninstaller kabi)
1. Ro'yxatdan dasturni tanlaysiz (bir nechtasini ham belgilash mumkin) va **Del** bosasiz.
2. Dasturning **rasmiy o'chiruvchisi** ishga tushadi. Iloji bo'lsa jim rejimda
   (MSI, Inno Setup, NSIS va `QuietUninstallString` aniqlanadi), aks holda o'chiruvchi oynasi ochiladi.
3. O'chiruvchi va u ochgan barcha jarayonlar tugashi kutiladi.
4. O'chiruvchi ishlamasa yoki bekor qilinsa — **majburiy o'chirish** taklif qilinadi.
5. Qolgan izlar qidiriladi:
   - o'rnatish papkasi, `Program Files`, `ProgramData`, `AppData\Roaming`, `AppData\Local`, `LocalLow` dagi papkalar
     (masalan `AppData\Roaming\Mozilla\Firefox`)
   - registr: `HKCU\Software`, `HKLM\SOFTWARE` (32 va 64-bit), App Paths, MSI yozuvlari
   - avtoyuklash (`Run`, `RunOnce`), Windows xizmatlari, rejalashtirilgan vazifalar
   - Start menyu, ish stoli va Quick Launch dagi yorliqlar
   - Windows Firewall qoidalari, `PATH` o'zgaruvchisidagi yozuvlar
   - Microsoft Store ilovalari uchun `AppData\Local\Packages\...`
6. Topilganlar ro'yxati ko'rsatiladi — siz ko'rib chiqib, kerakmaslarini belgilab o'chirasiz.

### 2. O'chirilgan dasturlar qoldiqlari (F2)
Kompyuterda **hozir o'rnatilmagan**, lekin izlari qolgan dasturlarni topadi:

| Toifa | Nima topiladi | Standart holat |
|---|---|---|
| Eskirgan o'chirish yozuvlari | "Programs and Features" da turgan, lekin fayllari yo'q dasturlar | belgilangan |
| Buzilgan yorliqlar | Start menyu / ish stolidagi mavjud bo'lmagan faylga ishora qiluvchi yorliqlar | belgilangan |
| Ishlamaydigan avtoyuklash / vazifalar | fayli o'chirilgan `Run` yozuvlari va rejalashtirilgan vazifalar | belgilangan |
| Eskirgan firewall qoidalari | o'chirilgan dasturlar uchun qoidalar | belgilangan |
| PATH dagi yo'q papkalar | `PATH` dan o'chirilgan papkalar | belgilangan |
| Vaqtinchalik fayllar | `%TEMP%` va `C:\Windows\Temp` dagi 1 kundan eski fayllar | belgilangan |
| Qoldiq papkalar | `Program Files`, `ProgramData`, `AppData` dagi hech bir o'rnatilgan dasturga tegishli bo'lmagan papkalar | bo'sh bo'lsa belgilangan, aks holda **tekshirish kerak** |
| Qoldiq registr kalitlari | `Software\...` dagi hech bir dasturga tegishli bo'lmagan kalitlar | bo'sh bo'lsa belgilangan, aks holda **tekshirish kerak** |
| Ishlamaydigan xizmatlar | fayli yo'q Windows xizmatlari | **tekshirish kerak** |

**Yashil** rangdagilar — xavfsiz, avtomatik belgilanadi. **To'q sariq** rangdagilar —
ehtimoliy qoldiq; ichida kerakli ma'lumot (masalan, portativ dastur sozlamalari yoki
o'yin saqlanmalari) yo'qligiga ishonch hosil qilib, o'zingiz belgilaysiz.

## Xavfsizlik

- Windows papkalari, `Program Files`/`ProgramData`/`AppData` ning o'zi, foydalanuvchi profili,
  Hujjatlar, Yuklamalar va boshqa tizim papkalari **hech qachon** o'chirilmaydi
  (har bir o'chirishdan oldin qayta tekshiriladi).
- Boshqa o'rnatilgan dastur foydalanayotgan papka va kalitlarga tegilmaydi.
- Ishlab turgan jarayon, xizmat yoki vazifa foydalanayotgan papka "qoldiq" deb hisoblanmaydi.
- O'chirilgan **registr kalitlari** `.reg` faylga zaxiralanadi:
  `C:\ProgramData\StorageTrashCleaner\Backup\<sana_vaqt>\` (qaytarish uchun `.reg` faylni ikki marta bosing).
  U yerda batafsil jurnal (`log.txt`) ham saqlanadi.
- Fayl va papkalar standart holatda **Savatga (Recycle Bin)** yuboriladi — **F3** bilan
  to'g'ridan-to'g'ri o'chirishga almashtirish mumkin (joy darhol bo'shaydi).
- Band fayllar kompyuter qayta yuklanganda o'chiriladi.

## Yuklab olish

GitHub'dagi **Actions → Build** bo'limidan oxirgi muvaffaqiyatli build'ning
`StorageTrashCleaner` artefaktini yuklab oling (yoki **Releases** bo'limidan).
`StorageTrashCleaner.exe` — oddiy kompyuterlar uchun, `StorageTrashCleaner-arm64.exe` — ARM qurilmalar uchun.

Dastur administrator huquqini so'raydi (UAC) — registrning `HKLM` qismi va `Program Files`
ni tozalash uchun bu zarur.

> Eslatma: agar siz oddiy foydalanuvchi bo'lib, boshqa administrator hisobining parolini
> kiritsangiz, `AppData` va `HKCU` o'sha administrator hisobiniki bo'ladi. Eng yaxshi natija uchun
> o'zingiz administrator bo'lgan hisobdan ishga tushiring.

## Boshqaruv

| Tugma | Amal |
|---|---|
| ↑ / ↓ | ro'yxat bo'ylab harakat |
| Space / Enter | belgilash |
| `/` yoki harf yozish | qidirish |
| Del | belgilangan (yoki tanlangan) dasturlarni o'chirish |
| Ctrl+A / Ctrl+D | hammasini belgilash / bekor qilish |
| F2 | o'chirilgan dasturlar qoldiqlarini qidirish |
| F3 | Savatga yuborish / to'g'ridan-to'g'ri o'chirish |
| F5 | ro'yxatni yangilash |
| Esc | chiqish / o'tkazib yuborish |

### Buyruq satri

```
StorageTrashCleaner.exe --list      # o'rnatilgan dasturlar ro'yxati
StorageTrashCleaner.exe --orphans   # qoldiqlarni topib, faqat ro'yxatini chiqaradi (hech narsa o'chirilmaydi)
```

## O'zingiz yig'ish (build)

[Go 1.26+](https://go.dev/dl/) kerak:

```
go build -trimpath -ldflags "-s -w" -o StorageTrashCleaner.exe .
```

Linux/macOS dan: `GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o StorageTrashCleaner.exe .`

Belgi (icon) va administrator manifesti `winres/` papkasida; ular o'zgartirilsa
`go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64,arm64` bilan `.syso` fayllarni qayta yarating.

Testlar: `go test ./...`
